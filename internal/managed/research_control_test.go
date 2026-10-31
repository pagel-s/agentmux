package managed

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func researchFixture(t *testing.T) (*Store, string, ResearchProgress) {
	t.Helper()
	s := testStore(t, testConfig())
	proof, err := s.InitResearchRunner()
	if err != nil {
		t.Fatal(err)
	}
	p := ResearchProgress{Trial: "run-001", Attempt: 1, Condition: "cooperate", StateDir: s.dir, State: "starting", Population: []string{"alice", "bob"}, Tasks: map[string]string{}, Revision: 1, AckVersion: 1}
	researchRunnerCall(t, s, proof, "research.publish", p)
	return s, proof, p
}
func researchRunnerCall(t *testing.T, s *Store, proof, operation string, args any) json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(args)
	response, problem := s.Call(context.Background(), credential(t, s, "").Token, Request{Operation: operation, ResearchRunnerProof: proof, RequestID: randomID("scheduled"), Args: raw})
	if problem != nil {
		t.Fatal(problem)
	}
	return response
}
func researchState(t *testing.T, s *Store) ResearchStatus {
	t.Helper()
	var response struct {
		Research ResearchStatus `json:"research"`
	}
	if err := json.Unmarshal(call(t, s, "", "research.status", map[string]any{}), &response); err != nil {
		t.Fatal(err)
	}
	return response.Research
}
func TestResearchCapabilityCASAuditAndRecovery(t *testing.T) {
	s, proof, p := researchFixture(t)
	expectFault(t, s, "alice", "research.status", "forbidden", map[string]any{})
	expectFault(t, s, "", "research.publish", "forbidden", p)
	args, _ := json.Marshal(p)
	if _, problem := s.Call(context.Background(), credential(t, s, "alice").Token, Request{Operation: "research.publish", ResearchRunnerProof: proof, RequestID: "forged-proof", Args: args}); problem == nil || problem.Code != "forbidden" {
		t.Fatal("agent used runner capability")
	}
	call(t, s, "", "research.control", map[string]any{"version": 1, "action": "pause", "reason": "inspect collaboration"})
	expectFault(t, s, "", "research.control", "conflict", map[string]any{"version": 1, "action": "resume", "reason": "stale view"})
	expectFault(t, s, "", "research.control", "invalid", map[string]any{"version": 2, "action": "resume", "reason": ""})
	state := researchState(t, s)
	if state.Control.Mode != "paused" || len(state.Control.Interventions) != 1 || state.Progress.AckVersion != 1 || state.OwnerMutations != 0 {
		t.Fatalf("bad pending intervention: %+v", state)
	}
	// A scheduled owner write is separated by its capability, never its request name.
	researchRunnerCall(t, s, proof, "board.post", map[string]any{"board": "team", "text": "scheduled"})
	call(t, s, "", "board.post", map[string]any{"board": "team", "text": "human steering"})
	if state := researchState(t, s); state.OwnerMutations != 1 {
		t.Fatalf("owner steering not recorded: %+v", state)
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if state := researchState(t, reopened); state.Control.Version != 2 || state.OwnerMutations != 1 {
		t.Fatal("research lifecycle reset on reopen")
	}
	// Losing the state while retaining its independent marker must fail closed.
	if _, err := s.db.Exec("DELETE FROM records WHERE kind='research'; DELETE FROM metadata WHERE key='state_digest:research:live'"); err != nil {
		t.Fatal(err)
	}
	expectFault(t, s, "", "research.status", "internal", map[string]any{})
}
func TestResearchProgressReadsTaskStateWithoutContentOrSpending(t *testing.T) {
	s, proof, p := researchFixture(t)
	raw := researchRunnerCall(t, s, proof, "instruct", map[string]any{"to": []string{"bob"}, "text": "PRIVATE OBSERVATION SENTINEL"})
	task := taskIn(t, raw)
	p.Revision = 2
	p.State = "running"
	p.TaskIDs = map[string]string{"bob": task.ID}
	researchRunnerCall(t, s, proof, "research.publish", p)
	before := researchState(t, s)
	var count int
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&count)
	for range 3 {
		raw := call(t, s, "", "research.status", map[string]any{})
		if strings.Contains(string(raw), "PRIVATE OBSERVATION") || !strings.Contains(string(raw), "proposed/pending") {
			t.Fatalf("invalid progress: %s", raw)
		}
	}
	var after int
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&after)
	if count != after || researchState(t, s).Control.Version != before.Control.Version {
		t.Fatal("observation consumed work or mutated control")
	}
	call(t, s, "", "research.control", map[string]any{"version": 1, "action": "stop", "reason": "finish inspection"})
	expectFault(t, s, "", "research.control", "conflict", map[string]any{"version": 2, "action": "resume", "reason": "must not revive"})
}

func TestResearchHeartbeatIsProofBoundedAndDoesNotSpendDurableBudget(t *testing.T) {
	s, proof, _ := researchFixture(t)
	expectFault(t, s, "", "research.heartbeat", "forbidden", map[string]any{})
	var before, after int
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&before)
	for range 3 {
		researchRunnerCall(t, s, proof, "research.heartbeat", map[string]any{})
	}
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&after)
	if before != after || researchState(t, s).RunnerHeartbeatAt == "" {
		t.Fatal("heartbeat spent durable receipts or did not report liveness")
	}
	page, err := s.Export(context.Background(), "", 200)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), proof) {
		t.Fatal("runner proof entered owner export")
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if researchState(t, reopened).RunnerHeartbeatAt != "" {
		t.Fatal("broker restart invented a live runner heartbeat")
	}
}
