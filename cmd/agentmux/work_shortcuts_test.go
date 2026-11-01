package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentmux/internal/managedlaunch"
	tea "github.com/charmbracelet/bubbletea"
)

func TestEverydayShortcutsPreserveIntentAndPayload(t *testing.T) {
	cases := []struct{ args, want []string }{
		{[]string{"up", "-s", "poem", "-p", "/project", "-d"}, []string{"start", "--session", "poem", "--project", "/project", "--detach"}},
		{[]string{"-s", "poem", "a", "claude"}, []string{"attach", "claude", "--session", "poem"}},
		{[]string{"-s=poem", "mode", "claude", "auto", "-j"}, []string{"delivery", "claude", "auto", "--json", "--session", "poem"}},
		{[]string{"--state", "/custom", "new", "--role", "-s", "-p=/project"}, []string{"add", "--role", "-s", "--project=/project", "--state", "/custom"}},
		{[]string{"allow", "codex", "claude", "commands", "-s", "poem"}, []string{"allow", "codex", "claude", "commands", "--session", "poem"}},
		{[]string{"-s", "poem"}, []string{"dashboard", "--session", "poem"}},
		{[]string{"send", "codex", "hello", "-s", "poem"}, []string{"send", "codex", "hello", "--session", "poem"}},
		{[]string{"-s", "poem", "send", "codex", "--", "-s", "literal"}, []string{"send", "codex", "--session", "poem", "--", "-s", "literal"}},
		{[]string{"send", "codex", "-s"}, []string{"send", "codex", "-s"}},
		{[]string{"send", "legacy", "pane", "-s"}, []string{"send", "legacy", "pane", "-s"}},
		{[]string{"send", "legacy", "pane", "echo", "-s", "payload"}, []string{"send", "legacy", "pane", "echo", "-s", "payload"}},
		{[]string{"send", "legacy", "pane", "--", "echo", "-s"}, []string{"send", "legacy", "pane", "--", "echo", "-s"}},
		{[]string{"status", "legacy", "--json"}, []string{"status", "legacy", "--json"}},
		{[]string{"web", "-s=poem", "-o"}, []string{"web", "--session=poem", "--open"}},
		{[]string{"mode", "-s", "poem", "claude", "--notes=false", "auto"}, []string{"delivery", "claude", "auto", "--session", "poem", "--notes=false"}},
		{[]string{"new", "-s", "poem", "reviewer", "--harness", "codex", "--role", "--help"}, []string{"add", "reviewer", "--session", "poem", "--harness", "codex", "--role", "--help"}},
		{[]string{"connect", "-s", "poem", "--name", "reviewer", "external-7"}, []string{"connect", "external-7", "--session", "poem", "--name", "reviewer"}},
	}
	for _, tc := range cases {
		before := append([]string(nil), tc.args...)
		got, err := normalizeWorkCLI(tc.args)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q => %q, %v; want %q", tc.args, got, err, tc.want)
		}
		if !reflect.DeepEqual(tc.args, before) {
			t.Fatal("caller arguments mutated")
		}
	}
	for short, long := range workAliases {
		got, err := normalizeWorkCLI([]string{short, "--help"})
		if err != nil || got[0] != long {
			t.Fatalf("alias %s: %q %v", short, got, err)
		}
	}
}

func TestEverydayShortcutsRejectConflictingSelectors(t *testing.T) {
	for _, args := range [][]string{
		{"-s"}, {"-s", "one", "status", "legacy"},
		{"-s", "one", "mode", "claude", "auto", "-s", "two"},
		{"new", "-s", "one", "--session=two"},
		{"-s", "one", "-s", "two"},
	} {
		if _, err := normalizeWorkCLI(args); err == nil {
			t.Fatalf("accepted ambiguous %q", args)
		}
	}
}

func shortcutRuntime(t *testing.T) string {
	t.Helper()
	state := filepath.Join(t.TempDir(), "state,with,commas")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(managedlaunch.Runtime{Version: 1, Session: "poem", AttemptID: "attempt", State: "running"})
	if err := os.WriteFile(filepath.Join(state, "runtime.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestWorkspaceContextRequiresExactAttachedAttempt(t *testing.T) {
	state := shortcutRuntime(t)
	value := filepath.Join(state, "tmux.sock") + ",123,7"
	for _, session := range []string{"poem", "view-poem", "foreign"} {
		t.Run(session, func(t *testing.T) {
			var queries [][]string
			query := func(ctx context.Context, socket string, args ...string) (string, error) {
				if socket != filepath.Join(state, "tmux.sock") {
					t.Fatal("wrong server")
				}
				queries = append(queries, args)
				if args[0] == "display-message" {
					if args[3] != "$7" {
						t.Fatalf("wrong attachment %q", args)
					}
					return session, nil
				}
				if session == "foreign" {
					return "another-attempt", nil
				}
				want := "@agentmux_view"
				if session == "poem" {
					want = "@agentmux_runtime"
				}
				if args[len(args)-1] != want {
					t.Fatalf("wrong ownership tag: %q", args)
				}
				return "attempt", nil
			}
			got, found, err := ownedWorkContext(value, query)
			if !found || len(queries) != 2 {
				t.Fatal("attachment not verified")
			}
			if session == "foreign" {
				if err == nil || got.state != "" {
					t.Fatal("foreign session inherited owner context")
				}
			} else if err != nil || got.state != state || got.session != "poem" {
				t.Fatalf("context: %+v %v", got, err)
			}
		})
	}
	_, found, err := ownedWorkContext(value, func(context.Context, string, ...string) (string, error) { return "", errors.New("unreachable") })
	if !found || err == nil {
		t.Fatal("unreachable attachment silently fell back")
	}
	if err := os.Remove(filepath.Join(state, "runtime.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "state.sqlite"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, found, err = ownedWorkContext(value, contextTMux)
	if !found || err == nil {
		t.Fatal("missing owned manifest silently fell back")
	}
}

func TestWorkspaceChooserNeverCreatesOrSelectsOnCancel(t *testing.T) {
	m := workHome{rows: []workspaceListing{{Session: "first", State: "/first"}, {Session: "second", State: "/second"}}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(workHome)
	if !strings.Contains(m.View(), "second") {
		t.Fatal("selection not visible")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil || next.(workHome).chosen != "" {
		t.Fatal("cancel opened a workspace")
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || next.(workHome).chosen != "/second" {
		t.Fatal("selected a different workspace")
	}
}

func TestAmxHomeWithPipeListsInsteadOfStartingModels(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("TMUX", "")
	var out, diag bytes.Buffer
	if code := runWorkHome(strings.NewReader(""), &out, &diag); code != 0 || !strings.Contains(out.String(), "amx up -s NAME") {
		t.Fatalf("home: %d %s %s", code, out.String(), diag.String())
	}
}

func TestEverydayHelpPreservesRoleText(t *testing.T) {
	if workHelpRequested([]string{"--role", "--help"}) || workHelpRequested([]string{"--title", "-h"}) || !workHelpRequested([]string{"--role", "review", "--help"}) {
		t.Fatal("help flag consumed literal option text")
	}
}
