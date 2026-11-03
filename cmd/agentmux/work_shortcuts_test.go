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
		{[]string{"-s", "poem", "context", "-j"}, []string{"context", "--json", "--session", "poem"}},
		{[]string{"-s", "poem", "task", "task-1", "-j"}, []string{"task", "task-1", "--json", "--session", "poem"}},
		{[]string{"tasks", "--query", "-s", "--status", "--state", "--after", "-j", "--limit", "4", "-j", "-s", "poem"}, []string{"tasks", "--query", "-s", "--status", "--state", "--after", "-j", "--limit", "4", "--json", "--session", "poem"}},
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

func TestWorkspaceChooserFiltersDuplicateNamesByExactState(t *testing.T) {
	m := workHome{width: 100, height: 24, rows: []workspaceListing{
		{Session: "same", State: "/custom/alpha", Status: "running"},
		{Session: "same", State: "/custom/beta", Status: "attention"},
	}}
	view := m.View()
	for _, text := range []string{"same", "/custom/alpha", "/custom/beta", "running", "attention"} {
		if !strings.Contains(view, text) {
			t.Fatalf("chooser omitted %q: %s", text, view)
		}
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(workHome)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("beta attention")})
	m = next.(workHome)
	if len(m.visibleRows()) != 1 || m.selectedState() != "/custom/beta" {
		t.Fatalf("filter selected stale row: %+v", m)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || next.(workHome).chosen != "/custom/beta" {
		t.Fatalf("opened wrong duplicate name: %+v", next)
	}
}

func TestWorkspaceChooserFilterTextAndClearPreserveFocus(t *testing.T) {
	m := workHome{rows: []workspaceListing{
		{Session: "same", State: "/j/k/q"},
		{Session: "same", State: "/prior-focus"},
	}, selected: 1}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = next.(workHome)
	for _, text := range []string{"j", "/", "k", "/", "q"} {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
		if cmd != nil {
			t.Fatalf("%q quit or opened while filtering", text)
		}
		m = next.(workHome)
	}
	if m.query != "j/k/q" || m.selectedState() != "/j/k/q" {
		t.Fatalf("navigation keys were not literal filter text: %+v", m)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(workHome)
	if m.query != "" || m.selectedState() != "/prior-focus" {
		t.Fatalf("clearing filter lost prior focus: %+v", m)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(workHome)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("prior-focus")})
	m = next.(workHome)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(workHome)
	if m.selectedState() != "/j/k/q" {
		t.Fatalf("second query restored an old focus: %+v", m)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("nothing matches")})
	m = next.(workHome)
	if next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || next.(workHome).chosen != "" {
		t.Fatal("empty filter opened a stale selection")
	}
}

func TestAmxHomeInventoryPrioritizesActiveAndQuotesExactTargets(t *testing.T) {
	rows := []workspaceListing{
		{Session: "same", State: "/custom/saved1", Status: "stopped"},
		{Session: "same", State: "/custom/it's active", Status: "running", Agents: 2},
		{Session: "same", State: "/custom/saved2", Status: "stopped"},
		{Session: "same", State: "/custom/saved3", Status: "stopped"},
		{Session: "same", State: "/custom/saved4", Status: "stopped"},
	}
	var out bytes.Buffer
	writeWorkHomeInventory(&out, rows, "dashboard")
	text := out.String()
	active := strings.Index(text, "same · running")
	stopped := strings.Index(text, "same · stopped")
	if active < 0 || stopped < 0 || active >= stopped {
		t.Fatalf("active workspaces were buried: %s", text)
	}
	exact := managedlaunch.ShellCommand([]string{"amx", "ui", "--state", rows[1].State})
	if !strings.Contains(text, exact) || !strings.Contains(text, "Saved (3 of 4)") || strings.Contains(text, "/custom/saved4") || !strings.Contains(text, "All workspaces: amx ws") {
		t.Fatalf("home targets are ambiguous or saved inventory unbounded: %s", text)
	}
	out.Reset()
	writeWorkHomeInventory(&out, rows[1:2], "attach")
	if !strings.Contains(out.String(), managedlaunch.ShellCommand([]string{"amx", "a", "--state", rows[1].State})) {
		t.Fatal("attach landing view suggested a different operation")
	}
}

func TestEverydayHelpPreservesRoleText(t *testing.T) {
	if workHelpRequested([]string{"--role", "--help"}) || workHelpRequested([]string{"--title", "-h"}) || !workHelpRequested([]string{"--role", "review", "--help"}) {
		t.Fatal("help flag consumed literal option text")
	}
}
