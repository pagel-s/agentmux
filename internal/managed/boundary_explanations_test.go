package managed

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBoundaryExplanationsSeparateSuspendedStoredIntentAndEffectiveAccess(t *testing.T) {
	s := testStore(t, testConfig())
	call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "memory", "agent": "alice", "namespace": "team", "read": true, "write": true})
	call(t, s, "", "control.update", map[string]any{"version": 2, "kind": "independent", "agents": []string{"alice"}})
	var b Boundaries
	if err := json.Unmarshal(call(t, s, "", "boundaries.get", map[string]any{"agent": "alice"}), &b); err != nil {
		t.Fatal(err)
	}
	scope, ok := findTeamScope(b.Memory, "team")
	if !ok || scope.Read || scope.Write || !scope.Suspended || scope.StoredRead == nil || !*scope.StoredRead || !strings.Contains(scope.Reason, "suspended") {
		t.Fatalf("stored intent disguised effective authority %+v", scope)
	}
	connectWorkspaceGroup(t, s, 3, "alice", "bob")
	b = Boundaries{}
	if err := json.Unmarshal(call(t, s, "", "boundaries.get", map[string]any{"agent": "alice"}), &b); err != nil {
		t.Fatal(err)
	}
	scope, _ = findTeamScope(b.Memory, "team")
	if !scope.Read || !scope.Write || scope.Suspended {
		t.Fatalf("reconnect lost saved intent %+v", scope)
	}
	call(t, s, "", "control.update", map[string]any{"version": 4, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": false})
	b = Boundaries{}
	if err := json.Unmarshal(call(t, s, "", "boundaries.get", map[string]any{"agent": "alice"}), &b); err != nil {
		t.Fatal(err)
	}
	for _, target := range b.Targets {
		if target.To == "bob" {
			if target.DeniedReasons["instruct"] != "explicit owner deny" || target.Sources["message.send"] == "" {
				t.Fatalf("missing authority reasons %+v", target)
			}
			return
		}
	}
	t.Fatal("missing target")
}
func TestResetCanCleanHistoricalOverridesAfterMembershipOrAgentRevocation(t *testing.T) {
	s := workspaceGroupStore(t)
	c := connectWorkspaceGroup(t, s, 1, "alice", "bob")
	call(t, s, "", "control.update", map[string]any{"version": 2, "kind": "memory", "agent": "alice", "namespace": c.Group.Memory, "read": true, "write": true})
	call(t, s, "", "control.update", map[string]any{"version": 3, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": true})
	call(t, s, "", "control.update", map[string]any{"version": 4, "kind": "collaboration", "agents": []string{"alice", "bob"}, "enabled": false})
	call(t, s, "", "control.update", map[string]any{"version": 5, "kind": "memory", "agent": "alice", "namespace": c.Group.Memory, "reset": true})
	call(t, s, "", "agent.stop", map[string]any{"id": "alice"})
	call(t, s, "", "control.update", map[string]any{"version": 6, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "reset": true})
	var control ControlState
	if err := json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &control); err != nil {
		t.Fatal(err)
	}
	if len(control.Edges) != 0 || len(control.Scopes) != 0 {
		t.Fatal("historical records cannot be cleaned")
	}
}
