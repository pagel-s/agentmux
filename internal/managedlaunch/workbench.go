package managedlaunch

import "context"

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
		command := ShellCommand([]string{l.options.Executable, "managed", "dashboard", "--state", l.options.StateDir, "--view", binding.view, "--popup"})
		if _, err := l.tmux(ctx, "bind-key", binding.key, "display-popup", "-e", "AGENTMUX_CLIENT=#{client_name}", "-E", "-w", "95%", "-h", "90%", command); err != nil {
			return err
		}
	}
	return nil
}
