package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
)

func testConfig() managedconfig.Config {
	return managedconfig.Config{Version: 2, Mode: "managed", Session: "test", Agents: map[string]managedconfig.Agent{
		"alice": {Harness: "command", Command: []string{"/bin/cat"}, Env: map[string]string{"SELECTED_KEY": "secret-value"}},
		"bob":   {Harness: "command", Command: []string{"/bin/cat"}},
		"eve":   {Harness: "command", Command: []string{"/bin/cat"}},
	}, Boards: map[string]managedconfig.Access{"team": {Readers: []string{"alice", "bob"}, Writers: []string{"alice", "bob"}}, "public": {Readers: []string{"*"}, Writers: []string{"*"}}}, Memory: map[string]managedconfig.Access{"team": {Readers: []string{"alice", "bob"}, Writers: []string{"alice"}}}, Policy: managedconfig.Policy{Default: "deny", Grants: []managedconfig.Grant{{From: []string{"alice"}, To: []string{"bob"}, Actions: []string{"message.send", "ping.send", "instruct"}}}}}
}
func testStore(t *testing.T, cfg managedconfig.Config) *Store {
	t.Helper()
	s, err := Init(filepath.Join(t.TempDir(), "state"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func credential(t *testing.T, s *Store, id string) Credential {
	t.Helper()
	c, err := LoadCredential(CredentialPath(s.dir, id))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func invoke(s *Store, c Credential, op string, args any, id string) (json.RawMessage, *Fault) {
	raw, _ := json.Marshal(args)
	return s.Call(context.Background(), c.Token, Request{Operation: op, RequestID: id, Args: raw})
}
func call(t *testing.T, s *Store, id, op string, args any) json.RawMessage {
	t.Helper()
	raw, f := invoke(s, credential(t, s, id), op, args, randomID("request"))
	if f != nil {
		t.Fatalf("%s/%s: %v", id, op, f)
	}
	return raw
}
func taskIn(t *testing.T, raw []byte) Task {
	t.Helper()
	var v struct {
		Task Task `json:"task"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v.Task
}
func expectFault(t *testing.T, s *Store, id, op, code string, args any) {
	t.Helper()
	_, f := invoke(s, credential(t, s, id), op, args, randomID("request"))
	if f == nil || f.Code != code {
		t.Fatalf("%s: wanted %s, got %v", op, code, f)
	}
}
func instruction(t *testing.T, s *Store) Task {
	return taskIn(t, call(t, s, "alice", "instruct", map[string]any{"to": []string{"bob"}, "text": "Perform a bounded task"}))
}
func claim(t *testing.T, s *Store) (Task, string) {
	t.Helper()
	raw := call(t, s, "bob", "task.claim", map[string]any{})
	var v struct {
		Task  Task   `json:"task"`
		Lease string `json:"lease"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Lease == "" {
		t.Fatal("no lease")
	}
	return v.Task, v.Lease
}

func TestCredentialBoundPolicyAndVisibility(t *testing.T) {
	cfg := testConfig()
	s := testStore(t, cfg)
	// Mutation of the caller's policy and access lists cannot loosen a live run.
	cfg.Policy.Grants[0].From[0] = "eve"
	cfg.Boards["team"].Readers[0] = "eve"
	expectFault(t, s, "eve", "instruct", "forbidden", map[string]any{"to": []string{"bob"}, "text": "spoof"})
	expectFault(t, s, "bob", "instruct", "forbidden", map[string]any{"to": []string{"alice"}, "text": "reverse direction"})
	expectFault(t, s, "alice", "message.send", "invalid", map[string]any{"from": "bob", "to": []string{"bob"}, "text": "spoof"})
	private := call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "confidential"})
	var sent struct {
		Message Message `json:"message"`
	}
	_ = json.Unmarshal(private, &sent)
	call(t, s, "alice", "board.post", map[string]any{"board": "team", "text": "scoped"})
	call(t, s, "eve", "board.post", map[string]any{"board": "public", "text": "public note"})
	eves := call(t, s, "eve", "message.list", map[string]any{})
	if strings.Contains(string(eves), "confidential") || strings.Contains(string(eves), "scoped") || !strings.Contains(string(eves), "public note") {
		t.Fatalf("visibility leaked: %s", eves)
	}
	expectFault(t, s, "eve", "message.list", "invalid", map[string]any{"after": sent.Message.ID})
	expectFault(t, s, "eve", "message.list", "forbidden", map[string]any{"board": "team"})
	expectFault(t, s, "bob", "status", "forbidden", map[string]any{})
	expectFault(t, s, "bob", "events", "forbidden", map[string]any{})
	status := call(t, s, "", "status", map[string]any{})
	if strings.Contains(string(status), "secret-value") || strings.Contains(string(status), "lease_token") {
		t.Fatal("status exposed secrets")
	}
	if _, f := s.Call(context.Background(), "bad-token", Request{Operation: "identity", Args: json.RawMessage(`{}`)}); f == nil || f.Code != "unauthorized" {
		t.Fatal("invalid token accepted")
	}
}

func TestIdempotentAtomicInstructionAndRotation(t *testing.T) {
	s := testStore(t, testConfig())
	alice := credential(t, s, "alice")
	r := Request{Operation: "instruct", RequestID: "stable-retry", Args: json.RawMessage(`{"to":["bob"],"text":"once"}`)}
	one, f := s.Call(context.Background(), alice.Token, r)
	if f != nil {
		t.Fatal(f)
	}
	r.Args = json.RawMessage(`{ "text": "once", "to": ["bob"] }`)
	two, f := s.Call(context.Background(), alice.Token, r)
	if f != nil || !bytes.Equal(one, two) {
		t.Fatalf("retry changed result: %s %v", two, f)
	}
	r.Args = json.RawMessage(`{"to":["bob"],"text":"different"}`)
	_, f = s.Call(context.Background(), alice.Token, r)
	if f == nil || f.Code != "conflict" {
		t.Fatal("changed retry accepted")
	}
	r.Args = json.RawMessage(`{"to":["bob"],"text":"once"} {}`)
	_, f = s.Call(context.Background(), alice.Token, r)
	if f == nil || f.Code != "invalid" {
		t.Fatal("trailing JSON accepted via retry cache")
	}
	for _, kind := range []string{"task", "message"} {
		n, err := count(context.Background(), s.db, kind)
		if err != nil || n != 1 {
			t.Fatalf("atomic count %s=%d %v", kind, n, err)
		}
	}
	old := credential(t, s, "bob")
	task, lease := claim(t, s)
	if _, err := s.Rotate("bob"); err != nil {
		t.Fatal(err)
	}
	_, f = invoke(s, old, "task.complete", map[string]any{"id": task.ID, "lease": lease, "version": task.Version, "status": "submitted", "result": "stale"}, "old")
	if f == nil || f.Code != "unauthorized" {
		t.Fatal("revoked token worked")
	}
	expectFault(t, s, "bob", "task.complete", "conflict", map[string]any{"id": task.ID, "lease": lease, "version": task.Version, "status": "submitted", "result": "stale instance"})
	expectFault(t, s, "bob", "task.claim", "conflict", map[string]any{})
}

func TestTaskLifecycleCancellationAndRecovery(t *testing.T) {
	s := testStore(t, testConfig())
	instruction(t, s)
	task, lease := claim(t, s)
	expectFault(t, s, "bob", "task.claim", "conflict", map[string]any{})
	complete := taskIn(t, call(t, s, "bob", "task.complete", map[string]any{"id": task.ID, "lease": lease, "version": task.Version, "status": "submitted", "result": "evidence"}))
	expectFault(t, s, "alice", "task.verify", "forbidden", map[string]any{"id": task.ID, "version": complete.Version})
	verified := taskIn(t, call(t, s, "", "task.verify", map[string]any{"id": task.ID, "version": complete.Version}))
	if verified.State != "verified" {
		t.Fatal(verified)
	}
	instruction(t, s)
	task, lease = claim(t, s)
	cancelled := taskIn(t, call(t, s, "alice", "task.cancel", map[string]any{"id": task.ID, "version": task.Version}))
	if cancelled.Delivery != "cancel_requested" || cancelled.State != "cancelled" {
		t.Fatal(cancelled)
	}
	expectFault(t, s, "bob", "task.complete", "conflict", map[string]any{"id": task.ID, "lease": lease, "version": cancelled.Version, "status": "submitted"})
	stopped := taskIn(t, call(t, s, "bob", "task.stopped", map[string]any{"id": task.ID, "lease": lease, "version": cancelled.Version}))
	if stopped.Delivery != "stopped" {
		t.Fatal(stopped)
	}
	// A cancelled in-flight task stays cancelled when delivery is uncertain.
	instruction(t, s)
	task, _ = claim(t, s)
	call(t, s, "alice", "task.cancel", map[string]any{"id": task.ID, "version": task.Version})
	if err := s.RecoverInflight(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := taskIn(t, call(t, s, "alice", "task.get", map[string]any{"id": task.ID}))
	if recovered.State != "cancelled" || recovered.Delivery != "cancel_uncertain" {
		t.Fatal(recovered)
	}
	expectFault(t, s, "", "task.resolve", "conflict", map[string]any{"id": task.ID, "version": recovered.Version, "resolution": "retry"})
	call(t, s, "", "task.resolve", map[string]any{"id": task.ID, "version": recovered.Version, "resolution": "fail"})
	instruction(t, s)
	task, _ = claim(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.RecoverInflight(context.Background()); err != nil {
		t.Fatal(err)
	}
	uncertain := taskIn(t, call(t, reopened, "bob", "task.get", map[string]any{"id": task.ID}))
	if uncertain.Delivery != "uncertain" {
		t.Fatal(uncertain)
	}
	expectFault(t, reopened, "bob", "task.claim", "conflict", map[string]any{})
	retry := taskIn(t, call(t, reopened, "", "task.resolve", map[string]any{"id": task.ID, "version": uncertain.Version, "resolution": "retry"}))
	if retry.State != "proposed" || retry.Delivery != "pending" {
		t.Fatal(retry)
	}
}

func TestPrivateAndSharedMemoryCAS(t *testing.T) {
	s := testStore(t, testConfig())
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "private", "key": "note", "value": "only alice", "version": 0})
	expectFault(t, s, "bob", "memory.get", "not_found", map[string]any{"namespace": "private", "key": "note"})
	expectFault(t, s, "bob", "memory.get", "forbidden", map[string]any{"namespace": "private:alice", "key": "note"})
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "shared", "value": "v1", "version": 0})
	expectFault(t, s, "alice", "memory.put", "conflict", map[string]any{"namespace": "team", "key": "shared", "value": "stale", "version": 0})
	expectFault(t, s, "bob", "memory.put", "forbidden", map[string]any{"namespace": "team", "key": "shared", "value": "unauthorized", "version": 1})
	expectFault(t, s, "eve", "memory.get", "forbidden", map[string]any{"namespace": "team", "key": "shared"})
	got := call(t, s, "bob", "memory.get", map[string]any{"namespace": "team", "key": "shared"})
	if !strings.Contains(string(got), "v1") {
		t.Fatal(string(got))
	}
}

func TestStorageBudgetsRollbackWholeRequest(t *testing.T) {
	for _, kind := range []string{"records", "requests"} {
		t.Run(kind, func(t *testing.T) {
			cfg := testConfig()
			if kind == "records" {
				cfg.Limits.MaxRecords = 12
			} else {
				cfg.Limits.MaxRequests = 10
			}
			s := testStore(t, cfg)
			instruction(t, s)
			expectFault(t, s, "alice", "memory.put", "limit", map[string]any{"namespace": "private", "key": "over", "value": "no", "version": 0})
			var records, requests, memories int
			_ = s.db.QueryRow("SELECT count(*) FROM records").Scan(&records)
			_ = s.db.QueryRow("SELECT count(*) FROM requests").Scan(&requests)
			memories, _ = count(context.Background(), s.db, "memory")
			if records != 3 || requests != 1 || memories != 0 {
				t.Fatalf("partial rejected write: records=%d requests=%d memory=%d", records, requests, memories)
			}
			// Reserved capacity still permits stopping work after ordinary writes
			// have reached the run's budget.
			task, lease := claim(t, s)
			cancelled := taskIn(t, call(t, s, "alice", "task.cancel", map[string]any{"id": task.ID, "version": task.Version}))
			call(t, s, "bob", "task.stopped", map[string]any{"id": task.ID, "version": cancelled.Version, "lease": lease})
		})
	}
}

func TestUnixAPIRejectsBrowserAndSpoofedPayload(t *testing.T) {
	s := testStore(t, testConfig())
	token := credential(t, s, "bob").Token
	for _, test := range []struct {
		body, origin, token string
		status              int
	}{
		{`{"operation":"identity","args":{}}`, "", token, http.StatusOK},
		{`{"operation":"identity","actor":"alice","args":{}}`, "", token, http.StatusBadRequest},
		{`{"operation":"identity","args":{}}`, "https://example.com", token, http.StatusForbidden},
		{`{"operation":"identity","args":{}}`, "", "invalid", http.StatusUnauthorized},
	} {
		r := httptest.NewRequest("POST", "http://agentmux/v1/call", strings.NewReader(test.body))
		r.Header.Set("Authorization", "Bearer "+test.token)
		if test.origin != "" {
			r.Header.Set("Origin", test.origin)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("status %d want%d: %s", w.Code, test.status, w.Body)
		}
		if strings.Contains(w.Body.String(), token) {
			t.Fatal("credential echoed")
		}
	}
	info, err := os.Stat(CredentialPath(s.dir, "bob"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential mode", info, err)
	}
}

func TestCachedClaimCannotReplayLeaseAcrossCredentialRotation(t *testing.T) {
	s := testStore(t, testConfig())
	instruction(t, s)
	previous := credential(t, s, "bob")
	request := Request{Operation: "task.claim", RequestID: "claim-before-rotation", Args: json.RawMessage(`{}`)}
	original, problem := s.Call(context.Background(), previous.Token, request)
	if problem != nil {
		t.Fatal(problem)
	}
	var claimed struct {
		Task  Task   `json:"task"`
		Lease string `json:"lease"`
	}
	if err := json.Unmarshal(original, &claimed); err != nil || claimed.Task.ID == "" || claimed.Lease == "" {
		t.Fatalf("invalid original claim: %s, %v", original, err)
	}
	// Same-instance retries return the original durable receipt.
	replayed, problem := s.Call(context.Background(), previous.Token, request)
	if problem != nil || !bytes.Equal(replayed, original) {
		t.Fatalf("same-instance retry changed: %s, %v", replayed, problem)
	}
	current, err := s.Rotate("bob")
	if err != nil {
		t.Fatal(err)
	}
	if current.InstanceID == previous.InstanceID {
		t.Fatal("rotation must issue a fresh instance")
	}
	if raw, problem := s.Call(context.Background(), previous.Token, request); problem == nil || problem.Code != "unauthorized" || len(raw) != 0 {
		t.Fatalf("revoked instance obtained a cached lease: %s, %v", raw, problem)
	}
	// Reusing the same request ID in a new instance must miss the old cache,
	// observe the active old delivery, and return no lease material.
	if raw, problem := s.Call(context.Background(), current.Token, request); problem == nil || problem.Code != "conflict" || len(raw) != 0 || strings.Contains(string(raw), claimed.Lease) {
		t.Fatalf("new instance replayed an old claim: %s, %v", raw, problem)
	}
	record, err := loadTask(context.Background(), s.db, claimed.Task.ID)
	if err != nil || record.Instance != previous.InstanceID || record.LeaseToken != claimed.Lease || record.Version != claimed.Task.Version {
		t.Fatalf("rotation or rejected replay changed active work: %+v, %v", record.Task, err)
	}
}

func TestExpiredCancellationCanBeResolvedWithoutBrokerRestart(t *testing.T) {
	s := testStore(t, testConfig())
	instruction(t, s)
	task, _ := claim(t, s)
	cancelled := taskIn(t, call(t, s, "alice", "task.cancel", map[string]any{"id": task.ID, "version": task.Version}))
	record, err := loadTask(context.Background(), s.db, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.LeaseToken == "" || record.Instance == "" || cancelled.Delivery != "cancel_requested" {
		t.Fatal("cancellation must preserve the running lease until stop acknowledgement")
	}
	// Expiration is simulated directly rather than sleeping or invoking the
	// restart recovery path. The owner has independently stopped the process.
	record.Expires = time.Now().Add(-time.Second).Unix()
	if err := saveTask(context.Background(), s.db, record); err != nil {
		t.Fatal(err)
	}
	resolved := taskIn(t, call(t, s, "", "task.resolve", map[string]any{"id": task.ID, "version": cancelled.Version, "resolution": "fail"}))
	if resolved.State != "cancelled" || resolved.Delivery != "stopped" || resolved.Version != cancelled.Version+1 {
		t.Fatalf("expired cancellation not resolved: %+v", resolved)
	}
	record, err = loadTask(context.Background(), s.db, task.ID)
	if err != nil || record.LeaseToken != "" || record.Instance != "" || record.Expires != 0 {
		t.Fatalf("resolved cancellation retained a live lease: %+v, %v", record.Task, err)
	}
	// The resolved task no longer blocks the recipient from accepting work.
	next := instruction(t, s)
	claimed, lease := claim(t, s)
	if claimed.ID != next.ID || lease == "" {
		t.Fatal("resolved cancellation still blocks fresh work")
	}
}

func TestPendingInstructionSurvivesReopenWithoutBecomingUncertain(t *testing.T) {
	s := testStore(t, testConfig())
	pending := instruction(t, s)
	if pending.State != "proposed" || pending.Delivery != "pending" {
		t.Fatalf("unexpected pending task: %+v", pending)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.RecoverInflight(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := taskIn(t, call(t, reopened, "bob", "task.get", map[string]any{"id": pending.ID}))
	if !reflect.DeepEqual(after, pending) {
		t.Fatalf("unclaimed work changed during recovery: before %+v, after %+v", pending, after)
	}
	var messages struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(call(t, reopened, "bob", "message.list", map[string]any{}), &messages); err != nil || len(messages.Messages) != 1 || messages.Messages[0].TaskID != pending.ID || messages.Messages[0].ID != pending.MessageID {
		t.Fatalf("instruction message was not durably paired with task: %+v, %v", messages, err)
	}
	claimed, lease := claim(t, reopened)
	if claimed.ID != pending.ID || claimed.State != "running" || claimed.Delivery != "dispatching" || lease == "" {
		t.Fatalf("recovered pending task cannot be claimed: %+v", claimed)
	}
}
