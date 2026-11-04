package managed

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentmux/internal/managedconfig"
)

// Trace records exactly the prepared input and native events visible to this
// adapter. Hidden provider context and unreported native tool reads are outside
// its coverage. The HMAC key is never mounted into an agent namespace.
type TraceRecord struct {
	Agent    string          `json:"agent"`
	TaskID   string          `json:"task_id"`
	TraceID  string          `json:"trace_id"`
	Sequence int             `json:"sequence"`
	At       string          `json:"at"`
	Kind     string          `json:"kind"`
	Data     json.RawMessage `json:"data"`
	Previous string          `json:"previous"`
	MAC      string          `json:"mac"`
}
type TraceSummary struct {
	ID       string `json:"id"`
	Records  int    `json:"records"`
	Bytes    int64  `json:"bytes"`
	MAC      string `json:"mac"`
	Complete bool   `json:"complete"`
	Coverage string `json:"coverage"`
}
type turnTrace struct {
	agent, task  string
	file         *os.File
	key          []byte
	previous     string
	sequence     int
	bytes, limit int64
	summary      TraceSummary
	closed       bool
}

const traceCoverage = "prepared owner role/task/observations, adapter-visible native JSON events, final result/usage; excludes hidden provider state and unreported file/tool/network activity"

func privateTraceKey(dir string) ([]byte, error) {
	keyPath := filepath.Join(dir, "trace.key")
	info, err := os.Lstat(keyPath)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return nil, errors.New("trace key must be a private regular file")
		}
		key, err := os.ReadFile(keyPath)
		if err != nil || len(key) != 32 {
			return nil, errors.New("trace key invalid")
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, ".trace-key-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(key); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := os.Link(f.Name(), keyPath); os.IsExist(err) {
		return privateTraceKey(dir)
	} else if err != nil {
		return nil, err
	}
	return key, nil
}
func traceDirectory(state, agent string) (string, error) {
	if !managedconfig.ValidID(agent) {
		return "", errors.New("invalid trace agent")
	}
	current := state
	for _, part := range []string{"traces", agent} {
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return "", errors.New("trace directory must be private without symlink parents")
		}
	}
	return current, nil
}
func traceCapacity(state, agent string, policy managedconfig.Trace) (string, error) {
	policy = policy.Normalized()
	dir, err := traceDirectory(state, agent)
	if err != nil {
		return "", err
	}
	var used int64
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("trace store contains unsupported files")
		}
		used += info.Size()
		return nil
	})
	if err != nil {
		return "", err
	}
	if policy.MaxTotalBytes-used < policy.MaxTurnBytes {
		return "", errors.New("trace capacity cannot reserve another turn; archive traces before resuming")
	}
	return dir, nil
}
func beginTrace(state, agent, task string, policy managedconfig.Trace, header any) (*turnTrace, error) {
	if !managedconfig.ValidID(agent) || !validKey(task) || strings.ContainsAny(task, "/\\") {
		return nil, errors.New("invalid trace task/agent identity")
	}
	policy = policy.Normalized()
	dir, err := traceCapacity(state, agent, policy)
	if err != nil {
		return nil, err
	}
	key, err := privateTraceKey(filepath.Join(state, "traces"))
	if err != nil {
		return nil, err
	}
	id := task + "-" + randomID("attempt")[8:24]
	f, err := os.OpenFile(filepath.Join(dir, id+".jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	t := &turnTrace{file: f, key: key, limit: policy.MaxTurnBytes, agent: agent, task: task, summary: TraceSummary{ID: id, Coverage: traceCoverage}}
	if err = t.append("input", header); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, err
	}
	return t, nil
}
func (t *turnTrace) append(kind string, data any) error {
	if t == nil {
		return nil
	}
	if t.closed {
		return errors.New("trace already closed")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	r := TraceRecord{Agent: t.agent, TaskID: t.task, TraceID: t.summary.ID, Sequence: t.sequence + 1, At: time.Now().UTC().Format(time.RFC3339Nano), Kind: kind, Data: raw, Previous: t.previous}
	unsigned, _ := json.Marshal(r)
	mac := hmac.New(sha256.New, t.key)
	mac.Write(unsigned)
	r.MAC = hex.EncodeToString(mac.Sum(nil))
	encoded, _ := json.Marshal(r)
	if len(encoded)+1 > 8<<20 {
		return errors.New("trace record exceeds 8 MiB framing limit")
	}
	// Keep a fixed reserve for an authenticated completion marker.
	reserve := int64(1024)
	if kind == "end" {
		reserve = 0
	}
	if t.bytes+int64(len(encoded)+1)+reserve > t.limit {
		return errors.New("native trace turn byte limit reached")
	}
	if _, err = t.file.Write(append(encoded, '\n')); err != nil {
		return err
	}
	if err = t.file.Sync(); err != nil {
		return err
	}
	t.sequence++
	t.bytes += int64(len(encoded) + 1)
	t.previous = r.MAC
	return nil
}
func (t *turnTrace) finish(status string) (TraceSummary, error) {
	if t == nil {
		return TraceSummary{}, nil
	}
	if t.closed {
		return t.summary, errors.New("trace already finalized")
	}
	err := t.append("end", map[string]string{"status": status, "coverage": traceCoverage})
	t.summary.Records = t.sequence
	t.summary.Bytes = t.bytes
	t.summary.MAC = t.previous
	t.summary.Complete = err == nil
	t.closed = true
	closeErr := t.file.Close()
	if err != nil {
		return t.summary, err
	}
	return t.summary, closeErr
}

// VerifyTrace validates framing, sequence, every HMAC and the terminal marker.
// expectedMAC may anchor the trace to a stored task completion reference.
func VerifyTrace(state, agent, id, expectedMAC string) (TraceSummary, error) {
	f, key, err := openTrace(state, agent, id)
	if err != nil {
		return TraceSummary{}, err
	}
	defer f.Close()
	return verifyTraceReader(f, key, agent, id, expectedMAC)
}

// WriteVerifiedTrace publishes only the exact bytes that passed authentication.
// Capture privately before writing anything to the caller: reopening or seeking
// the source after verification would allow an append/replacement to change the
// displayed evidence. A private spool keeps memory bounded for large traces.
func WriteVerifiedTrace(state, agent, id, expectedMAC string, output io.Writer) (TraceSummary, error) {
	f, key, err := openTrace(state, agent, id)
	if err != nil {
		return TraceSummary{}, err
	}
	defer f.Close()
	captured, err := os.CreateTemp("", "amx-verified-trace-*")
	if err != nil {
		return TraceSummary{}, err
	}
	defer func() {
		captured.Close()
		os.Remove(captured.Name())
	}()
	// No configured turn may exceed 1 GiB. The extra byte makes an oversized
	// source fail verification rather than accepting a truncated prefix.
	summary, err := verifyTraceReader(io.TeeReader(io.LimitReader(f, 1<<30+1), captured), key, agent, id, expectedMAC)
	if err != nil {
		return TraceSummary{}, err
	}
	if _, err = captured.Seek(0, io.SeekStart); err != nil {
		return TraceSummary{}, err
	}
	if _, err = io.Copy(output, captured); err != nil {
		return TraceSummary{}, err
	}
	return summary, nil
}

func openTrace(state, agent, id string) (*os.File, []byte, error) {
	if !validKey(id) || strings.ContainsAny(id, "/\\") {
		return nil, nil, errors.New("invalid trace identifier")
	}
	if !managedconfig.ValidID(agent) {
		return nil, nil, errors.New("invalid trace agent")
	}
	dir := filepath.Join(state, "traces", agent)
	for _, path := range []string{filepath.Join(state, "traces"), dir} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return nil, nil, errors.New("trace directory unavailable or unsafe")
		}
	}
	keyPath := filepath.Join(state, "traces", "trace.key")
	keyInfo, err := os.Lstat(keyPath)
	if err != nil || !keyInfo.Mode().IsRegular() || keyInfo.Mode().Perm() != 0600 {
		return nil, nil, errors.New("trace key unavailable or unsafe")
	}
	key, err := os.ReadFile(keyPath)
	if err != nil || len(key) != 32 {
		return nil, nil, errors.New("trace key invalid")
	}
	path := filepath.Join(dir, id+".jsonl")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, nil, errors.New("trace must be a private regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, nil, errors.New("trace changed while opening")
	}
	return f, key, nil
}

