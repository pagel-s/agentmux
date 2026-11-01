package main

import (
	"strings"
	"testing"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
)

func TestTaskHistoryNavigationKeepsPageContextAndRejectsLatePages(t *testing.T) {
	m := console{tab: 4, taskMore: true, taskCursor: "last-old", taskQuery: "find", agents: []managed.AgentInfo{{ID: "a"}}, data: consoleData{selected: "a", activity: managedActivitySnapshot{Tasks: []managed.Task{{ID: "visible"}}}}}
	m.data.taskPage = &consoleTaskPage{Query: "find", Cursor: "last-old", More: true}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(console)
	if cmd == nil || m.taskAfter != "last-old" || len(m.taskBack) != 1 {
		t.Fatalf("older page not requested: %+v", m)
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = next.(console)
	if cmd != nil || len(m.taskBack) != 1 {
		t.Fatal("repeated paging advanced history before the requested page arrived")
	}
	data := m.data
	data.taskPage = &consoleTaskPage{Query: "previous", After: "", Cursor: "wrong"}
	data.activity.Tasks = []managed.Task{{ID: "late"}}
	data.agents = m.agents
	next, _ = m.Update(data)
	m = next.(console)
	if m.data.activity.Tasks[0].ID != "visible" || m.taskCursor == "wrong" {
		t.Fatal("late query replaced current history")
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(console)
	if cmd == nil || m.taskAfter != "" || len(m.taskBack) != 0 {
		t.Fatal("newer page context lost")
	}
	m.width, m.height = 120, 32
	if !strings.Contains(m.View(), "PgDn older") || !strings.Contains(m.View(), "Search: find") {
		t.Fatal("history controls not visible")
	}
}
