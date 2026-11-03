package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/nativebridge"
	"agentmux/internal/sessioninventory"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestConsoleControlCenterTeamsTabAlignsActionAndTerminalTarget(t *testing.T) {
	m := teamConsoleFixture()
	m.selected = 0
	m.teamMemberIndex = 0
	m.openSessionSwitcher()
	m.findIdentity = "agent:bob"
	next, cmd := m.chooseSession(false)
	m = next.(console)
	if cmd == nil || m.selectedID() != "bob" || m.teamTarget().ID != "bob" || m.tab != 6 {
		t.Fatal("Tab did not align both explicit Teams targets")
	}
	m.reviewTeamAction("team.invite")
	if m.teamAction == nil || m.teamAction.agentID != "bob" {
		t.Fatal("team action targeted the previous session")
	}
}
func TestConsoleControlCenterRevocationIsScopedFrozenAndReviewed(t *testing.T) {
	m := sessionSwitcherFixture()
	m.tab = 6
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	m = next.(console)
	if cmd != nil || m.ownerReview != nil || m.busy {
		t.Fatal("Teams accepted an unadvertised session revocation")
	}
	m.tab = 1
	m.agents = append(m.agents, managed.AgentInfo{ID: "child", Parent: "alice", State: "active"}, managed.AgentInfo{ID: "grandchild", Parent: "child", State: "active"})
	m.reviewStop()
	a := m.ownerReview
	if a == nil || a.operation != "agent.stop" {
		t.Fatal("x did not open review")
	}
	raw, _ := json.Marshal(a.args)
	if !strings.Contains(string(raw), `"expected_agents":["alice","child","grandchild"]`) {
		t.Fatalf("review did not freeze exact subtree: %s", raw)
	}
	m.selected = 1
	m.agents = append(m.agents, managed.AgentInfo{ID: "later", Parent: "alice", State: "active"})
	if a.args["id"] != "alice" || strings.Contains(a.body, "later") {
		t.Fatal("poll retargeted frozen review")
	}
	next, cmd = m.ownerReviewKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(console)
	if cmd != nil || m.busy || m.ownerReview != nil {
		t.Fatal("cancelled review submitted")
	}
}
func TestConsoleControlCenterQuickNewInheritsAndReviews(t *testing.T) {
	m := sessionSwitcherFixture()
	m.agents[0].Config.Harness = "pi"
	m.startNewSession(false)
	if m.newNative.provider != "pi" || m.newNative.project != "/work/alice" {
		t.Fatal("new session did not inherit provider/project")
	}
	m.text = []rune("Optional title")
	next, cmd := m.nativeDraftNext()
	m = next.(console)
	if cmd != nil || m.busy || m.ownerReview == nil || m.composer != "" {
		t.Fatal("first confirmation launched instead of reviewing")
	}
	for _, s := range []string{"pi", "/work/alice", "Optional title", "Model: provider default", "Execution: trusted local host"} {
		if !strings.Contains(m.ownerReview.body, s) {
			t.Fatalf("review hides %q", s)
		}
	}
	next, cmd = m.ownerReviewKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(console)
	if cmd == nil || !m.busy {
		t.Fatal("second confirmation did not schedule launch")
	}
}
func TestConsoleControlCenterExecutableMenuKeepsExactTarget(t *testing.T) {
	m := sessionSwitcherFixture()
	m.openActions()
	m.text = []rune("Rename session title")
	m.actionIndex = 0
	m.selected = 1
	next, cmd := m.invokeMenuAction()
	m = next.(console)
	if cmd != nil || m.composer != "label" || m.composeTarget != "alice" {
		t.Fatal("menu invocation followed the background selection")
	}
	m.composer = ""
	m.agents[0].State = "active"
	m.openActions()
	m.text = []rune("Retire session record")
	next, cmd = m.invokeMenuAction()
	m = next.(console)
	if cmd != nil || m.ownerReview != nil || !strings.Contains(m.message, "Revoke") {
		t.Fatal("disabled action became executable")
	}
}
func TestConsoleControlCenterUncertainRecoveryPinsTaskVersion(t *testing.T) {
	m := console{tab: 4, data: consoleData{activity: managedActivitySnapshot{Tasks: []managed.Task{{ID: "task-exact", Version: 7, To: "bob", State: "running", Delivery: "uncertain", Text: "Inspect side effects"}}}}}
	m.reviewTaskResolution("retry")
	a := m.ownerReview
	if a == nil || a.args["id"] != "task-exact" || a.args["version"] != 7 || a.args["resolution"] != "retry" {
		t.Fatal("uncertain recovery lacks exact task/version")
	}
	m.data.activity.Tasks[0].ID = "other"
	if a.args["id"] != "task-exact" || !strings.Contains(a.body, "may repeat effects") {
		t.Fatal("recovery changed task or omitted retry impact")
	}
}
func TestConsoleControlCenterAttentionUsesReportedEvidenceOnly(t *testing.T) {
	m := sessionSwitcherFixture()
	m.data.nativeStates = map[string]managed.NativeStatus{
		"alice": {Agent: "alice", Status: nativebridge.Status{State: "busy", Reason: "active native turn"}},
		"bob":   {Agent: "bob", Policy: managed.NativePolicy{NativeDelivery: managedconfig.NativeDelivery{Mode: "review"}}, Pending: []nativebridge.Delivery{{ID: "exact-request"}}},
		"carol": {Agent: "carol", Status: nativebridge.Status{State: "uncertain", Reason: "outcome unknown"}},
	}
	m.attentionOnly = true
	m.openSessionSwitcher()
	matches := m.sessionMatches()
	if len(matches) != 2 || m.agents[matches[0].agent].ID != "bob" || m.attentionReason("alice") != "" {
		t.Fatal("attention inferred thinking or omitted reported review/uncertainty")
	}
	m.offline = true
	if len(m.sessionMatches()) != 0 {
		t.Fatal("offline saved status claimed current attention evidence")
	}
}
func TestConsoleControlCenterEventRemainsPinnedWhenPageRollsOver(t *testing.T) {
	m := sessionSwitcherFixture()
	m.tab = 7
	m.data.selected = "alice"
	m.data.activity.Events = []managed.Event{{ID: "new"}, {ID: "read"}}
	m.stepContent(1)
	m.stepContent(-1) // Explicitly pin index zero too.
	d := m.data
	d.selected = "alice"
	d.agents = m.agents
	d.activity.Events = []managed.Event{{ID: "later"}}
	next, _ := m.Update(d)
	m = next.(console)
	if m.eventPinned == nil || m.data.activity.Events[m.eventIndex].ID != "new" {
		t.Fatal("rolling page replaced an explicitly selected newest event")
	}
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyHome})
	m = next.(console)
	if cmd == nil || m.eventPinned != nil || m.eventIndex != 0 {
		t.Fatal("Home failed to release pin and request live page")
	}
}
func TestConsoleControlCenterFocusDefersWhileDraftIsOpen(t *testing.T) {
	m := sessionSwitcherFixture()
	m.compose("send", "alice", "", "private draft")
	d := m.data
	d.agents = m.agents
	d.selected = "alice"
	d.focusID = "bob"
	next, _ := m.Update(d)
	m = next.(console)
	if m.selectedID() != "alice" || m.pendingFocus != "bob" || string(m.text) != "private draft" {
		t.Fatal("native focus disrupted draft")
	}
	m.composer = ""
	d.focusID = ""
	next, _ = m.Update(d)
	m = next.(console)
	if m.selectedID() != "bob" || m.pendingFocus != "" {
		t.Fatal("deferred exact focus was lost")
	}
}
func TestConsoleControlCenterSuspendedScopeCannotOverwriteSavedIntent(t *testing.T) {
	m := console{agents: []managed.AgentInfo{{ID: "alice"}}, data: consoleData{boundaries: managed.Boundaries{Agent: "alice", Memory: []managed.BoundaryScope{{Name: "group-memory", Suspended: true, Reason: "not a member"}}}}}
	if cmd := m.toggleScope(true); cmd != nil || m.busy || !strings.Contains(m.message, "Saved") {
		t.Fatal("suspended scope toggled its effective false state into saved intent")
	}
}
func TestConsoleControlCenterMiddleEllipsisAndExternalLocation(t *testing.T) {
	for _, w := range []int{12, 22, 40} {
		v := consoleMiddle("common-prefix-project-session-distinct-Bob", w)
		if ansi.StringWidth(v) > w || !strings.HasSuffix(v, "Bob") {
			t.Fatalf("identity suffix hidden at %d: %s", w, v)
		}
	}
	m := console{data: consoleData{observed: []sessioninventory.Session{{ID: "observed-1", TTY: "/dev/pts/22", Pane: "%83", TmuxSession: "original", AttachCommand: "tmux attach -t original"}}}}
	detail := m.externalObservationDetails("observed-1")
	for _, v := range []string{"/dev/pts/22", "%83", "original", "tmux attach"} {
		if !strings.Contains(detail, v) {
			t.Fatal("bridge details lost exact native location")
		}
	}
}

