package main

import (
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/sessioninventory"
	tea "github.com/charmbracelet/bubbletea"
)

func TestSessionSearchDoesNotMatchDisplayedCreationTime(t *testing.T) {
	const created = "2026-11-03T07:18:00Z"
	stamp, err := time.Parse(time.RFC3339, created)
	if err != nil {
		t.Fatal(err)
	}
	query := "Problem " + stamp.Local().Format("15")
	m := console{agents: []managed.AgentInfo{
		{ID: "target", Title: query, CreatedAt: created},
		{ID: "other", Title: "Problem other", CreatedAt: created},
	}, data: consoleData{observed: []sessioninventory.Session{
		{ID: "external-target", Harness: "codex", Title: query, StartedAt: created},
		{ID: "external-other", Harness: "codex", Title: "Problem other", StartedAt: created},
	}}}
	m.compose("find", "", "", query)
	matches := m.sessionMatches()
	if len(matches) != 2 || matches[0].agent != 0 || matches[1].observation != 0 {
		t.Fatalf("displayed timestamps introduced unrelated session matches: %+v", matches)
	}
	if !strings.Contains(matches[0].detail, "added "+sessionAdded(created)) || !strings.Contains(matches[1].detail, "started "+sessionAdded(created)) {
		t.Fatal("creation time disappeared from session details")
	}
}

func TestSessionPickerDistinguishesDuplicateTitlesAndDoesNotRetarget(t *testing.T) {
	m := console{agents: []managed.AgentInfo{
		{ID: "morning", Title: "Fix login", TitleVersion: 2, Config: managedconfig.Agent{Harness: "claude", Workdir: "/work/api"}},
		{ID: "afternoon", Title: "Fix login", Config: managedconfig.Agent{Harness: "codex", Workdir: "/work/api"}},
	}, data: consoleData{observed: []sessioninventory.Session{{ID: "process:9:abc", Harness: "claude", Project: "/work/other", TTY: "pts/9", Title: "Other bug"}}}}
	next, _ := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = next.(console)
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("fix login")})
	m = next.(console)
	matches := m.sessionMatches()
	if len(matches) != 2 || matches[0].agent == matches[1].agent || !strings.Contains(matches[1].detail, "afternoon") {
		t.Fatal("same title collapsed separate session identities")
	}
	if m.selectedID() != "morning" {
		t.Fatal("typing a search silently changed action target")
	}
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(console)
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(console)
	if m.selectedID() != "afternoon" || m.composer != "" || m.tab != 1 {
		t.Fatal("picker selected a title instead of its exact identity")
	}
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	m = next.(console)
	if m.composeTarget != "afternoon" || m.labelVersion != 0 {
		t.Fatal("rename did not freeze ID/version")
	}
	m.selected = 0
	if m.composeTarget != "afternoon" {
		t.Fatal("rename draft followed navigation")
	}
}

func TestSessionPickerFindsExternalTerminalAndHandlesNoMatches(t *testing.T) {
	m := console{agents: []managed.AgentInfo{{ID: "owned"}}, data: consoleData{observed: []sessioninventory.Session{{ID: "process:9:abc", Harness: "claude", Project: "/work/api", TTY: "pts/9", Title: "Login redirect"}}}}
	m.compose("find", "", "", "api pts/9")
	if len(m.sessionMatches()) != 1 {
		t.Fatal("external location not searchable")
	}
	next, _ := m.key(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(console)
	if m.tab != 5 || m.observedSessions()[m.observedSelected].ID != "process:9:abc" {
		t.Fatal("search failed to select observed lifetime")
	}
	m.compose("find", "", "", "nonexistent")
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || next.(console).composer != "find" || !strings.Contains(m.sessionSearchPanel(100, 20), "No sessions match") {
		t.Fatal("empty results triggered an action")
	}
}

func TestSessionIdentityPresentationRetainsIDAndShowsSelectedRoster(t *testing.T) {
	m := console{selected: 8}
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "last"} {
		m.agents = append(m.agents, managed.AgentInfo{ID: id, Title: "Same human title", Config: managedconfig.Agent{Harness: "claude", Workdir: "/work/api"}})
	}
	if !strings.Contains(m.roster(40, 22), "last") {
		t.Fatal("selected session clipped out of roster")
	}
	if label := m.agentLabel("last"); !strings.Contains(label, "Same human title") || !strings.Contains(label, "[last]") {
		t.Fatal("label hid stable identity")
	}
	if sessionTitle(managed.AgentInfo{ID: "opaque", Config: managedconfig.Agent{Harness: "codex", Workdir: "/work/api"}}) != "api · codex" {
		t.Fatal("untitled fallback lacks project/provider")
	}
}

func TestUnnamedDelegatedSessionUsesTemplateAndRetainsLineageAndID(t *testing.T) {
	a := managed.AgentInfo{ID: "child_opaque_lifetime", Parent: "alice", Template: "reviewer", Config: managedconfig.Agent{Harness: "codex", Workdir: "/private/workspaces/child_opaque_lifetime"}}
	if sessionTitle(a) != "reviewer · codex" {
		t.Fatal("delegated fallback used an opaque workspace name")
	}
	m := console{agents: []managed.AgentInfo{a}, text: []rune("alice reviewer")}
	if len(m.sessionMatches()) != 1 || !strings.Contains(m.agentLabel(a.ID), a.ID) {
		t.Fatal("readable fallback lost exact routing identity or parent search")
	}
	a.Title = "Check payment race"
	if sessionTitle(a) != a.Title {
		t.Fatal("template replaced the user's task title")
	}
}
