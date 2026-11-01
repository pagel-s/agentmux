package managedlaunch

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

type workbenchTerminalOutput struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *workbenchTerminalOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

func (b *workbenchTerminalOutput) contains(text string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Contains(b.data.String(), text)
}

func (b *workbenchTerminalOutput) snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

func TestWorkbenchServiceNavigationAndPopupBindingsUseInvokingClient(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_MANAGED_INTEGRATION=1 for actual tmux workbench bindings")
	}
	realTMux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.MkdirTemp("", "amx-#{pane_id}-workbench-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	popupRecord := filepath.Join(state, "popup-invocation")
	popupExecutable := filepath.Join(state, "popup-#fixture")
	popupScript := "#!/bin/sh\nprintf '%s\\n' \"$AGENTMUX_CLIENT\" \"$@\" > " + ShellCommand([]string{popupRecord}) + "\n"
	if err := os.WriteFile(popupExecutable, []byte(popupScript), 0700); err != nil {
		t.Fatal(err)
	}
	l, err := New(Options{StateDir: state, Executable: popupExecutable})
	if err != nil {
		t.Fatal(err)
	}
	fixtureBin := filepath.Join(state, "bin")
	if err := os.Mkdir(fixtureBin, 0700); err != nil {
		t.Fatal(err)
	}
	captureRecord := filepath.Join(state, "service-capture")
	captureScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + ShellCommand([]string{captureRecord}) + "\nexec " + ShellCommand([]string{realTMux}) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(fixtureBin, "tmux"), []byte(captureScript), 0700); err != nil {
		t.Fatal(err)
	}
	fixtureEnv := []string{"PATH=" + fixtureBin + ":/usr/bin:/bin", "HOME=" + state, "SHELL=/bin/sh", "TERM=xterm-256color", "LC_ALL=C"}
	l.run = func(ctx context.Context, args ...string) (string, error) {
		command := exec.CommandContext(ctx, realTMux, append([]string{"-u"}, args...)...)
		command.Env = fixtureEnv
		output, err := command.CombinedOutput()
		if err != nil {
			err = fmt.Errorf("tmux %q: %w: %s", args, err, output)
		}
		return string(output), err
	}
	type terminal struct {
		command *exec.Cmd
		done    <-chan error
	}
	var terminals []terminal
	t.Cleanup(func() {
		_, _ = l.tmux(context.Background(), "kill-server")
		for _, terminal := range terminals {
			select {
			case <-terminal.done:
			case <-time.After(3 * time.Second):
				_ = terminal.command.Process.Kill()
				<-terminal.done
			}
		}
		_ = os.RemoveAll(state)
	})
	tmux := func(args ...string) string {
		t.Helper()
		out, err := l.tmux(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	wait := func(description string, predicate func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			if predicate() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("timed out: " + description)
	}
	core := Runtime{Session: "core", AttemptID: "run_workbench"}
	core.DashboardPane = tmux("new-session", "-d", "-s", core.Session, "-n", "dashboard", "-P", "-F", "#{pane_id}", "exec /bin/sleep 60")
	if err := l.tagPane(ctx, core.DashboardPane, core.AttemptID, "dashboard", ""); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"supervisor", "broker"} {
		pane := tmux("new-window", "-d", "-t", core.Session, "-n", role, "-P", "-F", "#{pane_id}", "printf 'SERVICE_"+role+"\\n'; exec /bin/sleep 60")
		if err := l.tagPane(ctx, pane, core.AttemptID, role, ""); err != nil {
			t.Fatal(err)
		}
		if role == "broker" {
			core.BrokerPane = pane
		} else {
			core.SupervisorPane = pane
		}
		if got := tmux("display-message", "-p", "-t", pane, workbenchWindowFormat); got != "" {
			t.Fatalf("internal service exposed in ordinary status: %q", got)
		}
		if got := tmux("display-message", "-p", "-t", pane, workbenchCurrentWindowFormat); !strings.Contains(got, "Service:") || !strings.Contains(got, "diagnostics") {
			t.Fatalf("service inspection lacks its diagnostic label: %q", got)
		}
	}
	worker := tmux("new-window", "-d", "-t", core.Session, "-n", "agent-alice", "-P", "-F", "#{pane_id}", "exec /bin/sleep 60")
	if err := l.tagPane(ctx, worker, core.AttemptID, "worker", "alice"); err != nil {
		t.Fatal(err)
	}
	peer := core
	peer.Session = "peer"
	peer.DashboardPane = tmux("new-session", "-d", "-s", peer.Session, "-n", "dashboard", "-P", "-F", "#{pane_id}", "exec /bin/sleep 60")
	if err := l.tagPane(ctx, peer.DashboardPane, peer.AttemptID, "dashboard", ""); err != nil {
		t.Fatal(err)
	}
	tmux("link-window", "-d", "-s", worker, "-t", peer.Session+":1")
	for _, runtime := range []Runtime{core, peer} {
		if err := l.configureWorkbench(ctx, runtime); err != nil {
			t.Fatal(err)
		}
	}
	// A session-targeted window option only reaches its current window.
	// Services and linked workers must also inherit the dedicated defaults.
	for _, pane := range []string{core.BrokerPane, core.SupervisorPane, worker} {
		if got := tmux("show-options", "-w", "-A", "-v", "-t", pane, "window-status-format"); got != workbenchWindowFormat {
			t.Fatalf("window %s missed the workbench status format: %q", pane, got)
		}
	}
	if got := tmux("list-keys", "-T", "prefix", "n"); strings.Count(got, "next-window") != 3 {
		t.Fatalf("service-skipping commands escaped their binding: %s", got)
	}
	if got := tmux("list-keys", "-T", "prefix", "D"); !strings.Contains(got, "display-menu") || !strings.Contains(got, "read-only") {
		t.Fatalf("services are unavailable for explicit diagnostics: %s", got)
	}
	current := func(session string) string { return tmux("display-message", "-p", "-t", session+":", "#{pane_id}") }
	tmux("select-window", "-t", core.DashboardPane)
	tmux("select-window", "-t", peer.Session+":1")
	outputs := map[string]*workbenchTerminalOutput{}
	attach := func(session string) io.WriteCloser {
		t.Helper()
		command := exec.CommandContext(ctx, python, "-c", clientViewsPTYDriver, realTMux, "-u", "-S", SocketPath(state), "attach-session", "-t", session)
		command.Env = fixtureEnv
		output := &workbenchTerminalOutput{}
		outputs[session] = output
		command.Stdout, command.Stderr = output, output
		input, err := command.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		terminals = append(terminals, terminal{command, done})
		t.Cleanup(func() { _ = input.Close() })
		return input
	}
	first, second := attach(core.Session), attach(peer.Session)
	wait("both terminal clients attach", func() bool { return len(strings.Fields(tmux("list-clients", "-F", "#{client_session}"))) == 2 })
	keys := func(input io.WriteCloser, value string) {
		t.Helper()
		if _, err := io.WriteString(input, value); err != nil {
			t.Fatal(err)
		}
	}
	keys(first, "\x02n")
	wait("next-window skips two services", func() bool { return current(core.Session) == worker })
	keys(first, "\x02p")
	wait("previous-window skips two services", func() bool { return current(core.Session) == core.DashboardPane })
	tmux("select-window", "-t", core.Session+":3")
	keys(first, "\x02g")
	wait("prefix g returns to invoking dashboard", func() bool { return current(core.Session) == core.DashboardPane })
	tmux("select-window", "-t", core.Session+":3")
	keys(first, "\x1bOP")
	wait("F1 returns to invoking dashboard", func() bool { return current(core.Session) == core.DashboardPane })
	if current(peer.Session) != worker {
		t.Fatal("workbench shortcuts changed another client's selected window")
	}
	clientName := func(session string) string {
		for _, line := range strings.Split(tmux("list-clients", "-F", "#{client_session}\t#{client_name}"), "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) == 2 && fields[0] == session {
				return fields[1]
			}
		}
		return ""
	}
	keys(first, "\x02D")
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Workbench PTY: %q", outputs[core.Session].snapshot())
			data, err := os.ReadFile(captureRecord)
			t.Logf("Service capture: %q (%v)", data, err)
		}
	})
	wait("read-only service menu appears", func() bool { return outputs[core.Session].contains("Runtime services") })
	keys(first, "b")
	wait("broker log snapshot appears", func() bool { return outputs[core.Session].contains("SERVICE_broker") })
	captured, err := os.ReadFile(captureRecord)
	if err != nil || !strings.Contains(string(captured), "-S\n"+SocketPath(state)+"\ncapture-pane\n") || !strings.HasSuffix(string(captured), "-t\n"+core.BrokerPane+"\n") {
		t.Fatalf("service snapshot reinterpreted the socket or pane: %s %v", captured, err)
	}
	if current(core.Session) != core.DashboardPane || current(peer.Session) != worker || clientName(core.Session) == "" {
		t.Fatal("service diagnostics changed a client's session or selected window")
	}
	keys(first, "\r")
	keys(second, "\x02b")
	wait("worker opens a client-owned popup", func() bool { _, err := os.Stat(popupRecord); return err == nil })
	popupData, err := os.ReadFile(popupRecord)
	if err != nil || !strings.HasPrefix(string(popupData), clientName(peer.Session)+"\nmanaged\ndashboard\n") || !strings.Contains(string(popupData), "\n--view\nboard\n--popup\n") {
		t.Fatalf("popup lost client identity or literal executable path: %s %v", popupData, err)
	}
	keys(first, "\x02b")
	wait("existing dashboard gives view guidance", func() bool {
		return strings.Contains(tmux("show-messages", "-t", clientName(core.Session)), "Workbench already open. Press 1")
	})
	if after, err := os.ReadFile(popupRecord); err != nil || !bytes.Equal(after, popupData) {
		t.Fatalf("dashboard layered another popup: %s %v", after, err)
	}
}

