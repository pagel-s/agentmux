package managedlaunch

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real control-mode clients exercise AttachAgent without introducing a PTY
// dependency. The interactive smoke script separately checks real PTY keys.
func TestAttachAgentRealClientsHaveIndependentViewsAndOwnedCleanup(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_MANAGED_INTEGRATION=1 for real tmux client views")
	}
	realTMux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.MkdirTemp("", "amx-views-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	l, err := New(Options{StateDir: state, Executable: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	// Only AttachAgent's foreground client is converted to control mode. All
	// owner-side commands still invoke the real tmux executable directly.
	l.run = func(ctx context.Context, args ...string) (string, error) {
		command := exec.CommandContext(ctx, realTMux, args...)
		command.Env = withoutTMux(os.Environ())
		output, err := command.CombinedOutput()
		return string(output), err
	}
	wrapperDir := t.TempDir()
	wrapper := "#!/bin/sh\nexec " + ShellCommand([]string{realTMux, "-C"}) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrapperDir, "tmux"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX", "")
	t.Cleanup(func() {
		cleanup := context.Background()
		out, _ := l.tmux(cleanup, "list-sessions", "-F", "#{session_name}")
		for _, session := range strings.Fields(out) {
			_, _ = l.tmux(cleanup, "kill-session", "-t", session)
		}
		_ = os.RemoveAll(state)
		_ = os.Remove(state + ".launch.lock")
	})
	tmux := func(args ...string) string {
		t.Helper()
		out, err := l.tmux(ctx, args...)
		if err != nil {
			t.Fatalf("tmux %q: %v %s", args, err, out)
		}
		return strings.TrimSpace(out)
	}
	runtime := Runtime{Version: 1, AttemptID: "run_views", Session: "core", State: "running", Agents: map[string]AgentPane{}}
	runtime.DashboardPane = tmux("new-session", "-d", "-s", runtime.Session, "-n", "dashboard", "-P", "-F", "#{pane_id}", "exec sleep 300")
	tmux("set-option", "-t", runtime.Session, "@agentmux_runtime", runtime.AttemptID)
	if err := l.tagPane(ctx, runtime.DashboardPane, runtime.AttemptID, "dashboard", ""); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"alice", "bob"} {
		pane := tmux("new-window", "-d", "-t", runtime.Session, "-n", "agent-"+agent, "-P", "-F", "#{pane_id}", "exec sleep 300")
		if err := l.tagPane(ctx, pane, runtime.AttemptID, "worker", agent); err != nil {
			t.Fatal(err)
		}
		runtime.Agents[agent] = AgentPane{PaneID: pane, State: "running"}
	}
	if err := writeRuntime(state, runtime); err != nil {
		t.Fatal(err)
	}
	wait := func(description string, predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if predicate() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out: %s", description)
	}
	attach := func(agent string) (io.WriteCloser, <-chan error) {
		t.Helper()
		reader, writer := io.Pipe()
		done := make(chan error, 1)
		go func() {
			done <- l.AttachAgent(ctx, runtime, agent, reader, io.Discard, io.Discard)
			_ = reader.Close()
		}()
		t.Cleanup(func() { _ = writer.Close() })
		return writer, done
	}
	clientSessions := func() []string {
		out, _ := l.tmux(ctx, "list-clients", "-F", "#{client_session}")
		return strings.Fields(out)
	}
	firstInput, firstDone := attach("alice")
	wait("first attached view", func() bool { return len(clientSessions()) == 1 })
	first := clientSessions()[0]
	secondInput, secondDone := attach("bob")
	wait("second attached view", func() bool { return len(clientSessions()) == 2 })
	second := ""
	for _, view := range clientSessions() {
		if view != first {
			second = view
		}
	}
	if first == runtime.Session || second == "" || second == runtime.Session {
		t.Fatalf("clients did not get independent views: %q", clientSessions())
	}
	current := func(view string) string { return tmux("display-message", "-p", "-t", view+":", "#{window_name}") }
	if current(first) != "agent-alice" || current(second) != "agent-bob" {
		t.Fatal("attaching a second agent moved the first client's selected window")
	}
	// Navigation in either grouped session must leave the other untouched.
	tmux("select-window", "-t", first+":0")
	if current(first) != "dashboard" || current(second) != "agent-bob" {
		t.Fatal("grouped client navigation changed the other terminal")
	}
	sessionID := strings.TrimPrefix(tmux("display-message", "-p", "-t", first, "#{session_id}"), "$")
	t.Setenv("TMUX", SocketPath(state)+",1,"+sessionID)
	if err := l.Focus(ctx, runtime, "alice"); err != nil {
		t.Fatal(err)
	}
	if current(first) != "agent-alice" || current(second) != "agent-bob" {
		t.Fatal("contextual Focus selected another client's view")
	}
	if _, err := io.WriteString(firstInput, "detach-client\n"); err != nil {
		t.Fatal(err)
	}
	_ = firstInput.Close()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("first client did not detach")
	}
	if _, err := l.tmux(ctx, "has-session", "-t", first); err == nil {
		t.Fatal("detached client's private view survived")
	}
	if current(second) != "agent-bob" || !l.ownsSession(ctx, runtime) {
		t.Fatal("detaching one client destroyed the shared runtime or another view")
	}
	tmux("new-session", "-d", "-s", "foreign", "exec sleep 300")
	tmux("set-option", "-t", "foreign", "@agentmux_view", "different-attempt")
	stopped, err := l.Down(ctx)
	if err != nil || stopped.State != "stopped" {
		t.Fatalf("down: %+v %v", stopped, err)
	}
	_ = secondInput.Close()
	select {
	case <-secondDone:
	case <-ctx.Done():
		t.Fatal("down left a client attached to an owned view")
	}
	if got := tmux("list-sessions", "-F", "#{session_name}"); got != "foreign" {
		t.Fatalf("down removed foreign state or left owned views: %s", got)
	}
}

func TestAttachAgentRejectsChangedRuntimeBeforeCreatingView(t *testing.T) {
	l := &Launcher{options: Options{StateDir: "/private/state"}}
	l.run = func(_ context.Context, args ...string) (string, error) {
		if args[4] != "show-options" {
			t.Fatalf("unowned attach attempted mutation: %q", args)
		}
		return "foreign-attempt\n", nil
	}
	err := l.AttachAgent(context.Background(), Runtime{Session: "core", AttemptID: "owned-attempt"}, "alice", strings.NewReader(""), io.Discard, io.Discard)
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatal("unowned runtime accepted an attach")
	}
}

func TestAttachAgentRejectsChangedWorkerIdentityBeforeCreatingView(t *testing.T) {
	for _, pane := range []string{
		"%7\t1\towned\tworker\talice\n",
		"%7\t0\tforeign\tworker\talice\n",
		"%7\t0\towned\tworker\tbob\n",
		"%7\t0\towned\tbroker\talice\n",
	} {
		l := &Launcher{options: Options{StateDir: "/private/state"}}
		l.run = func(_ context.Context, args ...string) (string, error) {
			switch args[4] {
			case "show-options":
				return "owned\n", nil
			case "list-panes":
				return pane, nil
			default:
				t.Fatalf("changed worker identity caused mutation: %q", args)
				return "", nil
			}
		}
		runtime := Runtime{Session: "core", AttemptID: "owned", Agents: map[string]AgentPane{"alice": {PaneID: "%7"}}}
		if err := l.AttachAgent(context.Background(), runtime, "alice", strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted changed/dead worker identity: %q", pane)
		}
	}
}
