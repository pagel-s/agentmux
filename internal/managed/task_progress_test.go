package managed

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func observeProgress(t *testing.T, s *Store, id, lease string, count int, kind string) TaskProgress {
	t.Helper()
	var response struct {
		Observed bool         `json:"observed"`
		Progress TaskProgress `json:"progress"`
	}
	if err := json.Unmarshal(call(t, s, "bob", "task.observe", map[string]any{"id": id, "lease": lease, "event_count": count, "event_kind": kind}), &response); err != nil || !response.Observed {
		t.Fatalf("missing progress acknowledgement: %+v %v", response, err)
	}
	return response.Progress
}

func progressStorage(t *testing.T, s *Store) string {
	t.Helper()
	var records, requests, metadata string
	for _, query := range []struct {
		sql    string
		target *string
	}{
		{"SELECT COALESCE(group_concat(data,''),'') FROM records ORDER BY seq", &records},
		{"SELECT COALESCE(group_concat(response,''),'') FROM requests ORDER BY actor,request_id", &requests},
		{"SELECT COALESCE(group_concat(value,''),'') FROM metadata ORDER BY key", &metadata},
	} {
		if err := s.db.QueryRow(query.sql).Scan(query.target); err != nil {
			t.Fatal(err)
		}
	}
	return records + requests + metadata
}

func TestTaskProgressIsEphemeralLeaseFencedMetadataWithSeparateCheckinsAndEvents(t *testing.T) {
	s := testStore(t, testConfig())
	instruction(t, s)
	task, lease := claim(t, s)
	before := progressStorage(t, s)
	initial := observeProgress(t, s, task.ID, lease, 0, "")
	if initial.WorkerObservedAt == "" || initial.EventObservedAt != "" || initial.EventCount != 0 || initial.EventKind != "" {
		t.Fatalf("check-in invented native activity: %+v", initial)
	}
	event := observeProgress(t, s, task.ID, lease, 2, "tool")
	if event.EventCount != 2 || event.EventKind != "tool" || event.EventObservedAt == "" {
		t.Fatalf("missing event evidence: %+v", event)
	}
	heartbeat := observeProgress(t, s, task.ID, lease, 2, "tool")
	if heartbeat.EventObservedAt != event.EventObservedAt || heartbeat.EventCount != 2 || heartbeat.EventKind != "tool" {
		t.Fatal("heartbeat fabricated a new native event")
	}
	if _, err := time.Parse(time.RFC3339Nano, heartbeat.WorkerObservedAt); err != nil {
		t.Fatal(err)
	}
	got := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": task.ID}))
	if got.Progress == nil || !reflect.DeepEqual(*got.Progress, heartbeat) || got.State != task.State || got.Delivery != task.Delivery || got.Version != task.Version {
		t.Fatalf("progress changed task state or was lost: %+v", got)
	}
	for _, op := range []string{"task.list", "activity"} {
		var result struct {
			Tasks []Task `json:"tasks"`
		}
		if err := json.Unmarshal(call(t, s, "", op, map[string]any{}), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Tasks) != 1 || result.Tasks[0].Progress == nil || !reflect.DeepEqual(*result.Tasks[0].Progress, heartbeat) {
			t.Fatalf("%s omitted current attempt progress", op)
		}
	}
	owner := observationIn(t, s, map[string]any{})
	if owner.ActiveCount != 1 || len(owner.Active) != 1 || owner.ActiveMore || owner.Active[0].Progress == nil || !reflect.DeepEqual(*owner.Active[0].Progress, heartbeat) || owner.AttentionCount != 0 {
		t.Fatalf("active work was confused with attention: %+v", owner)
	}
	for _, id := range []string{"alice", "bob"} {
		if got := taskIn(t, call(t, s, id, "task.get", map[string]any{"id": task.ID})); got.Progress != nil {
			t.Fatal("owner progress projection leaked to an agent")
		}
	}
	if before != progressStorage(t, s) {
		t.Fatal("progress observations changed durable records, requests or execution lease")
	}
	// A new Store (as on restart) has no ephemeral process observations.
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := taskIn(t, call(t, reopened, "", "task.get", map[string]any{"id": task.ID})); got.Progress != nil {
		t.Fatal("broker restart fabricated retained liveness")
	}
}

