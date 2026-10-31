package managed

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"agentmux/internal/managedconfig"
)

func conversationFixture(t *testing.T) (*Store, Credential, Credential) {
	t.Helper()
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "conversation", Agents: map[string]managedconfig.Agent{
		"worker": {Harness: "claude", Sandbox: "bubblewrap", Conversation: "persistent"},
	}}
	s, err := Init(filepath.Join(t.TempDir(), "state"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	owner, err := LoadCredential(CredentialPath(s.dir, ownerID))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := LoadCredential(CredentialPath(s.dir, "worker"))
	if err != nil {
		t.Fatal(err)
	}
	return s, owner, worker
}

func conversationView(t *testing.T, raw json.RawMessage) ConversationState {
	t.Helper()
	var out struct {
		Conversation ConversationState `json:"conversation"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out.Conversation
}

func TestConversationCommitResumeAndExplicitReset(t *testing.T) {
	s, owner, worker := conversationFixture(t)
	task, lease := workerTestTask(t, s, owner, worker)
	c := conversationView(t, workerTestCall(t, s, worker.Token, "conversation.begin", map[string]any{"id": task.ID, "lease": lease}))
	if c.State != "busy" || c.Generation != 1 || c.SessionID != "" {
		t.Fatalf("initial: %+v", c)
	}
	workerTestCall(t, s, worker.Token, "task.complete", map[string]any{"id": task.ID, "lease": lease, "version": task.Version, "status": "submitted", "result": "one", "session_id": "exact-native-id"})
	c = conversationView(t, workerTestCall(t, s, worker.Token, "conversation.status", map[string]any{}))
	if c.State != "ready" || c.SessionID != "exact-native-id" {
		t.Fatalf("committed: %+v", c)
	}
	task, lease = workerTestTask(t, s, owner, worker)
	c = conversationView(t, workerTestCall(t, s, worker.Token, "conversation.begin", map[string]any{"id": task.ID, "lease": lease}))
	if c.SessionID != "exact-native-id" {
		t.Fatal("native reference lost between turns")
	}
	workerTestCall(t, s, worker.Token, "task.complete", map[string]any{"id": task.ID, "lease": lease, "version": task.Version, "status": "failed", "result": "failed"})
	c = conversationView(t, workerTestCall(t, s, worker.Token, "conversation.status", map[string]any{}))
	if c.State != "blocked" {
		t.Fatalf("failure reused history: %+v", c)
	}
	raw, _ := json.Marshal(map[string]any{"agent": "worker"})
	if _, p := s.Call(context.Background(), worker.Token, Request{Operation: "conversation.reset", RequestID: randomID("reset"), Args: raw}); p == nil || p.Code != "forbidden" {
		t.Fatalf("agent reset own history: %v", p)
	}
	c = conversationView(t, workerTestCall(t, s, owner.Token, "conversation.reset", map[string]any{"agent": "worker"}))
	if c.Generation != 2 || c.State != "ready" || c.SessionID != "" {
		t.Fatalf("reset: %+v", c)
	}
}

func TestConversationCrashCannotResumeOrResetUntilDeliveryResolved(t *testing.T) {
	s, owner, worker := conversationFixture(t)
	task, lease := workerTestTask(t, s, owner, worker)
	workerTestCall(t, s, worker.Token, "conversation.begin", map[string]any{"id": task.ID, "lease": lease})
	if err := s.RecoverInflight(context.Background()); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"agent": "worker"})
	if _, p := s.Call(context.Background(), owner.Token, Request{Operation: "conversation.reset", RequestID: randomID("reset"), Args: args}); p == nil || p.Code != "conflict" {
		t.Fatalf("reset unfenced delivery: %v", p)
	}
	current := workerGetTaskForTest(t, s, owner, task.ID)
	workerTestCall(t, s, owner.Token, "task.resolve", map[string]any{"id": task.ID, "version": current.Version, "resolution": "fail"})
	if _, p := s.Call(context.Background(), worker.Token, Request{Operation: "task.claim", RequestID: randomID("claim"), Args: json.RawMessage(`{}`)}); p == nil || (p.Code != "conflict" && p.Code != "busy") {
		t.Fatalf("silently resumed interrupted history: %v", p)
	}
	c := conversationView(t, workerTestCall(t, s, owner.Token, "conversation.reset", map[string]any{"agent": "worker"}))
	if c.Generation != 2 {
		t.Fatal("reset did not fence old native files")
	}
}

func workerGetTaskForTest(t *testing.T, s *Store, c Credential, id string) Task {
	t.Helper()
	var out struct {
		Task Task `json:"task"`
	}
	if err := json.Unmarshal(workerTestCall(t, s, c.Token, "task.get", map[string]any{"id": id}), &out); err != nil {
		t.Fatal(err)
	}
	return out.Task
}

func TestConversationReferenceSurvivesReopenAndRejectsMismatchedNativeID(t *testing.T) {
	s, owner, worker := conversationFixture(t)
	task, lease := workerTestTask(t, s, owner, worker)
	workerTestCall(t, s, worker.Token, "conversation.begin", map[string]any{"id": task.ID, "lease": lease})
	workerTestCall(t, s, worker.Token, "task.complete", map[string]any{"id": task.ID, "lease": lease, "version": task.Version, "status": "submitted", "session_id": "native-one"})
	dir := s.dir
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	c := conversationView(t, workerTestCall(t, reopened, worker.Token, "conversation.status", map[string]any{}))
	if c.SessionID != "native-one" || c.State != "ready" {
		t.Fatalf("lost durable native reference: %+v", c)
	}
	task, lease = workerTestTask(t, reopened, owner, worker)
	workerTestCall(t, reopened, worker.Token, "conversation.begin", map[string]any{"id": task.ID, "lease": lease})
	args, _ := json.Marshal(map[string]any{"id": task.ID, "lease": lease, "version": task.Version, "status": "submitted", "session_id": "native-other"})
	if _, p := reopened.Call(context.Background(), worker.Token, Request{Operation: "task.complete", RequestID: randomID("bad_complete"), Args: args}); p == nil || p.Code != "invalid" {
		t.Fatalf("accepted mismatched history: %v", p)
	}
	current := workerGetTaskForTest(t, reopened, owner, task.ID)
	if current.State != "running" {
		t.Fatal("invalid completion partially committed")
	}
}

func TestBlockedConversationReservesOwnerResetAtStorageQuota(t *testing.T) {
	s, owner, worker := conversationFixture(t)
	task, lease := workerTestTask(t, s, owner, worker)
	workerTestCall(t, s, worker.Token, "conversation.begin", map[string]any{"id": task.ID, "lease": lease})
	workerTestCall(t, s, worker.Token, "task.complete", map[string]any{"id": task.ID, "lease": lease, "version": task.Version, "status": "failed"})
	ctx := context.Background()
	reserved, err := lifecycleReserve(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	var records, requests int
	if err := s.db.QueryRow("SELECT count(*) FROM records").Scan(&records); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM requests").Scan(&requests); err != nil {
		t.Fatal(err)
	}
	s.cfg.Limits.MaxRecords, s.cfg.Limits.MaxRequests = records+reserved, requests+reserved
	c := conversationView(t, workerTestCall(t, s, owner.Token, "conversation.reset", map[string]any{"agent": "worker"}))
	if c.Generation != 2 || c.State != "ready" {
		t.Fatalf("reset blocked by quota: %+v", c)
	}
	after, err := lifecycleReserve(ctx, s.db)
	if err != nil || after != reserved-1 {
		t.Fatalf("reset reserve not released: %d -> %d (%v)", reserved, after, err)
	}
}