func TestConsoleControlCenterWorkspacePickerOwnsEveryOverlay(t *testing.T) {
	for _, kind := range []string{"owner", "team", "native", "reader", "actions", "find", "editor"} {
		t.Run(kind, func(t *testing.T) {
			m := sessionSwitcherFixture()
			m.width, m.height = 80, 28
			switch kind {
			case "owner":
				m.ownerReview = &consoleOwnerReview{title: "FROZEN OWNER REVIEW", body: "EXACT BODY"}
			case "team":
				m.teamAction = &consoleTeamAction{operation: "team.dissolve", teamID: "exact-team"}
			case "native":
				m.nativeAction = &consoleNativeAction{agent: "alice"}
			case "reader":
				m.reader = &consoleReader{title: "FROZEN READER", body: "READER BODY", offset: 2}
			case "actions":
				m.openActions()
			case "find":
				m.openSessionSwitcher()
			case "editor":
				m.compose("send", "alice", "", "UNSENT DRAFT")
			}
			reader, owner, team, native, composer, text := m.reader, m.ownerReview, m.teamAction, m.nativeAction, m.composer, string(m.text)
			n, _ := m.key(tea.KeyMsg{Type: tea.KeyCtrlW})
			m = n.(console)
			n, _ = m.Update(consoleWorkspaceList{request: m.workspaceRequest, rows: []workspaceListing{{Session: "CHOOSE-FIRST", Status: "running", State: "/tmp/first"}, {Session: "CHOOSE-SECOND", Status: "running", State: "/tmp/second"}}})
			m = n.(console)
			v := ansi.Strip(m.View())
			if !strings.Contains(v, "CHOOSE-FIRST") || !strings.Contains(v, "↑/↓ workspace") || strings.Contains(v, "EXACT BODY") || strings.Contains(v, "UNSENT DRAFT") {
				t.Fatalf("workspace picker does not own visible panel and footer:\n%s", v)
			}
			n, _ = m.key(tea.KeyMsg{Type: tea.KeyDown})
			m = n.(console)
			if m.workspacePicker.selected != 1 {
				t.Fatal("visible picker did not own keyboard navigation")
			}
			n, _ = m.key(tea.KeyMsg{Type: tea.KeyEsc})
			m = n.(console)
			if m.workspacePicker != nil || m.reader != reader || m.ownerReview != owner || m.teamAction != team || m.nativeAction != native || m.composer != composer || string(m.text) != text {
				t.Fatal("closing workspace picker changed its retained underlying panel")
			}
		})
	}
}
func TestConsoleControlCenterExplicitSelectionFencesDeferredAndInflightFocus(t *testing.T) {
	for _, route := range []string{"picker", "arrow", "action"} {
		t.Run(route, func(t *testing.T) {
			m := sessionSwitcherFixture()
			m.pendingFocus = "bob"
			oldRevision := m.selectionRevision
			switch route {
			case "picker":
				m.openSessionSwitcher()
				m.findIdentity = "agent:carol"
				n, _ := m.chooseSession(false)
				m = n.(console)
			case "arrow":
				m.selected = 1
				n, _ := m.key(tea.KeyMsg{Type: tea.KeyDown})
				m = n.(console)
			case "action":
				m.selected = 2
				m.openActions()
				m.text = []rune("Session details")
				n, _ := m.invokeMenuAction()
				m = n.(console)
				m.reader = nil
			}
			if m.selectedID() != "carol" || m.pendingFocus != "" || m.selectionRevision <= oldRevision {
				t.Fatal("explicit choice did not supersede pending native focus")
			}
			d := m.data
			d.agents = m.agents
			d.selected = "carol"
			d.focusID = "bob"
			d.focusRevision = oldRevision
			n, _ := m.Update(d)
			m = n.(console)
			if m.selectedID() != "carol" || m.pendingFocus != "" {
				t.Fatal("an older in-flight focus snapshot overrode the explicit choice")
			}
			d.focusRevision = m.selectionRevision
			n, _ = m.Update(d)
			m = n.(console)
			if m.selectedID() != "bob" {
				t.Fatal("a later native focus event stopped reconciling")
			}
		})
	}
}
func TestConsoleControlCenterScopedHistoryResetsEveryCursorAndRejectsOldPage(t *testing.T) {
	m := sessionSwitcherFixture()
	m.tab = 0
	m.boardFilter = 2
	m.messageAfter = "alice-old"
	m.messageCursor = "alice-older"
	m.messageBack = []string{""}
	m.messageMore = true
	m.messageIndex = 2
	m.data.activity.Messages = []managed.Message{{ID: "alice-message", To: []string{"alice"}}}
	old := m.messagePageRequest()
	m.data.messagePage = &old
	n, _ := m.key(tea.KeyMsg{Type: tea.KeyDown})
	m = n.(console)
	if m.selectedID() != "bob" || m.messageAfter != "" || m.messageCursor != "" || len(m.messageBack) != 0 || m.messageMore || m.messageIndex != 0 || len(m.data.activity.Messages) != 0 || m.data.messagePage != nil {
		t.Fatal("recipient change retained a different inbox's page state")
	}
	d := m.data
	d.agents = m.agents
	d.selected = "bob"
	d.messagePage = &old
	d.activity.Messages = []managed.Message{{ID: "alice-message", To: []string{"alice"}}}
	n, _ = m.Update(d)
	m = n.(console)
	if len(m.data.activity.Messages) != 0 {
		t.Fatal("late prior-recipient page entered the new inbox")
	}
	current := m.messagePageRequest()
	d.messagePage = &current
	d.activity.Messages = []managed.Message{{ID: "bob-message", To: []string{"bob"}}}
	n, _ = m.Update(d)
	m = n.(console)
	if len(m.boardMessages()) != 1 || m.boardMessages()[0].ID != "bob-message" {
		t.Fatal("new inbox did not start at its newest page")
	}
	m.data.boundaries = managed.Boundaries{Agent: "bob", Boards: []managed.BoundaryScope{{Name: "board-a", Read: true}, {Name: "board-b", Read: true}}}
	m.boardFilter = 1
	m.syncHistoryScopes()
	m.messageAfter = "board-a-old"
	m.messageBack = []string{""}
	m.messageCursor = "board-a-next"
	m.messageMore = true
	n, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'+'}})
	m = n.(console)
	if cmd == nil || m.selectedBoard() != "board-b" || m.messageAfter != "" || m.messageCursor != "" || m.messageMore || len(m.messageBack) != 0 || len(m.data.activity.Messages) != 0 {
		t.Fatal("board change retained another board's cursor/page")
	}
}
func TestConsoleControlCenterEventPinCannotCrossScopeOrQuery(t *testing.T) {
	m := sessionSwitcherFixture()
	m.tab = 7
	m.eventScope = true
	m.eventAfter = "alice-old"
	m.eventCursor = "alice-next"
	m.eventBack = []string{""}
	m.eventMore = true
	m.eventPinned = &managed.Event{ID: "alice-event", Actor: "alice"}
	m.data.selected = "alice"
	n, _ := m.key(tea.KeyMsg{Type: tea.KeyDown})
	m = n.(console)
	if m.eventPinned != nil || m.eventAfter != "" || m.eventCursor != "" || len(m.eventBack) != 0 || m.eventMore {
		t.Fatal("recipient change retained scoped event state")
	}
	d := m.data
	d.agents = m.agents
	d.selected = "bob"
	d.activity.Events = []managed.Event{{ID: "bob-event", Actor: "bob"}}
	p := m.eventPageRequest()
	d.eventPage = &p
	n, _ = m.Update(d)
	m = n.(console)
	if len(m.data.activity.Events) != 1 || m.data.activity.Events[m.eventIndex].Actor != "bob" {
		t.Fatal("Alice's pinned event was relabeled under Bob")
	}
	m.stepContent(0)
	m.compose("event-search", "search", "", "new-query")
	n, _ = m.key(tea.KeyMsg{Type: tea.KeyEnter})
	m = n.(console)
	if m.eventPinned != nil || m.eventCursor != "" || len(m.data.activity.Events) != 0 {
		t.Fatal("query change retained an unrelated pinned event")
	}
}
func TestConsoleControlCenterAttentionIncludesWholeHistoryTaskTotals(t *testing.T) {
	m := sessionSwitcherFixture()
	m.attentionOnly = true
	m.data.activity.Tasks = []managed.Task{{ID: "recent-healthy", To: "alice", State: "submitted"}}
	m.data.taskSummary = map[string]managed.TaskSummary{"bob": {Agent: "bob", Total: 900, Failed: 6}, "carol": {Agent: "carol", Total: 1000, Uncertain: 2}}
	m.openSessionSwitcher()
	matches := m.sessionMatches()
	if len(matches) != 2 || !strings.Contains(m.attentionReason("bob"), "6 failed") || !strings.Contains(m.attentionReason("carol"), "2 uncertain") || m.attentionReason("alice") != "" {
		t.Fatal("attention queue was limited to the loaded task page")
	}
}

