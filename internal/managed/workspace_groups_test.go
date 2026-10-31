package managed

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"agentmux/internal/managedconfig"
)

func workspaceGroupStore(t *testing.T) *Store {
	t.Helper()
	cfg := testConfig()
	cfg.Agents["dave"] = managedconfig.Agent{Harness: "command", Command: []string{"/bin/cat"}}
	cfg.Boards, cfg.Memory, cfg.Policy.Grants = nil, nil, nil
	return testStore(t, cfg)
}

func connectWorkspaceGroup(t *testing.T, s *Store, version int, ids ...string) ControlState {
	t.Helper()
	raw := call(t, s, "", "control.update", map[string]any{"version": version, "kind": "collaboration", "agents": ids, "enabled": true})
	var c ControlState
	if err := json.Unmarshal(raw, &c); err != nil || c.Group == nil {
		t.Fatalf("missing collaboration scope: %s %v", raw, err)
	}
	return c
}

func TestWorkspaceGroupsKeepUnrelatedProjectsSeparate(t *testing.T) {
	s := workspaceGroupStore(t)
	one := connectWorkspaceGroup(t, s, 1, "alice", "bob")
	two := connectWorkspaceGroup(t, s, 2, "eve", "dave")
	if one.Group.Board == two.Group.Board || one.Group.Memory == two.Group.Memory {
		t.Fatal("unrelated member sets reused global coordination scopes")
	}
	call(t, s, "alice", "board.post", map[string]any{"board": one.Group.Board, "text": "project-alpha-secret"})
	call(t, s, "alice", "memory.put", map[string]any{"namespace": one.Group.Memory, "key": "design", "value": "alpha", "version": 0})
	call(t, s, "bob", "memory.get", map[string]any{"namespace": one.Group.Memory, "key": "design"})
	for _, id := range []string{"eve", "dave"} {
		expectFault(t, s, id, "memory.get", "forbidden", map[string]any{"namespace": one.Group.Memory, "key": "design"})
		expectFault(t, s, id, "board.post", "forbidden", map[string]any{"board": one.Group.Board, "text": "unauthorized"})
		raw := call(t, s, id, "message.list", map[string]any{})
		if strings.Contains(string(raw), "project-alpha-secret") {
			t.Fatal("default inbox leaked another group's board")
		}
		raw = call(t, s, id, "boundaries.get", map[string]any{})
		if strings.Contains(string(raw), one.Group.Board) || strings.Contains(string(raw), one.Group.Memory) || strings.Contains(string(raw), one.Group.ID) {
			t.Fatalf("nonmember discovered another group's scopes: %s", raw)
		}
	}
	if raw := call(t, s, "bob", "message.list", map[string]any{}); !strings.Contains(string(raw), "project-alpha-secret") {
		t.Fatal("member's default inbox omitted group board")
	}
	expectFault(t, s, "alice", "control.get", "forbidden", map[string]any{})
	expectFault(t, s, "", "control.update", "invalid", map[string]any{"version": 3, "kind": "memory", "agent": "eve", "namespace": one.Group.Memory, "read": true, "write": true})
	expectFault(t, s, "eve", "message.send", "forbidden", map[string]any{"to": []string{"alice"}, "text": "no cross-group edge"})
}

func TestWorkspaceGroupReconnectIsDurableAndPreservesScopedHistory(t *testing.T) {
	s := workspaceGroupStore(t)
	c := connectWorkspaceGroup(t, s, 1, "bob", "alice")
	group := *c.Group
	call(t, s, "alice", "memory.put", map[string]any{"namespace": group.Memory, "key": "saved", "value": "retained", "version": 0})
	call(t, s, "", "control.update", map[string]any{"version": 2, "kind": "independent", "agents": []string{"alice"}})
	expectFault(t, s, "alice", "memory.get", "forbidden", map[string]any{"namespace": group.Memory, "key": "saved"})
	expectFault(t, s, "", "control.update", "invalid", map[string]any{"version": 3, "kind": "memory", "agent": "alice", "namespace": group.Memory, "read": true})
	call(t, s, "bob", "memory.get", map[string]any{"namespace": group.Memory, "key": "saved"})
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	c = connectWorkspaceGroup(t, reopened, 3, "alice", "bob")
	if c.Group.ID != group.ID || c.Group.Board != group.Board || c.Group.Memory != group.Memory || !reflect.DeepEqual(c.Group.Members, []string{"alice", "bob"}) {
		t.Fatalf("reconnect replaced group identity: %+v %+v", group, c.Group)
	}
	call(t, reopened, "alice", "memory.get", map[string]any{"namespace": group.Memory, "key": "saved"})
	stored, err := controls(context.Background(), reopened.db)
	if err != nil || stored.Group != nil || len(stored.Groups) != 0 {
		t.Fatalf("ephemeral group response persisted in live control record: %+v %v", stored, err)
	}
	var owner ControlState
	if err := json.Unmarshal(call(t, reopened, "", "control.get", map[string]any{}), &owner); err != nil || len(owner.Groups) != 1 {
		t.Fatalf("owner cannot observe durable group: %+v %v", owner, err)
	}
}

