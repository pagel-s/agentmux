package main

import (
	"fmt"
	"strings"
	"testing"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConsoleArrowViewNavigationPreservesTargetSelection(t *testing.T) {
	for tab := 0; tab < 8; tab++ {
		for _, direction := range []struct {
			key  tea.KeyType
			step int
		}{{tea.KeyLeft, -1}, {tea.KeyRight, 1}} {
			t.Run(fmt.Sprintf("view%d/%d", tab, direction.step), func(t *testing.T) {
				m := teamConsoleFixture()
				m.tab, m.target = tab, 1
				next, cmd := consolePress(m, tea.KeyMsg{Type: direction.key})
				if tab == 2 || tab == 6 {
					if next.tab != tab || cmd != nil {
						t.Fatal("target-selection arrows unexpectedly changed views")
					}
					want := (1 + direction.step + len(m.agents)) % len(m.agents)
					if tab == 2 && next.target != want || tab == 6 && next.teamMemberIndex != want {
						t.Fatal("Connections/Teams arrows lost their existing target behavior")
					}
					return
				}
				want := (tab + direction.step + 8) % 8
				if next.tab != want || next.target != m.target || next.teamMemberIndex != m.teamMemberIndex || next.selected != m.selected {
					t.Fatal("view arrows changed the selected agent or an invisible target")
				}
				if (cmd != nil) != (want == 4) {
					t.Fatal("arrow navigation did not fetch Tasks on entry")
				}
			})
		}
	}
}

func TestConsoleModifiedArrowsNavigateEveryView(t *testing.T) {
	for tab := 0; tab < 8; tab++ {
		for _, direction := range []struct {
			key  tea.KeyType
			step int
		}{{tea.KeyCtrlLeft, -1}, {tea.KeyCtrlRight, 1}} {
			m := teamConsoleFixture()
			m.tab, m.target = tab, 1
			next, cmd := consolePress(m, tea.KeyMsg{Type: direction.key})
			want := (tab + direction.step + 8) % 8
			if next.tab != want || next.target != m.target || next.teamMemberIndex != m.teamMemberIndex {
				t.Fatalf("Ctrl-arrow from view %d changed a target or failed to navigate", tab)
			}
			if (cmd != nil) != (want == 4) {
				t.Fatalf("Ctrl-arrow from view %d did not fetch Tasks on entry", tab)
			}
		}
	}
}

func TestConsoleEveryTaskEntryFetchesAndTabsWrap(t *testing.T) {
	for _, entry := range []struct {
		tab int
		key tea.KeyMsg
	}{
		{3, tea.KeyMsg{Type: tea.KeyTab}},
		{5, tea.KeyMsg{Type: tea.KeyShiftTab}},
		{3, tea.KeyMsg{Type: tea.KeyRight}},
		{5, tea.KeyMsg{Type: tea.KeyLeft}},
		{3, tea.KeyMsg{Type: tea.KeyCtrlRight}},
		{5, tea.KeyMsg{Type: tea.KeyCtrlLeft}},
		{0, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}}},
	} {
		m := console{tab: entry.tab}
		next, cmd := consolePress(m, entry.key)
		if next.tab != 4 || cmd == nil {
			t.Fatalf("%s from view %d did not load the task history page", entry.key.String(), entry.tab)
		}
	}
	for _, wrap := range []struct {
		tab, want int
		key       tea.KeyType
	}{{0, 7, tea.KeyLeft}, {7, 0, tea.KeyRight}, {0, 7, tea.KeyShiftTab}, {7, 0, tea.KeyTab}} {
		next, _ := consolePress(console{tab: wrap.tab}, tea.KeyMsg{Type: wrap.key})
		if next.tab != wrap.want {
			t.Fatalf("%s failed to wrap at the end of the view strip", tea.KeyMsg{Type: wrap.key}.String())
		}
	}
}

func TestConsoleArrowViewChangeInvalidatesPendingMemory(t *testing.T) {
	m := console{tab: 3, memoryOpen: true, memoryLoading: true, busy: true, memoryRequest: 7, memoryNamespace: "old", agents: []managed.AgentInfo{{ID: "alice"}}}
	next, _ := consolePress(m, tea.KeyMsg{Type: tea.KeyLeft})
	if next.memoryOpen || next.memoryLoading || next.busy || next.memoryRequest != 8 || next.memoryNamespace != "" {
		t.Fatal("leaving Memory retained its open namespace or pending read")
	}
	result, cmd := next.Update(consoleMemory{agent: "alice", request: 7, namespace: "old", entries: []managed.Memory{{Key: "late", Value: "stale"}}})
	next = result.(console)
	if cmd != nil || next.memoryOpen || len(next.memoryEntries) != 0 {
		t.Fatal("late memory response reopened the namespace after arrow navigation")
	}
}

func TestConsoleViewArrowsStayInsideDraftsReadersAndReviews(t *testing.T) {
	m := teamConsoleFixture()
	m.compose("send", "alice", "", "abc")
	m.textCursor = 1
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.textCursor != 2 || m.tab != 6 || string(m.text) != "abc" {
		t.Fatal("Right escaped the draft instead of moving its cursor")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyLeft})
	for _, key := range []tea.KeyType{tea.KeyCtrlLeft, tea.KeyCtrlRight} {
		m, cmd := consolePress(m, tea.KeyMsg{Type: key})
		if cmd != nil || m.tab != 6 || m.composer != "send" || m.composeTarget != "alice" || string(m.text) != "abc" || m.textCursor != 1 {
			t.Fatal("Ctrl-arrow escaped or changed a draft")
		}
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hl")})
	if string(m.text) != "ahlbc" || m.tab != 6 {
		t.Fatal("navigation letters stopped being literal editor text")
	}

	m = console{tab: 2, width: 80, height: 24, reader: &consoleReader{title: "frozen", body: strings.Repeat("saved line\n", 40)}}
	for _, key := range []tea.KeyType{tea.KeyLeft, tea.KeyRight, tea.KeyCtrlLeft, tea.KeyCtrlRight} {
		next, cmd := consolePress(m, tea.KeyMsg{Type: key})
		if cmd != nil || next.tab != 2 || next.reader == nil || next.reader.title != "frozen" || next.reader.body != m.reader.body {
			t.Fatal("view arrows escaped the frozen reader")
		}
	}

	m = teamConsoleFixture()
	m.reviewTeamAction("team.invite")
	for _, key := range []tea.KeyType{tea.KeyLeft, tea.KeyRight, tea.KeyCtrlLeft, tea.KeyCtrlRight} {
		next, cmd := consolePress(m, tea.KeyMsg{Type: key})
		if cmd != nil || next.tab != 6 || next.teamAction == nil || next.teamAction.agentID != "bob" || next.teamAction.teamID != "team_original" || next.teamMemberIndex != m.teamMemberIndex {
			t.Fatal("view arrows escaped or retargeted an explicit action review")
		}
	}
}

func TestConsoleArrowHintsMatchActiveView(t *testing.T) {
	for tab := 0; tab < 8; tab++ {
		m := console{tab: tab}
		want := "←/→ views"
		if tab == 2 || tab == 6 {
			want = "Ctrl+←/→ views"
		}
		if !strings.Contains(m.contextualKeys(), want) {
			t.Fatalf("view %d hides its arrow navigation cue", tab)
		}
	}
}