func TestNewAttachmentPreservesLegacyGroupedDashboardShortcuts(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_MANAGED_INTEGRATION=1 for legacy tmux view compatibility")
	}
	realTMux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.MkdirTemp("", "amx-legacy-view-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	store, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: "core", Agents: map[string]managedconfig.Agent{"alice": {Harness: "command", Command: []string{"/bin/true"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	observer := filepath.Join(state, "observer")
	if err := os.WriteFile(observer, []byte("#!/bin/sh\nexec /bin/sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	l, err := New(Options{StateDir: state, Executable: observer})
	if err != nil {
		t.Fatal(err)
	}
	fixtureEnv := []string{"PATH=/usr/bin:/bin", "HOME=" + state, "SHELL=/bin/sh", "TERM=xterm-256color", "LC_ALL=C"}
	l.run = func(ctx context.Context, args ...string) (string, error) {
		command := exec.CommandContext(ctx, realTMux, append([]string{"-u"}, args...)...)
		command.Env = fixtureEnv
		output, err := command.CombinedOutput()
		if err != nil {
			err = fmt.Errorf("tmux %q: %w: %s", args, err, output)
		}
		return string(output), err
	}
	var clientDone []<-chan error
	t.Cleanup(func() {
		_, _ = l.tmux(context.Background(), "kill-server")
		for _, done := range clientDone {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("fixture client did not stop with its server")
			}
		}
		_ = os.RemoveAll(state)
		_ = os.RemoveAll(state + ".bin")
	})
	tmux := func(args ...string) string {
		t.Helper()
		out, err := l.tmux(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	wait := func(description string, predicate func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			if predicate() {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("timed out: " + description)
	}
	runtime := Runtime{Session: "core", AttemptID: "run_legacy_view", Agents: map[string]AgentPane{}}
	runtime.DashboardPane = tmux("new-session", "-d", "-s", runtime.Session, "-n", "dashboard", "-P", "-F", "#{pane_id}", "exec /bin/sleep 60")
	tmux("set-option", "-t", runtime.Session, "@agentmux_runtime", runtime.AttemptID)
	if err := l.tagPane(ctx, runtime.DashboardPane, runtime.AttemptID, "dashboard", ""); err != nil {
		t.Fatal(err)
	}
	worker := tmux("new-window", "-d", "-t", runtime.Session, "-n", "agent-alice", "-P", "-F", "#{pane_id}", "exec /bin/sleep 60")
	if err := l.tagPane(ctx, worker, runtime.AttemptID, "worker", "alice"); err != nil {
		t.Fatal(err)
	}
	runtime.Agents["alice"] = AgentPane{PaneID: worker, State: "running"}
	tmux("new-session", "-d", "-t", runtime.Session, "-s", "legacy")
	tmux("set-option", "-t", "legacy", "@agentmux_view", runtime.AttemptID)
	for _, session := range []string{runtime.Session, "legacy"} {
		if got := tmux("display-message", "-p", "-t", session+":", "#{@agentmux_dashboard}"); got != "" {
			t.Fatalf("legacy fixture unexpectedly had a dashboard option: %q", got)
		}
	}
	tmux("bind-key", "-n", "F1", "select-window", "-t", runtime.DashboardPane)
	tmux("select-window", "-t", "legacy:1")
	oldCommand := exec.CommandContext(ctx, python, "-c", clientViewsPTYDriver, realTMux, "-u", "-S", SocketPath(state), "attach-session", "-t", "legacy")
	oldCommand.Env = fixtureEnv
	oldInput, err := oldCommand.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = oldInput.Close() })
	oldCommand.Stdout, oldCommand.Stderr = io.Discard, io.Discard
	if err := oldCommand.Start(); err != nil {
		t.Fatal(err)
	}
	oldDone := make(chan error, 1)
	go func() { oldDone <- oldCommand.Wait() }()
	clientDone = append(clientDone, oldDone)
	wait("legacy terminal attaches", func() bool { return tmux("list-clients", "-F", "#{client_session}") == "legacy" })
	// Foreground AttachAgent gets a real PTY, while owner operations retain the
	// real tmux path and the server sees only the fixture environment above.
	bin := t.TempDir()
	command := append([]string{"/usr/bin/env", "-i"}, fixtureEnv...)
	command = append(command, python, "-c", clientViewsPTYDriver, realTMux, "-u")
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexec "+ShellCommand(command)+" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("TMUX", "")
	newInput, newWriter := io.Pipe()
	t.Cleanup(func() { _ = newWriter.Close(); _ = newInput.Close() })
	newDone := make(chan error, 1)
	go func() { newDone <- l.AttachAgent(ctx, runtime, "alice", newInput, io.Discard, io.Discard) }()
	clientDone = append(clientDone, newDone)
	view := ""
	wait("new private attachment joins legacy terminal", func() bool {
		for _, session := range strings.Fields(tmux("list-clients", "-F", "#{client_session}")) {
			if session != "legacy" {
				view = session
			}
		}
		return view != ""
	})
	private := tmux("show-options", "-v", "-t", view, "@agentmux_dashboard")
	if private == runtime.DashboardPane || !validPane(private) {
		t.Fatalf("new view did not retain its private dashboard: %q", private)
	}
	for _, session := range []string{runtime.Session, "legacy"} {
		if got := tmux("display-message", "-p", "-t", session+":", "#{@agentmux_dashboard}"); got != runtime.DashboardPane {
			t.Fatalf("legacy session %s lost its core dashboard fallback: %q", session, got)
		}
	}
	current := func(session string) string { return tmux("display-message", "-p", "-t", session+":", "#{pane_id}") }
	for _, keys := range []string{"\x1bOP", "\x02g"} {
		tmux("select-window", "-t", "legacy:1")
		if _, err := io.WriteString(oldInput, keys); err != nil {
			t.Fatal(err)
		}
		wait("legacy shortcut returns to core dashboard", func() bool { return current("legacy") == runtime.DashboardPane })
		if current(view) != worker {
			t.Fatal("legacy shortcut moved the new private client")
		}
	}
	if _, err := io.WriteString(newWriter, "\x1bOP"); err != nil {
		t.Fatal(err)
	}
	wait("new shortcut returns to private dashboard", func() bool { return current(view) == private })
	if current("legacy") != runtime.DashboardPane {
		t.Fatal("new shortcut moved the legacy client")
	}
	// A known pane with changed ownership must not become an inherited target.
	tmux("set-option", "-p", "-t", runtime.DashboardPane, "@agentmux_role", "worker")
	if err := l.ensureDashboardFallback(ctx, runtime); err == nil {
		t.Fatal("retagged core dashboard was accepted as an owner UI")
	}
	tmux("set-option", "-p", "-t", runtime.DashboardPane, "@agentmux_role", "dashboard")
	workerPID := tmux("display-message", "-p", "-t", worker, "#{pane_pid}")
	tmux("kill-window", "-t", runtime.DashboardPane)
	// Optional compatibility fallback must not make the old core UI a new
	// lifecycle dependency: a replacement attachment has its own dashboard.
	replacementInput, replacementWriter := io.Pipe()
	t.Cleanup(func() { _ = replacementWriter.Close(); _ = replacementInput.Close() })
	replacementDone := make(chan error, 1)
	go func() {
		replacementDone <- l.AttachAgent(ctx, runtime, "alice", replacementInput, io.Discard, io.Discard)
	}()
	clientDone = append(clientDone, replacementDone)
	replacement := ""
	wait("new attachment survives a removed core dashboard", func() bool {
		for _, session := range strings.Fields(tmux("list-clients", "-F", "#{client_session}")) {
			if session != "legacy" && session != view {
				replacement = session
			}
		}
		return replacement != ""
	})
	replacementDashboard := tmux("show-options", "-v", "-t", replacement, "@agentmux_dashboard")
	if !validPane(replacementDashboard) || replacementDashboard == runtime.DashboardPane || replacementDashboard == private {
		t.Fatalf("missing core UI prevented a new private dashboard: %q", replacementDashboard)
	}
	if _, err := io.WriteString(replacementWriter, "\x1bOP"); err != nil {
		t.Fatal(err)
	}
	wait("replacement F1 selects its private dashboard", func() bool { return current(replacement) == replacementDashboard })
	if current(view) != private || tmux("display-message", "-p", "-t", worker, "#{pane_pid}") != workerPID {
		t.Fatal("replacing a removed core UI changed an existing attachment or worker")
	}
}
