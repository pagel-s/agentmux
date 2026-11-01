package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"agentmux/internal/managed"
	"github.com/charmbracelet/x/term"
)

func managedWorkerIsInteractive(opts managed.WorkerOptions) bool {
	if opts.DefinitionFile != "" {
		definition, err := managed.ReadWorkerDefinition(opts.DefinitionFile)
		return err == nil && definition.Config.Agents[opts.AgentID].Interaction == "interactive"
	}
	store, err := managed.Open(opts.StateDir)
	if err != nil {
		return false
	}
	defer store.Close()
	agent, err := store.Agent(opts.AgentID)
	return err == nil && agent.Interaction == "interactive"
}

func managedWorkerTerminal(out io.Writer) bool {
	file, ok := out.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(file.Fd())
}

// Keep the machine-readable worker envelope for pipes and automation. A native
// tmux pane should instead leave readable exit information and a recovery path.
func writeManagedWorkerResult(out io.Writer, agentID string, native bool, err error) int {
	failed := err != nil && !errors.Is(err, context.Canceled)
	if !managedWorkerTerminal(out) {
		if failed {
			return managedCLIError(out, "worker", "", exitRuntime, err)
		}
		return writeManagedEnvelope(out, managedEnvelope{Operation: "worker", OK: true, Result: map[string]any{"agent_id": agentID, "stopped": true}})
	}
	label := "Worker"
	if native {
		label = "Native session"
	}
	switch {
	case failed:
		fmt.Fprintf(out, "\n%s %s stopped with an error.\n%s\n", label, activityText(agentID, 64), activityText(err.Error(), 800))
	case errors.Is(err, context.Canceled):
		fmt.Fprintf(out, "\n%s %s stopped after cancellation.\n", label, activityText(agentID, 64))
	case native:
		fmt.Fprintf(out, "\nNative session %s exited normally (exit 0).\nNative updates can close the CLI. Nothing is restarted automatically.\n", activityText(agentID, 64))
	default:
		fmt.Fprintf(out, "\nWorker %s stopped normally.\n", activityText(agentID, 64))
	}
	if native {
		fmt.Fprintln(out, "In Agentmux tmux: F1 returns to the control center. Select this session, then Enter or Shift+U to review relaunch.")
		fmt.Fprintln(out, "Your session identity and saved configuration are unchanged.")
	} else {
		fmt.Fprintln(out, "In Agentmux tmux: F1 returns to the control center. Inspect Tasks and Events before restarting work.")
	}
	if failed {
		return exitRuntime
	}
	return exitSuccess
}
