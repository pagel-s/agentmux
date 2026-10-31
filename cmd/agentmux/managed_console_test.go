package main

import (
	"errors"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
	"agentmux/internal/sessioninventory"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConsoleComposerAndHelpRemainVisibleInSmallTerminal(t *testing.T) {
	m := console{width: 80, height: 24, composer: "send", text: []rune("human message")}
	for i := 0; i < 20; i++ {
		m.agents = append(m.agents, managed.AgentInfo{ID: "agent", Config: managedconfig.Agent{Harness: "command"}})
	}
	view := m.View()
	for _, text := range []string{"human message", "Enter submits", "q detach/close"} {
		if !strings.Contains(view, text) {
			t.Fatalf("critical UI hidden at 80x24: %q", text)
		}
	}
}

func TestConsoleOutsideAttachUsesIndependentClientViewWithoutFocusingOthers(t *testing.T) {
	t.Setenv("TMUX", "")
	m := console{state: "/workspace/state", agents: []managed.AgentInfo{{ID: "alice"}}, data: consoleData{runtime: managedlaunch.Runtime{Controller: "/trusted/controller"}}}
	msg := m.attach()()
	attach, ok := msg.(consoleAttach)
	if !ok || attach.agent != "alice" || attach.controller != "/trusted/controller" {
		t.Fatalf("outside attach attempted to focus existing view: %+v", msg)
	}
}

func TestConsoleBoardDraftFreezesNamespaceAndReplyPreservesSource(t *testing.T) {
	m := console{agents: []managed.AgentInfo{{ID: "alice"}}, data: consoleData{boundaries: managed.Boundaries{Agent: "alice", Boards: []managed.BoundaryScope{{Name: "scoped_alpha", Read: true, Write: true}, {Name: "scoped_beta", Read: true, Write: true}}}}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = next.(console)
	m.boardSelected = 1
	if m.composeTarget != "scoped_alpha" {
		t.Fatal("board draft changed namespace after selection changed")
	}
	m.composer = ""
	m.data.activity.Messages = []managed.Message{{ID: "original", Board: "scoped_alpha", Text: "original board note"}}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = next.(console)
	if m.composeTarget != "scoped_alpha" || m.replyID != "original" {
		t.Fatalf("reply retargeted to another board: %+v", m)
	}
}

func TestConsoleNativeWizardAcceptsEmptyRoleAndKeepsSelectedProject(t *testing.T) {
	m := console{agents: []managed.AgentInfo{{ID: "alice", Config: managedconfig.Agent{Workdir: "/my/project"}}}}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'N'}}, {Type: tea.KeyRunes, Runes: []rune("daily")}, {Type: tea.KeyEnter}, {Type: tea.KeyEnter}, {Type: tea.KeyEnter}} {
		next, _ := m.Update(key)
		m = next.(console)
	}
	if m.composer != "new-role" || m.newNative.project != "/my/project" || m.newNative.provider != "claude" {
		t.Fatalf("native defaults lost: %+v", m.newNative)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(console)
	if cmd == nil || !m.busy || m.composer != "" || m.newNative.role != "" {
		t.Fatal("optional empty role prevented creating native session")
	}
}

