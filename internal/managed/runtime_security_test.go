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
)

func TestLeaseBoundTaskReadsAreTargetOnlyAndFenceExpiredAttempts(t *testing.T) {
	s := testStore(t, testConfig())
	instruction(t, s)
	task, lease := claim(t, s)
	input := map[string]any{"id": task.ID, "lease": lease}
	for _, id := range []string{"", "alice"} {
		expectFault(t, s, id, "task.get", "forbidden", input)
		// Public inspection remains usable without serving as a lease oracle.
		call(t, s, id, "task.get", map[string]any{"id": task.ID})
	}
	expectFault(t, s, "eve", "task.get", "not_found", input)
	expectFault(t, s, "bob", "task.get", "conflict", map[string]any{"id": task.ID, "lease": "not-the-lease"})
	if raw := call(t, s, "bob", "task.get", input); strings.Contains(string(raw), lease) || strings.Contains(string(raw), "lease_token") || strings.Contains(string(raw), "instance") {
		t.Fatal("lease-bound inspection exposed private execution metadata")
	}
	record, err := loadTask(context.Background(), s.db, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.Expires = time.Now().Add(-time.Minute).Unix()
	if err := saveTask(context.Background(), s.db, record); err != nil {
		t.Fatal(err)
	}
	expectFault(t, s, "bob", "task.get", "conflict", input)
	call(t, s, "bob", "task.get", map[string]any{"id": task.ID})
	// Cancellation keeps the matching attempt observable even after expiry:
	// the worker must still confirm actual process termination with its lease.
	cancelled := taskIn(t, call(t, s, "alice", "task.cancel", map[string]any{"id": task.ID, "version": task.Version}))
	call(t, s, "bob", "task.get", input)
	call(t, s, "bob", "task.stopped", map[string]any{"id": task.ID, "version": cancelled.Version, "lease": lease})
	expectFault(t, s, "bob", "task.get", "conflict", input)
}

func retryWorkerAttempt(t *testing.T, s *Store, owner, worker Credential, id string) (Task, string) {
	t.Helper()
	if err := s.RecoverInflight(context.Background()); err != nil {
		t.Fatal(err)
	}
	current := workerGetTaskForTest(t, s, owner, id)
	workerTestCall(t, s, owner.Token, "task.resolve", map[string]any{"id": id, "version": current.Version, "resolution": "retry"})
	var claimed struct {
		Task  Task   `json:"task"`
		Lease string `json:"lease"`
	}
	if err := json.Unmarshal(workerTestCall(t, s, worker.Token, "task.claim", map[string]any{}), &claimed); err != nil || claimed.Task.ID != id || claimed.Lease == "" {
		t.Fatalf("retry was not claimed: %+v, %v", claimed, err)
	}
	return claimed.Task, claimed.Lease
}

func TestStaleWorkerCannotStartRetriedTaskAttempt(t *testing.T) {
	s, owner, worker := workerTestStore(t, []string{"/bin/cat"}, "local")
	oldTask, oldLease := workerTestTask(t, s, owner, worker)
	newTask, newLease := retryWorkerAttempt(t, s, owner, worker, oldTask.ID)
	if oldLease == newLease {
		t.Fatal("retry reused its old lease")
	}
	marker := filepath.Join(s.cfg.Agents["worker"].Workdir, "stale-turn-ran")
	req := harness.Request{Harness: "command", Command: []string{"/bin/sh", "-c", `printf stale > "$1"`, "worker", marker}, Workdir: s.cfg.Agents["worker"].Workdir}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := executeWorkerTask(ctx, workerTestCaller(s, worker), req, oldTask, oldLease, worker.Token, 1024); err == nil {
		t.Fatal("stale attempt was accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("a stale claim executed a new task attempt: %v", err)
	}
	current := workerGetTaskForTest(t, s, owner, oldTask.ID)
	if current.Version != newTask.Version || current.State != "running" || current.Delivery != "dispatching" {
		t.Fatalf("stale worker changed the replacement attempt: %+v", current)
	}
}

func TestRunningWorkerStopsWhenSameTaskGetsNewLease(t *testing.T) {
	s, owner, worker := workerTestStore(t, []string{"/bin/cat"}, "local")
	oldTask, oldLease := workerTestTask(t, s, owner, worker)
	base := workerTestCaller(s, worker)
	monitorEntered, releaseMonitor := make(chan struct{}), make(chan struct{})
	var gets atomic.Int32
	caller := workerCallFunc(func(ctx context.Context, r Request) (json.RawMessage, *Fault) {
		if r.Operation == "task.get" && gets.Add(1) == 2 {
			close(monitorEntered)
			select {
			case <-releaseMonitor:
			case <-ctx.Done():
				return nil, fault("cancelled", "cancelled")
			}
		}
		return base.Call(ctx, r)
	})
	marker := filepath.Join(s.cfg.Agents["worker"].Workdir, "old-turn-started")
	req := harness.Request{Harness: "command", Command: []string{"/bin/sh", "-c", `printf started > "$1"; sleep 30`, "worker", marker}, Workdir: s.cfg.Agents["worker"].Workdir}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- executeWorkerTask(ctx, caller, req, oldTask, oldLease, worker.Token, 1024) }()
	select {
	case <-monitorEntered:
	case <-ctx.Done():
		t.Fatal("monitor did not start")
	}
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("old process exited before retry: %v", err)
		case <-ctx.Done():
			t.Fatal("old process did not start")
		case <-time.After(time.Millisecond):
		}
	}
	newTask, _ := retryWorkerAttempt(t, s, owner, worker, oldTask.ID)
	close(releaseMonitor)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("replaced lease was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("old process continued under its replacement's running task state")
	}
	current := workerGetTaskForTest(t, s, owner, oldTask.ID)
	if current.Version != newTask.Version || current.State != "running" || current.Delivery != "dispatching" {
		t.Fatalf("old process modified the new attempt: %+v", current)
	}
}
