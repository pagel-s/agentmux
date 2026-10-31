package managed

import (
	"context"
	"strings"
	"testing"
)

func namedTeamCapabilities(team Team, join bool) Capabilities {
	return Capabilities{BoardsRead: []string{team.Board}, BoardsWrite: []string{team.Board}, MemoryRead: []string{team.Memory}, MemoryWrite: []string{team.Memory}, JoinTeams: join}
}

func assertNoTeamKnowledge(t *testing.T, s *Store, id string, team Team) {
	t.Helper()
	expectFault(t, s, id, "memory.get", "forbidden", map[string]any{"namespace": team.Memory, "key": "finding"})
	expectFault(t, s, id, "memory.put", "forbidden", map[string]any{"namespace": team.Memory, "key": "unauthorized", "value": "blocked", "version": 0})
	expectFault(t, s, id, "board.post", "forbidden", map[string]any{"board": team.Board, "text": "blocked"})
	ctx := context.Background()
	for _, memory := range []bool{false, true} {
		name := team.Board
		if memory {
			name = team.Memory
		}
		for _, write := range []bool{false, true} {
			if s.namespace(ctx, s.db, id, name, memory, write) {
				t.Fatalf("named team namespace escaped consent gate: %s %s", id, name)
			}
		}
	}
}

func TestTeamNamespacesRequireChildConsentDespiteNamedDelegation(t *testing.T) {
	s := testStore(t, dynamicConfig())
	team := teamIn(t, call(t, s, "alice", "team.create", map[string]any{"name": "Consent is not delegation"}))
	call(t, s, "alice", "memory.put", map[string]any{"namespace": team.Memory, "key": "finding", "value": "team-only knowledge", "version": 0})
	for _, join := range []bool{false, true} {
		child := spawned(t, s, "alice", namedTeamCapabilities(team, join))
		assertNoTeamKnowledge(t, s, child.ID, team)
		team = teamIn(t, call(t, s, "alice", "team.invite", map[string]any{"id": team.ID, "agent": child.ID, "version": team.Version}))
		assertNoTeamKnowledge(t, s, child.ID, team)
		if !join {
			expectFault(t, s, child.ID, "team.join", "forbidden", map[string]any{"id": team.ID, "version": team.Version})
			continue
		}
		team = teamIn(t, call(t, s, child.ID, "team.join", map[string]any{"id": team.ID, "version": team.Version}))
		if raw := call(t, s, child.ID, "memory.get", map[string]any{"namespace": team.Memory, "key": "finding"}); !strings.Contains(string(raw), "team-only knowledge") {
			t.Fatal("consented child cannot read team memory")
		}
		call(t, s, child.ID, "board.post", map[string]any{"board": team.Board, "text": "consented"})
		team = teamIn(t, call(t, s, "", "team.remove", map[string]any{"id": team.ID, "agent": child.ID, "version": team.Version}))
		assertNoTeamKnowledge(t, s, child.ID, team)
	}
}

func TestOwnerChildTeamAccessCannotBypassRemovalDissolutionOrIndependence(t *testing.T) {
	s := testStore(t, dynamicConfig())
	team := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "Owner child consent"}))
	call(t, s, "", "memory.put", map[string]any{"namespace": team.Memory, "key": "finding", "value": "private", "version": 0})
	child := spawned(t, s, "", namedTeamCapabilities(team, true))
	assertNoTeamKnowledge(t, s, child.ID, team)
	// Historical/raw owner overrides must not convert consent-based team scopes
	// into ordinary ACL namespaces, including when the child has no parent cap.
	c, err := controls(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	c.Scopes = append(c.Scopes, ScopeControl{Agent: child.ID, Kind: "board", Namespace: team.Board, Read: true, Write: true}, ScopeControl{Agent: child.ID, Kind: "memory", Namespace: team.Memory, Read: true, Write: true})
	c.Version++
	if err := saveControls(context.Background(), s.db, c); err != nil {
		t.Fatal(err)
	}
	assertNoTeamKnowledge(t, s, child.ID, team)
	join := func() {
		team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": child.ID, "version": team.Version}))
		team = teamIn(t, call(t, s, child.ID, "team.join", map[string]any{"id": team.ID, "version": team.Version}))
		call(t, s, child.ID, "memory.get", map[string]any{"namespace": team.Memory, "key": "finding"})
	}
	join()
	c, err = controls(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	c.Independent = append(c.Independent, child.ID)
	c.Version++
	if err := saveControls(context.Background(), s.db, c); err != nil {
		t.Fatal(err)
	}
	assertNoTeamKnowledge(t, s, child.ID, team)
	c.Independent = without(c.Independent, child.ID)
	c.Version++
	if err := saveControls(context.Background(), s.db, c); err != nil {
		t.Fatal(err)
	}
	call(t, s, child.ID, "memory.get", map[string]any{"namespace": team.Memory, "key": "finding"})
	team = teamIn(t, call(t, s, "", "team.remove", map[string]any{"id": team.ID, "agent": child.ID, "version": team.Version}))
	assertNoTeamKnowledge(t, s, child.ID, team)
	join()
	team = teamIn(t, call(t, s, "", "team.dissolve", map[string]any{"id": team.ID, "version": team.Version}))
	assertNoTeamKnowledge(t, s, child.ID, team)
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertNoTeamKnowledge(t, reopened, child.ID, team)
}

func TestConfiguredNamespaceDelegationRemainsAvailable(t *testing.T) {
	s := testStore(t, dynamicConfig())
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "finding", "value": "configured namespace", "version": 0})
	child := spawned(t, s, "alice", Capabilities{BoardsRead: []string{"team"}, BoardsWrite: []string{"team"}, MemoryRead: []string{"team"}, MemoryWrite: []string{"team"}})
	call(t, s, child.ID, "memory.get", map[string]any{"namespace": "team", "key": "finding"})
	call(t, s, child.ID, "memory.put", map[string]any{"namespace": "team", "key": "child-note", "value": "delegated", "version": 0})
	call(t, s, child.ID, "board.post", map[string]any{"board": "team", "text": "delegated configured board"})
}
