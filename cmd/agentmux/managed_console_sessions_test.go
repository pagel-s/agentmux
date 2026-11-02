package main

import (
	"fmt"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func sessionSwitcherFixture() console {
	m := console{width: 80, height: 28, tab: 2, target: 2, data: consoleData{runtime: managedlaunch.Runtime{Session: "disposable-work", State: "running", AttemptID: "owned", Agents: map[string]managedlaunch.AgentPane{}}}}
	for _, id := range []string{"alice", "bob", "carol"} {
		m.agents = append(m.agents, managed.AgentInfo{ID: id, Title: "Same task", State: "active", Config: managedconfig.Agent{Harness: "claude", Interaction: "interactive", Workdir: "/work/" + id}})
		m.data.runtime.Agents[id] = managedlaunch.AgentPane{PaneID: "%" + id, State: "running"}
	}
	m.data.boundaries.Agent = "alice"
	return m
}

func TestConsoleSessionRosterIsVerticalAtEverydaySplitWidths(t *testing.T) {
	for _, width := range []int{64, 72, 80, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := sessionSwitcherFixture()
			m.width, m.height = width, 40
			m.agents[0].Title, m.agents[1].Title = "Poem writer", "Poem critic"
			view := ansi.Strip(m.View())
			for _, cue := range []string{"SESSIONS · ↑/↓", "1/3 · Ctrl+P switch", "SENDER · Tab", "RECIPIENT · ↑/↓", "Poem writer", "Poem critic"} {
				if !strings.Contains(view, cue) {
					t.Fatalf("missing session navigation cue %q at %d columns:\n%s", cue, width, view)
				}
			}
			for _, line := range strings.Split(view, "\n") {
				left, _, ok := strings.Cut(line, "│")
				if ok && strings.Contains(left, "Poem writer") && strings.Contains(left, "Poem critic") {
					t.Fatal("sessions were collapsed into one horizontal strip")
				}
			}
		})
	}
	for _, width := range []int{40, 50, 60} {
		m := sessionSwitcherFixture()
		m.width = width
		view := ansi.Strip(m.View())
		if !strings.Contains(view, "Session 1/3") || !strings.Contains(view, "Ctrl+P switch") || strings.Contains(view, "│") {
			t.Fatalf("tiny view hid session selection:\n%s", view)
		}
	}
}

func TestConsoleSessionSwitcherPreviewsWithoutRetargetingAndEscReturns(t *testing.T) {
	m := sessionSwitcherFixture()
	next, _ := m.key(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = next.(console)
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(console)
	match, ok := m.findMatch()
	if !ok || m.findMatchIdentity(match) != "agent:bob" || m.selectedID() != "alice" || m.target != 2 {
		t.Fatal("preview changed the sender or recipient")
	}
	for _, width := range []int{50, 64, 80} {
		m.width = width
		view := ansi.Strip(m.View())
		if !strings.Contains(view, "Switch session") || !strings.Contains(view, "Search:") || !strings.Contains(view, "Enter terminal/details") || strings.Contains(view, "│") {
			t.Fatalf("picker did not own the focused view at %d columns:\n%s", width, view)
		}
	}
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(console)
	if cmd != nil || m.composer != "" || m.selectedID() != "alice" || m.tab != 2 || m.target != 2 {
		t.Fatal("Esc changed the original workbench selection")
	}
}

func TestConsoleSessionSwitcherTabSelectsHereAndEnterOpensNative(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyEnter}} {
		m := sessionSwitcherFixture()
		next, _ := m.key(tea.KeyMsg{Type: tea.KeyCtrlP})
		m = next.(console)
		next, _ = m.key(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(console)
		next, cmd := m.key(key)
		m = next.(console)
		if m.selectedID() != "bob" || m.composer != "" || cmd == nil || m.target != 2 {
			t.Fatal("picker did not apply exact selected session")
		}
		if key.Type == tea.KeyTab && m.tab != 2 {
			t.Fatal("Tab left the current workbench view")
		}
		if key.Type == tea.KeyEnter && m.tab != 1 {
			t.Fatal("Enter failed to prepare the native session view")
		}
	}
}

