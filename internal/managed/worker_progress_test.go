package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentmux/internal/harness"
)

func TestWorkerProgressKindsNeverEchoNativeContent(t *testing.T) {
	for _, test := range []struct{ raw, expected string }{
		{`{"type":"assistant"}`, "message"},
		{`{"type":"item.completed","item":{"type":"command_execution"}}`, "tool"},
		{`{"type":"item.completed","item":{"type":"agent_message"}}`, "message"},
		{`{"type":"turn.completed"}`, "result"},
		{`{"event":"tool_execution_start"}`, "tool"},
		{`{"type":"usage"}`, "usage"},
		{`{"type":"turn.failed"}`, "error"},
		{`{"type":"thread.started"}`, "native_event"},
		{`{"type":"untrusted-native-secret"}`, "other"},
		{`{"type":{"nested":"untrusted-native-secret"}}`, "other"},
	} {
		if got := workerEventKind(json.RawMessage(test.raw)); got != test.expected {
			t.Fatalf("kind for %s = %q, expected %q", test.raw, got, test.expected)
		}
	}
	p := &workerTurnProgress{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if err := p.event(json.RawMessage(`{"type":"assistant","text":"private output"}`)); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	p.mu.Lock()
	if p.count != 400 || p.kind != "message" {
		t.Fatalf("concurrent event count lost: %+v", p)
	}
	p.count = 1000000
	p.mu.Unlock()
	if err := p.event(json.RawMessage(`{}`)); !errors.Is(err, errWorkerProgressLimit) {
		t.Fatalf("progress event bound was not enforced: %v", err)
	}
}

func TestWorkerProgressFailureStopsNativeWithoutClaimingCompletion(t *testing.T) {
	store, owner, credential := workerTestStore(t, []string{"/bin/cat"}, "local")
	task, lease := workerTestTask(t, store, owner, credential)
	marker := filepath.Join(t.TempDir(), "never-released")
	observations := 0
	caller := workerCallFunc(func(ctx context.Context, request Request) (json.RawMessage, *Fault) {
		if request.Operation != "task.observe" {
			return store.Call(ctx, credential.Token, request)
		}
		observations++
		if observations == 1 {
			return json.RawMessage(`{"observed":true}`), nil
		}
		return nil, fault("transport", "raw native-private diagnostic")
	})
	req := harness.Request{Harness: "codex", Command: []string{os.Args[0], "-test.run=TestWorkerProgressNativeHelper", "--"}, Prompt: "fixture task", Workdir: store.cfg.Agents["worker"].Workdir, Env: map[string]string{"AMX_PROGRESS_NATIVE_HELPER": "1", "AMX_PROGRESS_NATIVE_MARKER": marker}}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	started := time.Now()
	err := executeWorkerTask(ctx, caller, req, task, lease, credential.Token, 1024)
	if err == nil || !strings.Contains(err.Error(), "task.observe") || strings.Contains(err.Error(), "native-private") || time.Since(started) >= 5*time.Second {
		t.Fatalf("observation transport loss did not stop bounded execution safely: %v", err)
	}
	current, readErr := workerGetTask(context.Background(), workerTestCaller(store, credential), task.ID)
	if readErr != nil || current.State != "running" || current.Delivery != "dispatching" {
		t.Fatalf("lost observation was represented as confirmed completion: %+v %v", current, readErr)
	}
}

func TestWorkerProgressUnsupportedControllerDisablesOnlyOptionalChannel(t *testing.T) {
	p := &workerTurnProgress{}
	calls := 0
	caller := workerCallFunc(func(_ context.Context, request Request) (json.RawMessage, *Fault) {
		calls++
		if request.Operation != "task.observe" || request.RequestID != "" {
			t.Fatalf("progress created durable request metadata: %+v", request)
		}
		return nil, fault("unsupported", "older controller")
	})
	for i := 0; i < 3; i++ {
		if err := p.flush(context.Background(), caller, "task_one", "lease_one"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("unsupported optional channel was retried %d times", calls)
	}
	for _, code := range []string{"transport", "conflict", "unauthorized", "forbidden", "invalid"} {
		p := &workerTurnProgress{}
		caller := workerCallFunc(func(context.Context, Request) (json.RawMessage, *Fault) {
			return nil, fault(code, "private raw diagnostic")
		})
		if err := p.flush(context.Background(), caller, "task_one", "lease_one"); err == nil || strings.Contains(err.Error(), "private raw diagnostic") {
			t.Fatalf("observation error was ignored or leaked raw content: %s %v", code, err)
		}
	}
}

func TestWorkerProgressNativeHelper(t *testing.T) {
	if os.Getenv("AMX_PROGRESS_NATIVE_HELPER") != "1" {
		return
	}
	_, _ = io.ReadAll(os.Stdin)
	fmt.Fprintln(os.Stdout, `{"type":"thread.started","thread_id":"fixture-native-id"}`)
	fmt.Fprintln(os.Stdout, `{"type":"item.completed","item":{"type":"agent_message","text":"PRIVATE-NATIVE-CONTENT-817425"}}`)
	// Complete only after the monitor has actually reported the live frames.
	// This is an observation handshake, not a timing assumption about CI speed.
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv("AMX_PROGRESS_NATIVE_MARKER")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(8)
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Fprintln(os.Stdout, `{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":4}}`)
	os.Exit(0)
}

func TestWorkerReportsLiveMetadataWithoutTraceAndFinalCount(t *testing.T) {
	store, owner, credential := workerTestStore(t, []string{"/bin/cat"}, "local")
	task, lease := workerTestTask(t, store, owner, credential)
	marker := filepath.Join(t.TempDir(), "native-progress-observed")
	counts := []int{}
	caller := workerCallFunc(func(ctx context.Context, request Request) (json.RawMessage, *Fault) {
		if request.Operation != "task.observe" {
			return store.Call(ctx, credential.Token, request)
		}
		if request.RequestID != "" || strings.Contains(string(request.Args), "PRIVATE-NATIVE-CONTENT") {
			t.Errorf("live metadata retained native content or a durable request ID: %+v", request)
		}
		var args struct {
			ID    string `json:"id"`
			Lease string `json:"lease"`
			Count int    `json:"event_count"`
			Kind  string `json:"event_kind"`
		}
		if err := json.Unmarshal(request.Args, &args); err != nil || args.ID != task.ID || args.Lease != lease {
			t.Errorf("progress escaped exact task/lease: %s %v", request.Args, err)
		}
		counts = append(counts, args.Count)
		if args.Count >= 2 {
			if err := os.WriteFile(marker, []byte("observed"), 0600); err != nil {
				t.Error(err)
			}
		}
		return json.RawMessage(`{"observed":true}`), nil
	})
	req := harness.Request{Harness: "codex", Command: []string{os.Args[0], "-test.run=TestWorkerProgressNativeHelper", "--"}, Prompt: "fixture task", Workdir: store.cfg.Agents["worker"].Workdir, Env: map[string]string{"AMX_PROGRESS_NATIVE_HELPER": "1", "AMX_PROGRESS_NATIVE_MARKER": marker}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := executeWorkerTask(ctx, caller, req, task, lease, credential.Token, 1024); err != nil {
		t.Fatal(err)
	}
	if len(counts) < 3 || counts[0] != 0 || counts[len(counts)-1] != 3 {
		t.Fatalf("missing initial/live/final observation: %v", counts)
	}
	foundLive := false
	for i, count := range counts {
		foundLive = foundLive || count == 2
		if i > 0 && count < counts[i-1] {
			t.Fatalf("progress count moved backwards: %v", counts)
		}
	}
	if !foundLive {
		t.Fatalf("worker never exposed live native events: %v", counts)
	}
	current, err := workerGetTask(ctx, workerTestCaller(store, credential), task.ID)
	if err != nil || current.State != "submitted" || current.Trace != nil {
		t.Fatalf("metadata observer changed completion or enabled content tracing: %+v %v", current, err)
	}
}
