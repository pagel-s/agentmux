package tmux

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/resolve"
)

func TestExecuteCreatesIsolatedSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-test.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-itest",
		Windows: []resolve.Window{
			{
				Name:   "editor",
				Root:   t.TempDir(),
				Layout: "even-horizontal",
				Panes: []resolve.Pane{
					{
						Name:       "editor-main",
						SourceType: "command",
						Command:    "printf 'pane-one-ready\n'",
						Cwd:        t.TempDir(),
						Title:      "Editor",
						Focus:      true,
					},
					{
						Name:       "checks",
						SourceType: "command",
						Command:    "printf 'pane-two-ready\n'",
						Cwd:        t.TempDir(),
						Title:      "Checks",
					},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	windowCount := tmuxOutput(t, "list-windows", "-t", cfg.Session, "-F", "#{window_name}")
	if strings.TrimSpace(windowCount) != "editor" {
		t.Fatalf("list-windows = %q, want editor", windowCount)
	}

	paneCount := tmuxOutput(t, "list-panes", "-t", cfg.Session+":editor", "-F", "#{pane_index}")
	if len(strings.Fields(strings.TrimSpace(paneCount))) != 2 {
		t.Fatalf("expected 2 panes, got output %q", paneCount)
	}

	paneName := strings.TrimSpace(tmuxOutput(t, "display-message", "-p", "-t", cfg.Session+":editor.1", "#{@agentmux_name}"))
	if paneName != "editor-main" {
		t.Fatalf("pane metadata name = %q, want %q", paneName, "editor-main")
	}

	captured := waitForPaneContains(t, cfg.Session+":editor.1", "pane-one-ready")
	if !strings.Contains(captured, "pane-one-ready") {
		t.Fatalf("capture-pane output %q does not include pane-one-ready", captured)
	}
	if strings.Contains(captured, "cd '") || strings.Contains(captured, "printf 'pane-one-ready") {
		t.Fatalf("capture-pane output %q still includes the startup command echo", captured)
	}
}

func TestExecuteRejectsExistingSessionWithoutReplace(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-test.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-itest-existing",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{SourceType: "command", Command: "printf ready"},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("first Execute returned error: %v", err)
	}
	if err := Execute(cfg, Options{}); err == nil {
		t.Fatal("second Execute returned nil error, want existing session failure")
	}
}

func TestExecuteCreatesExactGridWindow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-grid.sock")
	t.Setenv(envTmuxSocket, socket)

	panes := make([]resolve.Pane, 0, 9)
	for i := 0; i < 9; i++ {
		panes = append(panes, resolve.Pane{
			SourceType: "command",
			Command:    "printf grid",
			Title:      "Grid",
		})
	}

	cfg := resolve.Config{
		Session: "agentmux-grid",
		Windows: []resolve.Window{
			{
				Name:        "grid",
				Layout:      "grid",
				GridRows:    3,
				GridColumns: 3,
				Panes:       panes,
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	geometry, err := listPaneGeometry(cfg.Session+":grid", false)
	if err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("listPaneGeometry returned error: %v", err)
	}
	if len(geometry) != 9 {
		t.Fatalf("expected 9 panes, got %d", len(geometry))
	}

	rowSet := map[int]struct{}{}
	colSet := map[int]struct{}{}
	for _, pane := range geometry {
		rowSet[pane.Top] = struct{}{}
		colSet[pane.Left] = struct{}{}
	}
	if len(rowSet) != 3 {
		t.Fatalf("expected 3 unique row positions, got %d from %#v", len(rowSet), geometry)
	}
	if len(colSet) != 3 {
		t.Fatalf("expected 3 unique column positions, got %d from %#v", len(colSet), geometry)
	}
}

func TestStatusAndFocusNamedPanes(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-focus.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-focus",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{Name: "editor", SourceType: "command", Command: "printf ready", Title: "Editor"},
					{Name: "checks", SourceType: "agent", AgentName: "reviewer", Command: "printf checks", Title: "Checks"},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	panes, err := Status(cfg.Session)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if len(panes) != 2 {
		t.Fatalf("expected 2 panes, got %d", len(panes))
	}
	if panes[0].Name != "editor" || panes[1].Name != "checks" {
		t.Fatalf("unexpected pane names: %#v", panes)
	}
	if panes[0].SourceType != "command" || panes[1].SourceType != "agent" {
		t.Fatalf("unexpected pane source types: %#v", panes)
	}
	if panes[1].AgentName != "reviewer" {
		t.Fatalf("pane agent name = %q, want %q", panes[1].AgentName, "reviewer")
	}
	if panes[0].CurrentCmd == "" || panes[1].CurrentCmd == "" {
		t.Fatalf("expected current command metadata, got %#v", panes)
	}

	if err := FocusPaneByName(cfg.Session, "checks", false); err != nil {
		t.Fatalf("FocusPaneByName returned error: %v", err)
	}

	activeName := strings.TrimSpace(tmuxOutput(t, "display-message", "-p", "-t", cfg.Session+":main", "#{pane_id}\t#{@agentmux_name}"))
	if !strings.HasSuffix(activeName, "\tchecks") {
		t.Fatalf("active pane metadata = %q, want checks", activeName)
	}

	if err := FocusPaneByName(cfg.Session, "missing", false); err == nil {
		t.Fatal("FocusPaneByName returned nil error for missing pane")
	} else {
		message := err.Error()
		if !strings.Contains(message, `available named panes: checks, editor`) {
			t.Fatalf("missing-pane error %q does not list available panes", message)
		}
		if !strings.Contains(message, "agentmux status agentmux-focus") {
			t.Fatalf("missing-pane error %q does not include status hint", message)
		}
	}
}

func TestStatusMissingSessionReturnsHelpfulError(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-missing.sock")
	t.Setenv(envTmuxSocket, socket)

	_, err := Status("missing-session")
	if err == nil {
		t.Fatal("Status returned nil error for missing session")
	}
	if !strings.Contains(err.Error(), `session "missing-session" not found`) {
		t.Fatalf("missing-session error = %q, want session not found", err)
	}
	if !strings.Contains(err.Error(), "agentmux list") {
		t.Fatalf("missing-session error = %q, want list hint", err)
	}
}

func TestCurrentSocketPathParsesTMUXEnv(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	if got := currentSocketPath(); got != "/tmp/tmux-1000/default" {
		t.Fatalf("currentSocketPath() = %q, want %q", got, "/tmp/tmux-1000/default")
	}
}

func TestCurrentSessionUsesPaneContext(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-current.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-current",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{Name: "runner", SourceType: "command", Command: "printf ready", Title: "Runner"},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	panes, err := Status(cfg.Session)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(panes))
	}

	t.Setenv("TMUX_PANE", panes[0].PaneID)
	session, ok, err := CurrentSession()
	if err != nil {
		t.Fatalf("CurrentSession returned error: %v", err)
	}
	if !ok {
		t.Fatal("CurrentSession reported ok=false, want true")
	}
	if session != cfg.Session {
		t.Fatalf("CurrentSession = %q, want %q", session, cfg.Session)
	}
}

func TestEnsureCanReplaceRejectsCurrentPaneSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-replace-guard.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-replace-guard",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{Name: "runner", SourceType: "command", Command: "printf ready", Title: "Runner"},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	panes, err := Status(cfg.Session)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane, got %d", len(panes))
	}

	t.Setenv("TMUX_PANE", panes[0].PaneID)
	err = ensureCanReplace(cfg.Session)
	if err == nil {
		t.Fatal("ensureCanReplace returned nil error for current pane session")
	}
	if !strings.Contains(err.Error(), `cannot replace session "agentmux-replace-guard" from inside the same tmux session`) {
		t.Fatalf("ensureCanReplace error = %q, want self-replace guard", err)
	}
}