func TestConsoleSessionSwitcherKeepsIdentityAcrossPollingAndBlocksMissingSelection(t *testing.T) {
	for _, removed := range []bool{false, true} {
		m := sessionSwitcherFixture()
		next, _ := m.key(tea.KeyMsg{Type: tea.KeyCtrlP})
		m = next.(console)
		next, _ = m.key(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(console)
		agents := []managed.AgentInfo{m.agents[1], m.agents[2], m.agents[0]}
		if removed {
			agents = agents[1:]
		}
		data := m.data
		data.selected = "alice"
		data.agents = agents
		next, _ = m.Update(data)
		m = next.(console)
		next, cmd := m.key(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(console)
		if removed {
			if cmd != nil || m.composer != "find" || m.selectedID() != "alice" {
				t.Fatal("vanished selected identity silently attached another session")
			}
		} else if m.selectedID() != "bob" || m.composer != "" || cmd == nil {
			t.Fatal("polling reordered the selected native target")
		}
	}
}

func TestConsoleSessionSwitcherSearchAndShortcutPreserveEditors(t *testing.T) {
	m := sessionSwitcherFixture()
	m.compose("send", "alice", "", "private unsent draft")
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = next.(console)
	if cmd != nil || m.composer != "send" || string(m.text) != "private unsent draft" || m.composeTarget != "alice" {
		t.Fatal("session shortcut discarded or retargeted a draft")
	}
	m.composer = ""
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = next.(console)
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("work/bob")})
	m = next.(console)
	match, ok := m.findMatch()
	if !ok || m.findMatchIdentity(match) != "agent:bob" || len(m.sessionMatches()) != 1 {
		t.Fatal("project search failed to identify a session")
	}
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyLeft})
	m = next.(console)
	if m.tab != 2 || m.composer != "find" {
		t.Fatal("query editing switched the global view")
	}
}

func TestConsoleSessionRosterKeepsSelectedPositionVisibleAtScale(t *testing.T) {
	m := sessionSwitcherFixture()
	m.agents = nil
	m.width, m.height = 64, 24
	for i := range 100 {
		m.agents = append(m.agents, managed.AgentInfo{ID: fmt.Sprintf("session_%03d", i), Title: fmt.Sprintf("Problem %03d", i), Config: managedconfig.Agent{Harness: "codex", Interaction: "interactive", Workdir: "/work/project"}})
	}
	m.selected = 79
	view := ansi.Strip(m.View())
	for _, cue := range []string{"80/100", "› Problem 079", "above", "below"} {
		if !strings.Contains(view, cue) {
			t.Fatalf("selected session is hidden at scale (%q):\n%s", cue, view)
		}
	}
}

func TestConsoleSessionPreviewWaitsForExactSelectedIdentity(t *testing.T) {
	for _, state := range []string{"running", "exited"} {
		t.Run(state, func(t *testing.T) {
			m := sessionSwitcherFixture()
			m.tab = 1
			m.data.selected, m.data.preview = "alice", "ALICE_PRIVATE_TERMINAL_OUTPUT"
			pane := m.data.runtime.Agents["bob"]
			pane.State = state
			m.data.runtime.Agents["bob"] = pane
			m.openSessionSwitcher()
			next, _ := m.key(tea.KeyMsg{Type: tea.KeyDown})
			m = next.(console)
			next, _ = m.key(tea.KeyMsg{Type: tea.KeyTab})
			m = next.(console)
			view := ansi.Strip(m.panel(100, 100))
			if m.selectedID() != "bob" || strings.Contains(view, m.data.preview) || !strings.Contains(view, "Loading this session's terminal preview") {
				t.Fatalf("retained output was misattributed to a newly selected session:\n%s", view)
			}
			m.data.selected, m.data.preview = "bob", "BOB_CURRENT_TERMINAL_OUTPUT"
			view = ansi.Strip(m.panel(100, 100))
			if !strings.Contains(view, m.data.preview) || strings.Contains(view, "Loading this session's terminal preview") {
				t.Fatalf("current selected terminal output was hidden:\n%s", view)
			}
		})
	}
}
