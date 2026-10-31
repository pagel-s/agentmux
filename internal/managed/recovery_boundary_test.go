package managed

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestLivePermissionsFailClosedWhenRecoveryStateIsMissingOrIncomplete(t *testing.T) {
	for _, state := range []string{"missing", "null", "{}", `{"version":2}`, `{"version":1,"edges":[],"scopes":[]}`, `{"version":2,"edges":null,"scopes":[]}`, `{"version":2,"edges":[],"scopes":[]}`, `{"version":2,"edges":[],"scopes":[]`} {
		t.Run(state, func(t *testing.T) {
			s := testStore(t, testConfig())
			call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": false})
			if state == "missing" {
				if _, err := s.db.Exec("DELETE FROM records WHERE id='live_access'"); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.db.Exec("UPDATE records SET data=? WHERE id='live_access'", state); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := Open(s.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			expectFault(t, reopened, "alice", "instruct", "forbidden", map[string]any{"to": []string{"bob"}, "text": "revoked before recovery"})
			expectFault(t, reopened, "alice", "board.post", "forbidden", map[string]any{"board": "team", "text": "no fallback to launch ACL"})
			expectFault(t, reopened, "", "control.get", "internal", map[string]any{})
		})
	}
}

func TestAutonomyRecoveryCannotResetOwnerPauseOrTurnBudget(t *testing.T) {
	for _, damage := range []string{"missing", "missing-record-and-digest", "null", "{}", "fresh-running-state"} {
		t.Run(damage, func(t *testing.T) {
			s := testStore(t, autonomyConfig())
			state, err := loadAutonomy(context.Background(), s.db, "bob")
			if err != nil {
				t.Fatal(err)
			}
			call(t, s, "", "autonomy.control", map[string]any{"agent": "bob", "version": state.Version, "mode": "paused"})
			if damage == "missing" || damage == "missing-record-and-digest" {
				_, err = s.db.Exec("DELETE FROM records WHERE id='autonomy_bob'")
			} else {
				raw := damage
				if damage == "fresh-running-state" {
					encoded, _ := json.Marshal(autonomyState{Agent: "bob", Mode: "running", Version: state.Version + 1, Pending: []string{}})
					raw = string(encoded)
				}
				_, err = s.db.Exec("UPDATE records SET data=? WHERE id='autonomy_bob'", raw)
			}
			if err != nil {
				t.Fatal(err)
			}
			if damage == "missing-record-and-digest" {
				if _, err = s.db.Exec("DELETE FROM metadata WHERE key='state_digest:autonomy:bob'"); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := Open(s.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			expectFault(t, reopened, "bob", "worker.next", "internal", map[string]any{})
			expectFault(t, reopened, "", "autonomy.status", "internal", map[string]any{"agent": "bob"})
		})
	}
}

func TestConversationRecoveryCannotForgetBusyNativeHistory(t *testing.T) {
	for _, damage := range []string{"missing", "missing-record-and-digest", "null", "{}", "fresh-ready-state"} {
		t.Run(damage, func(t *testing.T) {
			s, owner, worker := conversationFixture(t)
			task, lease := workerTestTask(t, s, owner, worker)
			workerTestCall(t, s, worker.Token, "conversation.begin", map[string]any{"id": task.ID, "lease": lease})
			var err error
			if damage == "missing" || damage == "missing-record-and-digest" {
				_, err = s.db.Exec("DELETE FROM records WHERE id='conversation_worker'")
			} else {
				raw := damage
				if damage == "fresh-ready-state" {
					encoded, _ := json.Marshal(ConversationState{Agent: "worker", Generation: 1, State: "ready"})
					raw = string(encoded)
				}
				_, err = s.db.Exec("UPDATE records SET data=? WHERE id='conversation_worker'", raw)
			}
			if err != nil {
				t.Fatal(err)
			}
			if damage == "missing-record-and-digest" {
				if _, err = s.db.Exec("DELETE FROM metadata WHERE key='state_digest:conversation:worker'"); err != nil {
					t.Fatal(err)
				}
			}
			expectFault(t, s, "worker", "conversation.status", "internal", map[string]any{})
			expectFault(t, s, "worker", "conversation.begin", "internal", map[string]any{"id": task.ID, "lease": lease})
		})
	}
}

func TestIntactLegacyLifecycleRecordsRemainReadableWithoutRecoveryDigests(t *testing.T) {
	s := testStore(t, autonomyConfig())
	call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": false})
	state, err := loadAutonomy(context.Background(), s.db, "bob")
	if err != nil {
		t.Fatal(err)
	}
	state.Pending = nil // Legacy serializers legitimately wrote null for an empty queue.
	if err = saveAutonomy(context.Background(), s.db, state); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DELETE FROM metadata WHERE key LIKE 'state_digest:%' OR key LIKE 'state_initialized:%'"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAutonomy(context.Background(), s.db, "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := controls(context.Background(), s.db); err != nil {
		t.Fatal(err)
	}
	expectFault(t, s, "alice", "instruct", "forbidden", map[string]any{"to": []string{"bob"}, "text": "legacy revoke remains"})
}

func TestUnmarkedLegacyAutonomyRequiresExplicitBudgetCounters(t *testing.T) {
	for _, damage := range []string{"missing-turns", "null-turns", "missing-pending", "missing-cooldown"} {
		t.Run(damage, func(t *testing.T) {
			s := testStore(t, autonomyConfig())
			state, err := loadAutonomy(context.Background(), s.db, "bob")
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "missing-turns":
				delete(fields, "turns_used")
			case "null-turns":
				fields["turns_used"] = json.RawMessage("null")
			case "missing-pending":
				delete(fields, "pending")
			case "missing-cooldown":
				delete(fields, "next_allowed")
			}
			encoded, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec("UPDATE records SET data=? WHERE id='autonomy_bob'", string(encoded)); err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec("DELETE FROM metadata WHERE key IN ('state_digest:autonomy:bob','state_initialized:autonomy:bob')"); err != nil {
				t.Fatal(err)
			}
			expectFault(t, s, "bob", "worker.next", "internal", map[string]any{})
		})
	}
}

func TestNativeWorkspaceAdmissionCountsDistinctDefinitions(t *testing.T) {
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "limit", Agents: map[string]managedconfig.Agent{}}
	for i := 0; i < 199; i++ {
		cfg.Agents[fmt.Sprintf("a%d", i)] = managedconfig.Agent{Harness: "command", Command: []string{"/bin/cat"}}
	}
	s := testStore(t, cfg)
	call(t, s, "", "agent.stop", map[string]any{"id": "a0"})
	definition := managedconfig.Agent{Harness: "command", Interaction: "interactive", Workdir: t.TempDir(), Command: []string{"/bin/cat"}}
	owner := credential(t, s, "")
	type admission struct {
		id      string
		problem *Fault
	}
	results := make(chan admission, 2)
	for _, id := range []string{"lastone", "lasttwo"} {
		go func(id string) {
			_, problem := invoke(s, owner, "agent.add", map[string]any{"id": id, "config": definition}, "admit-"+id)
			results <- admission{id, problem}
		}(id)
	}
	winner := ""
	for i := 0; i < 2; i++ {
		result := <-results
		if result.problem == nil {
			if winner != "" {
				t.Fatal("concurrent additions overfilled final registry slot")
			}
			winner = result.id
		} else if result.problem.Code != "limit" {
			t.Fatalf("unexpected admission error: %v", result.problem)
		}
	}
	if winner == "" {
		t.Fatal("revoked static definition consumed the final registry slot twice")
	}
	expectFault(t, s, "", "agent.add", "limit", map[string]any{"id": "overflow", "config": definition})
	call(t, s, "", "agent.stop", map[string]any{"id": winner})
	expectFault(t, s, "", "agent.add", "limit", map[string]any{"id": "reusecapacity", "config": definition})
}

func TestOwnerNativeAdditionsDoNotSpendDelegatedSpawnBudget(t *testing.T) {
	cfg := dynamicConfig()
	cfg.Limits.MaxSpawns = 1
	s := testStore(t, cfg)
	definition := managedconfig.Agent{Harness: "command", Interaction: "interactive", Workdir: t.TempDir(), Command: []string{"/bin/cat"}}
	call(t, s, "", "agent.add", map[string]any{"id": "manual", "config": definition})
	child := spawned(t, s, "alice", Capabilities{})
	if child.ID == "" {
		t.Fatal("native addition consumed delegated spawn budget")
	}
	expectFault(t, s, "alice", "agent.spawn", "limit", map[string]any{"template": "echo"})
}

func TestScopedKnowledgeCASAndInflightRevocationAcrossRestart(t *testing.T) {
	s := workspaceGroupStore(t)
	c := connectWorkspaceGroup(t, s, 1, "alice", "bob")
	group := c.Group
	call(t, s, "alice", "memory.put", map[string]any{"namespace": group.Memory, "key": "fact", "value": "initial", "version": 0})
	call(t, s, "bob", "memory.put", map[string]any{"namespace": group.Memory, "key": "fact", "value": "updated", "version": 1})
	expectFault(t, s, "alice", "memory.put", "conflict", map[string]any{"namespace": group.Memory, "key": "fact", "value": "stale", "version": 1})
	call(t, s, "", "control.update", map[string]any{"version": 2, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": true})
	var result struct {
		Task  Task   `json:"task"`
		Tasks []Task `json:"tasks"`
	}
	raw := call(t, s, "alice", "instruct", map[string]any{"to": []string{"bob"}, "text": "accepted task"})
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	accepted, lease := claim(t, s)
	call(t, s, "", "control.update", map[string]any{"version": 3, "kind": "independent", "agents": []string{"bob"}})
	expectFault(t, s, "bob", "memory.get", "forbidden", map[string]any{"namespace": group.Memory, "key": "fact"})
	expectFault(t, s, "alice", "instruct", "forbidden", map[string]any{"to": []string{"bob"}, "text": "new task after revoke"})
	// Accepted work is explicit surviving state; permission edits never report
	// a cancellation they did not perform.
	call(t, s, "bob", "task.complete", map[string]any{"id": accepted.ID, "version": accepted.Version, "lease": lease, "status": "submitted", "result": "completed accepted work"})
	old := credential(t, s, "bob")
	if _, err := s.Rotate("bob"); err != nil {
		t.Fatal(err)
	}
	if _, problem := invoke(s, old, "identity", map[string]any{}, ""); problem == nil || problem.Code != "unauthorized" {
		t.Fatal("stale credential survived identity rotation")
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	expectFault(t, reopened, "bob", "memory.get", "forbidden", map[string]any{"namespace": group.Memory, "key": "fact"})
	connectWorkspaceGroup(t, reopened, 4, "bob", "alice")
	raw = call(t, reopened, "bob", "memory.get", map[string]any{"namespace": group.Memory, "key": "fact"})
	var memory struct {
		Entry Memory `json:"entry"`
	}
	if err := json.Unmarshal(raw, &memory); err != nil || memory.Entry.Value != "updated" {
		t.Fatalf("reconnect lost versioned history: %s %v", raw, err)
	}
	if _, err := controls(context.Background(), reopened.db); err != nil {
		t.Fatal(err)
	}
}
