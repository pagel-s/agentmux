package managedlaunch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func TestWindowTitleStatusUsesLiteralDataAndKeepsWindowIdentity(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_MANAGED_INTEGRATION=1 for actual tmux status formats")
	}
	state, err := os.MkdirTemp("", "amx-title-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(state)
	l, err := New(Options{StateDir: state, Executable: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tmux := func(args ...string) string {
		t.Helper()
		out, err := l.tmux(ctx, args...)
		if err != nil {
			t.Fatalf("tmux %q: %v", args, err)
		}
		return strings.TrimSpace(out)
	}
	runtime := Runtime{Session: "private-titled-test", AttemptID: "run_titles"}
	pane := tmux("new-session", "-d", "-s", runtime.Session, "-n", "agent-opaque-id", "-P", "-F", "#{pane_id}", "exec sleep 60")
	t.Cleanup(func() { _, _ = l.tmux(context.Background(), "kill-session", "-t", runtime.Session) })
	tmux("set-option", "-t", runtime.Session, "@agentmux_runtime", runtime.AttemptID)
	if err := l.tagPane(ctx, pane, runtime.AttemptID, "worker", "opaque-id"); err != nil {
		t.Fatal(err)
	}
	runtime.DashboardPane = pane
	if err := l.configureWorkbench(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	sentinel, err := os.CreateTemp("", "w")
	if err != nil {
		t.Fatal(err)
	}
	sentinelPath := sentinel.Name()
	sentinel.Close()
	os.Remove(sentinelPath)
	t.Cleanup(func() { _ = os.Remove(sentinelPath) })
	for _, title := range []string{"Fix login redirect", "#(touch " + sentinelPath + ")", "#{session_name} #[fg=red]", strings.Repeat("界", 50)} {
		if err := l.syncWindowTitle(ctx, runtime, pane, "opaque-id", title); err != nil {
			t.Fatal(err)
		}
		option := tmux("show-options", "-w", "-v", "-t", pane, "@agentmux_title")
		if !strings.HasSuffix(option, " [opaque-id]") || len([]rune(option)) > 37+len(" [opaque-id]") {
			t.Fatalf("status label lost stable ID or its bound: %q", option)
		}
		for _, format := range []string{"window-status-format", "window-status-current-format"} {
			rendered := tmux("display-message", "-p", "-t", pane, tmux("show-options", "-v", "-t", runtime.Session, format))
			if !strings.Contains(rendered, strings.ReplaceAll(option, "#", "##")) {
				t.Fatalf("label reinterpreted or styling hashes unescaped: %q", rendered)
			}
		}
		if got := tmux("display-message", "-p", "-t", pane, "#{window_name}"); got != "agent-opaque-id" {
			t.Fatalf("presentation renamed lifecycle window: %s", got)
		}
	}
	// Format commands execute asynchronously if accidentally re-expanded.
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(sentinelPath); !os.IsNotExist(err) {
		t.Fatal("status title executed a tmux shell format")
	}
	if err := l.syncWindowTitle(ctx, runtime, pane, "opaque-id", ""); err != nil {
		t.Fatal(err)
	}
	if got := tmux("display-message", "-p", "-t", pane, workbenchWindowFormat); got != "opaque-id" {
		t.Fatalf("cleared title did not restore stable agent ID fallback: %q", got)
	}
	tmux("set-option", "-p", "-t", pane, "@agentmux_runtime", "foreign-attempt")
	if err := l.syncWindowTitle(ctx, runtime, pane, "opaque-id", "must not overwrite foreign window"); err == nil {
		t.Fatal("presentation update accepted changed ownership")
	}
	if got := tmux("show-options", "-w", "-v", "-t", pane, "@agentmux_title"); got != "" {
		t.Fatalf("foreign window received label: %q", got)
	}
}

func TestSupervisorRefreshesSessionLabelsWithoutRestartingNativePane(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_MANAGED_INTEGRATION=1 for live label reconciliation")
	}
	executable := os.Getenv("AGENTMUX_TEST_BINARY")
	if executable == "" {
		executable = filepath.Join("..", "..", "bin", "agentmux")
	}
	state, err := os.MkdirTemp("", "amx-live-title-")
	if err != nil {
		t.Fatal(err)
	}
	l, err := New(Options{StateDir: state, Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := l.Down(cleanup); err != nil {
			t.Errorf("title fixture cleanup failed: %v", err)
			return
		}
		_ = os.RemoveAll(state)
		_ = os.RemoveAll(state + ".bin")
		_ = os.Remove(state + ".launch.lock")
	})
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "title-reconciliation", Agents: map[string]managedconfig.Agent{
		"opaque-id": {Harness: "command", Interaction: "interactive", Command: []string{"/bin/cat"}},
		"unrelated": {Harness: "command", Interaction: "interactive", Command: []string{"/bin/cat"}},
	}}.Normalized()
	runtime, err := l.Up(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	pane := runtime.Agents["opaque-id"].PaneID
	before, err := l.tmux(ctx, "display-message", "-p", "-t", pane, "#{pane_pid}\t#{window_name}")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		t.Fatal(err)
	}
	client := managed.NewClient(credential)
	for version, title := range []string{"Debug auth race", "Recheck auth tests", ""} {
		args, _ := json.Marshal(map[string]any{"id": "opaque-id", "title": title, "version": version})
		if _, problem := client.Call(ctx, managed.Request{Operation: "agent.label", RequestID: "label-" + string(rune('a'+version)), Args: args}); problem != nil {
			t.Fatal(problem)
		}
		want := title
		if want != "" {
			want += " [opaque-id]"
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			got, err := l.tmux(ctx, "show-options", "-w", "-v", "-t", pane, "@agentmux_title")
			if err == nil && strings.TrimSpace(got) == want {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("supervisor did not reconcile label %q: got %q (%v)", title, got, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if got, err := l.tmux(ctx, "display-message", "-p", "-t", pane, "#{pane_pid}\t#{window_name}"); err != nil || got != before {
			t.Fatalf("label edit restarted or renamed native pane: before %q after %q (%v)", before, got, err)
		}
	}
	otherPane := runtime.Agents["unrelated"].PaneID
	otherIdentity, err := l.tmux(ctx, "display-message", "-p", "-t", otherPane, "#{pane_pid}\t#{pane_dead}")
	if err != nil || !strings.HasSuffix(strings.TrimSpace(otherIdentity), "\t0") {
		t.Fatalf("unrelated native fixture was not alive: %q %v", otherIdentity, err)
	}
	label := func(version int, title string) {
		t.Helper()
		args, _ := json.Marshal(map[string]any{"id": "opaque-id", "title": title, "version": version})
		if _, problem := client.Call(ctx, managed.Request{Operation: "agent.label", RequestID: "cosmetic-" + title, Args: args}); problem != nil {
			t.Fatal(problem)
		}
	}
	assertUnrelatedSurvives := func() {
		t.Helper()
		last, err := Snapshot(state)
		if err != nil {
			t.Fatal(err)
		}
		updates, deadline := 0, time.Now().Add(5*time.Second)
		for updates < 2 {
			current, err := Snapshot(state)
			if err != nil || current.State != "running" {
				t.Fatalf("cosmetic failure stopped the workspace: %+v (%v)", current, err)
			}
			if current.UpdatedAt != last.UpdatedAt {
				updates++
				last = current
			}
			if time.Now().After(deadline) {
				t.Fatal("supervisor stopped reconciling after a cosmetic failure")
			}
			time.Sleep(20 * time.Millisecond)
		}
		got, err := l.tmux(ctx, "display-message", "-p", "-t", otherPane, "#{pane_pid}\t#{pane_dead}")
		if err != nil || got != otherIdentity {
			t.Fatalf("cosmetic failure stopped/restarted unrelated native process: %q %v", got, err)
		}
	}
	if _, err := l.tmux(ctx, "set-option", "-p", "-t", pane, "@agentmux_agent", "foreign-identity"); err != nil {
		t.Fatal(err)
	}
	label(3, "Retry after ownership restored")
	assertUnrelatedSurvives()
	if _, err := l.tmux(ctx, "set-option", "-p", "-t", pane, "@agentmux_agent", "opaque-id"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := l.tmux(ctx, "show-options", "-w", "-v", "-t", pane, "@agentmux_title")
		if err == nil && strings.TrimSpace(got) == "Retry after ownership restored [opaque-id]" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed presentation update was cached instead of retried")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := l.tmux(ctx, "kill-pane", "-t", pane); err != nil {
		t.Fatal(err)
	}
	label(4, "Owner removed original pane")
	assertUnrelatedSurvives()
	observed, err := Snapshot(state)
	if err != nil || observed.Agents["opaque-id"].State != "unavailable" || observed.Agents["opaque-id"].PaneID != pane || observed.Agents["unrelated"].State != "running" {
		t.Fatalf("successful inventory concealed missing pane or changed identities: %+v %v", observed, err)
	}
}
