package managedlaunch

import (
	"context"
	"errors"
	"strings"

	"agentmux/internal/managed"
)

// Option values are expanded once. q/h escapes status styling hashes; E/T
// would interpret label text as another format and must never be used here.
const workbenchServiceFormat = "#{||:#{==:#{@agentmux_role},broker},#{==:#{@agentmux_role},supervisor}}"
const workbenchVisibleFormat = "#{&&:#{!=:#{@agentmux_role},broker},#{!=:#{@agentmux_role},supervisor}}"
const workbenchDashboardFormat = "#{==:#{@agentmux_role},dashboard}"
const workbenchWindowLabel = "#{?" + workbenchDashboardFormat + ",Workbench,#{?@agentmux_layout_label,#{q/h:@agentmux_layout_label},#{?@agentmux_title,#{q/h:@agentmux_title},#{?@agentmux_agent,#{q/h:@agentmux_agent},#W}}}}"
const workbenchWindowFormat = "#{?" + workbenchServiceFormat + ",, " + workbenchWindowLabel + " }"
const workbenchCurrentWindowFormat = "#{?" + workbenchServiceFormat + ", Service: #W (diagnostics) , " + workbenchWindowLabel + " }"

// All external values are positional arguments, never shell or tmux formats.
// Capture is read-only and bounded; the popup cannot select a runtime window.
const workbenchServiceLogScript = `case "$2" in
  %*[!0-9]*|%|"") exit 2 ;;
  %*) ;;
  *) exit 2 ;;
esac
printf 'Runtime service: %s\nRead-only recent output snapshot.\n\n' "$3"
if output=$(tmux -S "$1" capture-pane -p -S -2000 -t "$2"); then
  if [ -n "$output" ]; then
    printf '%s\n' "$output"
  else
    printf '(No recent output; this runtime service may be idle.)\n'
  fi
else
  printf '\nService output unavailable; inspect managed status for diagnostics.\n'
fi
printf '\nPress Enter to close. Agent terminals keep running.\n'
IFS= read -r _
`

// Older grouped attachments have no session dashboard option. Keep their
// original core dashboard as the inherited default when a new attachment
// installs the dynamic server-wide shortcuts; new views override it locally.
func (l *Launcher) ensureDashboardFallback(ctx context.Context, runtime Runtime) error {
	if !l.ownsSession(ctx, runtime) {
		return errors.New("managed dashboard ownership changed")
	}
	if runtime.DashboardPane == "" {
		return nil
	}
	if !validPane(runtime.DashboardPane) {
		return errors.New("invalid managed dashboard identity")
	}
	panes, err := l.panes(ctx, runtime)
	if err != nil {
		return err
	}
	for _, pane := range panes {
		if pane.ID == runtime.DashboardPane {
			if pane.Attempt != runtime.AttemptID || pane.Role != "dashboard" {
				return errors.New("managed dashboard ownership changed")
			}
			_, err := l.tmux(ctx, "set-option", "-g", "@agentmux_dashboard", runtime.DashboardPane)
			return err
		}
	}
	// A removed core dashboard cannot serve old views, but it must not prevent
	// creating a healthy new private UI for the still-owned worker lifecycle.
	return nil
}

