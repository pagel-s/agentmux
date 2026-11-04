package managed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentmux/internal/harness"
)

func TestWorkerProgressCancellationBeforeInitialCheckin(t *testing.T) {
	s, owner, credential := workerTestStore(t, []string{"/bin/cat"}, "local")
	task, lease := workerTestTask(t, s, owner, credential)
	base := workerTestCaller(s, credential)
	cancelled := false
	caller := workerCallFunc(func(ctx context.Context, r Request) (json.RawMessage, *Fault) {
		if r.Operation == "task.observe" && !cancelled {
			cancelled = true
			current := workerGetTaskForTest(t, s, owner, task.ID)
			workerTestCall(t, s, owner.Token, "task.cancel", map[string]any{"id": task.ID, "version": current.Version})
		}
		return base.Call(ctx, r)
	})
	marker := filepath.Join(t.TempDir(), "not-started")
	req := harness.Request{Harness: "command", Command: []string{"/bin/sh", "-c", "touch \"$1\"", "fixture", marker}, Workdir: s.cfg.Agents["worker"].Workdir}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := executeWorkerTask(ctx, caller, req, task, lease, credential.Token, 1024)
	if err != nil {
		t.Fatalf("a definite pre-launch cancellation was not acknowledged: %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("child was started: %v", statErr)
	}
	current := workerGetTaskForTest(t, s, owner, task.ID)
	if current.State != "cancelled" || current.Delivery != "stopped" {
		t.Fatalf("initial progress/cancellation race lost definite no-process acknowledgement: err=%v state=%s delivery=%s", err, current.State, current.Delivery)
	}
}

func TestWorkerProgressCancellationDuringCheckin(t *testing.T) {
	s, owner, credential := workerTestStore(t, []string{"/bin/cat"}, "local")
	task, lease := workerTestTask(t, s, owner, credential)
	base := workerTestCaller(s, credential)
	observations := 0
	marker := filepath.Join(t.TempDir(), "started-before-cancellation")
	caller := workerCallFunc(func(ctx context.Context, r Request) (json.RawMessage, *Fault) {
		if r.Operation == "task.observe" {
			observations++
			if observations == 2 {
				if _, err := os.Stat(marker); err != nil {
					t.Errorf("expected a live process before the heartbeat cancellation: %v", err)
					return nil, fault("internal", "fixture child did not start")
				}
				// This callback runs in the monitor goroutine. Return errors to
				// it instead of using a fatal test helper that could strand its
				// completion channel. Check-ins never change the claimed version.
				args, _ := json.Marshal(map[string]any{"id": task.ID, "version": task.Version})
				if _, problem := s.Call(ctx, owner.Token, Request{Operation: "task.cancel", RequestID: randomID("cancel"), Args: args}); problem != nil {
					t.Errorf("fixture cancellation failed: %v", problem)
					return nil, problem
				}
			}
		}
		return base.Call(ctx, r)
	})
	req := harness.Request{Harness: "command", Command: []string{"/bin/sh", "-c", `printf started > "$1"; exec /bin/sleep 30`, "fixture", marker}, Workdir: s.cfg.Agents["worker"].Workdir}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	err := executeWorkerTask(ctx, caller, req, task, lease, credential.Token, 1024)
	if err != nil {
		t.Fatalf("confirmed process termination was not acknowledged: %v", err)
	}
	current := workerGetTaskForTest(t, s, owner, task.ID)
	if observations < 2 || current.State != "cancelled" || current.Delivery != "stopped" {
		t.Fatalf("heartbeat cancellation race lost confirmed process-stop acknowledgement: observed=%d err=%v state=%s delivery=%s", observations, err, current.State, current.Delivery)
	}
}

func TestWorkerProgressFailureCannotAcknowledgeReplacementAttempt(t *testing.T) {
	s, owner, credential := workerTestStore(t, []string{"/bin/cat"}, "local")
	task, lease := workerTestTask(t, s, owner, credential)
	base := workerTestCaller(s, credential)
	var replacement Task
	var replacementLease string
	caller := workerCallFunc(func(ctx context.Context, r Request) (json.RawMessage, *Fault) {
		if r.Operation == "task.observe" && replacementLease == "" {
			replacement, replacementLease = retryWorkerAttempt(t, s, owner, credential, task.ID)
		}
		return base.Call(ctx, r)
	})
	marker := filepath.Join(t.TempDir(), "stale-child-must-not-start")
	req := harness.Request{Harness: "command", Command: []string{"/bin/sh", "-c", `touch "$1"`, "fixture", marker}, Workdir: s.cfg.Agents["worker"].Workdir}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := executeWorkerTask(ctx, caller, req, task, lease, credential.Token, 1024); err == nil {
		t.Fatal("a replacement lease was accepted as the old execution attempt")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the replaced attempt started a child: %v", err)
	}
	current := workerGetTaskForTest(t, s, owner, task.ID)
	if replacementLease == "" || replacementLease == lease || current.State != "running" || current.Delivery != "dispatching" || current.Version != replacement.Version {
		t.Fatalf("observation failure changed the replacement attempt: %+v", current)
	}
}
