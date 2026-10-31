package managed

import (
	"agentmux/internal/managedconfig"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRealWorkerReloadsFilteredNativeCredentialsBetweenTurns(t *testing.T) {
	binary := os.Getenv("AGENTMUX_INTEGRATION_BINARY")
	if binary == "" {
		t.Skip("set AGENTMUX_INTEGRATION_BINARY for real sandbox credential-refresh regression")
	}
	binary, _ = filepath.Abs(binary)
	root, err := os.MkdirTemp("", "amx-auth-reload-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	auth := filepath.Join(root, "auth.json")
	writeAuth := func(token string) {
		t.Helper()
		if err := os.WriteFile(auth, []byte(`{"claudeAiOauth":{"accessToken":"`+token+`"},"unrelated":{"secret":"must-not-mount"}}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeAuth("fixture-first-token")
	fake := filepath.Join(root, "native.py")
	script := `#!/usr/bin/python3
import json,os,sys
sys.stdin.read()
with open('/home/agent/.claude/.credentials.json') as f: auth=json.load(f)
assert set(auth)=={'claudeAiOauth'}
token=auth['claudeAiOauth']['accessToken']
marker='FIRST' if token=='fixture-first-token' else 'SECOND' if token=='fixture-second-token' else 'INVALID'
with open('observed','a') as f: f.write(marker+'\n')
print(json.dumps({'type':'result','subtype':'success','result':marker,'is_error':False,'fixture_secret':token}))
`
	os.WriteFile(fake, []byte(script), 0700)
	state := filepath.Join(root, "s")
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "auth-reload", Agents: map[string]managedconfig.Agent{"worker": {Harness: "claude", Sandbox: "bubblewrap", Network: "none", Command: []string{fake}, ReadOnlyPaths: []string{fake}, Auth: managedconfig.NativeAuth{File: auth}, Trace: managedconfig.Trace{Enabled: true}}}, Limits: managedconfig.Limits{MaxRuntimeSeconds: 30}}
	s, err := Init(state, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	brokerCtx, stopBroker := context.WithCancel(ctx)
	defer stopBroker()
	brokerDone := make(chan error, 1)
	go func() { brokerDone <- Serve(brokerCtx, s) }()
	client := NewClient(credential(t, s, ""))
	for i := 0; i < 100; i++ {
		if _, p := client.Call(ctx, Request{Operation: "identity", Args: []byte(`{}`)}); p == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	workerDone := make(chan error, 1)
	go func() {
		workerDone <- RunWorker(workerCtx, WorkerOptions{StateDir: state, AgentID: "worker", Executable: binary})
	}()
	for _, marker := range []string{"FIRST", "SECOND"} {
		if marker == "SECOND" {
			writeAuth("fixture-second-token")
		}
		task := taskIn(t, call(t, s, "", "instruct", map[string]any{"to": []string{"worker"}, "text": "Read only the explicitly supplied filtered credential fixture."}))
		for i := 0; i < 200; i++ {
			current := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": task.ID}))
			if current.State == "submitted" {
				if current.Result != marker {
					t.Fatalf("credential did not reload: %s", current.Result)
				}
				if current.Trace == nil {
					t.Fatal("native trace missing")
				}
				if _, err := VerifyTrace(state, "worker", current.Trace.ID, current.Trace.MAC); err != nil {
					t.Fatal(err)
				}
				content, err := os.ReadFile(filepath.Join(state, "traces", "worker", current.Trace.ID+".jsonl"))
				if err != nil || !strings.Contains(string(content), "native_event") || strings.Contains(string(content), "fixture-first-token") || strings.Contains(string(content), "fixture-second-token") {
					t.Fatalf("native events missing or secret not redacted: %v", err)
				}
				break
			}
			if current.State == "failed" {
				t.Fatalf("fixture failed: %s", current.Result)
			}
			if i == 199 {
				t.Fatal("worker failed to submit")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	stopWorker()
	<-workerDone
	stopBroker()
	<-brokerDone
	observed, err := os.ReadFile(filepath.Join(state, "workspaces", "worker", "observed"))
	if err != nil || strings.TrimSpace(string(observed)) != "FIRST\nSECOND" {
		t.Fatalf("unexpected mounted snapshots %q %v", observed, err)
	}
	original, _ := os.ReadFile(auth)
	if !strings.Contains(string(original), "must-not-mount") {
		t.Fatal("worker changed owner native auth")
	}
}