// Bind only the dedicated managed server. User tmux servers and configuration
// are never modified. Popups run owner controls outside agent sandboxes.
func (l *Launcher) configureWorkbench(ctx context.Context, runtime Runtime) error {
	for _, args := range [][]string{
		{"set-option", "-t", runtime.Session, "@agentmux_dashboard", runtime.DashboardPane},
		// These are window options. Install dedicated-server defaults as well
		// as the current window values, including newly linked/created workers.
		{"set-option", "-gw", "window-status-format", workbenchWindowFormat},
		{"set-option", "-gw", "window-status-current-format", workbenchCurrentWindowFormat},
		{"set-option", "-gw", "window-status-current-style", "fg=#ede9fe,bg=#4c1d95,bold"},
		{"set-option", "-t", runtime.Session, "status-style", "bg=#18151f,fg=#a8a1b6"},
		{"set-option", "-t", runtime.Session, "status-left", "#[fg=#c4b5fd,bold] agentmux #[default]│ " + runtime.Session + " "},
		{"set-option", "-t", runtime.Session, "status-left-length", "60"},
		{"set-option", "-t", runtime.Session, "status-right", "#[fg=#a78bfa]F1#[default] Workbench  #[fg=#a78bfa]Ctrl+b then D#[default]: Services "},
		{"set-option", "-t", runtime.Session, "status-right-length", "45"},
		{"set-option", "-t", runtime.Session, "window-status-current-style", "fg=#ede9fe,bg=#4c1d95,bold"},
		{"set-option", "-t", runtime.Session, "window-status-format", workbenchWindowFormat},
		{"set-option", "-t", runtime.Session, "window-status-current-format", workbenchCurrentWindowFormat},
		{"set-option", "-t", runtime.Session, "mouse", "on"},
		{"bind-key", "-n", "F1", "run-shell", "-C", "select-window -t #{q:@agentmux_dashboard}"},
		{"bind-key", "g", "run-shell", "-C", "select-window -t #{q:@agentmux_dashboard}"},
		{"bind-key", "w", "choose-tree", "-w", "-f", workbenchVisibleFormat},
		{"bind-key", "c", "new-window", "-c", "#{?@agentmux_project,#{@agentmux_project},#{pane_current_path}}"},
		{"bind-key", "%", "split-window", "-h", "-c", "#{?@agentmux_project,#{@agentmux_project},#{pane_current_path}}"},
		{"bind-key", "\"", "split-window", "-v", "-c", "#{?@agentmux_project,#{@agentmux_project},#{pane_current_path}}"},
	} {
		if _, err := l.tmux(ctx, args...); err != nil {
			return err
		}
	}
	// Each attachment owns this hook and its private dashboard. Record a native
	// window selection before F1 returns, without sending keys to an editor.
	if err := l.configurePaneProjects(ctx, runtime); err != nil {
		return err
	}
	return l.configureWorkbenchHooks(ctx, runtime)
}

func (l *Launcher) configurePaneProjects(ctx context.Context, runtime Runtime) error {
	store, err := managed.Open(l.options.StateDir)
	if err != nil {
		return nil
	}
	defer store.Close()
	panes, err := l.panes(ctx, runtime)
	if err != nil {
		return err
	}
	for _, pane := range panes {
		tracked, ok := runtime.Agents[pane.Agent]
		if !ok || tracked.PaneID != pane.ID || pane.Attempt != runtime.AttemptID || pane.Role != "worker" {
			continue
		}
		agent, err := store.Agent(pane.Agent)
		if err != nil || agent.Workdir == "" {
			continue
		}
		if _, err := l.tmux(ctx, "set-option", "-p", "-t", pane.ID, "@agentmux_project", agent.Workdir); err != nil {
			return err
		}
	}
	return nil
}

