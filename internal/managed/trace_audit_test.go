package managed

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestTraceMACBindsAgentTaskAndTraceFilename(t *testing.T) {
	state := t.TempDir()
	tr, err := beginTrace(state, "alice", "task_original", managedconfig.Trace{MaxTurnBytes: 8192, MaxTotalBytes: 16384}, map[string]string{"prompt": "bound input"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := tr.finish("submitted")
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(state, "traces", "alice", summary.ID+".jsonl"))
	bob := filepath.Join(state, "traces", "bob")
	os.Mkdir(bob, 0700)
	os.WriteFile(filepath.Join(bob, summary.ID+".jsonl"), original, 0600)
	if _, err = VerifyTrace(state, "bob", summary.ID, summary.MAC); err == nil {
		t.Fatal("signed trace relabeled as another agent")
	}
	newID := "task_other-attempt_copy"
	os.WriteFile(filepath.Join(state, "traces", "alice", newID+".jsonl"), original, 0600)
	if _, err = VerifyTrace(state, "alice", newID, summary.MAC); err == nil {
		t.Fatal("signed trace relabeled as another task/attempt")
	}
}

type replacingTraceOutput struct {
	buffer bytes.Buffer
	path   string
	once   bool
}

func (w *replacingTraceOutput) Write(p []byte) (int, error) {
	if !w.once {
		w.once = true
		f, err := os.OpenFile(w.path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return 0, err
		}
		_, err = f.WriteString("UNAUTHENTICATED-APPENDED-CONTENT\n")
		f.Close()
		if err != nil {
			return 0, err
		}
	}
	return w.buffer.Write(p)
}

func TestTraceShowPublishesCapturedAuthenticatedBytesDespiteConcurrentAppend(t *testing.T) {
	state := t.TempDir()
	tr, err := beginTrace(state, "alice", "task_snapshot", managedconfig.Trace{MaxTurnBytes: 131072, MaxTotalBytes: 262144}, map[string]string{"input": strings.Repeat("verified input ", 5000)})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := tr.finish("submitted")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "traces", "alice", summary.ID+".jsonl")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := &replacingTraceOutput{path: path}
	verified, err := WriteVerifiedTrace(state, "alice", summary.ID, summary.MAC, out)
	if err != nil || verified != summary || !bytes.Equal(out.buffer.Bytes(), original) {
		t.Fatalf("display differed from verified capture: bytes=%d, summary=%+v, err=%v", out.buffer.Len(), verified, err)
	}
	if _, err := VerifyTrace(state, "alice", summary.ID, summary.MAC); err == nil {
		t.Fatal("test did not append unauthenticated data to source")
	}
	// A failed authentication must publish no trace bytes at all.
	var refused bytes.Buffer
	if _, err := WriteVerifiedTrace(state, "alice", summary.ID, summary.MAC, &refused); err == nil || refused.Len() != 0 {
		t.Fatal("failed authentication emitted source content")
	}
}

type failingTraceOutput struct{}

func (failingTraceOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestVerifiedTraceOutputFailureIsReturned(t *testing.T) {
	state := t.TempDir()
	tr, err := beginTrace(state, "alice", "task_output", managedconfig.Trace{MaxTurnBytes: 8192, MaxTotalBytes: 16384}, map[string]string{"input": "verified"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := tr.finish("submitted")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteVerifiedTrace(state, "alice", summary.ID, summary.MAC, failingTraceOutput{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("output error was lost: %v", err)
	}
}

func TestTraceRejectsAmbiguousSignedEnvelopeWithoutChangingMAC(t *testing.T) {
	state := t.TempDir()
	tr, err := beginTrace(state, "alice", "task_envelope", managedconfig.Trace{MaxTurnBytes: 8192, MaxTotalBytes: 16384}, map[string]string{"input": "signed input"})
	if err != nil {
		t.Fatal(err)
	}
	// Nested provider payloads retain their existing raw signed representation.
	if err = tr.append("native_event", json.RawMessage(`{"type":"event","payload":{"key":1,"key":2}}`)); err != nil {
		t.Fatal(err)
	}
	summary, err := tr.finish("submitted")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "traces", "alice", summary.ID+".jsonl")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = VerifyTrace(state, "alice", summary.ID, summary.MAC); err != nil {
		t.Fatalf("ordinary signed native payload changed compatibility: %v", err)
	}
	variants := map[string][]byte{}
	for _, key := range []string{"agent", "task_id", "trace_id", "sequence", "at", "kind", "data", "previous", "mac", "DATA", `\u0064ata`} {
		value := `"UNAUTHENTICATED-DUPLICATE-CONTENT"`
		if key == "sequence" {
			value = "123"
		} else if key == "data" || key == "DATA" || key == `\u0064ata` {
			value = `{"injected":"UNAUTHENTICATED-DUPLICATE-CONTENT"}`
		}
		prefix := []byte(`{"` + key + `":` + value + `,`)
		variants[key] = append(prefix, original[1:]...)
	}
	variants["missing-field"] = bytes.Replace(original, []byte(`"previous":"",`), nil, 1)
	variants["nullable-string"] = bytes.Replace(original, []byte(`"previous":""`), []byte(`"previous":null`), 1)
	for name, altered := range variants {
		t.Run(name, func(t *testing.T) {
			// Tampering never reads the key or recomputes any MAC/task anchor.
			if err = os.WriteFile(path, altered, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = VerifyTrace(state, "alice", summary.ID, summary.MAC); err == nil {
				t.Fatal("ambiguous signed envelope accepted")
			}
			var out bytes.Buffer
			if _, err = WriteVerifiedTrace(state, "alice", summary.ID, summary.MAC, &out); err == nil || out.Len() != 0 {
				t.Fatal("ambiguous signed envelope published trace bytes")
			}
			output := filepath.Join(state, "bundle-"+name+".json")
			if _, err = ExportTraceBundle(state, "alice", summary.ID, summary.MAC, output); err == nil {
				t.Fatal("ambiguous signed envelope exported")
			}
			if _, err = os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("refused export published a bundle: %v", err)
			}
		})
	}
}
