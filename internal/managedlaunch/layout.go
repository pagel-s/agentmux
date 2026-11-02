package managedlaunch

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
)

// Split puts an existing native terminal beside another terminal without
// launching a second provider or changing either coordination identity.
func (l *Launcher) Split(ctx context.Context, runtime Runtime, source, beside string, vertical bool) error {
	pane, err := l.liveAgentPane(ctx, runtime, source)
	if err != nil {
		return err
	}
	target := os.Getenv("TMUX_PANE")
	if beside != "" {
		target, err = l.liveAgentPane(ctx, runtime, beside)
		if err != nil {
			return err
		}
	} else if !strings.HasPrefix(os.Getenv("TMUX"), SocketPath(l.options.StateDir)+",") {
		return errors.New("use amx split FIRST SECOND outside this workspace; inside, amx split ID uses the current pane")
	}
	if !validPane(target) || target == pane {
		return errors.New("choose a different destination pane or two different agent IDs")
	}
	panes, err := l.panes(ctx, runtime)
	if err != nil {
		return err
	}
	found := false
	for _, p := range panes {
		if p.ID == target {
			found = p.Role == "" && p.Attempt == "" || p.Role == "worker" && p.Attempt == runtime.AttemptID
		}
	}
	if !found {
		return errors.New("destination must be an agent or scratch pane in this workspace; dashboard and runtime services cannot be split with agents")
	}
	window := func(id string) (string, error) {
		out, err := l.tmux(ctx, "display-message", "-p", "-t", id, "#{window_id}")
		out = strings.TrimSpace(out)
		if err != nil || !validWindow(out) {
			return "", errors.New("terminal window is no longer available")
		}
		return out, nil
	}
	old, err := window(pane)
	if err != nil {
		return err
	}
	destination, err := window(target)
	if err != nil {
		return err
	}
	roles, err := l.tmux(ctx, "list-panes", "-t", destination, "-F", "#{@agentmux_role}")
	if err != nil {
		return err
	}
	for _, role := range strings.Fields(roles) {
		if role == "dashboard" || role == "broker" || role == "supervisor" {
			return errors.New("dashboard and runtime service windows cannot be combined with agents")
		}
	}
	if old != destination {
		direction := "-h"
		if vertical {
			direction = "-v"
		}
		if _, err := l.tmux(ctx, "join-pane", "-d", direction, "-s", pane, "-t", target); err != nil {
			return err
		}
	}
	_ = l.labelLayout(ctx, runtime, old)
	_ = l.labelLayout(ctx, runtime, destination)
	return l.Focus(ctx, runtime, source)
}

func (l *Launcher) Unsplit(ctx context.Context, runtime Runtime, agent string) error {
	pane, err := l.liveAgentPane(ctx, runtime, agent)
	if err != nil {
		return err
	}
	value, err := l.tmux(ctx, "display-message", "-p", "-t", pane, "#{window_id}\t#{window_panes}")
	if err != nil {
		return err
	}
	parts := strings.Split(strings.TrimSpace(value), "\t")
	if len(parts) != 2 || !validWindow(parts[0]) {
		return errors.New("terminal layout is unavailable")
	}
	if parts[1] != "1" {
		if _, err := l.tmux(ctx, "break-pane", "-d", "-s", pane, "-t", runtime.Session+":"); err != nil {
			return err
		}
		_ = l.labelLayout(ctx, runtime, parts[0])
		if err := l.syncClientViews(ctx, runtime); err != nil {
			return err
		}
	}
	return l.Focus(ctx, runtime, agent)
}

func (l *Launcher) labelLayout(ctx context.Context, runtime Runtime, window string) error {
	out, err := l.tmux(ctx, "list-panes", "-t", window, "-F", "#{pane_id}\t#{@agentmux_runtime}\t#{@agentmux_agent}")
	if err != nil {
		return err
	}
	labels := []string{}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		tracked, ok := runtime.Agents[f[2]]
		if ok && tracked.PaneID == f[0] && f[1] == runtime.AttemptID {
			labels = append(labels, f[2])
		} else {
			labels = append(labels, "shell")
		}
	}
	if len(labels) < 2 {
		_, err = l.tmux(ctx, "set-option", "-wu", "-t", window, "@agentmux_layout_label")
		return err
	}
	sort.Strings(labels)
	if _, err = l.tmux(ctx, "set-option", "-w", "-t", window, "@agentmux_layout_label", strings.Join(labels, " + ")); err != nil {
		return err
	}
	if _, err = l.tmux(ctx, "set-option", "-w", "-t", window, "pane-border-status", "top"); err != nil {
		return err
	}
	_, err = l.tmux(ctx, "set-option", "-w", "-t", window, "pane-border-format", " #{pane_index} #{?@agentmux_agent,#{q/h:@agentmux_agent},shell} ")
	return err
}
