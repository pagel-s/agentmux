package managedlaunch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

// Real PTY clients exercise the same terminal attach path humans use. Python's
// standard-library PTY support is also used by the interactive smoke script.
func TestAttachAgentRealClientsHaveIndependentViewsAndOwnedCleanup(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_MANAGED_INTEGRATION=1 for real tmux client views")
	}
	realTMux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("real terminal integration requires python3:", err)
	}
	state, err := os.MkdirTemp("", "amx-views-")
	if err != nil {
		t.Fatal(err)
	}
	store, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: "core", Agents: map[string]managedconfig.Agent{
		"alice": {Harness: "command", Command: []string{"/bin/true"}},
		"bob":   {Harness: "command", Command: []string{"/bin/true"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	l, err := New(Options{StateDir: state, Executable: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	wrapperDir := t.TempDir()
	fixturePath := strings.Join([]string{wrapperDir, filepath.Dir(realTMux), "/usr/bin", "/bin"}, string(os.PathListSeparator))
	// Verbose tmux diagnostics include environment updates. Both owner commands
	// and the foreground terminal client must see fixture-only values, never CI
	// credentials or the user's native configuration/environment.
	fixtureEnv := []string{"PATH=" + fixturePath, "HOME=" + state, "SHELL=/bin/sh", "TERM=xterm-256color", "LC_ALL=C"}
	t.Setenv("AGENTMUX_FIXTURE_SECRET", "sentinel-must-stay-outside-tmux")
	// Only AttachAgent's foreground client gets a PTY relay. All owner-side
	// commands still invoke the real tmux executable directly. Do not replace
	// this with -C clients: tmux 3.2a can dereference a retained dead pane's freed
	// output buffer when another control client still has pending output. The
	// upstream guard is present in tmux 3.3 control.c:control_write_pending.
	l.run = func(ctx context.Context, args ...string) (string, error) {
		// -u keeps machine-readable tab separators intact under the deliberately
		// minimal C locale; non-UTF-8 tmux clients replace them with underscores.
		commandArgs := append([]string{"-u"}, args...)
		if os.Getenv("AGENTMUX_TMUX_DEBUG") == "1" {
			commandArgs = append([]string{"-vv"}, commandArgs...)
		}
		command := exec.CommandContext(ctx, realTMux, commandArgs...)
		command.Dir = state
		command.Env = fixtureEnv
		output, err := command.CombinedOutput()
		if err != nil {
			err = fmt.Errorf("tmux %q failed: %w: %s", args, err, output)
		}
		return string(output), err
	}
	ptyDriver := filepath.Join(wrapperDir, "pty-client.py")
	if err := os.WriteFile(ptyDriver, []byte(clientViewsPTYDriver), 0600); err != nil {
		t.Fatal(err)
	}
	terminalCommand := append([]string{"/usr/bin/env", "-i"}, fixtureEnv...)
	terminalCommand = append(terminalCommand, python, ptyDriver, realTMux, "-u")
	if os.Getenv("AGENTMUX_TMUX_DEBUG") == "1" {
		terminalCommand = append(terminalCommand, "-vv")
	}
	wrapper := "#!/bin/sh\n" + ShellCommand([]string{"cd", state}) + " || exit 1\nexec " + ShellCommand(terminalCommand) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(wrapperDir, "tmux"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	popupRecord := filepath.Join(state, "popup-invocation")
	popupExecutable := filepath.Join(wrapperDir, "popup-#fixture")
	popupScript := "#!/bin/sh\ncase \" $* \" in\n*' --popup '*) printf '%s\\n' \"$AGENTMUX_CLIENT\" \"$@\" > " + ShellCommand([]string{popupRecord}) + ";;\n*) exec sleep 300;;\nesac\n"
	if err := os.WriteFile(popupExecutable, []byte(popupScript), 0700); err != nil {
		t.Fatal(err)
	}
	l.options.Executable = popupExecutable
	t.Setenv("PATH", fixturePath)
	t.Setenv("TMUX", "")
	t.Cleanup(func() {
		cleanup := context.Background()
		out, _ := l.tmux(cleanup, "list-sessions", "-F", "#{session_name}")
		for _, session := range strings.Fields(out) {
			_, _ = l.tmux(cleanup, "kill-session", "-t", session)
		}
		if t.Failed() {
			t.Logf("Private tmux client-view diagnostics retained at %s", state)
		} else {
			_ = os.RemoveAll(state)
			_ = os.RemoveAll(state + ".bin")
		}
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
	runtime := Runtime{Version: 1, AttemptID: "run_views", Controller: "/old/frozen-lifecycle-controller", Session: "core", State: "running", Agents: map[string]AgentPane{}}
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
	for _, role := range []string{"supervisor", "broker"} {
		pane := tmux("new-window", "-d", "-t", runtime.Session, "-n", role, "-P", "-F", "#{pane_id}", "exec sleep 300")
		if err := l.tagPane(ctx, pane, runtime.AttemptID, role, ""); err != nil {
			t.Fatal(err)
		}
		if role == "supervisor" {
			runtime.SupervisorPane = pane
		} else {
			runtime.BrokerPane = pane
		}
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
			var diagnostics bytes.Buffer
			err := l.AttachAgent(ctx, runtime, agent, reader, io.Discard, &diagnostics)
			if err != nil {
				err = fmt.Errorf("%w: %s", err, diagnostics.String())
			}
			done <- err
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
	wait("first attached view", func() bool {
		select {
		case err := <-firstDone:
			t.Fatalf("first AttachAgent exited before attaching: %v", err)
		default:
		}
		return len(clientSessions()) == 1
	})
	first := clientSessions()[0]
	if err := os.WriteFile(filepath.Join(state, "pty-columns"), []byte("100"), 0600); err != nil {
		t.Fatal(err)
	}
	secondInput, secondDone := attach("bob")
	wait("second attached view", func() bool {
		select {
		case err := <-secondDone:
			t.Fatalf("second AttachAgent exited before attaching: %v", err)
		default:
		}
		return len(clientSessions()) == 2
	})
	second := ""
	for _, view := range clientSessions() {
		if view != first {
			second = view
		}
	}
	if first == runtime.Session || second == "" || second == runtime.Session {
		t.Fatalf("clients did not get independent views: %q", clientSessions())
	}
	dashboard := func(view string) string { return tmux("show-options", "-v", "-t", view, "@agentmux_dashboard") }
	firstDashboard, secondDashboard := dashboard(first), dashboard(second)
	if firstDashboard == secondDashboard || firstDashboard == runtime.DashboardPane || secondDashboard == runtime.DashboardPane || !validPane(firstDashboard) || !validPane(secondDashboard) {
		t.Fatalf("attachments share dashboard process state: first=%s second=%s core=%s", firstDashboard, secondDashboard, runtime.DashboardPane)
	}
	if tmux("display-message", "-p", "-t", firstDashboard, "#{pane_pid}") == tmux("display-message", "-p", "-t", secondDashboard, "#{pane_pid}") {
		t.Fatal("private dashboards do not have independent processes")
	}
	for _, pane := range []string{firstDashboard, secondDashboard} {
		if command := tmux("display-message", "-p", "-t", pane, "#{pane_start_command}"); !strings.Contains(command, state+".bin/") || strings.Contains(command, popupExecutable) {
			t.Fatalf("owner dashboard launched from a mutable source or old lifecycle controller: %s", command)
		}
	}
	if saved, err := Snapshot(state); err != nil || saved.Controller != runtime.Controller || l.options.Executable != popupExecutable {
		t.Fatalf("pinning upgraded presentation changed the lifecycle controller or caller: runtime=%+v err=%v", saved, err)
	}
	for _, view := range []string{first, second} {
		if group := tmux("display-message", "-p", "-t", view, "#{session_group}"); group != "" {
			t.Fatalf("private view still belongs to a shared session group: %s", group)
		}
		panes := strings.Fields(tmux("list-panes", "-s", "-t", view, "-F", "#{pane_id}"))
		if len(panes) != 3 {
			t.Fatalf("view should contain one private dashboard and two workers: %q", panes)
		}
		for _, pane := range panes {
			if pane == runtime.DashboardPane || pane == runtime.SupervisorPane || pane == runtime.BrokerPane {
				t.Fatalf("core owner UI or service linked into private attachment: %s", pane)
			}
		}
	}
	peerPID := tmux("display-message", "-p", "-t", runtime.Agents["bob"].PaneID, "#{pane_pid}")
	if environment := tmux("show-environment", "-g"); strings.Contains(environment, "AGENTMUX_FIXTURE_SECRET") || strings.Contains(environment, "sentinel-must-stay-outside-tmux") {
		t.Fatal("model-free tmux fixture inherited the caller's environment")
	}
	current := func(view string) string { return tmux("display-message", "-p", "-t", view+":", "#{window_name}") }
	if current(first) != "agent-alice" || current(second) != "agent-bob" {
		t.Fatal("attaching a second agent moved the first client's selected window")
	}
	// Private dashboard selection and input must leave the other untouched.
	tmux("select-window", "-t", first+":0")
	if current(first) != "dashboard" || current(second) != "agent-bob" {
		t.Fatal("private client navigation changed the other terminal")
	}
	for _, pane := range []string{runtime.SupervisorPane, runtime.BrokerPane} {
		if got := tmux("display-message", "-p", "-t", pane, workbenchWindowFormat); got != "" {
			t.Fatalf("service leaked into ordinary window status: %q", got)
		}
		if got := tmux("display-message", "-p", "-t", pane, workbenchCurrentWindowFormat); !strings.Contains(got, "Service:") || !strings.Contains(got, "diagnostics") {
			t.Fatalf("explicit service inspection is not labelled: %q", got)
		}
	}
	if got := tmux("display-message", "-p", "-t", runtime.DashboardPane, workbenchWindowFormat); got != "Workbench" {
		t.Fatalf("dashboard status still resembles numbered dashboard tabs: %q", got)
	}
	if got := tmux("list-keys", "-T", "prefix", "D"); !strings.Contains(got, "display-menu") || !strings.Contains(got, "read-only") || strings.Contains(got, "switch-client") {
		t.Fatalf("service diagnostics are inaccessible: %s", got)
	}
	if _, err := io.WriteString(secondInput, "\x02n"); err != nil {
		t.Fatal(err)
	}
	wait("next-window skips both service windows", func() bool { return current(second) == "dashboard" })
	if current(first) != "dashboard" {
		t.Fatal("filtered next-window moved another client")
	}
	if _, err := io.WriteString(secondInput, "\x02p"); err != nil {
		t.Fatal(err)
	}
	wait("previous-window skips both service windows", func() bool { return current(second) == "agent-bob" })
	clientName := func(view string) string {
		t.Helper()
		for _, line := range strings.Split(tmux("list-clients", "-F", "#{client_session}\t#{client_name}"), "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) == 2 && fields[0] == view {
				return fields[1]
			}
		}
		t.Fatalf("no client for %s", view)
		return ""
	}
	if _, err := io.WriteString(secondInput, "\x02b"); err != nil {
		t.Fatal(err)
	}
	wait("native pane opens an owner popup", func() bool { _, err := os.Stat(popupRecord); return err == nil })
	popupData, err := os.ReadFile(popupRecord)
	if err != nil || !strings.HasPrefix(string(popupData), clientName(second)+"\nmanaged\ndashboard\n") || !strings.Contains(string(popupData), "\n--view\nboard\n--popup\n") {
		t.Fatalf("popup lost its invoking client or literal executable path: %s %v", popupData, err)
	}
	if _, err := io.WriteString(firstInput, "\x02b"); err != nil {
		t.Fatal(err)
	}
	wait("dashboard shortcut explains its existing view instead of layering", func() bool {
		return strings.Contains(tmux("show-messages", "-t", clientName(first)), "Workbench already open. Press 1")
	})
	if after, err := os.ReadFile(popupRecord); err != nil || !bytes.Equal(after, popupData) {
		t.Fatalf("dashboard opened a second popup: %s %v", after, err)
	}
	sessionID := strings.TrimPrefix(tmux("display-message", "-p", "-t", first, "#{session_id}"), "$")
	t.Setenv("TMUX", SocketPath(state)+",1,"+sessionID)
	if err := l.Focus(ctx, runtime, "alice"); err != nil {
		t.Fatal(err)
	}
	if current(first) != "agent-alice" || current(second) != "agent-bob" {
		t.Fatal("contextual Focus selected another client's view")
	}
	if _, err := io.WriteString(firstInput, "\x1bOP"); err != nil {
		t.Fatal(err)
	}
	wait("F1 returns first attachment to its private dashboard", func() bool {
		return tmux("display-message", "-p", "-t", first+":", "#{pane_id}") == firstDashboard
	})
	if current(second) != "agent-bob" {
		t.Fatal("first attachment's F1 moved another attachment")
	}
	if got := tmux("show-options", "-v", "-t", first, "@agentmux_focus_pending"); got != "alice" {
		t.Fatalf("F1 lost first view's exact focus: %q", got)
	}
	if got := tmux("show-options", "-v", "-t", second, "@agentmux_focus_pending"); got != "bob" {
		t.Fatalf("first view focus changed second view: %q", got)
	}
	// Native window selection is observed without an explicit launcher Focus.
	tmux("set-option", "-t", first, "@agentmux_focus_pending", "")
	tmux("select-window", "-t", first+":agent-bob")
	wait("native window hook retains the exact selected agent", func() bool { return tmux("show-options", "-v", "-t", first, "@agentmux_focus_pending") == "bob" })
	tmux("select-window", "-t", firstDashboard)

	if _, err := io.WriteString(secondInput, "\x02g"); err != nil {
		t.Fatal(err)
	}
	wait("prefix-g returns second attachment to its private dashboard", func() bool {
		return tmux("display-message", "-p", "-t", second+":", "#{pane_id}") == secondDashboard
	})
	wait("different terminal widths size only their own dashboards", func() bool {
		return tmux("display-message", "-p", "-t", firstDashboard, "#{pane_width}") == "80" && tmux("display-message", "-p", "-t", secondDashboard, "#{pane_width}") == "100"
	})
	// A dynamically added worker is linked without stealing either UI. Its
	// core index differs because the core retains hidden service windows.
	carol := tmux("new-window", "-d", "-t", runtime.Session, "-n", "agent-carol", "-P", "-F", "#{pane_id}", "exec sleep 300")
	if err := l.tagPane(ctx, carol, runtime.AttemptID, "worker", "carol"); err != nil {
		t.Fatal(err)
	}
	runtime.Agents["carol"] = AgentPane{PaneID: carol, State: "running"}
	if err := l.syncClientViews(ctx, runtime); err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{first, second} {
		if !strings.Contains(tmux("list-panes", "-s", "-t", view, "-F", "#{pane_id}"), carol) {
			t.Fatalf("new worker is absent from existing view %s", view)
		}
	}
	if tmux("display-message", "-p", "-t", first+":", "#{pane_id}") != firstDashboard || tmux("display-message", "-p", "-t", second+":", "#{pane_id}") != secondDashboard {
		t.Fatal("linking a dynamic worker stole a dashboard selection")
	}
	if err := l.Focus(ctx, runtime, "carol"); err != nil {
		t.Fatal(err)
	}
	if current(first) != "agent-carol" || current(second) != "dashboard" {
		t.Fatal("Focus used the core window index rather than the target view's index")
	}
	// Focus must repair a missing link, including views attached to older
	// supervisors that have not learned to sync structural changes.
	carolIndex, err := l.workerWindowIndex(ctx, runtime, first, carol)
	if err != nil {
		t.Fatal(err)
	}
	tmux("unlink-window", "-t", first+":"+carolIndex)
	if err := l.Focus(ctx, runtime, "carol"); err != nil || current(first) != "agent-carol" {
		t.Fatalf("on-demand link repair failed: %v", err)
	}
	if err := l.Focus(ctx, runtime, "alice"); err != nil {
		t.Fatal(err)
	}
	secondID := strings.TrimPrefix(tmux("display-message", "-p", "-t", second, "#{session_id}"), "$")
	t.Setenv("TMUX", SocketPath(state)+",1,"+secondID)
	if err := l.Focus(ctx, runtime, "bob"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", SocketPath(state)+",1,"+sessionID)
	// Production keeps exited native windows visible. Exercise that transition
	// while both real clients are attached, before testing detach and shutdown.
	tmux("new-session", "-d", "-s", "foreign", "exec sleep 300")
	tmux("set-option", "-t", "foreign", "@agentmux_view", "different-attempt")
	tmux("send-keys", "-t", runtime.Agents["alice"].PaneID, "C-c")
	wait("exited worker pane retained", func() bool {
		return tmux("display-message", "-p", "-t", runtime.Agents["alice"].PaneID, "#{pane_dead}") == "1"
	})
	if current(first) != "agent-alice" || current(second) != "agent-bob" || len(clientSessions()) != 2 {
		t.Fatal("one native process exit moved or disconnected an independent client")
	}
	if tmux("display-message", "-p", "-t", runtime.Agents["bob"].PaneID, "#{pane_dead}") != "0" || !l.ownsSession(ctx, runtime) {
		t.Fatal("one native process exit destroyed another worker or the owner session")
	}
	if _, err := io.WriteString(firstInput, "\x02d"); err != nil {
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
	if got := tmux("display-message", "-p", "-t", runtime.Agents["bob"].PaneID, "#{pane_pid}"); got != peerPID {
		t.Fatal("detaching a private dashboard restarted a healthy peer")
	}
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
	logs, err := filepath.Glob(filepath.Join(state, "tmux-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range logs {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("sentinel-must-stay-outside-tmux")) {
			t.Fatal("verbose fixture diagnostics leaked the caller environment")
		}
	}
}

// The child owns a real terminal; the parent drains all its output and relays
// keys from AttachAgent's pipe. EOF on the input pipe is not a terminal detach:
// keep draining until tmux exits, just as an ordinary terminal emulator does.
// The wrapper supplies an explicit fixture-only environment to both processes.
const clientViewsPTYDriver = `import errno
import fcntl
import os
import pty
import select
import struct
import sys
import termios

pid, master = pty.fork()
if pid == 0:
    columns = 80
    try:
        with open(os.path.join(os.environ["HOME"], "pty-columns")) as size_file:
            columns = int(size_file.read())
    except FileNotFoundError:
        pass
    fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", 24, columns, 0, 0))
    os.execv(sys.argv[1], sys.argv[1:])

input_open = True
try:
    while True:
        ready, _, _ = select.select([master, 0] if input_open else [master], [], [])
        if master in ready:
            try:
                data = os.read(master, 4096)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                break
            if not data:
                break
            sys.stdout.buffer.write(data)
            sys.stdout.buffer.flush()
        if input_open and 0 in ready:
            data = os.read(0, 4096)
            if not data:
                input_open = False
            else:
                while data:
                    data = data[os.write(master, data):]
finally:
    os.close(master)
_, status = os.waitpid(pid, 0)
sys.exit(os.waitstatus_to_exitcode(status))
`

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
