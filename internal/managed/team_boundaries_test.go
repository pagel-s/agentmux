package managed

import (
	"encoding/json"
	"testing"
)

func teamBoundary(t *testing.T, s *Store, viewer, agent string) Boundaries {
	t.Helper()
	var b Boundaries
	if err := json.Unmarshal(call(t, s, viewer, "boundaries.get", map[string]any{"agent": agent}), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func findTeamScope(scopes []BoundaryScope, name string) (BoundaryScope, bool) {
	for _, scope := range scopes {
		if scope.Name == name {
			return scope, true
		}
	}
	return BoundaryScope{}, false
}

func TestTeamBoundariesFollowConsentAndInvalidatePermissionSnapshots(t *testing.T) {
	s := testStore(t, dynamicConfig())
	initial := teamBoundary(t, s, "", "bob").Version
	team := teamIn(t, call(t, s, "alice", "team.create", map[string]any{"name": "Live collaboration"}))
	created := teamBoundary(t, s, "", "bob")
	if created.Version <= initial {
		t.Fatal("team creation did not invalidate boundary snapshots")
	}
	for _, item := range []struct {
		scopes []BoundaryScope
		name   string
	}{{created.Boards, team.Board}, {created.Memory, team.Memory}} {
		scope, ok := findTeamScope(item.scopes, item.name)
		if !ok || scope.Read || scope.Write || scope.Source != "team" || scope.TeamID != team.ID || scope.Label != team.Name {
			t.Fatalf("owner cannot distinguish team source from effective access: %+v", scope)
		}
	}
	team = teamIn(t, call(t, s, "alice", "team.invite", map[string]any{"id": team.ID, "agent": "bob", "version": team.Version}))
	invited := teamBoundary(t, s, "bob", "bob")
	if invited.Version <= created.Version {
		t.Fatal("invitation did not advance boundary revision")
	}
	if _, ok := findTeamScope(invited.Memory, team.Memory); ok {
		t.Fatal("invitation silently granted member memory access")
	}
	args := map[string]any{"id": team.ID, "version": team.Version}
	joinedReceipt, problem := invoke(s, credential(t, s, "bob"), "team.join", args, "reviewed-team-consent")
	if problem != nil {
		t.Fatal(problem)
	}
	team = teamIn(t, joinedReceipt)
	joined := teamBoundary(t, s, "bob", "bob")
	for _, item := range []struct {
		scopes []BoundaryScope
		name   string
	}{{joined.Boards, team.Board}, {joined.Memory, team.Memory}} {
		scope, ok := findTeamScope(item.scopes, item.name)
		if !ok || !scope.Read || !scope.Write || scope.Source != "team" || scope.TeamID != team.ID {
			t.Fatalf("consented namespace absent from effective boundaries: %+v", joined)
		}
	}
	call(t, s, "bob", "memory.put", map[string]any{"namespace": team.Memory, "key": "finding", "value": "Member knowledge", "version": 0})
	team = teamIn(t, call(t, s, "", "team.remove", map[string]any{"id": team.ID, "agent": "bob", "version": team.Version}))
	removed := teamBoundary(t, s, "bob", "bob")
	if removed.Version <= joined.Version {
		t.Fatal("departure did not invalidate selected permission snapshot")
	}
	if _, ok := findTeamScope(removed.Memory, team.Memory); ok {
		t.Fatal("former participant still sees member memory scope")
	}
	expectFault(t, s, "bob", "memory.get", "forbidden", map[string]any{"namespace": team.Memory, "key": "finding"})
	expectFault(t, s, "", "control.update", "conflict", map[string]any{"version": joined.Version, "kind": "edge", "from": "alice", "to": "bob", "action": "message.send", "allow": false})
	if _, problem := invoke(s, credential(t, s, "bob"), "team.join", args, "reviewed-team-consent"); problem != nil {
		t.Fatal(problem)
	}
	if replay := teamBoundary(t, s, "bob", "bob"); replay.Version != removed.Version {
		t.Fatal("cached join receipt replay changed current permissions")
	}
	teamIn(t, call(t, s, "", "team.dissolve", map[string]any{"id": team.ID, "version": team.Version}))
	final := teamBoundary(t, s, "", "alice")
	if final.Version <= removed.Version {
		t.Fatal("dissolution did not invalidate boundary snapshot")
	}
	if _, ok := findTeamScope(final.Boards, team.Board); ok {
		t.Fatal("dissolved team still advertised a live board")
	}
	var events struct {
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal(call(t, s, "", "events", map[string]any{}), &events); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events.Events {
		if event.Type == "team.remove" && event.EntityID == team.ID {
			found = event.Actor == ownerID && event.Details["subject"] == "bob" && event.Details["before_members"] != nil && event.Details["members"] != nil
		}
	}
	if !found {
		t.Fatal("membership event omitted actor, subject or before/after membership")
	}
}

func TestIndependenceKeepsRevisionMonotonicAcrossNestedTeamRemovals(t *testing.T) {
	s := testStore(t, dynamicConfig())
	for _, name := range []string{"one", "two"} {
		team := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": name}))
		team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": "alice", "version": team.Version}))
		teamIn(t, call(t, s, "alice", "team.join", map[string]any{"id": team.ID, "version": team.Version}))
	}
	prior := teamBoundary(t, s, "", "alice").Version
	call(t, s, "", "control.update", map[string]any{"version": prior, "kind": "independent", "agents": []string{"alice"}})
	current := teamBoundary(t, s, "", "alice")
	if current.Version < prior+3 {
		t.Fatalf("nested team removal revisions were overwritten: %d -> %d", prior, current.Version)
	}
	for _, scope := range current.Memory {
		if scope.Read || scope.Write {
			t.Fatal("independence retained shared knowledge access")
		}
	}
}
