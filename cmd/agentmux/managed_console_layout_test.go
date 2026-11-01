package main

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestConsoleDividerKeepsItsColumnAcrossRowsAndResizes(t *testing.T) {
	// Exercise real ANSI styles as well as wide and combined characters: byte
	// offsets and rune counts cannot establish a terminal column alignment.
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.TrueColor)
	oldAccent, oldMuted, oldSelected := consoleAccent, consoleMuted, consoleSelected
	consoleAccent = renderer.NewStyle().Foreground(lipgloss.Color("#A78BFA")).Bold(true)
	consoleMuted = renderer.NewStyle().Foreground(lipgloss.Color("#94A3B8"))
	consoleSelected = renderer.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#4C1D95")).Bold(true)
	t.Cleanup(func() {
		consoleAccent, consoleMuted, consoleSelected = oldAccent, oldMuted, oldSelected
	})

	agents := []managed.AgentInfo{
		{ID: "claude", Title: "探究 the project 👩‍💻 e\u0301", State: "active", Config: managedconfig.Agent{Harness: "claude", Interaction: "interactive", Workdir: "/home/owner/探索/project"}},
		{ID: "codex", Title: "Review the approach", State: "active", Config: managedconfig.Agent{Harness: "codex", Interaction: "interactive", Workdir: "/home/owner/project"}},
	}
	for _, size := range [][2]int{{60, 16}, {64, 22}, {80, 24}, {85, 12}, {100, 18}, {105, 24}, {231, 62}} {
		for tab := range 8 {
			for selected := range agents {
				t.Run(fmt.Sprintf("%dx%d/view%d/agent%d", size[0], size[1], tab+1, selected), func(t *testing.T) {
					m := console{width: size[0], height: size[1], tab: tab, selected: selected, agents: agents, data: consoleData{runtime: managedlaunch.Runtime{Session: "try-agentmux", State: "running"}}}
					view := m.View()
					if !strings.Contains(view, "\x1b[") {
						t.Fatal("fixture did not exercise ANSI styles")
					}
					lines := strings.Split(ansi.Strip(view), "\n")
					if len(lines) > size[1] {
						t.Fatalf("rendered %d rows in %d-row terminal", len(lines), size[1])
					}
					dividerRows := 0
					for row, line := range lines {
						if got := ansi.StringWidth(line); got > size[0] {
							t.Fatalf("row %d uses %d cells in %d-column terminal: %q", row, got, size[0], line)
						}
						before, _, hasDivider := strings.Cut(line, "│")
						if !hasDivider {
							continue
						}
						dividerRows++
						wantColumn := 31
						if size[0] < 105 {
							wantColumn = 25
						}
						if size[0] < 85 {
							wantColumn = 23
						}
						if got := ansi.StringWidth(before); got != wantColumn {
							t.Fatalf("divider on row %d is at cell %d, want %d: %q", row, got, wantColumn, line)
						}
					}
					if size[0] >= 64 && dividerRows < 2 {
						t.Fatalf("expected multiline divider, found %d rows", dividerRows)
					}
					if size[0] < 64 && dividerRows != 0 {
						t.Fatal("compact layout unexpectedly includes a sidebar divider")
					}
				})
			}
		}
	}
}
