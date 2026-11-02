package main

import (
	"errors"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConsoleEmptyTeamsExplainsEveryUnavailableTeamAction(t *testing.T) {
	m := teamConsoleFixture()
	m.contentFocus = true
	m.data.teams = nil
	for _, size := range [][2]int{{80, 24}, {231, 62}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		for _, forbidden := range []string{"i invite", "r remove", "d dissolve", "e edit", "t owner", "b board", "v memory", "Enter native"} {
			if strings.Contains(view, forbidden) {
				t.Fatalf("empty Teams at %v advertises unavailable %q", size, forbidden)
			}
		}
		for _, required := range []string{"3 for Connections", "then c", "agent-created teams", "q detach/close"} {
			if !strings.Contains(view, required) {
				t.Fatalf("empty Teams at %v lacks useful next step %q", size, required)
			}
		}
	}
	for _, key := range []rune{'i', 'r', 'd', 'e', 't', 'b', 'v'} {
		next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if cmd != nil || next.busy || next.teamAction != nil || next.composer != "" || next.tab != 6 || !strings.Contains(next.message, "Connections") {
			t.Fatalf("empty Teams key %q did not explain the missing team without acting: %+v", key, next)
		}
	}
	next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || next.selectedID() != m.selectedID() || !strings.Contains(next.message, "No team target") {
		t.Fatal("Enter on empty Teams silently attached an unrelated roster agent")
	}
}

func TestConsoleTeamHintsFollowMembershipAndConnectivity(t *testing.T) {
	m := teamConsoleFixture()
	if !strings.Contains(m.teamKeyHints(), "i invite") || strings.Contains(m.teamKeyHints(), "r remove") || strings.Contains(m.teamKeyHints(), "t owner") {
		t.Fatal("a distinct nonmember was offered member-only actions")
	}
	m.data.teams[0].Invited = []string{"bob"}
	if strings.Contains(m.teamKeyHints(), "i invite") || !strings.Contains(m.teamKeyHints(), "r remove") {
		t.Fatal("pending invitation hints did not allow revocation and suppress reinvitation")
	}
	m.data.teams[0].Invited = nil
	m.data.teams[0].Members = []string{"alice", "bob"}
	if !strings.Contains(m.teamKeyHints(), "t owner") || strings.Contains(m.teamKeyHints(), "i invite") {
		t.Fatal("current member lacked owner-proposal review or was offered an invitation")
	}
	m.offline = true
	for _, key := range []rune{'i', 'r', 'd', 'e', 't', 'b', 'v'} {
		next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if cmd != nil || next.composer != "" || next.teamAction != nil || !strings.Contains(next.message, "offline") {
			t.Fatalf("offline Teams key %q offered a live action", key)
		}
	}
	if strings.Contains(m.teamKeyHints(), "b board") || strings.Contains(m.teamKeyHints(), "e edit") {
		t.Fatal("offline Teams advertises changes")
	}
	m.offline = false
	m.data.teams[0].State = "dissolved"
	if strings.Contains(m.teamKeyHints(), "b board") || !strings.Contains(m.teamKeyHints(), "v memory") {
		t.Fatal("dissolved team should expose retained memory rather than board posting")
	}
	next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if cmd != nil || next.composer != "" || !strings.Contains(next.message, "dissolved") {
		t.Fatal("dissolved team board key did not explain its state")
	}
	m.data.teamsErr = errors.New("stale team response")
	next, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	if cmd != nil || next.tab != 6 || !strings.Contains(next.message, "unavailable") {
		t.Fatal("failed team refresh allowed a stale namespace read")
	}
}

func exitedNativeConsole() console {
	return console{tab: 1, width: 80, height: 24, agents: []managed.AgentInfo{{ID: "codex", State: "active", Title: "A long, descriptive task name", Config: managedconfig.Agent{Harness: "codex", Interaction: "interactive", Workdir: "/a/very-long-project-name-that-fills-the-roster"}}}, data: consoleData{runtime: managedlaunch.Runtime{RelaunchVersion: 1, Agents: map[string]managedlaunch.AgentPane{"codex": {PaneID: "%12", State: "exited"}}}, preview: "Native CLI exited with status 0"}}
}

func TestConsoleExitedNativeStatusAndRelaunchStayVisible(t *testing.T) {
	m := exitedNativeConsole()
	if !strings.Contains(m.roster(30, 22), "exited") {
		t.Fatal("long project name clipped native process exit state")
	}
	for _, size := range [][2]int{{80, 24}, {231, 62}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		for _, text := range []string{"NATIVE CLI EXITED", "Enter relaunch review", "Nothing restarts automatically", "LAST TERMINAL OUTPUT"} {
			if !strings.Contains(view, text) {
				t.Fatalf("exited agent recovery is hidden at %v: %q", size, text)
			}
		}
		if strings.Contains(view, "LIVE TERMINAL PREVIEW") || strings.Contains(view, "failed") {
			t.Fatal("clean exit was presented as a live or failed CLI")
		}
	}
	next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || next.busy || next.composer != "relaunch" || next.composeTarget != "codex" {
		t.Fatal("Enter on exited native CLI must open review without restarting or focusing its dead pane")
	}
	next, cmd = consolePress(next, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || next.busy || next.composer != "" {
		t.Fatal("cancelling relaunch review caused an action")
	}
	m.offline = true
	next, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || next.composer != "" || !strings.Contains(next.message, "offline") {
		t.Fatal("saved exit state allowed an offline relaunch")
	}
}

func TestConsoleRunningNativeEnterStillAttachesAndShiftUExplains(t *testing.T) {
	t.Setenv("TMUX", "")
	m := exitedNativeConsole()
	m.data.runtime.Controller = "/trusted/controller"
	m.data.runtime.Agents["codex"] = managedlaunch.AgentPane{PaneID: "%12", State: "running"}
	next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || next.composer != "" {
		t.Fatal("running native session lost its direct terminal route")
	}
	if _, ok := cmd().(consoleAttach); !ok {
		t.Fatal("outside-tmux Enter did not request an independent attachment")
	}
	next, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'U'}})
	if cmd != nil || next.composer != "" || !strings.Contains(next.message, "Exit the selected") {
		t.Fatal("running native CLI could enter relaunch review")
	}
}

