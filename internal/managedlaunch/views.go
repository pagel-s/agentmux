package managedlaunch

import (
	"context"
	"errors"
	"strings"
)

type workerWindow struct{ pane, window string }

func validWindow(value string) bool {
	return strings.HasPrefix(value, "@") && validPane("%"+strings.TrimPrefix(value, "@"))
}

// The core session owns worker lifecycles. Attachments share only these exact
// tracked worker windows; each attachment owns its own dashboard process.
func (l *Launcher) workerWindows(ctx context.Context, runtime Runtime) ([]workerWindow, error) {
	out, err := l.tmux(ctx, "list-panes", "-s", "-t", runtime.Session, "-F", "#{pane_id}\t#{window_id}\t#{@agentmux_runtime}\t#{@agentmux_role}\t#{@agentmux_agent}")
	if err != nil {
		return nil, err
	}
	var windows []workerWindow
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 || !validPane(fields[0]) || !validWindow(fields[1]) {
			return nil, errors.New("invalid worker window metadata")
		}
		tracked, ok := runtime.Agents[fields[4]]
		if !ok || tracked.PaneID != fields[0] || fields[2] != runtime.AttemptID || fields[3] != "worker" || seen[fields[1]] {
			continue
		}
		seen[fields[1]] = true
		windows = append(windows, workerWindow{pane: fields[0], window: fields[1]})
	}
	return windows, nil
}

func (l *Launcher) windowIndices(ctx context.Context, session string) (map[string]string, error) {
	out, err := l.tmux(ctx, "list-windows", "-t", session, "-F", "#{window_id}\t#{window_index}")
	if err != nil {
		return nil, err
	}
	indices := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 || !validWindow(fields[0]) || !validPane("%"+fields[1]) {
			return nil, errors.New("invalid view window metadata")
		}
		indices[fields[0]] = fields[1]
	}
	return indices, nil
}

func (l *Launcher) linkWorkerWindows(ctx context.Context, runtime Runtime, view string, windows []workerWindow) error {
	tag, err := l.tmux(ctx, "show-options", "-v", "-t", view, "@agentmux_view")
	if err != nil || strings.TrimSpace(tag) != runtime.AttemptID {
		return errors.New("client view ownership changed")
	}
	indices, err := l.windowIndices(ctx, view)
	if err != nil {
		return err
	}
	for _, window := range windows {
		if _, exists := indices[window.window]; exists {
			continue
		}
		// Link by immutable window ID, never the core session's window index.
		// -d keeps every attachment's current window and draft untouched.
		if _, err := l.tmux(ctx, "link-window", "-d", "-s", window.window, "-t", view+":"); err != nil {
			return err
		}
		indices[window.window] = ""
	}
	return nil
}

// syncClientViews runs only after structural changes, rather than polling every
// view/agent at each supervisor tick. Focus also links on demand for compatible
// older supervisors or a view that detached during a previous sync.
func (l *Launcher) syncClientViews(ctx context.Context, runtime Runtime) error {
	if !l.ownsSession(ctx, runtime) {
		return errors.New("managed session ownership changed")
	}
	windows, err := l.workerWindows(ctx, runtime)
	if err != nil {
		return err
	}
	out, err := l.tmux(ctx, "list-sessions", "-F", "#{session_name}\t#{@agentmux_view}")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 2 && fields[1] == runtime.AttemptID {
			// A view may detach independently at any time. Its disappearing UI
			// must never take down the broker or a healthy peer worker.
			_ = l.linkWorkerWindows(ctx, runtime, fields[0], windows)
		}
	}
	return nil
}

func (l *Launcher) workerWindowIndex(ctx context.Context, runtime Runtime, target, pane string) (string, error) {
	windows, err := l.workerWindows(ctx, runtime)
	if err != nil {
		return "", err
	}
	windowID := ""
	for _, window := range windows {
		if window.pane == pane {
			windowID = window.window
			break
		}
	}
	if windowID == "" {
		return "", errors.New("worker window is no longer owned by this attempt")
	}
	if target != runtime.Session {
		if err := l.linkWorkerWindows(ctx, runtime, target, windows); err != nil {
			return "", err
		}
	}
	indices, err := l.windowIndices(ctx, target)
	if err != nil {
		return "", err
	}
	index, ok := indices[windowID]
	if !ok {
		return "", errors.New("worker window is not available in this client view")
	}
	return index, nil
}
