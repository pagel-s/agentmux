package main

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
)

// A dashboard consumes only its own view's pending native focus. Every attached
// client has a separate view/dashboard, so no observer state is shared.
func consumeConsoleFocus(state string) (string, uint64) {
	if !strings.HasPrefix(os.Getenv("TMUX"), managedlaunch.SocketPath(state)+",") || os.Getenv("TMUX_PANE") == "" {
		return "", 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	pane := os.Getenv("TMUX_PANE")
	out, err := exec.CommandContext(ctx, "tmux", "-S", managedlaunch.SocketPath(state), "display-message", "-p", "-t", pane, "#{@agentmux_role}\t#{session_id}\t#{@agentmux_focus_pending}\t#{?@agentmux_selection_revision,#{@agentmux_selection_revision},0}").Output()
	if err != nil {
		return "", 0
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(parts) != 4 || parts[0] != "dashboard" || !managedconfig.ValidID(parts[2]) {
		return "", 0
	}
	revision, err := strconv.ParseUint(parts[3], 10, 64)
	if err != nil {
		return "", 0
	}
	clear := managedlaunch.ShellCommand([]string{"set-option", "-t", parts[1], "@agentmux_focus_pending", ""})
	condition := "#{&&:#{==:#{@agentmux_focus_pending}," + parts[2] + "},#{==:#{?@agentmux_selection_revision,#{@agentmux_selection_revision},0}," + parts[3] + "}}"
	if exec.CommandContext(ctx, "tmux", "-S", managedlaunch.SocketPath(state), "if-shell", "-t", parts[1], "-F", condition, clear).Run() != nil {
		return "", 0
	}
	return parts[2], revision
}

// Explicit dashboard navigation supersedes native focus that was observed
// before that choice. Refresh results also carry this revision, fencing reads
// already in flight when the user chooses a different session.
func (m *console) explicitSessionChoice(index int) {
	m.syncHistoryScopes()
	previous := m.selectedID()
	m.selectionRevision++
	m.pendingFocus = ""
	discardConsoleFocus(m.state, m.selectionRevision)
	if len(m.agents) > 0 {
		m.selected = max(0, min(index, len(m.agents)-1))
	}
	m.resetSessionHistory(previous, m.selectedID())
	m.syncHistoryScopes()
}
func discardConsoleFocus(state string, revision uint64) {
	if !strings.HasPrefix(os.Getenv("TMUX"), managedlaunch.SocketPath(state)+",") || os.Getenv("TMUX_PANE") == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "-S", managedlaunch.SocketPath(state), "display-message", "-p", "-t", os.Getenv("TMUX_PANE"), "#{@agentmux_role}\t#{session_id}").Output()
	if err != nil {
		return
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(parts) != 2 || parts[0] != "dashboard" {
		return
	}
	_ = exec.CommandContext(ctx, "tmux", "-S", managedlaunch.SocketPath(state), "set-option", "-t", parts[1], "@agentmux_selection_revision", strconv.FormatUint(revision, 10), ";", "set-option", "-t", parts[1], "@agentmux_focus_pending", "").Run()
}

func currentConsoleFocusRevision(state string) uint64 {
	if !strings.HasPrefix(os.Getenv("TMUX"), managedlaunch.SocketPath(state)+",") || os.Getenv("TMUX_PANE") == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "tmux", "-S", managedlaunch.SocketPath(state), "display-message", "-p", "-t", os.Getenv("TMUX_PANE"), "#{?@agentmux_selection_revision,#{@agentmux_selection_revision},0}").Output()
	if err != nil {
		return 0
	}
	revision, _ := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	return revision
}
