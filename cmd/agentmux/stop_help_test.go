package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStopHelpUsesExplicitLegacyNamesWithoutTouchingRuntime(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "tmux-was-called")
	fakeTMux := filepath.Join(root, "tmux")
	if err := os.WriteFile(fakeTMux, []byte("#!/bin/sh\nprintf called > \"$STOP_HELP_TMUX_MARKER\"\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "selected-state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(state, "owner.json")
	if err := os.WriteFile(fixture, []byte("PRIVATE_FIXTURE_UNCHANGED"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("HOME", root)
	t.Setenv("XDG_STATE_HOME", "invalid-relative-state-root")
	t.Setenv("TMUX", filepath.Join(root, "selected.sock")+",123,0")
	t.Setenv("TMUX_PANE", "%99")
	t.Setenv("AGENTMUX_STATE", state)
	t.Setenv("STOP_HELP_TMUX_MARKER", marker)
	for _, args := range [][]string{{"stop", "--help"}, {"stop", "-h"}, {"stop", "fixture-session", "fixture-pane", "--help"}, {"stop", "--json", "-h"}} {
		code, out, diagnostics := captureRunOutput(t, args)
		if code != exitSuccess || diagnostics != "" {
			t.Fatalf("focused help failed: %q: exit=%d out=%s err=%s", args, code, out, diagnostics)
		}
		for _, want := range []string{"Usage: amx stop SESSION PANE [--json]", "explicitly named pane", "interactive shell", "amx down --session NAME", "amx down --state DIR", "one workspace selector"} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing truthful stop guidance %q: %s", want, out)
			}
		}
		if strings.Contains(out, state) || strings.Contains(out, "PRIVATE_FIXTURE") {
			t.Fatal("help exposed the selected workspace")
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("help called tmux: %v", err)
	}
	if raw, err := os.ReadFile(fixture); err != nil || string(raw) != "PRIVATE_FIXTURE_UNCHANGED" {
		t.Fatal("help changed the selected fixture")
	}
}

func TestStopHelpPreservesNonHelpValidationAndJSONErrors(t *testing.T) {
	for _, args := range [][]string{
		{"stop"}, {"stop", "fixture-session"},
		{"stop", "--state", "/missing"},
		{"stop", "--session", "fixture", "--state", "/missing"},
		{"stop", "--", "--help"},
		{"stop", "--state", "--help"},
	} {
		code, out, diagnostics := captureRunOutput(t, args)
		if code != exitValidation || strings.Contains(out, "Usage:") {
			t.Fatalf("invalid legacy arguments became help or an action: %q: exit=%d out=%s err=%s", args, code, out, diagnostics)
		}
	}
	code, out, diagnostics := captureRunOutput(t, []string{"stop", "fixture-session", "fixture-pane", "--unknown", "--json"})
	var result struct {
		Kind      string
		Command   string
		ErrorCode string `json:"error_code"`
		ExitCode  int    `json:"exit_code"`
	}
	if code != exitValidation || diagnostics != "" || json.Unmarshal([]byte(out), &result) != nil || result.Kind != "error" || result.Command != "stop" || result.ErrorCode != "validation" || result.ExitCode != exitValidation {
		t.Fatalf("legacy JSON validation changed: exit=%d out=%s err=%s", code, out, diagnostics)
	}
}
