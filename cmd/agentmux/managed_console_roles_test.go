package main

import (
	"context"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConsoleRoleConflictReviewPreservesDraftAndRequiresSeparateSave(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	m := console{state: state, client: client, width: 90, height: 30, agents: []managed.AgentInfo{{ID: "peer", Config: managedconfig.Agent{Role: "initial role"}}}}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'V'}})
	if m.composer != "role" || m.composeTarget != "peer" || m.roleVersion != 0 {
		t.Fatal("role editor did not freeze identity/version")
	}
	m.text = []rune("my retained role")
	m.textCursor = len(m.text)
	if _, err := workCall(context.Background(), client, "agent.role", map[string]any{"id": "peer", "role": "another owner's edit", "version": 0}); err != nil {
		t.Fatal(err)
	}
	m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("role submit missing")
	}
	next, _ := m.Update(cmd())
	m = next.(console)
	if m.failedDraft == nil || !strings.Contains(m.message, "conflict") {
		t.Fatalf("role conflict draft missing: %s", m.message)
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if m.composer != "role" || string(m.text) != "my retained role" || m.roleVersion != 0 {
		t.Fatal("restore silently adopted a new role version")
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd == nil {
		t.Fatal("explicit role review missing")
	}
	next, _ = m.Update(cmd())
	m = next.(console)
	if m.reader == nil || m.reader.roleReview == nil || !strings.Contains(m.reader.body, "another owner's edit") {
		t.Fatal("role review omitted latest value")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.reader != nil || m.roleVersion != 1 || string(m.text) != "my retained role" {
		t.Fatal("review did not preserve draft/adopt only reviewed version")
	}
	info, err := workAgentForLabel(context.Background(), client, "peer")
	if err != nil || info.Config.Role != "another owner's edit" {
		t.Fatal("review saved a role implicitly")
	}
	m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("explicit save missing")
	}
	next, _ = m.Update(cmd())
	m = next.(console)
	info, err = workAgentForLabel(context.Background(), client, "peer")
	if err != nil || info.Config.Role != "my retained role" || info.RoleVersion != 2 {
		t.Fatal("reviewed role save failed")
	}
}

func TestConsoleNativeProfileChoiceIsExplicitAndPromptFreeSkipsRole(t *testing.T) {
	m := console{newNative: consoleNativeDraft{provider: "antigravity", project: "/tmp/project"}}
	m.compose("new-project", "project directory", "", "/tmp/project")
	next, _ := m.nativeDraftNext()
	m = next.(console)
	if m.composer != "new-profile" || string(m.text) != "native-only" {
		t.Fatal("Antigravity profile not explicit")
	}
	m.text = []rune("coordinated")
	next, cmd := m.nativeDraftNext()
	m = next.(console)
	if cmd != nil || m.composer != "new-profile" {
		t.Fatal("unsupported Antigravity role injection accepted")
	}
	m.text = []rune("native-only")
	next, cmd = m.nativeDraftNext()
	m = next.(console)
	if cmd == nil || !m.newNative.nativeOnly || m.newNative.role != "" || m.composer != "" {
		t.Fatal("native-only profile asked for or injected role")
	}
}
