// Package memoryio provides explicit, credential-scoped memory transfers. It
// never discovers a provider credential or widens the destination's ACLs.
package memoryio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"agentmux/internal/managed"
)

const MaxArchiveBytes = 64 << 20

type Caller interface {
	Call(context.Context, managed.Request) (json.RawMessage, *managed.Fault)
}
type Archive struct {
	Version       int              `json:"version"`
	SourceSession string           `json:"source_session"`
	SourceAgent   string           `json:"source_agent"`
	Namespace     string           `json:"namespace"`
	ExportedAt    string           `json:"exported_at"`
	Entries       []managed.Memory `json:"entries"`
	SHA256        string           `json:"sha256"`
}
type ImportResult struct{ Imported, Skipped, Expired int }

func digest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func archiveDigest(a Archive) string { a.SHA256 = ""; return digest(a) }
func validateArchive(a Archive) error {
	if a.Version != 1 || len(a.SourceSession) < 1 || len(a.SourceSession) > 64 || len(a.SourceAgent) < 1 || len(a.SourceAgent) > 64 || len(a.Namespace) < 1 || len(a.Namespace) > 128 || len(a.Entries) > 10000 || a.SHA256 != archiveDigest(a) {
		return errors.New("archive version, identity or content digest invalid")
	}
	for _, value := range []string{a.SourceSession, a.SourceAgent, a.Namespace} {
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return errors.New("archive identity contains control characters")
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, a.ExportedAt); err != nil {
		return errors.New("archive export timestamp invalid")
	}
	encoded, err := json.Marshal(a)
	if err != nil || len(encoded) > MaxArchiveBytes {
		return errors.New("archive exceeds byte limit")
	}
	seen := map[string]bool{}
	for _, m := range a.Entries {
		if m.Deleted || m.Version < 1 || len(m.Author) < 1 || len(m.Author) > 64 || strings.IndexFunc(m.Author, unicode.IsControl) >= 0 || len(m.Key) < 1 || len(m.Key) > 128 || strings.IndexFunc(m.Key, unicode.IsControl) >= 0 || seen[m.Key] || m.SHA256 != "" && m.SHA256 != digestText(m.Value) {
			return errors.New("archive contains duplicate, deleted or mismatched memory")
		}
		seen[m.Key] = true
		if len(m.Tags) > 16 {
			return errors.New("archive memory has too many tags")
		}
		tags := map[string]bool{}
		for _, tag := range m.Tags {
			if len(tag) < 1 || len(tag) > 64 || strings.IndexFunc(tag, unicode.IsControl) >= 0 || tags[tag] {
				return errors.New("archive memory tags invalid")
			}
			tags[tag] = true
		}
		if m.ExpiresAt != "" {
			if _, err := time.Parse(time.RFC3339Nano, m.ExpiresAt); err != nil {
				return errors.New("archive memory expiry invalid")
			}
		}
	}
	return nil
}
func Call(ctx context.Context, c Caller, op string, args any, id string) (json.RawMessage, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	out, p := c.Call(ctx, managed.Request{Operation: op, RequestID: id, Args: raw})
	if p != nil {
		return nil, fmt.Errorf("memory operation %s failed (%s)", op, p.Code)
	}
	return out, nil
}
func Export(ctx context.Context, c Caller, namespace string) (Archive, error) {
	identity, err := Call(ctx, c, "identity", map[string]any{}, "")
	if err != nil {
		return Archive{}, err
	}
	var who struct {
		Session string `json:"session"`
		Agent   string `json:"agent_id"`
	}
	if json.Unmarshal(identity, &who) != nil || who.Session == "" {
		return Archive{}, errors.New("invalid memory identity")
	}
	a := Archive{Version: 1, SourceSession: who.Session, SourceAgent: who.Agent, Namespace: namespace, ExportedAt: time.Now().UTC().Format(time.RFC3339Nano), Entries: []managed.Memory{}}
	cursor := ""
	bytes := 0
	for page := 0; page < 10000; page++ {
		raw, err := Call(ctx, c, "memory.list", map[string]any{"namespace": namespace, "limit": 200, "after": cursor}, "")
		if err != nil {
			return a, err
		}
		var result struct {
			Entries []managed.Memory `json:"entries"`
			Cursor  string           `json:"cursor"`
		}
		if json.Unmarshal(raw, &result) != nil {
			return a, errors.New("invalid memory page")
		}
		bytes += len(raw)
		if bytes > MaxArchiveBytes || len(a.Entries)+len(result.Entries) > 10000 {
			return a, errors.New("memory archive exceeds 64MiB or 10000 entry bound")
		}
		a.Entries = append(a.Entries, result.Entries...)
		if len(result.Entries) == 0 || result.Cursor == "" || result.Cursor == cursor {
			a.SHA256 = archiveDigest(a)
			return a, nil
		}
		cursor = result.Cursor
	}
	return a, errors.New("memory pagination exceeded bound")
}
func Write(path string, a Archive) error {
	if err := validateArchive(a); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > MaxArchiveBytes {
		return errors.New("memory archive exceeds byte limit")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	if _, err = f.Write(append(raw, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}
func Read(path string) (Archive, error) {
	var a Archive
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return a, errors.New("memory archive must be a private regular file")
	}
	if info.Size() > MaxArchiveBytes {
		return a, errors.New("memory archive exceeds byte limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return a, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, MaxArchiveBytes+1))
	d.DisallowUnknownFields()
	if err = d.Decode(&a); err != nil {
		return a, errors.New("invalid memory archive")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return a, errors.New("archive must contain one JSON object")
	}
	if err := validateArchive(a); err != nil {
		return a, err
	}
	return a, nil
}
func digestText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func Import(ctx context.Context, c Caller, a Archive, namespace string, replace bool) (ImportResult, error) {
	var result ImportResult
	if err := validateArchive(a); err != nil {
		return result, err
	}
	marker := a.SourceSession + "/" + a.SHA256[:32]
	// Inspect every destination before the first write. Import is explicitly
	// per-entry CAS, not an all-or-nothing cross-run transaction.
	type planned struct {
		entry   managed.Memory
		version int
	}
	plan := []planned{}
	for _, m := range a.Entries {
		if m.ExpiresAt != "" {
			expiry, err := time.Parse(time.RFC3339Nano, m.ExpiresAt)
			if err != nil {
				return result, errors.New("archive expiry invalid")
			}
			remaining := time.Until(expiry)
			if remaining <= 0 {
				result.Expired++
				continue
			}
		}
		raw, err := Call(ctx, c, "memory.head", map[string]any{"namespace": namespace, "key": m.Key}, "")
		if err != nil {
			return result, err
		}
		var head struct {
			Version int    `json:"version"`
			State   string `json:"state"`
			SHA256  string `json:"sha256"`
		}
		if json.Unmarshal(raw, &head) != nil {
			return result, errors.New("invalid memory head")
		}
		if head.State == "live" && head.SHA256 == digestText(m.Value) {
			current, err := Call(ctx, c, "memory.get", map[string]any{"namespace": namespace, "key": m.Key}, "")
			if err != nil {
				return result, err
			}
			var out struct {
				Entry managed.Memory `json:"entry"`
			}
			if json.Unmarshal(current, &out) != nil {
				return result, errors.New("invalid memory entry")
			}
			// memory.head and memory.get are separate authenticated snapshots.
			// A value-only patch retains import claims, so claims alone cannot
			// establish that the destination still holds this archived value.
			if out.Entry.Namespace != namespace || out.Entry.Key != m.Key || out.Entry.Deleted || out.Entry.Version != head.Version || out.Entry.SHA256 != head.SHA256 || digestText(out.Entry.Value) != digestText(m.Value) {
				return result, fmt.Errorf("destination key %q changed during import preflight; retry after reviewing the current version", m.Key)
			}
			matching := false
			for _, source := range out.Entry.Sources {
				if source.Kind == "import" && source.ID == marker && source.SHA256 == digestText(m.Value) && source.ArchiveSHA256 == a.SHA256 && source.SourceSession == a.SourceSession && source.SourceAgent == a.SourceAgent && source.SourceNamespace == a.Namespace && source.SourceAuthor == m.Author && source.SourceKey == m.Key && source.SourceVersion == m.Version {
					matching = true
				}
			}
			if matching {
				result.Skipped++
				continue
			}
		}
		if head.Version > 0 && !replace {
			return result, fmt.Errorf("destination key %q already has a version; explicit replace is required", m.Key)
		}
		plan = append(plan, planned{m, head.Version})
	}
	for _, p := range plan {
		// Preflight can take arbitrarily long. Keep the source's exact absolute
		// deadline and recheck just before writing, never extend it via rounding.
		if p.entry.ExpiresAt != "" {
			expiry, _ := time.Parse(time.RFC3339Nano, p.entry.ExpiresAt)
			if !time.Now().Before(expiry) {
				result.Expired++
				continue
			}
		}
		sources := []managed.MemorySource{{Kind: "import", ID: marker, SHA256: digestText(p.entry.Value), SourceSession: a.SourceSession, SourceAgent: a.SourceAgent, SourceNamespace: a.Namespace, SourceAuthor: p.entry.Author, SourceKey: p.entry.Key, SourceVersion: p.entry.Version, ArchiveSHA256: a.SHA256}}
		args := map[string]any{"namespace": namespace, "key": p.entry.Key, "value": p.entry.Value, "version": p.version, "expires_at": p.entry.ExpiresAt, "tags": p.entry.Tags, "sources": sources}
		id := "memory_import_" + digest([]string{a.SHA256, namespace, p.entry.Key, fmt.Sprint(p.version)})
		encoded, err := json.Marshal(args)
		if err != nil {
			return result, err
		}
		if _, p := c.Call(ctx, managed.Request{Operation: "memory.put", RequestID: id, Args: encoded}); p != nil {
			if p.Code == "expired" {
				result.Expired++
				continue
			}
			return result, fmt.Errorf("memory operation memory.put failed (%s)", p.Code)
		}
		result.Imported++
	}
	return result, nil
}

func ValidateBaseURL(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("an explicit embedding API base is required")
	}
	return nil
}