func TestWorkspaceGroupMutationIsAtomicIdempotentAndVersioned(t *testing.T) {
	s := workspaceGroupStore(t)
	owner := credential(t, s, "")
	input := map[string]any{"version": 1, "kind": "collaboration", "agents": []string{"alice", "bob"}, "enabled": true}
	one, p := invoke(s, owner, "control.update", input, "group-create")
	if p != nil {
		t.Fatal(p)
	}
	two, p := invoke(s, owner, "control.update", input, "group-create")
	if p != nil || string(one) != string(two) {
		t.Fatalf("unstable group mutation receipt: %s %s %v", one, two, p)
	}
	expectFault(t, s, "", "control.update", "conflict", input)
	groups, err := workspaceGroups(context.Background(), s.db)
	if err != nil || len(groups) != 1 {
		t.Fatalf("replayed mutation created duplicate group: %+v %v", groups, err)
	}
	c := connectWorkspaceGroup(t, s, 2, "alice", "bob")
	call(t, s, "", "control.update", map[string]any{"version": 3, "kind": "memory", "agent": "alice", "namespace": c.Group.Memory, "read": false, "write": false})
	expectFault(t, s, "alice", "memory.list", "forbidden", map[string]any{"namespace": c.Group.Memory})
	connectWorkspaceGroup(t, s, 4, "alice", "bob")
	call(t, s, "alice", "memory.list", map[string]any{"namespace": c.Group.Memory})
	call(t, s, "", "control.update", map[string]any{"version": 5, "kind": "collaboration", "agents": []string{"bob", "alice"}, "enabled": false})
	expectFault(t, s, "bob", "memory.list", "forbidden", map[string]any{"namespace": c.Group.Memory})
	// Exhaustion after dispatch must roll the new scope and edges back together.
	s.cfg.Limits.MaxRecords = 1
	expectFault(t, s, "", "control.update", "limit", map[string]any{"version": 6, "kind": "collaboration", "agents": []string{"eve", "dave"}, "enabled": true})
	groups, err = workspaceGroups(context.Background(), s.db)
	if err != nil || len(groups) != 1 {
		t.Fatalf("failed transaction left a workspace group: %+v %v", groups, err)
	}
	current, err := controls(context.Background(), s.db)
	if err != nil || current.Version != 6 {
		t.Fatalf("failed group mutation changed live permissions: %+v %v", current, err)
	}
}

func TestWorkspaceGroupExplicitGlobalScopesRemainAnOwnerChoice(t *testing.T) {
	s := testStore(t, testConfig())
	raw := call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "collaboration", "agents": []string{"alice", "bob"}, "enabled": true, "board": "team", "memory": "team"})
	var c ControlState
	if err := json.Unmarshal(raw, &c); err != nil || c.Group != nil {
		t.Fatalf("explicit global scope unexpectedly created a group: %s %v", raw, err)
	}
	groups, err := workspaceGroups(context.Background(), s.db)
	if err != nil || len(groups) != 0 {
		t.Fatalf("explicit scopes created implicit groups: %+v %v", groups, err)
	}
	call(t, s, "bob", "memory.put", map[string]any{"namespace": "team", "key": "explicit", "value": "shared", "version": 0})
}

func TestWorkspaceGroupsHaveABoundedLifetimeMemberSetRegistry(t *testing.T) {
	s := workspaceGroupStore(t)
	for n := 0; n < 128; n++ {
		g := WorkspaceGroup{ID: randomID("group"), State: "inactive", Members: []string{}}
		if err := save(context.Background(), s.db, "workspace_group", g.ID, ownerID, "", "owner", g); err != nil {
			t.Fatal(err)
		}
	}
	expectFault(t, s, "", "control.update", "limit", map[string]any{"version": 1, "kind": "collaboration", "agents": []string{"alice", "bob"}, "enabled": true})
	c, err := controls(context.Background(), s.db)
	if err != nil || c.Version != 1 {
		t.Fatalf("exhausted group registry changed permissions: %+v %v", c, err)
	}
}