func TestTaskProgressRejectsSpoofedRegressingExpiredCancelledAndOldAttempts(t *testing.T) {
	s := testStore(t, testConfig())
	instruction(t, s)
	task, lease := claim(t, s)
	args := func(id, token string, count int, kind string) map[string]any {
		return map[string]any{"id": id, "lease": token, "event_count": count, "event_kind": kind}
	}
	expectFault(t, s, "", "task.observe", "forbidden", args(task.ID, lease, 0, ""))
	expectFault(t, s, "alice", "task.observe", "conflict", args(task.ID, lease, 0, ""))
	expectFault(t, s, "bob", "task.observe", "conflict", args(task.ID, "wrong-lease", 0, ""))
	for _, input := range []map[string]any{args(task.ID, lease, -1, ""), args(task.ID, lease, 1000001, "tool"), args(task.ID, lease, 1, "RAW_SECRET_EVENT"), args(task.ID, lease, 0, "tool")} {
		expectFault(t, s, "bob", "task.observe", "invalid", input)
	}
	input := args(task.ID, lease, 0, "")
	input["worker_observed_at"] = "2100-01-01T00:00:00Z"
	expectFault(t, s, "bob", "task.observe", "invalid", input)
	latest := observeProgress(t, s, task.ID, lease, 4, "message")
	expectFault(t, s, "bob", "task.observe", "conflict", args(task.ID, lease, 3, "message"))
	expectFault(t, s, "bob", "task.observe", "conflict", args(task.ID, lease, 4, "error"))
	if got := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": task.ID})); got.Progress == nil || !reflect.DeepEqual(*got.Progress, latest) {
		t.Fatal("rejected observation altered progress")
	}
	// An expired lease may retain historical observations, but cannot refresh
	// them; recovery and effective uncertainty remain visible alongside them.
	record, err := loadTask(context.Background(), s.db, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.Expires = time.Now().Add(-time.Minute).Unix()
	if err = saveTask(context.Background(), s.db, record); err != nil {
		t.Fatal(err)
	}
	expectFault(t, s, "bob", "task.observe", "conflict", args(task.ID, lease, 5, "message"))
	got := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": task.ID}))
	if got.Progress == nil || !reflect.DeepEqual(*got.Progress, latest) || got.Recovery == nil {
		t.Fatal("expiry erased historical evidence or hid uncertainty")
	}
	observation := observationIn(t, s, map[string]any{})
	if observation.ActiveCount != 0 || len(observation.Attention) != 1 || observation.Attention[0].Progress == nil || observation.Attention[0].Kind != "recovery" {
		t.Fatalf("expired lease appeared active: %+v", observation)
	}
	call(t, s, "", "task.resolve", map[string]any{"id": task.ID, "version": got.Version, "resolution": "retry"})
	retry, newLease := claim(t, s)
	if retry.ID != task.ID || newLease == lease {
		t.Fatal("retry did not replace lease fixture")
	}
	if retry.Progress != nil {
		t.Fatal("retry inherited a prior attempt's observations")
	}
	expectFault(t, s, "bob", "task.observe", "conflict", args(task.ID, lease, 6, "message"))
	observeProgress(t, s, task.ID, newLease, 0, "")
	call(t, s, "", "task.cancel", map[string]any{"id": task.ID, "version": retry.Version})
	expectFault(t, s, "bob", "task.observe", "conflict", args(task.ID, newLease, 1, "tool"))
	if got := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": task.ID})); got.Progress != nil {
		t.Fatal("cancelled task retained an active worker projection")
	}
}

func TestTaskProgressCannotCrossCredentialGenerationAndPrunesRetiredEntries(t *testing.T) {
	s := testStore(t, testConfig())
	instruction(t, s)
	task, lease := claim(t, s)
	observeProgress(t, s, task.ID, lease, 1, "message")
	old := credential(t, s, "bob")
	if _, err := s.Rotate("bob"); err != nil {
		t.Fatal(err)
	}
	if _, problem := invoke(s, old, "task.observe", map[string]any{"id": task.ID, "lease": lease, "event_count": 2, "event_kind": "message"}, ""); problem == nil || problem.Code != "unauthorized" {
		t.Fatalf("rotated credential updated observations: %v", problem)
	}
	expectFault(t, s, "bob", "task.observe", "conflict", map[string]any{"id": task.ID, "lease": lease, "event_count": 2, "event_kind": "message"})
	if got := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": task.ID})); got.Progress != nil {
		t.Fatal("new generation inherited old process evidence")
	}
	if len(s.taskProgress) != 0 {
		t.Fatal("rotated worker entry was not pruned")
	}
}
