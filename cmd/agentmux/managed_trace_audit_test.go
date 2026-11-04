package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func TestTraceCLIAuthenticatesOwnerInsteadOfFileLabel(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	s, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: "trace-auth", Agents: map[string]managedconfig.Agent{"agent": {Harness: "command", Command: []string{"/usr/bin/true"}}}})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	agent, err := managed.LoadCredential(managed.CredentialPath(state, "agent"))
	if err != nil {
		t.Fatal(err)
	}
	agent.AgentID = "$owner"
	raw, _ := json.Marshal(agent)
	os.WriteFile(managed.CredentialPath(state, ""), raw, 0600)
	var out, stderr bytes.Buffer
	if code := runManagedTrace(context.Background(), []string{"list", "--state", state, "--agent", "agent"}, &out, &stderr); code == 0 {
		t.Fatal("agent token relabeled owner granted trace access")
	}
}

type traceCLIAppendOutput struct {
	buffer bytes.Buffer
	path   string
	once   bool
}

func (w *traceCLIAppendOutput) Write(p []byte) (int, error) {
	if !w.once {
		w.once = true
		f, err := os.OpenFile(w.path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return 0, err
		}
		_, err = f.WriteString("UNAUTHENTICATED-CONTENT-AFTER-VERIFICATION\n")
		f.Close()
		if err != nil {
			return 0, err
		}
	}
	return w.buffer.Write(p)
}

func authenticatedTraceCLIFixture(t *testing.T) (string, string, string, string, []byte) {
	t.Helper()
	state := filepath.Join(t.TempDir(), "state")
	s, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: "trace-display", Agents: map[string]managedconfig.Agent{"agent": {Harness: "command", Command: []string{"/usr/bin/true"}}}})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	dir := filepath.Join(state, "traces", "agent")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	if err := os.WriteFile(filepath.Join(state, "traces", "trace.key"), key, 0600); err != nil {
		t.Fatal(err)
	}
	id := "task_cli-attempt_display"
	input, _ := json.Marshal(map[string]string{"input": strings.Repeat("verified input ", 5000)})
	records := []managed.TraceRecord{
		{Agent: "agent", TaskID: "task_cli", TraceID: id, Sequence: 1, At: "2026-11-03T00:00:00Z", Kind: "input", Data: input},
		{Agent: "agent", TaskID: "task_cli", TraceID: id, Sequence: 2, At: "2026-11-03T00:00:01Z", Kind: "end", Data: json.RawMessage(`{"status":"submitted"}`)},
	}
	var original bytes.Buffer
	previous := ""
	for _, record := range records {
		record.Previous = previous
		unsigned, _ := json.Marshal(record)
		mac := hmac.New(sha256.New, key)
		mac.Write(unsigned)
		record.MAC = hex.EncodeToString(mac.Sum(nil))
		previous = record.MAC
		encoded, _ := json.Marshal(record)
		original.Write(encoded)
		original.WriteByte('\n')
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, original.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := managed.VerifyTrace(state, "agent", id, previous); err != nil {
		t.Fatalf("fixture is not authenticated: %v", err)
	}
	return state, id, previous, path, original.Bytes()
}

func TestTraceCLIShowUsesAuthenticatedCaptureDuringSourceAppend(t *testing.T) {
	state, id, anchor, path, original := authenticatedTraceCLIFixture(t)
	out := &traceCLIAppendOutput{path: path}
	var stderr bytes.Buffer
	if code := runManagedTrace(context.Background(), []string{"show", "--state", state, "--agent", "agent", "--id", id, "--mac", anchor}, out, &stderr); code != 0 || !bytes.Equal(out.buffer.Bytes(), original) {
		t.Fatalf("show published a changed source: code=%d bytes=%d stderr=%s", code, out.buffer.Len(), stderr.String())
	}
	if _, err := managed.VerifyTrace(state, "agent", id, anchor); err == nil {
		t.Fatal("fixture did not append unauthenticated source content")
	}
}

func TestTraceCLIRejectsAmbiguousSignedEnvelope(t *testing.T) {
	state, id, anchor, path, original := authenticatedTraceCLIFixture(t)
	for _, key := range []string{"data", "DATA", `\u0064ata`, "mac"} {
		value := `{"injected":"UNAUTHENTICATED-DUPLICATE-CONTENT"}`
		if key == "mac" {
			value = `"UNAUTHENTICATED-DUPLICATE-CONTENT"`
		}
		altered := append([]byte(`{"`+key+`":`+value+`,`), original[1:]...)
		if err := os.WriteFile(path, altered, 0600); err != nil {
			t.Fatal(err)
		}
		for _, operation := range []string{"verify", "show", "export"} {
			t.Run(key+"/"+operation, func(t *testing.T) {
				args := []string{operation, "--state", state, "--agent", "agent", "--id", id, "--mac", anchor}
				output := filepath.Join(state, "bundle.json")
				if operation == "export" {
					args = append(args, "--output", output)
				}
				var out, stderr bytes.Buffer
				if code := runManagedTrace(context.Background(), args, &out, &stderr); code == 0 || bytes.Contains(out.Bytes(), []byte("UNAUTHENTICATED-DUPLICATE-CONTENT")) {
					t.Fatalf("ambiguous envelope was accepted or printed: code=%d output=%s", code, out.String())
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatalf("refused trace exported bytes: %v", err)
				}
			})
		}
	}
}
