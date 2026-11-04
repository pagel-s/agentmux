package main

import (
	"errors"
	"fmt"
	"io"
	"os/exec"

	"agentmux/internal/workflow"
)

// Only ordinary trusted-local CLI defaults are discovered. Isolated roles have
// explicit credential/bridge bindings, and embedded profiles retain their owner
// selection. Discovery never runs a CLI or claims that its account is logged in.
func selectGuidedProviders(options workflow.GuidedOptions, lookPath func(string) (string, error)) (workflow.GuidedOptions, error) {
	if options.Isolation != nil || options.Coder != nil || options.Reviewer != nil || options.CoderHarness != "" && options.ReviewerHarness != "" {
		return options, nil
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	var installed []string
	available := make(map[string]bool)
	for _, profile := range []struct{ harness, binary string }{{"codex", "codex"}, {"claude", "claude"}, {"antigravity", "agy"}} {
		if _, err := lookPath(profile.binary); err == nil {
			installed = append(installed, profile.harness)
			available[profile.harness] = true
		}
	}
	if options.CoderHarness == "" {
		if len(installed) == 0 {
			return options, errors.New("no supported native CLI found on PATH; install and sign in to Codex, Claude Code or Antigravity, or select installed profiles with --coder and --reviewer")
		}
		options.CoderHarness = installed[0]
	}
	if options.ReviewerHarness == "" {
		// Prefer Claude for a Codex implementation and another provider for any
		// selected coder. A lone CLI still gets independent fresh review context.
		options.ReviewerHarness = options.CoderHarness
		for _, reviewer := range []string{"claude", "codex", "antigravity"} {
			if reviewer != options.CoderHarness && available[reviewer] {
				options.ReviewerHarness = reviewer
				break
			}
		}
	}
	return options, nil
}

func printGuidedProfiles(out io.Writer, candidates []workflow.Candidate) {
	if len(candidates) != 2 {
		return
	}
	coder, reviewer := candidates[0].Harness, candidates[1].Harness
	fmt.Fprintf(out, "Coder: %s · reviewer: %s (fresh context)\n", guidedLine(coder), guidedLine(reviewer))
	if coder == reviewer && coder != "command" {
		fmt.Fprintln(out, "Review uses a separate fresh context with the same provider.")
	}
}
