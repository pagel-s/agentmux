package managedlaunch

import (
	"context"
	"errors"
	"strings"

	"agentmux/internal/managed"
)

// Option values are expanded once. q/h escapes status styling hashes; E/T
// would interpret label text as another format and must never be used here.
const workbenchWindowFormat = " #I:#{?@agentmux_title,#{q/h:@agentmux_title},#W} "

// Bind only the dedicated managed server. User tmux servers and configuration
// are never modified. Popups run owner controls outside agent sandboxes.
func (l *Launcher) configureWorkbench(ctx context.Context, runtime Runtime) error {
	for _, args := range [][]string{
		{"set-option", "-t", runtime.Session, "status-style", "bg=#18151f,fg=#a8a1b6"},
		{"set-option", "-t", runtime.Session, "status-left", "#[fg=#c4b5fd,bold] agentmux #[default]│ " + runtime.Session + " "},
		{"set-option", "-t", runtime.Session, "status-left-length", "60"},
		{"set-option", "-t", runtime.Session, "status-right", "#[fg=#a78bfa]F1#[default] workbench  #[fg=#a78bfa]C-b b#[default] board "},
		{"set-option", "-t", runtime.Session, "window-status-current-style", "fg=#ede9fe,bg=#4c1d95,bold"},
		{"set-option", "-t", runtime.Session, "window-status-format", workbenchWindowFormat},
		{"set-option", "-t", runtime.Session, "window-status-current-format", workbenchWindowFormat},
		{"set-option", "-t", runtime.Session, "mouse", "on"},
		{"bind-key", "-n", "F1", "select-window", "-t", runtime.DashboardPane},
		{"bind-key", "g", "select-window", "-t", runtime.DashboardPane},
	} {
		if _, err := l.tmux(ctx, args...); err != nil {
			return err
		}
	}
	for _, binding := range []struct{ key, view string }{{"b", "board"}, {"P", "connections"}, {"m", "memory"}, {"t", "tasks"}} {
		// tmux 3.2 supports popups but not display-popup -e, and does not
		// expand formats in popup argv. run-shell -C expands the invoking
		// client's identity once, then executes a tmux command, not a shell.
		// Multiple popup arguments execute env directly; owner paths remain
		// literal argv, including format-looking # text and shell metacharacters.
		command := ShellCommand([]string{l.options.Executable, "managed", "dashboard", "--state", l.options.StateDir, "--view", binding.view, "--popup"})
		command = strings.ReplaceAll(command, "#", "##")
		popup := "display-popup -E -w 95% -h 90% env AGENTMUX_CLIENT=#{q:client_name} " + command
		if _, err := l.tmux(ctx, "bind-key", binding.key, "run-shell", "-C", popup); err != nil {
			return err
		}
	}
	return nil
}

func (l *Launcher) syncWindowTitle(ctx context.Context, runtime Runtime, pane, agent, title string) error {
	if !validPane(pane) || !validAgentID(agent) {
		return errors.New("invalid labelled worker identity")
	}
	if err := managed.ValidateSessionTitle(title); err != nil {
		return err
	}
	identity, err := l.tmux(ctx, "display-message", "-p", "-t", pane, "#{@agentmux_runtime}\t#{@agentmux_role}\t#{@agentmux_agent}")
	if err != nil || strings.TrimSpace(identity) != runtime.AttemptID+"\tworker\t"+agent {
		return errors.New("labelled worker ownership changed")
	}
	label := ""
	if title != "" {
		runes := []rune(title)
		if len(runes) > 36 {
			title = string(runes[:36]) + "…"
		}
		label = title + " [" + agent + "]"
	}
	_, err = l.tmux(ctx, "set-option", "-w", "-t", pane, "@agentmux_title", label)
	return err
}
