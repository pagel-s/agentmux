package main

import (
	"bytes"
	"strings"
	"testing"

	"agentmux/internal/managedlaunch"
)

func TestExperimentLiveHintsQuoteExactStateAndKeepLegacyScope(t *testing.T) {
	var diagnostics bytes.Buffer
	executable, state := "/tmp/agentmux binary", "/private/pilot's run/attempt-001/state"
	printExperimentLive(&diagnostics, executable, state)
	for _, command := range []string{"dashboard", "web", "activity"} {
		if !strings.Contains(diagnostics.String(), managedlaunch.ShellCommand([]string{executable, command, "--state", state})) {
			t.Fatalf("missing exact shell-safe %s command", command)
		}
	}
	for _, text := range []string{"single-task pilot", "no native terminal", "does not independently classify pristine versus intervened"} {
		if !strings.Contains(diagnostics.String(), text) {
			t.Fatalf("legacy scope hint omitted %q", text)
		}
	}
	if strings.Contains(diagnostics.String(), "phase barrier") || strings.Contains(diagnostics.String(), "research control") {
		t.Fatal("single-task pilot hints invented research phase controls")
	}
}