func TestConsoleControlCenterAttentionReadsFailureOlderThanLoadedTaskPage(t *testing.T) {
	state, owner, _ := inventoryBroker(t)
	peerCredential, err := managed.LoadCredential(managed.CredentialPath(state, "peer"))
	if err != nil {
		t.Fatal(err)
	}
	peer := managed.NewClient(peerCredential)
	rpc := func(client *managed.Client, op string, args any) json.RawMessage {
		t.Helper()
		raw, err := workCall(context.Background(), client, op, args)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	rpc(owner, "instruct", map[string]any{"to": []string{"peer"}, "text": "old failed fixture"})
	var claimed struct {
		Task  managed.Task `json:"task"`
		Lease string       `json:"lease"`
	}
	if err = json.Unmarshal(rpc(peer, "task.claim", map[string]any{}), &claimed); err != nil {
		t.Fatal(err)
	}
	rpc(peer, "task.complete", map[string]any{"id": claimed.Task.ID, "version": claimed.Task.Version, "lease": claimed.Lease, "status": "failed", "result": "fixture failure without a model"})
	for i := 0; i < 45; i++ {
		rpc(owner, "instruct", map[string]any{"to": []string{"peer"}, "text": "newer pending fixture"})
	}
	var recent struct {
		Tasks []managed.Task `json:"tasks"`
	}
	if err = json.Unmarshal(rpc(owner, "task.list", map[string]any{"limit": 40, "order": "desc"}), &recent); err != nil {
		t.Fatal(err)
	}
	for _, task := range recent.Tasks {
		if task.State == "failed" {
			t.Fatal("fixture failure is still in the recent page")
		}
	}
	agents, err := workRegistry(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	m := console{client: owner, attentionOnly: true, agents: agents}
	d := consoleData{agents: agents}
	d.activity.Tasks = recent.Tasks
	m.fetchTaskSummary(&d)
	m.data = d
	if d.taskSummaryErr != nil || d.taskSummary["peer"].Total != 46 || d.taskSummary["peer"].Failed != 1 || !strings.Contains(m.attentionReason("peer"), "1 failed") {
		t.Fatalf("whole-history attention lost old failure: %+v %v", d.taskSummary, d.taskSummaryErr)
	}
	d.agents[0].State = "retired"
	m.fetchTaskSummary(&d)
	if _, ok := d.taskSummary["peer"]; ok {
		t.Fatal("retired recipient created current attention noise")
	}
}
