package tui

import (
	"strings"
	"testing"

	"agentmux/internal/tmux"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelFocusKeyQueuesAttachAndQuits(t *testing.T) {
	initial := model{
		session: "demo",
		panes: []tmux.PaneStatus{
			{Name: "architect"},
			{Name: "coder"},
		},
		cursor: 1,
	}

	next, cmd := initial.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	updated := next.(model)
	if updated.focus != "coder" {
		t.Fatalf("focus = %q, want %q", updated.focus, "coder")
	}
	if cmd == nil {
		t.Fatal("focus key returned nil cmd, want quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("focus cmd returned %T, want tea.QuitMsg", cmd())
	}
}

func TestViewShowsSelectedPaneDetailsLogsAndCompactKV(t *testing.T) {
	m := model{
		session: "demo",
		panes: []tmux.PaneStatus{
			{
				Name:            "architect",
				PaneID:          "%1",
				WindowIndex:     0,
				WindowName:      "main",
				Provider:        "claude",
				Model:           "sonnet",
				IntegrationMode: "native-mcp",
				PersonaChannel:  "system-prompt",
				ToolChannel:     "native-mcp",
				Pid:             123,
				PaneActive:      true,
			},
		},
		kv: map[string]string{
			"design_spec": "{\n  \"colors\": [\"blue\", \"gold\"]\n}",
			"probe":       "ok-42",
		},
		logs:   "● agentmux - kv_set (MCP)\n● ARCH_DONE\n",
		width:  140,
		height: 40,
	}

	out := m.View()
	for _, want := range []string{
		"SELECTED PANE",
		"RECENT OUTPUT",
		"architect",
		"native-mcp",
		"agentmux - kv_set (MCP)",
		"ARCH_DONE",
		"BLACKBOARD (KV)",
		"design_spec: { \"colors\": [\"blue\", \"gold\"] }",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q in output:\n%s", want, out)
		}
	}
}

func TestSummarizeKVValueCollapsesWhitespace(t *testing.T) {
	got := summarizeKVValue("{\n  \"a\": 1,\n  \"b\": 2\n}\n", 80)
	if got != `{ "a": 1, "b": 2 }` {
		t.Fatalf("summarizeKVValue = %q, want compact single-line JSON-ish string", got)
	}
}