func TestMissingNamedPaneInUnnamedSessionReturnsHelpfulError(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-unnamed.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-unnamed",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{SourceType: "command", Command: "printf ready", Title: "Ready"},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	err := FocusPaneByName(cfg.Session, "missing", false)
	if err == nil {
		t.Fatal("FocusPaneByName returned nil error for unnamed session")
	}
	if !strings.Contains(err.Error(), "session has no named panes") {
		t.Fatalf("unnamed-session error = %q, want no named panes", err)
	}
	if !strings.Contains(err.Error(), "Add `name:` fields in config and reload the session") {
		t.Fatalf("unnamed-session error = %q, want name hint", err)
	}
}

func TestRestartAndRerunNamedPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-rerun.sock")
	t.Setenv(envTmuxSocket, socket)

	counterFile := filepath.Join(t.TempDir(), "counter.txt")
	commandText := fmt.Sprintf(
		"count=$(cat %s 2>/dev/null || echo 0); count=$((count+1)); echo \"$count\" > %s; printf 'run-%%s\\n' \"$count\"",
		singleQuote(counterFile),
		singleQuote(counterFile),
	)

	cfg := resolve.Config{
		Session: "agentmux-rerun",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "runner",
						SourceType: "command",
						Command:    commandText,
						Title:      "Runner",
					},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	initial := waitForPaneContains(t, cfg.Session+":main.1", "run-1")
	if !strings.Contains(initial, "run-1") {
		t.Fatalf("initial output %q does not include run-1", initial)
	}

	if err := RestartPaneByName(cfg.Session, "runner", false); err != nil {
		t.Fatalf("RestartPaneByName returned error: %v", err)
	}
	restarted := waitForPaneContains(t, cfg.Session+":main.1", "run-2")
	if !strings.Contains(restarted, "run-2") {
		t.Fatalf("restarted output %q does not include run-2", restarted)
	}

	if err := RerunPaneByName(cfg.Session, "runner", false); err != nil {
		t.Fatalf("RerunPaneByName returned error: %v", err)
	}
	rerun := waitForPaneContains(t, cfg.Session+":main.1", "run-3")
	if !strings.Contains(rerun, "run-3") {
		t.Fatalf("rerun output %q does not include run-3", rerun)
	}

	name := strings.TrimSpace(tmuxOutput(t, "display-message", "-p", "-t", cfg.Session+":main.1", "#{@agentmux_name}"))
	if name != "runner" {
		t.Fatalf("pane metadata name = %q, want %q", name, "runner")
	}
}

func TestSendCommandToNamedPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-send.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-send",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "runner",
						SourceType: "command",
						Command:    "printf 'ready\\n'",
						Title:      "Runner",
					},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	if out := waitForPaneContains(t, cfg.Session+":main.1", "ready"); !strings.Contains(out, "ready") {
		t.Fatalf("initial output %q does not include ready", out)
	}

	if err := SendCommandToPaneByName(cfg.Session, "runner", "printf 'sent-from-test\\n'", false); err != nil {
		t.Fatalf("SendCommandToPaneByName returned error: %v", err)
	}

	sent := waitForPaneContains(t, cfg.Session+":main.1", "sent-from-test")
	if !strings.Contains(sent, "sent-from-test") {
		t.Fatalf("sent output %q does not include sent-from-test", sent)
	}
}

func TestCaptureNamedPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-capture.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-capture",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "runner",
						SourceType: "command",
						Command:    "printf 'ready\\n'",
						Title:      "Runner",
					},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	if err := SendCommandToPaneByName(cfg.Session, "runner", "printf 'captured-from-test\\n'", false); err != nil {
		t.Fatalf("SendCommandToPaneByName returned error: %v", err)
	}

	captured := waitForCaptureContains(t, cfg.Session, "runner", "captured-from-test")
	if !strings.Contains(captured, "captured-from-test") {
		t.Fatalf("captured output %q does not include captured-from-test", captured)
	}
}

func TestStopNamedPaneLeavesShellUsable(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-stop.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-stop",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "runner",
						SourceType: "command",
						Command:    "printf 'running\\n'; while :; do sleep 1; done",
						Title:      "Runner",
					},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	if out := waitForPaneContains(t, cfg.Session+":main.1", "running"); !strings.Contains(out, "running") {
		t.Fatalf("initial output %q does not include running", out)
	}

	if err := StopPaneByName(cfg.Session, "runner", false); err != nil {
		t.Fatalf("StopPaneByName returned error: %v", err)
	}
	if err := SendCommandToPaneByName(cfg.Session, "runner", "printf 'after-stop\\n'", false); err != nil {
		t.Fatalf("SendCommandToPaneByName after stop returned error: %v", err)
	}

	afterStop := waitForPaneContains(t, cfg.Session+":main.1", "after-stop")
	if !strings.Contains(afterStop, "after-stop") {
		t.Fatalf("output after stop %q does not include after-stop", afterStop)
	}
}

func TestKillNamedPaneRemovesItFromSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-kill.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-kill",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "keep",
						SourceType: "command",
						Command:    "printf 'keep\\n'; exec $SHELL",
						Title:      "Keep",
					},
					{
						Name:       "drop",
						SourceType: "command",
						Command:    "printf 'drop\\n'; exec $SHELL",
						Title:      "Drop",
					},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	if err := KillPaneByName(cfg.Session, "drop", false); err != nil {
		t.Fatalf("KillPaneByName returned error: %v", err)
	}

	panes, err := Status(cfg.Session)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if len(panes) != 1 {
		t.Fatalf("expected 1 pane after kill, got %d", len(panes))
	}
	if panes[0].Name != "keep" {
		t.Fatalf("remaining pane = %#v, want keep", panes[0])
	}

	if err := KillPaneByName(cfg.Session, "drop", false); err == nil {
		t.Fatal("KillPaneByName returned nil error for already-killed pane")
	}
}

func tmuxOutput(t *testing.T, args ...string) string {
	t.Helper()

	out, err := capture(false, args...)
	if err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("tmux %v returned error: %v", args, err)
	}
	return out
}

func waitForPaneContains(t *testing.T, target string, want string) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		last = tmuxOutput(t, "capture-pane", "-J", "-pt", target)
		if strings.Contains(last, want) {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}

	return last
}

func waitForCaptureContains(t *testing.T, session string, paneName string, want string) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		out, err := CapturePaneByName(session, paneName, false)
		if err != nil {
			if isTmuxPermissionError(err) {
				t.Skipf("tmux integration unavailable in this environment: %v", err)
			}
			t.Fatalf("CapturePaneByName returned error: %v", err)
		}
		last = out
		if strings.Contains(last, want) {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}

	return last
}

func isTmuxPermissionError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "Operation not permitted") || strings.Contains(message, "permission denied")
}
