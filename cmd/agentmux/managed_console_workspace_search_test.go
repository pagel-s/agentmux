package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managedlaunch"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func workspaceSearchPress(m console, msg tea.KeyMsg) console {
	next, _ := m.key(msg)
	return next.(console)
}

func workspaceSearchText(m console, text string) console {
	for _, r := range text {
		m = workspaceSearchPress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestWorkspaceSearchIsModalLiteralAndRestoresExactFocus(t *testing.T) {
	m := sessionSwitcherFixture()
	m.compose("send", "alice", "", "UNSENT OWNER DRAFT")
	m.reader = &consoleReader{title: "FROZEN READER", body: "frozen content"}
	m.ownerReview = &consoleOwnerReview{operation: "agent.stop", args: map[string]any{"id": "frozen-target"}}
	review, reader, draft := m.ownerReview, m.reader, string(m.text)
	m.workspacePicker = &consoleWorkspacePicker{selected: 2, rows: []workspaceListing{
		{Session: "work", State: "/tmp/work", Status: "running"},
		{Session: "review", State: "/tmp/review-old", Status: "stopped"},
		{Session: "review", State: "/tmp/jqk/review-current", Status: "running"},
	}}
	m = workspaceSearchPress(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	m = workspaceSearchText(m, "jqk")
	if m.workspacePicker == nil || m.workspacePicker.query != "jqk" || len(m.workspacePicker.visibleRows()) != 1 || m.workspacePicker.selectedState() != "/tmp/jqk/review-current" {
		t.Fatal("literal j/k/q query navigated or closed the picker")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "Filter: jqk") || !strings.Contains(view, "Ctrl+U clears") || strings.Contains(view, "Esc/q") || strings.Contains(view, "UNSENT OWNER DRAFT") {
		t.Fatalf("search panel or footer misrepresented keyboard ownership:\n%s", view)
	}
	m = workspaceSearchText(m, " missing")
	next, command := m.key(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(console)
	if command != nil || m.workspacePicker == nil || !strings.Contains(ansi.Strip(m.View()), "No matching workspaces") {
		t.Fatal("empty filtered results opened an unseen workspace")
	}
	m = workspaceSearchPress(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if m.workspacePicker.selectedState() != "/tmp/jqk/review-current" {
		t.Fatal("clearing filter lost the exact original selected state")
	}
	m = workspaceSearchPress(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.workspacePicker != nil || m.ownerReview != review || m.reader != reader || string(m.text) != draft {
		t.Fatal("searching/cancelling changed the frozen underlying action or draft")
	}
}

func TestWorkspaceSearchSurvivesInventoryLoadAndUsesExactFilteredState(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	stateA, stateB := filepath.Join(root, "current"), filepath.Join(root, "jqk-target")
	channel := filepath.Join(root, "handoff.json")
	t.Setenv("AGENTMUX_HANDOFF_FILE", channel)
	t.Setenv("TMUX", managedlaunch.SocketPath(stateA)+",123,0")
	m := console{state: stateA, workspaceRequest: 4, workspacePicker: &consoleWorkspacePicker{loading: true}}
	m = workspaceSearchPress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = workspaceSearchText(m, "jqk running")
	rows := []workspaceListing{{Session: "same-label", State: stateA, Status: "running"}, {Session: "same-label", State: stateB, Status: "running"}}
	next, _ := m.Update(consoleWorkspaceList{request: 4, rows: rows})
	m = next.(console)
	if m.workspacePicker.query != "jqk running" || m.workspacePicker.selectedState() != stateB || m.workspacePicker.loading {
		t.Fatal("asynchronous inventory load reset the query or selected an unseen state")
	}
	next, _ = m.Update(consoleWorkspaceList{request: 4, rows: []workspaceListing{rows[1], rows[0]}})
	m = next.(console)
	if m.workspacePicker.selectedState() != stateB {
		t.Fatal("inventory reordering retargeted the filtered exact state")
	}
	next, command := m.key(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(console)
	if command == nil || m.workspacePicker != nil {
		t.Fatalf("explicit filtered selection did not request handoff: %s", m.message)
	}
	raw, err := os.ReadFile(channel)
	var request map[string]string
	if err != nil || json.Unmarshal(raw, &request) != nil || request["state"] != stateB {
		t.Fatalf("handoff did not retain the exact displayed state: %q %v", raw, err)
	}
}

func TestWorkspaceSearchUnicodeBackspaceAndCtrlNavigation(t *testing.T) {
	m := console{workspacePicker: &consoleWorkspacePicker{rows: []workspaceListing{
		{Session: "東京 one", State: "/tmp/one", Status: "running"},
		{Session: "東京 two", State: "/tmp/two", Status: "running"},
	}}}
	m = workspaceSearchPress(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	m = workspaceSearchText(m, "東京x")
	m = workspaceSearchPress(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.workspacePicker.query != "東京" || len(m.workspacePicker.visibleRows()) != 2 {
		t.Fatal("backspace corrupted a Unicode query")
	}
	m = workspaceSearchPress(m, tea.KeyMsg{Type: tea.KeyCtrlN})
	if m.workspacePicker.selectedState() != "/tmp/two" {
		t.Fatal("Ctrl+N did not navigate filtered results")
	}
	m = workspaceSearchPress(m, tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.workspacePicker.selectedState() != "/tmp/one" {
		t.Fatal("Ctrl+P escaped the modal workspace picker")
	}
}
