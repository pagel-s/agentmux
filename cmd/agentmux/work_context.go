package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"agentmux/internal/managedlaunch"
)

// Use one resolver for action commands and the read-only context diagnostic.
// An unverifiable Agentmux attachment fails closed rather than selecting work.
func resolveWorkSelection(o workOptions, explicitSession, explicitState, inferAttachment bool) (workOptions, error) {
	switch {
	case explicitState && explicitSession:
		o.selectionSource = "explicit --session and --state"
	case explicitState:
		o.selectionSource = "explicit --state"
	case explicitSession:
		o.selectionSource = "explicit --session"
	default:
		o.selectionSource = "default work"
		if inferAttachment {
			current, found, err := ownedWorkContext(os.Getenv("TMUX"), contextTMux)
			if err != nil {
				return o, err
			}
			if found {
				o.state, o.session = current.state, current.session
				o.selectionSource = "verified attachment"
			}
		}
	}
	var err error
	if o.state == "" {
		o.state, err = defaultWorkState(o.session)
	} else {
		o.state, err = filepath.Abs(expandUserPath(o.state))
	}
	return o, err
}

type workContext struct {
	Workspace      string `json:"workspace"`
	State          string `json:"state"`
	Source         string `json:"source"`
	Status         string `json:"status"`
	AttemptID      string `json:"attempt_id,omitempty"`
	Problem        string `json:"problem,omitempty"`
	AttachCommand  string `json:"attach_command"`
	InspectCommand string `json:"inspect_command"`
}

func runWorkContext(args []string, out, diagnostics io.Writer) int {
	diagnostics = workflowDiagnosticWriter{diagnostics}
	if workHelpRequested(args) {
		fmt.Fprintln(out, "Usage: amx context [--session NAME | --state DIR] [--json]\nShows the workspace ordinary commands select, why it was selected, and current runtime status.\nWith no selector, uses a verified Agentmux attachment or the default work workspace.\nRead-only: does not start, attach, register or change a workspace.")
		return exitSuccess
	}
	if _, err := checkedWorkSelectors(append([]string{"context"}, args...)); err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitValidation
	}
	flags := flag.NewFlagSet("context", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	session := flags.String("session", "work", "workspace name")
	state := flags.String("state", "", "exact workspace state directory")
	asJSON := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return exitValidation
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(diagnostics, "error: context accepts --session or --state and --json only")
		return exitValidation
	}
	var selectors []string
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "session":
			selectors = append(selectors, "--session", *session)
		case "state":
			selectors = append(selectors, "--state", *state)
		}
	})
	o, err := parseWorkOptions(selectors)
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitValidation
	}
	result := workContext{
		Workspace: o.session, State: o.state, Source: o.selectionSource, Status: "unknown",
		AttachCommand:  managedlaunch.ShellCommand([]string{"amx", "attach", "--state", o.state}),
		InspectCommand: managedlaunch.ShellCommand([]string{"amx", "sessions", "--state", o.state}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runtime, runtimeErr := describeWorkRuntime(ctx, o.state)
	if runtime.Session != "" {
		result.Workspace = runtime.Session
	}
	result.AttemptID = runtime.AttemptID
	result.Problem = runtime.Problem
	if runtimeErr != nil {
		// Describe may return its saved snapshot alongside a failed ownership
		// check. That saved running bit must never become current liveness.
		if os.IsNotExist(runtimeErr) {
			result.Status = "missing"
		}
		if result.Problem != "" {
			result.Problem += "; "
		}
		result.Problem += runtimeErr.Error()
	} else if runtime.State != "" {
		result.Status = runtime.State
	}
	if *asJSON {
		if err := json.NewEncoder(out).Encode(result); err != nil {
			fmt.Fprintln(diagnostics, "error:", err)
			return exitRuntime
		}
		return exitSuccess
	}
	fmt.Fprintf(out, "Workspace: %s\nSource: %s\nState: %s\nStatus: %s\n", consoleContent(result.Workspace, 100), result.Source, consoleContent(result.State, 1000), consoleContent(result.Status, 100))
	if result.AttemptID != "" {
		fmt.Fprintln(out, "Attempt:", consoleContent(result.AttemptID, 200))
	}
	if result.Problem != "" {
		fmt.Fprintln(out, "Problem:", consoleContent(result.Problem, 1000))
	}
	fmt.Fprintf(out, "Attach: %s\nInspect: %s\n", displayWorkCommand(result.AttachCommand), displayWorkCommand(result.InspectCommand))
	return exitSuccess
}
