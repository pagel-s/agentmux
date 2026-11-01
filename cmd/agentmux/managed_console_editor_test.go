package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestComposerMovesAcrossLogicalAndWrappedRows(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		width            int
	}{
		{"multiline", "LINE_ONE\nLINE_TWO", "LINE_ONEX\nLINE_TWO", 80},
		{"wrapped", "abcdefgh", "abcdXefgh", 4},
		{"wide", "東京abc\n京都abc", "東京abcX\n京都abc", 80},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := console{width: tc.width}
			m.compose("message", "exact-recipient", "", tc.text)
			next, _ := m.key(tea.KeyMsg{Type: tea.KeyUp})
			m = next.(console)
			next, _ = m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
			m = next.(console)
			if string(m.text) != tc.want || m.composeTarget != "exact-recipient" {
				t.Fatalf("draft=%q target=%q", string(m.text), m.composeTarget)
			}
		})
	}
}

func TestComposerVerticalMovementRetainsPreferredColumn(t *testing.T) {
	m := console{width: 80}
	m.compose("message", "alice", "", "abcdef\nx\nabcdef")
	for _, key := range []tea.KeyType{tea.KeyUp, tea.KeyUp} {
		next, _ := m.key(tea.KeyMsg{Type: key})
		m = next.(console)
	}
	if m.textCursor != 6 {
		t.Fatalf("preferred column lost on short row: %d", m.textCursor)
	}
}

func TestComposerLineEdgesAndWholeBufferShortcuts(t *testing.T) {
	m := console{width: 80}
	m.compose("message", "alice", "", "first\nsecond\nthird")
	m.textCursor = 9
	for _, tc := range []struct {
		key  tea.KeyType
		want int
	}{{tea.KeyHome, 6}, {tea.KeyEnd, 12}, {tea.KeyCtrlA, 0}, {tea.KeyCtrlE, len(m.text)}} {
		next, _ := m.key(tea.KeyMsg{Type: tc.key})
		m = next.(console)
		if m.textCursor != tc.want {
			t.Fatalf("key %v: cursor=%d want=%d", tc.key, m.textCursor, tc.want)
		}
	}
}

func TestOversizedPasteKeepsDraftAndExplainsLimit(t *testing.T) {
	m := console{}
	m.compose("message", "alice", "", "BASE")
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("界", 16385)), Paste: true})
	m = next.(console)
	if cmd != nil || string(m.text) != "BASE" || m.composeTarget != "alice" || !strings.Contains(m.message, "16,384") || !strings.Contains(m.message, "not inserted") {
		t.Fatalf("unsafe or silent rejected paste: draft=%q target=%q message=%q", string(m.text), m.composeTarget, m.message)
	}
}

func TestConsolePreservesJoinedEmojiWhileEscapingDirectionControls(t *testing.T) {
	text := "Engineer 👩🏽‍💻 · می\u200cروم"
	if actual := consoleContent(text, 120); actual != text {
		t.Fatalf("joined title changed: %q", actual)
	}
	if actual := consoleContent("hidden\u202e\x1b", 120); strings.ContainsAny(actual, "\u202e\x1b") {
		t.Fatalf("terminal controls escaped incorrectly: %q", actual)
	}
}
