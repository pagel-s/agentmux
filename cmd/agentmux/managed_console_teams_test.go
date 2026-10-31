package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	tea "github.com/charmbracelet/bubbletea"
)

func teamConsoleFixture() console {
	return console{
		tab: 6, width: 80, height: 24, teamMemberIndex: 1,
		agents: []managed.AgentInfo{{ID: "alice", State: "active"}, {ID: "bob", State: "active"}, {ID: "carol", State: "active"}},
		data:   consoleData{teams: []managed.Team{{ID: "team_original", Name: "Research pair", Owner: "alice", Members: []string{"alice"}, State: "active", Version: 7, Board: "board_original", Memory: "memory_original"}}},
	}
}

func TestConsoleResearchReasonPreservesPhysicalSpaceKeys(t *testing.T) {
	m := teamConsoleFixture()
	m.teamAction = &consoleTeamAction{operation: "research.control", researchAction: "step", version: 2}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("Inspect")}, {Type: tea.KeySpace}, {Type: tea.KeyRunes, Runes: []rune("teams")}} {
		m, _ = consolePress(m, key)
	}
	if m.teamAction.reason != "Inspect teams" {
		t.Fatalf("physical spaces lost from reason: %q", m.teamAction.reason)
	}
}

func consolePress(m console, key tea.KeyMsg) (console, tea.Cmd) {
	next, cmd := m.Update(key)
	return next.(console), cmd
}

func TestConsoleTeamReviewFreezesIdentityAndVersion(t *testing.T) {
	m := teamConsoleFixture()
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if cmd != nil || m.busy || m.teamAction == nil {
		t.Fatal("invite must enter review before issuing a mutation")
	}
	want := map[string]any{"id": "team_original", "version": 7, "agent": "bob"}
	// Polls and navigation must not silently retarget a reviewed invitation.
	m.data.teams = []managed.Team{{ID: "team_other", Name: "Other team", Version: 11, State: "active"}}
	m.agents[1], m.agents[2] = m.agents[2], m.agents[1]
	if m.teamAction.operation != "team.invite" || !reflect.DeepEqual(m.teamAction.args(), want) {
		t.Fatalf("review did not freeze exact invitation: %+v", m.teamAction)
	}
	for _, text := range []string{"team_original", "bob", "Version: 7", "Only the invited agent can consent"} {
		if !strings.Contains(m.teamReviewPanel(80, 20), text) {
			t.Fatalf("review omits %q", text)
		}
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.busy || m.teamAction != nil {
		t.Fatal("explicit review confirmation did not submit exactly one action")
	}
}

func TestConsoleTeamReviewCancellationAndConsentGuards(t *testing.T) {
	m := teamConsoleFixture()
	m.data.teamsErr = errors.New("offline")
	m.reviewTeamAction("team.invite")
	if m.teamAction != nil {
		t.Fatal("offered membership change from unavailable team state")
	}
	m.data.teamsErr = nil
	m.reviewTeamAction("team.invite")
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || m.teamAction != nil || m.busy {
		t.Fatal("cancelling review mutated team state")
	}
	m.data.teams[0].Invited = []string{"bob"}
	m.reviewTeamAction("team.invite")
	if m.teamAction != nil || !strings.Contains(m.message, "own acceptance") {
		t.Fatal("a pending invitation must not expose forced acceptance")
	}
	m.teamMemberIndex = 0
	m.reviewTeamAction("team.remove")
	if m.teamAction != nil || !strings.Contains(m.message, "cannot be removed") {
		t.Fatal("owner removal was offered")
	}
	m.teamMemberIndex = 1
	m.reviewTeamAction("team.remove")
	if m.teamAction == nil || m.teamAction.agentID != "bob" || m.teamAction.operation != "team.remove" {
		t.Fatal("owner cannot revoke pending invitation through reviewed removal")
	}
	m.teamAction = nil
	m.reviewTeamAction("team.dissolve")
	if m.teamAction == nil || m.teamAction.agentID != "" {
		t.Fatal("dissolve should target the team alone")
	}
	if _, exists := m.teamAction.args()["agent"]; exists {
		t.Fatal("dissolve retained a previous participant target")
	}
}

func TestConsoleEventsReturnToLiveWithHome(t *testing.T) {
	m := console{tab: 7, eventIndex: 3}
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyHome})
	if cmd != nil || m.eventIndex != 0 {
		t.Fatal("Home did not return audit selection to live newest event")
	}
}

func TestConsoleTeamBoardAndMemoryUseExactNamespace(t *testing.T) {
	m := teamConsoleFixture()
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if m.composer != "board" || m.composeTarget != "board_original" {
		t.Fatal("team board composer did not retain selected team namespace")
	}
	m.data.teams[0].Board = "board_other"
	if m.composeTarget != "board_original" {
		t.Fatal("draft followed a subsequent topology change")
	}
	m = teamConsoleFixture()
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	if cmd == nil || m.tab != 3 || !m.memoryLoading {
		t.Fatal("team memory inspection did not select its exact namespace")
	}
	next, _ := m.Update(consoleMemory{agent: m.selectedID(), request: m.memoryRequest, namespace: "memory_original"})
	m = next.(console)
	if !m.memoryOpen || m.memoryNamespace != "memory_original" {
		t.Fatal("memory response lost selected team namespace")
	}
}