func verifyTraceReader(input io.Reader, key []byte, agent, id, expectedMAC string) (TraceSummary, error) {
	reader := bufio.NewReaderSize(input, 8<<20+1)
	summary := TraceSummary{ID: id, Coverage: traceCoverage}
	previous := ""
	ended := false
	for {
		line, err := reader.ReadSlice('\n')
		if err == io.EOF && len(line) == 0 {
			break
		}
		if err != nil || len(line) > 8<<20 {
			return summary, errors.New("trace line missing, incomplete or oversized")
		}
		if ended {
			return summary, errors.New("trace contains data after terminal marker")
		}
		summary.Bytes += int64(len(line))
		r, decodeErr := decodeTraceRecord(line)
		if decodeErr != nil || r.Agent != agent || r.TraceID != id || r.TaskID == "" || !strings.HasPrefix(id, r.TaskID+"-") || r.Sequence != summary.Records+1 || r.Previous != previous || summary.Records == 0 && r.Kind != "input" {
			return summary, errors.New("trace sequence or framing invalid")
		}
		saved := r.MAC
		r.MAC = ""
		unsigned, _ := json.Marshal(r)
		mac := hmac.New(sha256.New, key)
		mac.Write(unsigned)
		want := hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(saved), []byte(want)) {
			return summary, errors.New("trace authentication failed")
		}
		summary.Records++
		previous = saved
		ended = r.Kind == "end"
	}
	if !ended || expectedMAC != "" && !hmac.Equal([]byte(previous), []byte(expectedMAC)) {
		return summary, fmt.Errorf("trace is incomplete or differs from its task anchor")
	}
	summary.MAC = previous
	summary.Complete = true
	return summary, nil
}

