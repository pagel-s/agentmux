package main

import (
	"agentmux/internal/managed"
	"strings"
	"testing"
)

func TestConsoleTeamsDistinguishesRetainedMembershipAndInheritedAccess(t *testing.T) {
	m := teamConsoleFixture()
	m.data.teams[0].Members = append(m.data.teams[0].Members, "bob")
	m.data.teams[0].MemberAccess = map[string]managed.TeamMemberAccess{"alice": {Allowed: true}, "bob": {Reason: "ancestor is not a team member", BlockedBy: "stable-parent-id"}}
	for _, text := range []string{"consented member", "Membership retained", "effective access blocked", "ancestor is not a team member", "stable-parent-id", "Blocked members (1): bob"} {
		if view := m.teamsPanel(80, 24); !strings.Contains(view, text) {
			t.Fatalf("missing %q in %s", text, view)
		}
	}
	m.data.teams[0].MemberAccess["bob"] = managed.TeamMemberAccess{Allowed: true}
	if view := m.teamsPanel(80, 24); !strings.Contains(view, "Current board + memory access allowed") || strings.Contains(view, "access blocked") {
		t.Fatalf("restored snapshot not reflected: %s", view)
	}
	delete(m.data.teams[0].MemberAccess, "bob")
	if view := m.teamsPanel(80, 24); !strings.Contains(view, "Effective access unavailable") || strings.Contains(view, "access allowed") {
		t.Fatalf("missing metadata invented authority: %s", view)
	}
}
