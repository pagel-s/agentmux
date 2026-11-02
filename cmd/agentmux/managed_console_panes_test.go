package main

import (
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/sessioninventory"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConsoleArrowsNavigateFocusedContentWithoutChangingAgent(t *testing.T) {
	for _, tab := range []int{0, 2, 3, 4, 5, 6, 7} {
		m := sessionSwitcherFixture()
		m.tab, m.contentFocus, m.target = tab, true, 0
		m.data.activity.Messages = []managed.Message{{ID: "one"}, {ID: "two"}}
		m.data.activity.Tasks = []managed.Task{{ID: "one"}, {ID: "two"}}
		m.data.activity.Events = []managed.Event{{ID: "one"}, {ID: "two"}}
		m.data.boundaries.Memory = []managed.BoundaryScope{{Name: "one"}, {Name: "two"}}
		m.data.teams = []managed.Team{{ID: "one"}, {ID: "two"}}
		m.data.observed = []sessioninventory.Session{{ID: "one"}, {ID: "two"}}
		before := m.selectedID()
		next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyDown})
		if cmd != nil || next.selectedID() != before || next.tab != tab {
			t.Fatalf("view %d content arrows moved the sidebar or submitted a command", tab)
		}
		indices := map[int]int{0: next.messageIndex, 2: next.target, 3: next.scope, 4: next.task, 5: next.observedSelected, 6: next.teamIndex, 7: next.eventIndex}
		if indices[tab] != 1 {
			t.Fatalf("view %d did not select its second content row: %d", tab, indices[tab])
		}
		next, _ = consolePress(next, tea.KeyMsg{Type: tea.KeyTab})
		if next.contentFocus || next.tab != tab {
			t.Fatal("Tab did not focus the sidebar in the same view")
		}
		next, _ = consolePress(next, tea.KeyMsg{Type: tea.KeyDown})
		if next.selectedID() == before {
			t.Fatalf("view %d sidebar arrows did not select another agent", tab)
		}
	}
}

func TestConsoleFocusedTaskEnterReadsExactTaskAndPickerKeepsTabMeaning(t *testing.T) {
	m := sessionSwitcherFixture()
	m.tab, m.contentFocus = 4, true
	m.data.activity.Tasks = []managed.Task{{ID: "task-exact", To: "alice", Text: "inspect this exact task"}}
	next, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || next.reader == nil || !strings.Contains(next.reader.body, "task-exact") || next.selectedID() != m.selectedID() {
		t.Fatal("Enter did not read the selected task without opening a native terminal")
	}
	m.openSessionSwitcher()
	next, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyTab})
	if next.composer != "" || !next.contentFocus || next.tab != 4 || cmd == nil {
		t.Fatal("Tab in the session picker lost select-here behavior")
	}
}
