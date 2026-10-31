package main

import (
	"errors"
	"strings"
	"testing"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConsoleSnapshotPreservesDistinctActionTargetsAcrossSpawnAdmission(t *testing.T) {
	m := console{agents: []managed.AgentInfo{{ID: "alice"}, {ID: "bob"}}, target: 1, teamMemberIndex: 1, data: consoleData{selected: "alice"}}
	next, _ := m.Update(consoleData{selected: "alice", agents: []managed.AgentInfo{{ID: "admitted"}, {ID: "alice"}, {ID: "bob"}}})
	m = next.(console)
	if m.selectedID() != "alice" || m.agents[m.target].ID != "bob" || m.teamTarget().ID != "bob" {
		t.Fatal("registry admission silently redirected a selected sender or recipient")
	}
}

func TestConsoleOfflineKeepsDraftAndReviewWithoutSubmitting(t *testing.T) {
	m := console{agents: []managed.AgentInfo{{ID: "alice"}}, composer: "board", composeTarget: "research", text: []rune("Keep this draft"), data: consoleData{selected: "alice"}}
	next, _ := m.Update(consoleData{selected: "alice", err: errors.New("broker unavailable"), research: &managed.ResearchStatus{Progress: managed.ResearchProgress{State: "stopped"}}})
	m = next.(console)
	if !m.offline || m.data.research.Progress.State != "stopped" {
		t.Fatal("saved terminal research state was lost")
	}
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(console)
	if cmd != nil || m.composer != "board" || string(m.text) != "Keep this draft" {
		t.Fatal("offline submit cleared or dispatched a draft")
	}
	m.composer = ""
	m.teamAction = &consoleTeamAction{operation: "team.dissolve", teamID: "frozen", version: 3}
	next, cmd = m.key(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(console)
	if cmd != nil || m.teamAction == nil || m.teamAction.teamID != "frozen" {
		t.Fatal("offline confirmation lost or submitted reviewed metadata")
	}
	m.teamAction = nil
	for _, key := range []rune{'c', 'd', 'a'} {
		next, cmd = m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		m = next.(console)
		if cmd != nil {
			t.Fatalf("offline %q submitted a direct control", key)
		}
	}
	if !strings.Contains(m.researchPanel(100), "broker offline") {
		t.Fatal("local saved status was presented as live")
	}
}
