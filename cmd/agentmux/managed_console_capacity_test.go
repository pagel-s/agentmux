package main

import (
	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	tea "github.com/charmbracelet/bubbletea"
	"reflect"
	"strings"
	"testing"
)

func TestConsoleMetadataEditorPreservesIdentityAndRequiresFinalReview(t *testing.T) {
	m := teamConsoleFixture()
	m.reviewTeamAction("team.update")
	if m.teamAction == nil || m.teamAction.editKind != "Team name" {
		t.Fatal("metadataeditor not opened")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("New title"), Paste: true})
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.teamAction.editKind != "Description" || m.busy {
		t.Fatal("firstfield appliedwrite")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Scoped review"), Paste: true})
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.teamAction.editKind != "" || m.busy || !reflect.DeepEqual(m.teamAction.args(), map[string]any{"id": "team_original", "version": 7, "name": "New title", "description": "Scoped review"}) {
		t.Fatalf("metadata review lost snapshot %+v", m.teamAction)
	}
	if !strings.Contains(m.teamReviewPanel(80, 20), "Scoped review") {
		t.Fatal("description hiddenfromreview")
	}
	m.data.teams[0].Version = 99
	if m.teamAction.args()["version"] != 7 {
		t.Fatal("poll retargetedversion")
	}
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.busy {
		t.Fatal("finalreviewnotapplied")
	}
}
func TestConsoleBudgetEditorKeepsPermanentChargesAndFrozenVersionVisible(t *testing.T) {
	m := console{tab: 1, agents: []managed.AgentInfo{{ID: "alice", State: "active"}}, data: consoleData{capacity: managed.Capacity{Agent: "alice", ResourceVersion: 4}, budget: managedconfig.AgentBudget{MaxTurnTokens: 10, MaxTurnCostMicrousd: 20, MaxSubtreeTokens: 100, MaxSubtreeCostMicrousd: 200}}}
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'B'}})
	if cmd != nil || m.teamAction == nil {
		t.Fatal("budgeteditor missing")
	}
	for i := 0; i < 4; i++ {
		m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
		if cmd != nil || m.busy {
			t.Fatal("fieldeditappliedwrite")
		}
	}
	m.data.capacity.ResourceVersion = 99
	if m.teamAction.version != 4 || m.teamAction.args()["agent"] != "alice" {
		t.Fatal("resourceeditfollowedpoll")
	}
	for _, text := range []string{"Resource version: 4", "Existing charges", "native inference", "not hard capped"} {
		if !strings.Contains(m.budgetReviewPanel(100, 20), text) {
			t.Fatalf("reviewomits%q", text)
		}
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || m.teamAction != nil || m.busy {
		t.Fatal("cancelledresourceeditorwrote")
	}
}
func TestConsoleScopesExplainEffectiveVersusStoredAndOwnerProposalConsent(t *testing.T) {
	allow := true
	m := console{tab: 3, agents: []managed.AgentInfo{{ID: "alice"}}, data: consoleData{boundaries: managed.Boundaries{Agent: "alice", Version: 3, Memory: []managed.BoundaryScope{{Name: "memory_group", Source: "group", StoredRead: &allow, StoredWrite: &allow, Read: false, Write: false, Reason: "collaboration group inactive"}}}}}
	panel := m.panel(110, 20)
	for _, text := range []string{"Effective: read denied", "Stored owner intent: read allowed", "collaboration group inactive"} {
		if !strings.Contains(panel, text) {
			t.Fatalf("scopehides%q", text)
		}
	}
	m = teamConsoleFixture()
	m.data.teams[0].Members = append(m.data.teams[0].Members, "bob")
	m.reviewTeamAction("team.transfer")
	if m.teamAction == nil || m.teamAction.agentID != "bob" || !strings.Contains(m.teamReviewPanel(90, 20), "Only the proposed member can accept") {
		t.Fatal("transferproposalimpliesforcedownership")
	}
}

func TestConsoleLegacyCollaborationRequiresExplicitChoiceAndFreezesPolicy(t *testing.T) {
	m := console{tab: 2, width: 80, height: 24, target: 1, agents: []managed.AgentInfo{{ID: "alice", State: "active"}, {ID: "bob", State: "active"}}, data: consoleData{controls: managed.ControlState{Version: 9, Groups: []managed.WorkspaceGroup{{ID: "group_old", Agents: []string{"alice", "bob"}, Members: []string{"alice", "bob"}, State: "active", Version: 3}}, Edges: []managed.EdgeControl{{From: "alice", To: "bob", Action: "message.send", Allow: true}, {From: "alice", To: "bob", Action: "instruct", Allow: false}}}}}
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if cmd != nil || m.teamAction == nil || m.teamAction.legacyPolicy != "" || m.busy {
		t.Fatal("legacyconnectskippedexplicitreview")
	}
	for _, text := range []string{"1 preserve", "2 group", "Enter applies", "Esc cancels", "allows 1", "denies 1", "none (required)"} {
		if !strings.Contains(m.View(), text) {
			t.Fatalf("legacyreviewcriticaldetailhidden%q", text)
		}
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.busy || m.teamAction == nil {
		t.Fatal("defaultlegacychoiceapplied")
	}
	m.data.controls.Version = 12
	m.data.controls.Groups[0].Agents = []string{"alice", "eve"}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if cmd != nil || m.teamAction.args()["version"] != 9 || m.teamAction.args()["legacy_policy"] != "group" || !reflect.DeepEqual(m.teamAction.args()["agents"], []string{"alice", "bob"}) {
		t.Fatal("migrationreviewretargetedafterpoll")
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.busy || m.teamAction != nil {
		t.Fatal("explicitreviewnotsubmitted")
	}
}
