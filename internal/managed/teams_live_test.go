package managed

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func addTeamMember(t *testing.T, s *Store, team Team, id string) Team {
	t.Helper()
	team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "version": team.Version, "agent": id}))
	return teamIn(t, call(t, s, id, "team.join", map[string]any{"id": team.ID, "version": team.Version}))
}
func TestTeamLiveMetadataAndConsentedOwnershipTransfer(t *testing.T) {
	s := testStore(t, dynamicConfig())
	team := teamIn(t, call(t, s, "alice", "team.create", map[string]any{"name": "Before"}))
	team = addTeamMember(t, s, team, "bob")
	expectFault(t, s, "bob", "team.update", "forbidden", map[string]any{"id": team.ID, "version": team.Version, "name": "Not mine"})
	old := team.Version
	team = teamIn(t, call(t, s, "alice", "team.update", map[string]any{"id": team.ID, "version": team.Version, "name": "Investigation", "description": "Two projects; temporary shared review"}))
	if team.Name != "Investigation" || team.Description == "" {
		t.Fatal("metadata not updated")
	}
	expectFault(t, s, "alice", "team.update", "conflict", map[string]any{"id": team.ID, "version": old, "name": "Stale"})
	team = teamIn(t, call(t, s, "alice", "team.transfer", map[string]any{"id": team.ID, "version": team.Version, "agent": "bob"}))
	if team.Owner != "alice" || team.PendingOwner != "bob" {
		t.Fatal("proposal changed owner before consent")
	}
	expectFault(t, s, "alice", "team.accept-owner", "forbidden", map[string]any{"id": team.ID, "version": team.Version})
	expectFault(t, s, "bob", "team.update", "forbidden", map[string]any{"id": team.ID, "version": team.Version, "name": "Still not mine"})
	team = teamIn(t, call(t, s, "bob", "team.accept-owner", map[string]any{"id": team.ID, "version": team.Version}))
	if team.Owner != "bob" || team.PendingOwner != "" {
		t.Fatal("ownership acceptance not recorded")
	}
	expectFault(t, s, "alice", "team.update", "forbidden", map[string]any{"id": team.ID, "version": team.Version, "name": "No longer mine"})
	team = teamIn(t, call(t, s, "alice", "team.leave", map[string]any{"id": team.ID, "version": team.Version}))
	call(t, s, "bob", "board.post", map[string]any{"board": team.Board, "text": "still interactive"})
}
func TestTeamTransferCannotManufactureDelegatedCreatorAuthority(t *testing.T) {
	s := testStore(t, dynamicConfig())
	child := spawned(t, s, "alice", Capabilities{JoinTeams: true})
	team := teamIn(t, call(t, s, "alice", "team.create", map[string]any{"name": "No escalation"}))
	team = addTeamMember(t, s, team, child.ID)
	team = teamIn(t, call(t, s, "alice", "team.transfer", map[string]any{"id": team.ID, "version": team.Version, "agent": child.ID}))
	expectFault(t, s, child.ID, "team.accept-owner", "forbidden", map[string]any{"id": team.ID, "version": team.Version})
	if current := ownerTeamAccess(t, s, team.ID); current.Owner != "alice" || current.Version != team.Version {
		t.Fatal("rejected transfer changed team")
	}
}
func TestTeamMoveRequiresInvitationConsentAndBothVersions(t *testing.T) {
	s := testStore(t, dynamicConfig())
	source := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "Source"}))
	source = addTeamMember(t, s, source, "bob")
	target := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "Destination"}))
	historyPost(t, s, target.Board, "before destination consent")
	args := map[string]any{"id": source.ID, "version": source.Version, "to_id": target.ID, "to_version": target.Version}
	expectFault(t, s, "bob", "team.move", "not_found", args)
	target = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": target.ID, "version": target.Version, "agent": "bob"}))
	expectFault(t, s, "", "team.move", "forbidden", args)
	expectFault(t, s, "bob", "team.move", "conflict", args)
	if !contains(ownerTeamAccess(t, s, source.ID).Members, "bob") {
		t.Fatal("stale destination removed source membership")
	}
	args["to_version"] = target.Version
	cred := credential(t, s, "bob")
	first, p := invoke(s, cred, "team.move", args, "consented-move")
	if p != nil {
		t.Fatal(p)
	}
	replay, p := invoke(s, cred, "team.move", args, "consented-move")
	if p != nil || string(first) != string(replay) {
		t.Fatal("move receipt unstable")
	}
	source = ownerTeamAccess(t, s, source.ID)
	target = ownerTeamAccess(t, s, target.ID)
	if contains(source.Members, "bob") || !contains(target.Members, "bob") || contains(target.Invited, "bob") {
		t.Fatal("move was not atomic")
	}
	expectFault(t, s, "bob", "board.post", "forbidden", map[string]any{"board": source.Board, "text": "left"})
	if raw := call(t, s, "bob", "message.list", map[string]any{"board": target.Board}); strings.Contains(string(raw), "before destination consent") {
		t.Fatal("move exposed preconsent history")
	}
	historyPost(t, s, target.Board, "after destination consent")
	if raw := call(t, s, "bob", "message.list", map[string]any{"board": target.Board}); !strings.Contains(string(raw), "after destination consent") {
		t.Fatal("move lost destination access")
	}
}
func TestTeamMoveStorageFailureRollsBackBothMemberships(t *testing.T) {
	s := testStore(t, dynamicConfig())
	source := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "Source"}))
	source = addTeamMember(t, s, source, "bob")
	target := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "Target"}))
	target = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": target.ID, "version": target.Version, "agent": "bob"}))
	s.cfg.Limits.MaxRecords = 1
	expectFault(t, s, "bob", "team.move", "limit", map[string]any{"id": source.ID, "version": source.Version, "to_id": target.ID, "to_version": target.Version})
	if !contains(ownerTeamAccess(t, s, source.ID).Members, "bob") || contains(ownerTeamAccess(t, s, target.ID).Members, "bob") || !contains(ownerTeamAccess(t, s, target.ID).Invited, "bob") {
		t.Fatal("failed move partially committed")
	}
}
func TestParticipantSeesOwnInheritedDenialWithoutOthersDiagnostics(t *testing.T) {
	s := testStore(t, dynamicConfig())
	child := spawned(t, s, "alice", Capabilities{JoinTeams: true})
	team := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "Lineage"}))
	team = addTeamMember(t, s, team, "alice")
	team = addTeamMember(t, s, team, child.ID)
	team = teamIn(t, call(t, s, "", "team.remove", map[string]any{"id": team.ID, "version": team.Version, "agent": "alice"}))
	var visible struct {
		Teams []Team `json:"teams"`
	}
	if err := json.Unmarshal(call(t, s, child.ID, "team.list", map[string]any{}), &visible); err != nil {
		t.Fatal(err)
	}
	if len(visible.Teams) != 1 || visible.Teams[0].SelfAccess == nil || visible.Teams[0].SelfAccess.Allowed || visible.Teams[0].SelfAccess.BlockedBy != "alice" || visible.Teams[0].MemberAccess != nil {
		t.Fatalf("self diagnostics wrong: %+v", visible)
	}
	var stored Team
	if err := get(context.Background(), s.db, "team", team.ID, &stored); err != nil || stored.SelfAccess != nil {
		t.Fatal("computed self access persisted")
	}
}