func TestConsoleObservedSessionsCannotCloseAnUnrelatedRegisteredAgent(t *testing.T) {
	m := console{tab: 5, agents: []managed.AgentInfo{{ID: "important"}}, data: consoleData{observed: []sessioninventory.Session{{ID: "observed", PID: 22, Harness: "claude"}}}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil || !strings.Contains(next.(console).message, "read-only") {
		t.Fatal("observed-session close targeted an unrelated managed agent")
	}
	m.tab = 1
	m.agents[0].External = true
	action := m.attach()().(consoleAction)
	if action.err != nil || !strings.Contains(action.details, "session-specific helper") || !strings.Contains(action.message, "not confirmed") {
		t.Fatal("external registry entry pretended to own or verify its native terminal")
	}
}

func TestConsoleDetachNeverSelectsAnotherClientsNativePane(t *testing.T) {
	clients := "client-a\t%4\t10\nclient-b\t%8\t99\nclient-c\t%4\t20\n"
	if got := consoleDetachClient(clients, "%4", ""); got != "client-c" {
		t.Fatalf("wrong current workbench client: %s", got)
	}
	if got := consoleDetachClient(clients, "%4", "client-a"); got != "client-a" {
		t.Fatalf("ignored explicit popup client: %s", got)
	}
	if got := consoleDetachClient(clients, "%4", "client-b"); got != "" {
		t.Fatal("detached another client's native pane")
	}
	if got := consoleDetachClient(clients, "", ""); got != "" {
		t.Fatal("detached without a known current workbench pane")
	}
}

func TestConsoleFiltersRegisteredObservationsWithoutLosingRoutingMetadata(t *testing.T) {
	agents := []managed.AgentInfo{{ID: "bridge", State: "active", External: true, ExternalRef: "known"}, {ID: "old", State: "revoked", External: true, ExternalRef: "unregistered"}}
	raw := []sessioninventory.Session{{ID: "known", TTY: "pts/1"}, {ID: "unregistered", TTY: "pts/2"}, {ID: "selected", TTY: "pts/3"}}
	m := console{tab: 5, agents: agents, observedSelected: 1, data: consoleData{selected: "bridge", observed: raw}}
	visible := m.observedSessions()
	if len(visible) != 2 || visible[0].ID != "unregistered" || visible[1].ID != "selected" {
		t.Fatalf("wrong terminal inventory: %+v", visible)
	}
	if len(m.data.observed) != 3 || m.data.observed[0].ID != "known" {
		t.Fatal("presentation filter destroyed external-attach/reconnect metadata")
	}
	if observationStatus("known", m.data.observed) != "native observed" || !strings.Contains(observationStatus("missing", m.data.observed), "not currently observed") {
		t.Fatal("bridge liveness confused observation with readiness")
	}
	next, _ := m.Update(consoleData{selected: "bridge", agents: agents, observed: []sessioninventory.Session{raw[2], raw[1], raw[0]}})
	m = next.(console)
	if m.observedSelected != 0 || m.observedSessions()[m.observedSelected].ID != "selected" || len(m.data.observed) != 3 {
		t.Fatal("inventory refresh changed the selected process or discarded raw observations")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(next.(console).details, "selected") || strings.Contains(next.(console).details, "pts/1") {
		t.Fatal("Sessions details targeted a hidden registered process")
	}
}

func TestConsoleKeepsCurrentSelectionOnLateSnapshot(t *testing.T) {
	m := console{agents: []managed.AgentInfo{{ID: "alice"}, {ID: "bob"}}, selected: 1, data: consoleData{selected: "bob"}}
	updated, _ := m.Update(consoleData{agents: m.agents, selected: "alice", boundaries: managed.Boundaries{Agent: "alice"}})
	if updated.(console).data.selected != "bob" {
		t.Fatal("stale ACLs were shown for another selected agent")
	}
}

func TestConsoleDoesNotPretendDisabledAutonomyCanPauseNativeTerminal(t *testing.T) {
	m := console{agents: []managed.AgentInfo{{ID: "alice", Config: managedconfig.Agent{Interaction: "interactive"}}}}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil || !strings.Contains(updated.(console).message, "disabled") {
		t.Fatal("human-led terminal falsely reported paused")
	}
}

func TestConsoleComposerKeepsRecipientAndAllowsEditing(t *testing.T) {
	m := console{agents: []managed.AgentInfo{{ID: "alice"}, {ID: "bob"}}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = next.(console)
	m.selected = 1 // A concurrent selection must not retarget an in-flight draft.
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("helo")}, {Type: tea.KeyLeft}, {Type: tea.KeyRunes, Runes: []rune{'l'}}} {
		next, _ = m.Update(key)
		m = next.(console)
	}
	if m.composeTarget != "alice" || string(m.text) != "hello" {
		t.Fatalf("draft changed: target %s text %q", m.composeTarget, string(m.text))
	}
	if strings.Contains(consoleContent("note\x1b[31m\u202espoof", 100), "\x1b") {
		t.Fatal("terminal escape reached owner UI")
	}
}

func TestConsoleLateMemoryDoesNotReopenAnotherAgentsOrClosedEditor(t *testing.T) {
	for _, navigation := range []tea.KeyMsg{{Type: tea.KeyDown}, {Type: tea.KeyEsc}, {Type: tea.KeyTab}} {
		m := console{tab: 3, agents: []managed.AgentInfo{{ID: "alice"}, {ID: "bob"}}, data: consoleData{selected: "alice", boundaries: managed.Boundaries{Agent: "alice", Memory: []managed.BoundaryScope{{Name: "alice-group", Read: true}}}}}
		next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
		m = next.(console)
		request := m.memoryRequest
		if cmd == nil || !m.memoryLoading || !m.busy {
			t.Fatal("memory load did not capture an outstanding request")
		}
		next, _ = m.key(navigation)
		m = next.(console)
		next, _ = m.Update(consoleMemory{agent: "alice", request: request, namespace: "alice-group", entries: []managed.Memory{{Key: "old-project", Value: "old project data"}}})
		m = next.(console)
		if m.memoryOpen || m.memoryNamespace != "" || len(m.memoryEntries) != 0 || m.busy {
			t.Fatalf("late memory response undid navigation %s: %+v", navigation.String(), m)
		}
	}
}

func TestConsoleMemoryWaitsForMatchingBoundariesAndKeepsNewestRequest(t *testing.T) {
	m := console{tab: 3, selected: 1, agents: []managed.AgentInfo{{ID: "alice"}, {ID: "bob"}}, data: consoleData{selected: "alice", boundaries: managed.Boundaries{Agent: "alice", Memory: []managed.BoundaryScope{{Name: "alice-group", Read: true}}}}}
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = next.(console)
	if cmd != nil || m.memoryLoading {
		t.Fatal("rapid navigation loaded the previous agent's memory namespace")
	}
	m.data.boundaries = managed.Boundaries{Agent: "bob", Memory: []managed.BoundaryScope{{Name: "bob-group", Read: true}}}
	_ = m.loadMemory("bob-group")
	oldRequest := m.memoryRequest
	_ = m.loadMemory("bob-group")
	newRequest := m.memoryRequest
	next, _ = m.Update(consoleMemory{agent: "bob", request: oldRequest, namespace: "bob-group", entries: []managed.Memory{{Key: "old"}}})
	m = next.(console)
	if m.memoryOpen || !m.memoryLoading || !m.busy {
		t.Fatal("older same-agent response replaced the current outstanding read")
	}
	next, _ = m.Update(consoleMemory{agent: "bob", request: newRequest, namespace: "bob-group", entries: []managed.Memory{{Key: "current"}}})
	m = next.(console)
	if !m.memoryOpen || m.memoryLoading || m.busy || m.memoryNamespace != "bob-group" || len(m.memoryEntries) != 1 || m.memoryEntries[0].Key != "current" {
		t.Fatal("current memory read was not applied to its selected agent")
	}
}

func TestConsoleTitleConflictPreservesDraftForExplicitReviewByIdentity(t *testing.T) {
	m := console{agents: []managed.AgentInfo{{ID: "alice", Title: "Concurrent owner edit", TitleVersion: 2}, {ID: "bob", Title: "Other project"}}}
	next, _ := m.Update(consoleAction{labelID: "alice", labelText: "My unsaved investigation", err: errors.New("conflict: session title changed")})
	m = next.(console)
	if m.labelDrafts["alice"] != "My unsaved investigation" || !strings.Contains(m.message, "draft kept") {
		t.Fatal("title conflict discarded the human's draft")
	}
	m.selected = 1
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	m = next.(console)
	if string(m.text) != "Other project" || m.composeTarget != "bob" {
		t.Fatal("failed title draft leaked into another session's editor")
	}
	m.composer, m.selected = "", 0
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	m = next.(console)
	if string(m.text) != "My unsaved investigation" || m.composeTarget != "alice" || m.labelVersion != 2 {
		t.Fatal("explicit review did not restore the draft against current identity/version")
	}
	next, _ = m.Update(consoleAction{labelID: "alice", labelText: "My unsaved investigation"})
	if _, exists := next.(console).labelDrafts["alice"]; exists {
		t.Fatal("successful label save retained an obsolete draft")
	}
}

func TestConsoleCoalescedNavigationAndSearchPreserveKeyOrder(t *testing.T) {
	m := console{agents: []managed.AgentInfo{{ID: "alice"}, {ID: "bob"}, {ID: "carol"}}}
	next, _ := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jj")})
	m = next.(console)
	if m.selectedID() != "carol" {
		t.Fatal("coalesced rapid navigation was ignored")
	}
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/alice")})
	m = next.(console)
	if m.composer != "find" || string(m.text) != "alice" || m.selectedID() != "carol" {
		t.Fatal("coalesced search was lost or silently changed action target")
	}
	m.composer = ""
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("xjj"), Paste: true})
	if cmd != nil || next.(console).selectedID() != "carol" || next.(console).busy {
		t.Fatal("bracketed pasted text executed commands outside an editor")
	}
}
