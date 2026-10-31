package managed

import (
	"context"
	"encoding/json"
	"testing"
)

func TestIndependenceFencesConfiguredSpawnAndTeamAuthorityDurably(t *testing.T) {
	s := testStore(t, dynamicConfig())
	identity := credential(t, s, "alice")
	call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "independent", "agents": []string{"alice"}})
	assertFenced := func(store *Store) {
		t.Helper()
		var identity struct {
			SpawnTemplates []string `json:"spawn_templates"`
		}
		if err := json.Unmarshal(call(t, store, "alice", "identity", map[string]any{}), &identity); err != nil || len(identity.SpawnTemplates) != 0 {
			t.Fatalf("independent identity advertised unavailable spawn templates: %+v %v", identity, err)
		}
		expectFault(t, store, "alice", "agent.spawn", "forbidden", map[string]any{"template": "echo"})
		expectFault(t, store, "alice", "team.create", "forbidden", map[string]any{"name": "ambient authority must stay disabled"})
		expectFault(t, store, "alice", "board.post", "forbidden", map[string]any{"board": "public", "text": "wildcard cannot reconnect me"})
		expectFault(t, store, "bob", "message.send", "forbidden", map[string]any{"to": []string{"alice"}, "text": "still independent"})
	}
	assertFenced(s)
	team := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "Owner invited team"}))
	team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": "alice", "version": team.Version}))
	expectFault(t, s, "alice", "team.join", "forbidden", map[string]any{"id": team.ID, "version": team.Version})
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "private", "key": "personal", "value": "keep working interactively", "version": 0})
	if credential(t, s, "alice") != identity {
		t.Fatal("independence replaced the native session identity")
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertFenced(reopened)
	expectFault(t, reopened, "alice", "team.join", "forbidden", map[string]any{"id": team.ID, "version": team.Version})
	var controls ControlState
	if err := json.Unmarshal(call(t, reopened, "", "control.get", map[string]any{}), &controls); err != nil || !contains(controls.Independent, "alice") {
		t.Fatalf("independence fence was not durable: %+v %v", controls, err)
	}
}

func TestOwnerCollaborationIntentionallyReenablesOnlyConfiguredDelegation(t *testing.T) {
	s := testStore(t, dynamicConfig())
	call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "independent", "agents": []string{"alice", "bob"}})
	expectFault(t, s, "alice", "agent.spawn", "forbidden", map[string]any{"template": "echo"})
	call(t, s, "", "control.update", map[string]any{"version": 2, "kind": "collaboration", "agents": []string{"alice", "bob"}, "enabled": true})
	var identity struct {
		SpawnTemplates []string `json:"spawn_templates"`
	}
	if err := json.Unmarshal(call(t, s, "alice", "identity", map[string]any{}), &identity); err != nil || !contains(identity.SpawnTemplates, "echo") {
		t.Fatalf("reenabled configured spawn absent from effective identity: %+v %v", identity, err)
	}
	child := spawned(t, s, "alice", Capabilities{})
	if child.Parent != "alice" {
		t.Fatal("reenabled configured spawn lost its parent identity")
	}
	teamIn(t, call(t, s, "alice", "team.create", map[string]any{"name": "Intentionally reenabled"}))
	// The owner action removes the fence; it does not invent spawn or team
	// creation capabilities absent from the immutable launch configuration.
	expectFault(t, s, "bob", "agent.spawn", "forbidden", map[string]any{"template": "echo"})
	expectFault(t, s, "bob", "team.create", "forbidden", map[string]any{"name": "Never configured"})
	var controls ControlState
	if err := json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &controls); err != nil || contains(controls.Independent, "alice") || contains(controls.Independent, "bob") {
		t.Fatalf("owner collaboration retained isolation fence: %+v %v", controls, err)
	}
}

func TestLateDescendantCannotRestoreIndependentParentsImplicitEdges(t *testing.T) {
	s := testStore(t, dynamicConfig())
	call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "independent", "agents": []string{"alice"}})
	// Model a descendant observed after the owner cut coordination. It has no
	// explicit edge overrides yet, so the parent/child defaults must be fenced.
	late := AgentInfo{ID: "late-child", Parent: "alice", State: "active", Config: testConfig().Agents["bob"], Capabilities: Capabilities{BoardsRead: []string{"public"}, BoardsWrite: []string{"public"}}}
	if err := save(context.Background(), s.db, "agent", "agent_"+late.ID, "alice", late.ID, "", late); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rotate(late.ID); err != nil {
		t.Fatal(err)
	}
	for from, to := range map[string]string{"alice": late.ID, late.ID: "alice"} {
		for _, action := range []string{"message.send", "ping.send", "instruct"} {
			expectFault(t, s, from, action, "forbidden", map[string]any{"to": []string{to}, "text": "implicit descendants cannot reconnect"})
		}
	}
	expectFault(t, s, late.ID, "board.post", "forbidden", map[string]any{"board": "public", "text": "parent no longer has shared scope"})
}

func TestExplicitOwnerGrantDoesNotRemoveIndependentTeamAndSpawnFence(t *testing.T) {
	s := testStore(t, dynamicConfig())
	call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "independent", "agents": []string{"alice"}})
	call(t, s, "", "control.update", map[string]any{"version": 2, "kind": "edge", "from": "alice", "to": "bob", "action": "message.send", "allow": true})
	call(t, s, "", "control.update", map[string]any{"version": 3, "kind": "board", "agent": "alice", "namespace": "team", "read": true, "write": true})
	call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "explicitly permitted note"})
	call(t, s, "alice", "board.post", map[string]any{"board": "team", "text": "explicitly permitted shared context"})
	expectFault(t, s, "bob", "message.send", "forbidden", map[string]any{"to": []string{"alice"}, "text": "reverse was not granted"})
	expectFault(t, s, "alice", "ping.send", "forbidden", map[string]any{"to": []string{"bob"}, "text": "ping was not granted"})
	expectFault(t, s, "alice", "board.post", "forbidden", map[string]any{"board": "public", "text": "other wildcard scope stays fenced"})
	expectFault(t, s, "alice", "agent.spawn", "forbidden", map[string]any{"template": "echo"})
	expectFault(t, s, "alice", "team.create", "forbidden", map[string]any{"name": "note grant is not team authority"})
	team := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "No implicit join"}))
	team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": "alice", "version": team.Version}))
	expectFault(t, s, "alice", "team.join", "forbidden", map[string]any{"id": team.ID, "version": team.Version})
	if !independent(context.Background(), s.db, "alice") {
		t.Fatal("an explicit peer/scope grant removed the ambient coordination fence")
	}
}
