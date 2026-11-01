package managed

import (
	"context"
	"encoding/json"
	"testing"
)

func ownerTeamAccess(t *testing.T, s *Store, id string) Team {
	t.Helper()
	var result struct {
		Teams []Team `json:"teams"`
	}
	if err := json.Unmarshal(call(t, s, "", "team.list", map[string]any{}), &result); err != nil {
		t.Fatal(err)
	}
	for _, team := range result.Teams {
		if team.ID == id {
			return team
		}
	}
	t.Fatal("team missing from owner inspection")
	return Team{}
}

func TestTeamMemberAccessExplainsRecursiveRetainedMembership(t *testing.T) {
	cfg := dynamicConfig()
	cfg.Limits.MaxSpawnDepth = 3
	cfg.Limits.MaxSpawns = 4
	cfg.Limits.MaxTeamMembers = 6
	s := testStore(t, cfg)
	child := spawned(t, s, "alice", Capabilities{JoinTeams: true, SpawnTemplates: []string{"echo"}})
	grandchild := spawned(t, s, child.ID, Capabilities{JoinTeams: true})
	team := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "Inherited knowledge"}))
	join := func(id string) {
		team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": id, "version": team.Version}))
		// Pending invites are not presented as members with effective access.
		if _, ok := ownerTeamAccess(t, s, team.ID).MemberAccess[id]; ok {
			t.Fatal("invitation presented as membership")
		}
		team = teamIn(t, call(t, s, id, "team.join", map[string]any{"id": team.ID, "version": team.Version}))
	}
	for _, id := range []string{"alice", child.ID, grandchild.ID} {
		join(id)
	}
	check := func(id, blockedBy string) {
		t.Helper()
		status, ok := ownerTeamAccess(t, s, team.ID).MemberAccess[id]
		if !ok || status.Allowed != (blockedBy == "") || status.BlockedBy != blockedBy {
			t.Fatalf("wrong effective access for %s: %+v", id, status)
		}
		if blockedBy != "" && status.Reason != "ancestor is not a team member" {
			t.Fatalf("missing inherited reason: %+v", status)
		}
		if actual := s.namespace(context.Background(), s.db, id, team.Memory, true, false); actual != status.Allowed {
			t.Fatalf("inspection differs from actual memory authorization: %+v", status)
		}
		if actual := s.namespace(context.Background(), s.db, id, team.Board, false, true); actual != status.Allowed {
			t.Fatalf("inspection differs from actual board authorization: %+v", status)
		}
	}
	check(child.ID, "")
	check(grandchild.ID, "")
	team = teamIn(t, call(t, s, "", "team.remove", map[string]any{"id": team.ID, "agent": "alice", "version": team.Version}))
	check(child.ID, "alice")
	check(grandchild.ID, "alice")
	expectFault(t, s, grandchild.ID, "board.post", "forbidden", map[string]any{"board": team.Board, "text": "must remain denied"})
	join("alice")
	check(grandchild.ID, "")
	team = teamIn(t, call(t, s, "", "team.remove", map[string]any{"id": team.ID, "agent": child.ID, "version": team.Version}))
	check(grandchild.ID, child.ID)
	call(t, s, "", "agent.stop", map[string]any{"id": grandchild.ID})
	status := ownerTeamAccess(t, s, team.ID).MemberAccess[grandchild.ID]
	if status.Allowed || status.Reason != "agent revoked" || status.BlockedBy != grandchild.ID {
		t.Fatalf("revoked member misreported: %+v", status)
	}
	team = teamIn(t, call(t, s, "", "team.dissolve", map[string]any{"id": team.ID, "version": team.Version}))
	status = ownerTeamAccess(t, s, team.ID).MemberAccess[grandchild.ID]
	if status.Allowed || status.Reason != "team dissolved" {
		t.Fatalf("dissolved team misreported: %+v", status)
	}
	// Inspection metadata never becomes durable team state or participant-visible
	// authorization claims from a previous owner read.
	var stored Team
	if err := get(context.Background(), s.db, "team", team.ID, &stored); err != nil || stored.MemberAccess != nil {
		t.Fatalf("computed access persisted: %+v %v", stored, err)
	}
}

func TestTeamMemberAccessPreservesCreatorAndOwnerChildExceptions(t *testing.T) {
	s := testStore(t, dynamicConfig())
	creator := spawned(t, s, "alice", Capabilities{CreateTeams: true})
	team := teamIn(t, call(t, s, creator.ID, "team.create", map[string]any{"name": "Child-created team"}))
	if !ownerTeamAccess(t, s, team.ID).MemberAccess[creator.ID].Allowed {
		t.Fatal("child creator incorrectly requires parent membership or join delegation")
	}
	ownerChild := spawned(t, s, "", Capabilities{JoinTeams: true})
	team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": ownerChild.ID, "version": team.Version}))
	team = teamIn(t, call(t, s, ownerChild.ID, "team.join", map[string]any{"id": team.ID, "version": team.Version}))
	if !ownerTeamAccess(t, s, team.ID).MemberAccess[ownerChild.ID].Allowed {
		t.Fatal("owner child incorrectly requires owner membership")
	}
	c, err := controls(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	c.Independent = append(c.Independent, ownerChild.ID)
	c.Version++
	if err := saveControls(context.Background(), s.db, c); err != nil {
		t.Fatal(err)
	}
	status := ownerTeamAccess(t, s, team.ID).MemberAccess[ownerChild.ID]
	if status.Allowed || status.Reason != "agent is independent" {
		t.Fatalf("independence ignored: %+v", status)
	}
	var visible struct {
		Teams []Team `json:"teams"`
	}
	if err := json.Unmarshal(call(t, s, creator.ID, "team.list", map[string]any{}), &visible); err != nil {
		t.Fatal(err)
	}
	if len(visible.Teams) != 1 || visible.Teams[0].MemberAccess != nil {
		t.Fatal("owner-only effective access inspection exposed to participants")
	}
}
