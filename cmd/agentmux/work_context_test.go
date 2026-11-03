package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managedlaunch"
)

func TestWorkContextReportsOrdinaryTargetWithoutCreatingState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_STATE_HOME", root)
	t.Setenv("TMUX", "")
	prior := describeWorkRuntime
	defer func() { describeWorkRuntime = prior }()
	var inspected string
	describeWorkRuntime = func(_ context.Context, state string) (managedlaunch.Runtime, error) {
		inspected = state
		return managedlaunch.Runtime{}, os.ErrNotExist
	}
	for _, tc := range []struct {
		args   []string
		state  string
		source string
	}{
		{nil, filepath.Join(root, "agentmux", "work"), "default work"},
		{[]string{"--session", "poem"}, filepath.Join(root, "agentmux", "poem"), "explicit --session"},
		{[]string{"--state", filepath.Join(root, "custom space")}, filepath.Join(root, "custom space"), "explicit --state"},
	} {
		var out, diag bytes.Buffer
		args := append(append([]string(nil), tc.args...), "--json")
		if code := runWorkContext(args, &out, &diag); code != exitSuccess {
			t.Fatalf("context %q: %d %s", args, code, diag.String())
		}
		var result workContext
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		ordinary, err := parseWorkOptions(tc.args)
		if err != nil || ordinary.state != result.State || inspected != tc.state || result.State != tc.state || result.Source != tc.source || result.Status != "missing" {
			t.Fatalf("diagnostic diverged from ordinary target: %+v ordinary=%+v err=%v", result, ordinary, err)
		}
		if !strings.Contains(result.AttachCommand, "--state") || !strings.Contains(result.InspectCommand, "--state") {
			t.Fatalf("ambiguous follow-up commands: %+v", result)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("read-only context created workspace files: %v %v", entries, err)
	}
}

func TestWorkContextDoesNotReportCachedRunningAsLive(t *testing.T) {
	t.Setenv("TMUX", "")
	prior := describeWorkRuntime
	defer func() { describeWorkRuntime = prior }()
	describeWorkRuntime = func(context.Context, string) (managedlaunch.Runtime, error) {
		return managedlaunch.Runtime{Session: "saved-name", AttemptID: "attempt", State: "running"}, errors.New("ownership could not be verified")
	}
	var out, diag bytes.Buffer
	if code := runWorkContext([]string{"--state", t.TempDir()}, &out, &diag); code != exitSuccess {
		t.Fatalf("context: %d %s", code, diag.String())
	}
	if !strings.Contains(out.String(), "Status: unknown") || !strings.Contains(out.String(), "ownership could not be verified") || strings.Contains(out.String(), "Status: running") {
		t.Fatalf("cached state presented as live: %s", out.String())
	}
}

func TestWorkContextVerifiesAttachmentOnceAndExplicitSelectorBypassesIt(t *testing.T) {
	state := shortcutRuntime(t)
	t.Setenv("TMUX", filepath.Join(state, "tmux.sock")+",123,7")
	bin := t.TempDir()
	log := filepath.Join(bin, "queries")
	t.Setenv("AMX_CONTEXT_TEST_LOG", log)
	script := "#!/bin/sh\nprintf 'query\\n' >> \"$AMX_CONTEXT_TEST_LOG\"\ncase \"$3\" in\n  display-message) printf 'poem\\n';;\n  show-options) printf 'attempt\\n';;\n  *) exit 1;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	prior := describeWorkRuntime
	defer func() { describeWorkRuntime = prior }()
	describeWorkRuntime = func(_ context.Context, selected string) (managedlaunch.Runtime, error) {
		if selected != state {
			t.Fatalf("attachment routed elsewhere: %s", selected)
		}
		return managedlaunch.Runtime{Session: "poem", AttemptID: "attempt", State: "running"}, nil
	}
	var out, diag bytes.Buffer
	if code := runWorkContext([]string{"--json"}, &out, &diag); code != exitSuccess {
		t.Fatalf("context: %d %s", code, diag.String())
	}
	var result workContext
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.State != state || result.Source != "verified attachment" || result.Status != "running" {
		t.Fatalf("attachment context: %+v %v", result, err)
	}
	queries, err := os.ReadFile(log)
	if err != nil || strings.Count(string(queries), "query\n") != 2 {
		t.Fatalf("context resolved attachment more than once: %q %v", queries, err)
	}
	// Corrupt ownership must fail closed with no fallback or runtime query.
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diag.Reset()
	if code := runWorkContext(nil, &out, &diag); code != exitValidation || out.Len() != 0 || !strings.Contains(diag.String(), "cannot verify") {
		t.Fatalf("unverified attachment fell back: %d %s %s", code, out.String(), diag.String())
	}
	out.Reset()
	diag.Reset()
	if code := runWorkContext([]string{"--state", state}, &out, &diag); code != exitSuccess || !strings.Contains(out.String(), "Source: explicit --state") {
		t.Fatalf("explicit target inherited bad attachment: %d %s %s", code, out.String(), diag.String())
	}
}

func TestWorkContextRejectsAmbiguousOrActionArguments(t *testing.T) {
	prior := describeWorkRuntime
	defer func() { describeWorkRuntime = prior }()
	describeWorkRuntime = func(context.Context, string) (managedlaunch.Runtime, error) {
		t.Fatal("invalid context inspected a runtime")
		return managedlaunch.Runtime{}, nil
	}
	for _, args := range [][]string{
		{"--session", "one", "--session", "two"},
		{"--state", "/one", "--state=/two"},
		{"--session", "one", "--state", "/two"},
		{"--state", ""}, {"--session", ""}, {"agent"}, {"--detach"},
	} {
		var out, diag bytes.Buffer
		if code := runWorkContext(args, &out, &diag); code != exitValidation || out.Len() != 0 {
			t.Fatalf("accepted %q: %d %s %s", args, code, out.String(), diag.String())
		}
	}
}

func TestWorkContextCommandsRemainExactOrExplicitlyUnavailable(t *testing.T) {
	t.Setenv("TMUX", "")
	prior := describeWorkRuntime
	defer func() { describeWorkRuntime = prior }()
	describeWorkRuntime = func(context.Context, string) (managedlaunch.Runtime, error) {
		return managedlaunch.Runtime{}, os.ErrNotExist
	}
	for _, state := range []string{"/private/" + strings.Repeat("segment/", 250), "/private/control\x1b[31m\u202e\npath"} {
		var out, diagnostics bytes.Buffer
		if code := runWorkContext([]string{"--state", state}, &out, &diagnostics); code != exitSuccess {
			t.Fatalf("context: %d %s", code, diagnostics.String())
		}
		if strings.ContainsAny(state, "\x1b\u202e\n") {
			if strings.ContainsAny(out.String(), "\x1b\u202e") || !strings.Contains(out.String(), "copyable command omitted") {
				t.Fatalf("offered unsafe or inaccurate command: %q", out.String())
			}
		} else {
			state = filepath.Clean(state)
			for _, operation := range []string{"attach", "sessions"} {
				command := managedlaunch.ShellCommand([]string{"amx", operation, "--state", state})
				if !strings.Contains(out.String(), command) {
					t.Fatalf("long command was truncated: %s", out.String())
				}
			}
		}
		out.Reset()
		if code := runWorkContext([]string{"--state", state, "--json"}, &out, &diagnostics); code != exitSuccess {
			t.Fatalf("JSON context: %d %s", code, diagnostics.String())
		}
		var result workContext
		if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.State != filepath.Clean(state) {
			t.Fatalf("JSON lost exact state: %q %v", out.String(), err)
		}
	}
}
