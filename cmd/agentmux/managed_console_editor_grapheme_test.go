package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestComposerVerticalMovementKeepsJoinedEmojiWhole(t *testing.T) {
	m := console{width: 80}
	m.compose("message", "alice", "", "👨‍👩‍👧‍👦a\nxyz")
	next, _ := m.key(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(console)
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	m = next.(console)
	if got, want := string(m.text), "👨‍👩‍👧‍👦aX\nxyz"; got != want {
		t.Fatalf("grapheme split by Up: got %q want %q", got, want)
	}
}

func TestComposerVerticalMovementUsesGraphemeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, text  string
		width, want int
	}{
		{"accent", "e\u0301\nx", 80, 2},
		{"skin tone emoji", "👩🏽‍💻\nxy", 80, 4},
		{"wrapped family", "👨‍👩‍👧‍👦abc", 3, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := []rune(tc.text)
			got, _ := consoleEditorMove(text, len(text), tc.width, -1, 0, false)
			if got != tc.want {
				t.Fatalf("cursor=%d want=%d", got, tc.want)
			}
			positions := consoleEditorPositions(text, tc.width)
			if positions[got].row < 0 {
				t.Fatalf("selected interior grapheme index %d", got)
			}
		})
	}
}