func (l *Launcher) configureWorkbenchHooks(ctx context.Context, runtime Runtime) error {
	core := runtime.Session
	if saved, err := Snapshot(l.options.StateDir); err == nil && saved.AttemptID == runtime.AttemptID {
		core = saved.Session
	}
	rememberCommand := "set-option -t #{q:session_id} @agentmux_focus_pending #{q:@agentmux_agent}; set-option -t #{q:session_id} @agentmux_last_agent #{q:@agentmux_agent}; " + ShellCommand([]string{"set-option", "-t", core, "@agentmux_last_agent"}) + " #{q:@agentmux_agent}"
	remember := ShellCommand([]string{"if-shell", "-F", "#{&&:#{==:#{@agentmux_role},worker},#{!=:#{@agentmux_agent},}}", "run-shell -C " + ShellCommand([]string{rememberCommand})})
	for _, event := range []string{"after-select-window[42]", "after-select-pane[42]"} {
		if _, err := l.tmux(ctx, "set-hook", "-t", runtime.Session, event, remember); err != nil {
			return err
		}
	}
	// User-created windows belong to the workspace, rather than the disposable
	// attachment. Linking preserves shells when this attachment detaches.
	persist := ShellCommand([]string{"set-option", "-w", "-t", "#{window_id}", "@agentmux_scratch", runtime.AttemptID}) + "; " + ShellCommand([]string{"link-window", "-d", "-s", "#{window_id}", "-t", core + ":"})
	hook := ShellCommand([]string{"if-shell", "-F", "#{&&:#{!=:#{@agentmux_view},},#{||:#{==:#{@agentmux_role},},#{==:#{@agentmux_role},worker}}}", "run-shell -C " + ShellCommand([]string{persist})})
	if _, err := l.tmux(ctx, "set-hook", "-t", runtime.Session, "after-new-window[43]", hook); err != nil {
		return err
	}
	// tmux has no after-break-pane hook. Anchor the resulting window as part
	// of the normal breakout binding, after break-pane selects its new window.
	if _, err := l.tmux(ctx, "bind-key", "!", "break-pane; "+ShellCommand([]string{"run-shell", "-C", persist})); err != nil {
		return err
	}
	menu := []string{"display-menu", "-T", "Runtime services (read-only)"}
	for _, service := range []struct{ name, key, pane string }{{"Broker logs", "b", runtime.BrokerPane}, {"Supervisor logs", "s", runtime.SupervisorPane}} {
		if !validPane(service.pane) {
			continue
		}
		command := ShellCommand([]string{"/bin/sh", "-c", workbenchServiceLogScript, "agentmux-service-log", SocketPath(l.options.StateDir), service.pane, service.name})
		command = strings.ReplaceAll(command, "#", "##")
		popup := "display-popup -E -w 95% -h 85% " + command
		menu = append(menu, service.name, service.key, popup)
	}
	if len(menu) == 3 {
		if _, err := l.tmux(ctx, "bind-key", "D", "display-message", "Runtime service panes unavailable; inspect amx managed status."); err != nil {
			return err
		}
	} else if _, err := l.tmux(ctx, append([]string{"bind-key", "D"}, menu...)...); err != nil {
		return err
	}
	// Keep service windows in the owned session for lifecycle observation and
	// diagnostics, but skip its two internal windows during ordinary navigation.
	// These commands use the invoking client's view, never the core
	// session's selected window or another attached client's view.
	for _, binding := range []struct{ key, command string }{{"n", "next-window"}, {"p", "previous-window"}} {
		skip := ShellCommand([]string{"if-shell", "-F", workbenchServiceFormat, binding.command})
		// One command-list argument keeps separators inside the binding instead
		// of executing them immediately while configuring the server.
		if _, err := l.tmux(ctx, "bind-key", binding.key, binding.command+"; "+skip+"; "+skip); err != nil {
			return err
		}
	}
	for _, binding := range []struct{ key, view, number string }{{"b", "board", "1"}, {"P", "connections", "3"}, {"m", "memory", "4"}, {"t", "tasks", "5"}} {
		// tmux 3.2 supports popups but not display-popup -e, and does not
		// expand formats in popup argv. run-shell -C expands the invoking
		// client's identity once, then executes a tmux command, not a shell.
		// Multiple popup arguments execute env directly; owner paths remain
		// literal argv, including format-looking # text and shell metacharacters.
		command := ShellCommand([]string{l.options.Executable, "managed", "dashboard", "--state", l.options.StateDir, "--view", binding.view, "--popup"})
		command = strings.ReplaceAll(command, "#", "##")
		popup := "display-popup -E -w 95% -h 90% env AGENTMUX_CLIENT=#{q:client_name} " + command
		// Opening a complete console above the existing dashboard makes two
		// workbenches appear stacked. Guide the user to its own view key instead;
		// synthetic input could otherwise change an active editor or draft.
		guide := ShellCommand([]string{"display-message", "-d", "4000", "Workbench already open. Press " + binding.number + " for " + binding.view + "; Esc leaves the current editor or review."})
		guarded := ShellCommand([]string{"if-shell", "-F", workbenchDashboardFormat, guide, popup})
		if _, err := l.tmux(ctx, "bind-key", binding.key, "run-shell", "-C", guarded); err != nil {
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
	// The pane keeps its own title when it moves to a combined or broken-out
	// window. Retain the window option for existing consumers as well.
	if _, err = l.tmux(ctx, "set-option", "-p", "-t", pane, "@agentmux_title", label); err != nil {
		return err
	}
	_, err = l.tmux(ctx, "set-option", "-w", "-t", pane, "@agentmux_title", label)
	return err
}
