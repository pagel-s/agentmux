package main

import (
	"strings"
	"testing"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestConsoleConnectionsDirectFocusPreservesIdentitiesAndPermissions(t *testing.T) {
	for _, focused := range []bool{false, true} {
		for _, key := range []rune{'[', ']'} {
			m := sessionSwitcherFixture()
			m.contentFocus, m.selected, m.target = focused, 1, 2
			m.message = "stale hint"
			m.data.boundaries.Agent, m.data.boundaries.Version = "bob", 17
			beforeSender, beforeRecipient := m.selectedID(), m.agents[m.target].ID
			for repeat := 0; repeat < 2; repeat++ {
				next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
				if cmd != nil || next.contentFocus != (key == ']') || next.tab != 2 || next.selectedID() != beforeSender || next.agents[next.target].ID != beforeRecipient || next.data.boundaries.Version != 17 || next.composer != "" || next.busy || next.message != "" {
					t.Fatalf("%c changed identities, permissions or view instead of explicitly focusing: %+v", key, next)
				}
				m = next
			}
			next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyUp})
			if key == '[' {
				if next.selectedID() != "alice" || next.target != 2 || cmd == nil {
					t.Fatal("Up after sender focus failed to select only sender and refresh its boundaries")
				}
			} else if next.selectedID() != "bob" || next.target != 1 || cmd != nil {
				t.Fatal("Up after recipient focus changed sender or issued a command")
			}
		}
	}
}

func TestConsoleConnectionsLabelsFollowActualFocusAtSplitWidths(t *testing.T) {
	for _, width := range []int{50, 64, 80, 110} {
		for _, recipient := range []bool{false, true} {
			m := sessionSwitcherFixture()
			m.width, m.height, m.contentFocus = width, 32, recipient
			sender := ansi.Strip(m.connectionFocusLabel(false))
			target := ansi.Strip(m.connectionFocusLabel(true))
			if strings.Contains(sender, "↑/↓") == recipient || strings.Contains(target, "↑/↓") != recipient {
				t.Fatalf("both identities or the wrong identity claim Up/Down at width %d: %q / %q", width, sender, target)
			}
			view := ansi.Strip(m.View())
			for _, hint := range []string{"SENDER", "RECIPIENT", "[ sender", "] recipient", "Tab switch"} {
				if !strings.Contains(view, hint) {
					t.Fatalf("width %d hides explicit focus hint %q:\n%s", width, hint, view)
				}
			}
		}
	}
}

func TestConsoleConnectionsFocusKeysStayInEditorsAndLeaveHistoryKeysIntact(t *testing.T) {
	m := sessionSwitcherFixture()
	m.contentFocus = true
	m.compose("send", "bob", "", "draft")
	for _, key := range []rune{'[', ']'} {
		next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if cmd != nil || next.composer != "send" || next.composeTarget != "bob" || !next.contentFocus {
			t.Fatal("focus shortcut escaped or retargeted an existing draft")
		}
		m = next
	}
	if string(m.text) != "draft[]" {
		t.Fatal("focus shortcuts stopped being literal editor text")
	}
	m = sessionSwitcherFixture()
	m.tab = 0
	m.data.activity.Messages = []managed.Message{{ID: "first"}, {ID: "second"}}
	next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}})
	if cmd != nil || next.messageIndex != 1 || !next.contentFocus || next.tab != 0 {
		t.Fatal("Connections shortcuts changed Board history navigation")
	}
}
