package tmux

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"agentmux/internal/config"
	"agentmux/internal/resolve"
)

var ErrStructuralChange = errors.New("structural changes detected")

type reconcileAction struct {
	PaneID           string
	DesiredPane      resolve.Pane
	TargetStartupCmd string
	NeedsRespawn     bool
	NeedsMetaUpdate  bool
}

func Reconcile(cfg resolve.Config, opts Options) error {
	panes, err := Status(cfg.Session)
	if err != nil {
		return err
	}

	// Group live panes by window index
	windowsLive := make(map[int][]PaneStatus)
	for _, p := range panes {
		windowsLive[p.WindowIndex] = append(windowsLive[p.WindowIndex], p)
	}

	if len(cfg.Windows) != len(windowsLive) {
		return fmt.Errorf("%w: expected %d windows, but live session has %d", ErrStructuralChange, len(cfg.Windows), len(windowsLive))
	}

	var actions []reconcileAction
	var windowsToRename []struct{ ID, NewName string }
	var windowsToLayout []struct{ ID, Layout string }

	focusWindowID := ""
	focusPaneID := ""

	for windowIndex, window := range cfg.Windows {
		livePanes, exists := windowsLive[windowIndex]
		if !exists {
			return fmt.Errorf("%w: missing window at index %d", ErrStructuralChange, windowIndex)
		}

		windowID := livePanes[0].WindowID
		if window.Name != livePanes[0].WindowName {
			windowsToRename = append(windowsToRename, struct{ ID, NewName string }{windowID, window.Name})
		}

		// Tmux indices can be 1-based or 0-based depending on config, but the number of panes should match
		if len(window.Panes) != len(livePanes) {
			return fmt.Errorf("%w: window %q expected %d panes, but has %d", ErrStructuralChange, window.Name, len(window.Panes), len(livePanes))
		}

		windowWasTouched := false
		for paneIndex, pane := range window.Panes {
			live := livePanes[paneIndex]

			if pane.Name != live.Name {
				return fmt.Errorf("%w: pane at window %d index %d name changed from %q to %q", ErrStructuralChange, windowIndex, paneIndex, live.Name, pane.Name)
			}

			targetStartupCmd := buildPaneCommand(cfg.Session, pane)

			needsRespawn := live.StartupCmd != targetStartupCmd
			needsMetaUpdate := false

			if live.OnExit != config.NormalizeOnExit(pane.OnExit) || live.Title != pane.Title || live.SourceType != pane.SourceType || live.AgentName != pane.AgentName {
				needsMetaUpdate = true
			}

			if needsRespawn || needsMetaUpdate {
				actions = append(actions, reconcileAction{
					PaneID:           live.PaneID,
					DesiredPane:      pane,
					TargetStartupCmd: targetStartupCmd,
					NeedsRespawn:     needsRespawn,
					NeedsMetaUpdate:  needsMetaUpdate,
				})
				if needsRespawn {
					windowWasTouched = true
				}
			}

			if pane.Focus {
				focusWindowID = live.WindowID
				focusPaneID = live.PaneID
			}
		}

		// Always re-apply layout if window was touched or if layout name doesn't match?
		// Tmux doesn't store the "name" of the layout (like 'even-horizontal'), it just has the raw geometry.
		// So we re-apply if the YAML specified a layout and either a pane was respawned OR we just want to be sure.
		if window.Layout != "" && window.Layout != "grid" {
			// For now, always re-apply layout during reload if specified, to ensure sync.
			windowsToLayout = append(windowsToLayout, struct{ ID, Layout string }{windowID, window.Layout})
		} else if windowWasTouched && window.Layout != "" && window.Layout != "grid" {
			windowsToLayout = append(windowsToLayout, struct{ ID, Layout string }{windowID, window.Layout})
		}
	}

	// 1. Rename windows
	for _, w := range windowsToRename {
		if err := run(opts.Verbose, "rename-window", "-t", w.ID, w.NewName); err != nil {
			return fmt.Errorf("rename window %s to %q: %w", w.ID, w.NewName, err)
		}
	}

	currentPaneID := strings.TrimSpace(os.Getenv("TMUX_PANE"))

	// 2. Apply pane actions
	var deferredAction *reconcileAction
	for _, action := range actions {
		if action.NeedsRespawn && action.PaneID == currentPaneID {
			// Defer self-respawn to the very end
			deferred := action
			deferredAction = &deferred
			continue
		}

		if err := applyAction(action, opts.Verbose); err != nil {
			return err
		}
	}

	// 3. Re-apply layouts
	for _, w := range windowsToLayout {
		if err := run(opts.Verbose, "select-layout", "-t", w.ID, w.Layout); err != nil {
			// Layout might fail if panes are in a weird state during respawn, but we try anyway
			if opts.Verbose {
				fmt.Fprintf(os.Stderr, "warning: failed to apply layout %q to window %s: %v\n", w.Layout, w.ID, err)
			}
		}
	}

	// 4. Apply focus before deferred self-respawn
	if focusPaneID != "" {
		if err := run(opts.Verbose, "select-window", "-t", focusWindowID); err != nil {
			return fmt.Errorf("focus window: %w", err)
		}
		if err := run(opts.Verbose, "select-pane", "-t", focusPaneID); err != nil {
			return fmt.Errorf("focus pane: %w", err)
		}
	}

	if deferredAction != nil {
		if err := applyAction(*deferredAction, opts.Verbose); err != nil {
			return err
		}
	} else if opts.Attach {
		return attachSession(cfg.Session, opts.Verbose)
	}

	return nil
}

func applyAction(action reconcileAction, verbose bool) error {
	paneID := action.PaneID
	pane := action.DesiredPane

	if action.NeedsRespawn {
		if err := applyPaneLifecycle(paneID, pane.OnExit, verbose); err != nil {
			return err
		}
		if err := applyPaneMetadata(paneID, paneMetadata{
			Name:           pane.Name,
			StartupCommand: action.TargetStartupCmd,
			SourceType:     pane.SourceType,
			AgentName:      pane.AgentName,
			OnExit:         pane.OnExit,
		}, verbose); err != nil {
			return err
		}

		if pane.Title != "" {
			if err := run(verbose, "select-pane", "-t", paneID, "-T", pane.Title); err != nil {
				return fmt.Errorf("set title for pane %s: %w", paneID, err)
			}
		}

		if err := run(verbose, "respawn-pane", "-k", "-t", paneID, action.TargetStartupCmd); err != nil {
			return fmt.Errorf("respawn pane %s: %w", paneID, err)
		}

		return nil
	}

	// Only metadata update
	if err := applyPaneLifecycle(paneID, pane.OnExit, verbose); err != nil {
		return err
	}
	if err := applyPaneMetadata(paneID, paneMetadata{
		Name:           pane.Name,
		StartupCommand: action.TargetStartupCmd,
		SourceType:     pane.SourceType,
		AgentName:      pane.AgentName,
		OnExit:         pane.OnExit,
	}, verbose); err != nil {
		return err
	}

	if pane.Title != "" {
		if err := run(verbose, "select-pane", "-t", paneID, "-T", pane.Title); err != nil {
			return fmt.Errorf("set title for pane %s: %w", paneID, err)
		}
	}

	return nil
}
