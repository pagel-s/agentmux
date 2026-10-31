package main

import (
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