func TestConsoleCrossWorkspaceNativeAttachShowsCommandWithoutNesting(t *testing.T) {
	t.Setenv("TMUX", "/tmp/other-workspace/tmux.sock,123,0")
	m := exitedNativeConsole()
	m.state = "/tmp/workspace 'two'"
	m.data.runtime.Agents["codex"] = managedlaunch.AgentPane{PaneID: "%12", State: "running"}
	m.data.runtime.Controller = "/trusted/controller"
	m.failedDraft = &consoleDraft{target: "codex", text: "retained note"}
	msg := m.attach()()
	guidance, ok := msg.(consoleAttachGuidance)
	if !ok || guidance.command != managedlaunch.ShellCommand([]string{"amx", "attach", "codex", "--state", m.state}) {
		t.Fatalf("cross-workspace attach did not produce a correctly quoted separate-terminal command: %+v", msg)
	}
	next, cmd := m.Update(msg)
	m = next.(console)
	if cmd != nil || m.reader == nil || !strings.Contains(m.reader.body, guidance.command) || m.failedDraft == nil || m.failedDraft.text != "retained note" {
		t.Fatal("cross-workspace attach nested a client or discarded the observer's draft")
	}
}

func TestConsoleContextHelpHidesStaleStatusAndWrapsWholeWords(t *testing.T) {
	m := console{tab: 7, width: 40, height: 24, message: "No consent-based teams exist in this workspace."}
	m.openContextHelp()
	view := m.View()
	if !strings.Contains(view, "Events help") || !strings.Contains(view, "Help · lines") || strings.Contains(view, "Saved selection") || strings.Contains(view, m.message) {
		t.Fatal("help inherited unrelated status or a saved-record label")
	}
	m.reader.body = "abcdefghijklmnopqrstuv collaboration remains interactive"
	panel := m.readerPanel(30, 12)
	if !strings.Contains(panel, "\ncollaboration remains") || strings.Contains(panel, "collabo\nration") {
		t.Fatalf("reader split a normal word instead of wrapping it: %q", panel)
	}
	// The help mode must survive scrolling, then return to the prior view.
	next, _ := consolePress(m, tea.KeyMsg{Type: tea.KeyDown})
	if !next.reader.help {
		t.Fatal("scrolling dropped help's contextual footer behavior")
	}
	next, cmd := consolePress(next, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || next.reader != nil || next.tab != 7 {
		t.Fatal("closing context help failed to return to the same view")
	}
}

func TestConsoleFooterSpellsShiftShortcutsAndKeepsEditorTextLiteral(t *testing.T) {
	m := exitedNativeConsole()
	if !strings.Contains(m.View(), "Shift+N new") || !strings.Contains(m.View(), "Shift+L title") {
		t.Fatal("footer did not explain that uppercase shortcuts require Shift")
	}
	next, _ := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if next.composer != "" {
		t.Fatal("lowercase n unexpectedly started a native-session wizard")
	}
	next, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	if next.composer != "new-title" {
		t.Fatal("documented Shift+N did not start a new session")
	}
	next, _ = consolePress(next, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N?q")})
	if string(next.text) != "N?q" || next.reader != nil || !strings.Contains(next.View(), "Esc cancels") {
		t.Fatal("contextual shortcuts consumed literal editor text")
	}
}
