package main

import (
	"testing"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConsolePowerTasksActionResetsFiltersAndKeepsExactRecipient(t *testing.T) {
	m := sessionSwitcherFixture()
	m.taskRecipient, m.taskQuery, m.taskState = "bob", "old query", "failed"
	m.taskAfter, m.taskCursor = "older", "last"
	m.taskBack, m.taskMore, m.task = []string{"first"}, true, 2
	m.taskEpoch = 7
	m.data.activity.Tasks = []managed.Task{{ID: "old-task", To: "bob"}}
	m.data.taskPage = &consoleTaskPage{Recipient: "bob", Query: "old query", State: "failed", After: "older", Epoch: 7}
	m.openActions()
	m.text = []rune("Tasks for selected session")
	m.selected = 1 // A poll or another selection must not retarget this action.
	next, cmd := m.invokeMenuAction()
	m = next.(console)
	if cmd == nil || m.selectedID() != "alice" || m.tab != 4 || !m.contentFocus || m.taskRecipient != "alice" {
		t.Fatal("task history did not open for the frozen selected recipient")
	}
	if m.taskQuery != "" || m.taskState != "" || m.taskAfter != "" || m.taskCursor != "" || len(m.taskBack) != 0 || m.taskMore || m.task != 0 || len(m.data.activity.Tasks) != 0 || m.data.taskPage != nil || m.taskEpoch != 8 {
		t.Fatal("direct task history retained an unrelated filter, page or request epoch")
	}
	data := m.data
	data.selected, data.agents = "alice", m.agents
	data.taskPage = &consoleTaskPage{Recipient: "alice", Epoch: m.taskEpoch, Cursor: "fresh"}
	data.activity.Tasks = []managed.Task{{ID: "fresh", To: "alice"}}
	next, _ = m.Update(data)
	m = next.(console)
	for _, stale := range []consoleTaskPage{
		{Recipient: "bob", Epoch: m.taskEpoch, Cursor: "wrong-recipient"},
		{Recipient: "alice", Epoch: m.taskEpoch - 1, Cursor: "same-scope-old-request"},
	} {
		data.taskPage = &stale
		data.activity.Tasks = []managed.Task{{ID: "stale", To: stale.Recipient}}
		next, _ = m.Update(data)
		m = next.(console)
		if len(m.data.activity.Tasks) != 1 || m.data.activity.Tasks[0].ID != "fresh" || m.taskCursor != "fresh" || m.data.taskPage.Cursor != "fresh" {
			t.Fatalf("late task history replaced the exact recipient's current page: %+v", stale)
		}
	}
}

func TestConsolePowerNamedAttentionIsIdempotentAndBangToggles(t *testing.T) {
	m := sessionSwitcherFixture()
	for range 2 {
		m.openActions()
		m.text = []rune("Attention queue")
		next, cmd := m.invokeMenuAction()
		m = next.(console)
		if cmd == nil || !m.attentionOnly || m.composer != "find" {
			t.Fatal("named Attention action did not consistently open attention")
		}
	}
	next, _ := m.key(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(console)
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'!'}})
	m = next.(console)
	if cmd == nil || m.attentionOnly || m.composer != "find" {
		t.Fatal("the ! shortcut no longer toggles attention off")
	}
}