func TestConsoleResearchInterventionRequiresReasonAndFrozenReview(t *testing.T) {
	m := console{tab: 7, data: consoleData{research: &managed.ResearchStatus{
		Progress: managed.ResearchProgress{Trial: "trial_1", Attempt: 2, Round: 3, Phase: "observe", State: "executing"},
		Control:  managed.ResearchControl{Mode: "running", Version: 9},
	}}}
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	if cmd != nil || m.teamAction == nil || m.teamAction.reasonReady {
		t.Fatal("research control must first request a reason")
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || m.teamAction.reasonReady {
		t.Fatal("blank research reason accepted")
	}
	// Ordinary q is reason text, and pasted terminal/bidi controls stay inert.
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("query unclear\x1b\u202e"), Paste: true})
	if cmd != nil || m.teamAction.reason != "query unclear" {
		t.Fatalf("reason editor consumed command keys or unsafe controls: %+v", m.teamAction)
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !m.teamAction.reasonReady || m.busy {
		t.Fatal("first Enter must review, rather than apply, the intervention")
	}
	m.data.research.Control.Version = 10
	if !reflect.DeepEqual(m.teamAction.args(), map[string]any{"version": 9, "action": "pause", "reason": "query unclear"}) {
		t.Fatal("research review version followed a subsequent poll")
	}
	if !strings.Contains(m.teamReviewPanel(80, 17), "phase barriers") {
		t.Fatal("research review must explain delayed barrier application")
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.busy || m.teamAction != nil {
		t.Fatal("reviewed intervention not submitted")
	}
}

func TestConsoleResearchControlsUnavailableOrTerminal(t *testing.T) {
	for _, data := range []consoleData{
		{},
		{researchErr: errors.New("offline")},
		{research: &managed.ResearchStatus{Control: managed.ResearchControl{Mode: "stopped"}}},
		{research: &managed.ResearchStatus{Progress: managed.ResearchProgress{State: "completed"}}},
	} {
		m := console{data: data}
		m.reviewResearchAction("resume")
		if m.teamAction != nil || m.message == "" {
			t.Fatal("offered a control without known live research status")
		}
	}
}

func TestConsoleRuntimeSeparatesAdmissionsExecutionAndNativeInteraction(t *testing.T) {
	m := console{agents: []managed.AgentInfo{{ID: "worker", Config: managedconfig.Agent{
		Conversation: "persistent", Autonomy: managedconfig.Autonomy{Enabled: true, MaxHops: 4},
	}}}, data: consoleData{
		autonomy:     managed.AutonomyStatus{Agent: "worker", Enabled: true, Mode: "paused", State: "working", Inflight: 1, Pending: 2, TurnsUsed: 3, MaxTurns: 8},
		conversation: managed.ConversationState{Agent: "worker", Generation: 2, State: "busy"},
	}}
	for _, text := range []string{"admissions paused", "in-flight 1", "queued observations 2", "Turns 3/8", "Native history: busy", "generation 2", "Tasks → c cancel"} {
		if !strings.Contains(m.agentRuntimePanel(100), text) {
			t.Fatalf("runtime status omitted %q", text)
		}
	}
	m.data.runtimeErr = errors.New("broker offline")
	if !strings.Contains(m.agentRuntimePanel(100), "unavailable") || strings.Contains(m.agentRuntimePanel(100), "in-flight 1") {
		t.Fatal("stale runtime data presented as current after failed refresh")
	}
	m.agents[0].Config.Interaction = "interactive"
	panel := m.agentRuntimePanel(100)
	if !strings.Contains(panel, "Human-led native session") || strings.Contains(panel, "admissions paused") {
		t.Fatal("native process was represented as controlled by managed autonomy")
	}
	m.agents[0].Config.Interaction = ""
	m.data.runtimeErr = nil
	m.data.autonomy.Agent = "other"
	if !strings.Contains(m.agentRuntimePanel(100), "execution is unknown") {
		t.Fatal("missing/mismatched runtime status implied an idle worker")
	}
}

func TestConsoleTeamsAndResearchCriticalControlsFitSmallTerminal(t *testing.T) {
	m := teamConsoleFixture()
	for _, text := range []string{"7 Teams", "8 Events", "Action target:", "Awaiting consent:", "i invite"} {
		if !strings.Contains(m.View(), text) {
			t.Fatalf("team view critical detail hidden at 80x24: %q", text)
		}
	}
	m.reviewTeamAction("team.invite")
	for _, text := range []string{"Enter applies", "Esc cancels", "team_original", "Stable ID: bob"} {
		if !strings.Contains(m.View(), text) {
			t.Fatalf("review critical detail hidden at 80x24: %q", text)
		}
	}
	m.teamAction, m.tab = nil, 7
	m.data.research = &managed.ResearchStatus{Progress: managed.ResearchProgress{Condition: "cooperation", Phase: "decide", State: "executing", AckVersion: 2}, Control: managed.ResearchControl{Mode: "paused", Version: 3}}
	m.data.activity.Events = []managed.Event{{ID: "event_one", Type: "team.remove", Actor: "$owner", EntityID: "team_original", Details: map[string]any{"agent": "bob"}}}
	for _, text := range []string{"runner executing", "admissions paused", "awaiting next barrier", "P pause", "team.remove", "Actor:", "Entity:"} {
		if !strings.Contains(m.View(), text) {
			t.Fatalf("research/event critical detail hidden at 80x24: %q", text)
		}
	}
}
