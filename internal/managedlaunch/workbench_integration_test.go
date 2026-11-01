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
