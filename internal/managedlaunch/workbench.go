package managedlaunch

import (
	"context"
	"strings"
)

// Bind only the dedicated managed server. User tmux servers and configuration
// are never modified. Popups run owner controls outside agent sandboxes.
func (l *Launcher) configureWorkbench(ctx context.Context, runtime Runtime) error {
	for _, args := range [][]string{
		{"set-option", "-t", runtime.Session, "status-style", "bg=#18151f,fg=#a8a1b6"},
		{"set-option", "-t", runtime.Session, "status-left", "#[fg=#c4b5fd,bold] agentmux #[default]│ " + runtime.Session + " "},
		{"set-option", "-t", runtime.Session, "status-left-length", "60"},
		{"set-option", "-t", runtime.Session, "status-right", "#[fg=#a78bfa]F1#[default] workbench  #[fg=#a78bfa]C-b b#[default] board "},
		{"set-option", "-t", runtime.Session, "window-status-current-style", "fg=#ede9fe,bg=#4c1d95,bold"},
		{"set-option", "-t", runtime.Session, "window-status-format", " #I:#W "},
		{"set-option", "-t", runtime.Session, "window-status-current-format", " #I:#W "},
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
