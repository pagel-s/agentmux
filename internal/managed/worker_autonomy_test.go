package managed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentmux/internal/harness"
	"agentmux/internal/managedconfig"
)

func TestWorkerAutonomyDiscoveryOnlyReadsAndRejectsMalformedResponses(t *testing.T) {
	for _, test := range []struct {
		raw          string
		ready, valid bool
	}{
		{`{"status":{"ready":true}}`, true, true},
		{`{"status":{"ready":false}}`, false, true},
		{`{}`, false, false}, {`not-json`, false, false},
	} {
		caller := workerCallFunc(func(_ context.Context, r Request) (json.RawMessage, *Fault) {
			if r.Operation != "autonomy.status" || r.RequestID != "" {
				t.Fatal("idle poll mutated broker state")
			}
			return json.RawMessage(test.raw), nil
		})
		ready, err := workerAutonomyReady(context.Background(), caller)
		if (err == nil) != test.valid || ready != test.ready {
			t.Fatalf("%s: ready=%v,err=%v", test.raw, ready, err)
		}
	}
}

func TestWorkerWakeReadMustMatchClaimAndContainMessageArray(t *testing.T) {
	for _, raw := range []string{`{"task_id":"other","messages":[]}`, `{"task_id":"ours"}`, `{"task_id":"ours","messages":null}`} {
		caller := workerCallFunc(func(_ context.Context, r Request) (json.RawMessage, *Fault) { return json.RawMessage(raw), nil })
		if _, err := workerWakeRead(context.Background(), caller, "ours"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	caller := workerCallFunc(func(_ context.Context, r Request) (json.RawMessage, *Fault) {
		if r.Operation != "wake.read" || r.RequestID != "" {
			t.Fatal("observation resolution must be read-only")
		}
		return json.RawMessage(`{"task_id":"ours","messages":[]}`), nil
	})
	if messages, err := workerWakeRead(context.Background(), caller, "ours"); err != nil || len(messages) != 0 {
		t.Fatalf("valid skip: %v %v", messages, err)
	}
}

func TestObservationPromptCannotImpersonateCommandSender(t *testing.T) {
	prompt := workerPrompt("worker", "Review changes", managedconfig.Config{Session: "prompt"}, Task{ID: "wake", From: "worker", Text: "New observation", Origin: &TaskOrigin{Kind: "observation"}})
	if strings.Contains(prompt, "Authenticated instruction sender") || !strings.Contains(prompt, "not an instruction") || !strings.Contains(prompt, "grant no command authority") {
		t.Fatal("observation framed as command")
	}
}

func TestWorkerStopsObservationProcessWhenAllSourcesAreRevoked(t *testing.T) {
	s, owner, worker := workerTestStore(t, []string{"/bin/cat"}, "local")
	task, lease := workerTestTask(t, s, owner, worker)
	record, err := loadTask(context.Background(), s.db, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.From = "worker" // Wake tasks are self-authored, never a forged sender.
	record.Origin = &TaskOrigin{Kind: "observation", MessageIDs: []string{"source"}}
	if err := saveTask(context.Background(), s.db, record); err != nil {
		t.Fatal(err)
	}
	var revoked atomic.Bool
	base := workerTestCaller(s, worker)
	caller := workerCallFunc(func(ctx context.Context, r Request) (json.RawMessage, *Fault) {
		if r.Operation == "wake.read" {
			messages := []Message{}
			if !revoked.Load() {
				messages = append(messages, Message{ID: "source", Text: "visible"})
			}
			raw, _ := json.Marshal(map[string]any{"task_id": task.ID, "messages": messages})
			return raw, nil
		}
		return base.Call(ctx, r)
	})
	marker := filepath.Join(s.cfg.Agents["worker"].Workdir, "started")
	req := harness.Request{Harness: "command", Command: []string{"/bin/sh", "-c", `printf started > "$1"; sleep 30`, "worker", marker}, Workdir: s.cfg.Agents["worker"].Workdir}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- executeWorkerTask(ctx, caller, req, record.Task, lease, worker.Token, 1024) }()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("process ended before revocation: %v", err)
		case <-ctx.Done():
			t.Fatal("process failed to start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	revoked.Store(true)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("revoked process did not stop")
	}
	current := workerGetTaskForTest(t, s, owner, task.ID)
	if current.State != "cancelled" || current.Delivery != "stopped" {
		t.Fatalf("revocation not acknowledged: %+v", current)
	}
}