// The signed envelope has one canonical set of field names. A struct decoder
// alone accepts duplicate fields and case-insensitive aliases, which can hide
// unsigned content before a later value that still passes the canonical MAC.
// Decode each envelope field exactly once. Data remains raw signed JSON; this
// does not impose a new duplicate-key policy on native provider payloads.
func decodeTraceRecord(line []byte) (TraceRecord, error) {
	var record TraceRecord
	decoder := json.NewDecoder(bytes.NewReader(line))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return record, errors.New("trace record must be an object")
	}
	seen := make(map[string]bool, 9)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return record, errors.New("trace envelope field invalid")
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return record, errors.New("trace envelope field repeated")
		}
		seen[key] = true
		var target any
		switch key {
		case "agent":
			target = &record.Agent
		case "task_id":
			target = &record.TaskID
		case "trace_id":
			target = &record.TraceID
		case "sequence":
			target = &record.Sequence
		case "at":
			target = &record.At
		case "kind":
			target = &record.Kind
		case "data":
			target = &record.Data
		case "previous":
			target = &record.Previous
		case "mac":
			target = &record.MAC
		default:
			return record, errors.New("trace envelope field unsupported")
		}
		if key != "data" && key != "sequence" {
			var value json.RawMessage
			if err = decoder.Decode(&value); err != nil || len(value) == 0 || value[0] != '"' || json.Unmarshal(value, target) != nil {
				return record, errors.New("trace envelope string invalid")
			}
			continue
		}
		if err = decoder.Decode(target); err != nil {
			return record, errors.New("trace envelope value invalid")
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || len(seen) != 9 {
		return record, errors.New("trace envelope incomplete")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return record, errors.New("trace envelope contains trailing data")
	}
	return record, nil
}
