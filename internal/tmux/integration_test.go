package tmux

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/config"
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
						Command:    "sleep 0.5; echo 'pane-one-ready' && sleep 3600",
						Cwd:        t.TempDir(),
						Title:      "Editor",
						Focus:      true,
						OnExit:     config.OnExitKill,
					},
					{
						Name:       "checks",
						SourceType: "command",
						Command:    "sleep 0.5; echo 'pane-two-ready' && sleep 3600",
						Cwd:        t.TempDir(),
						Title:      "Checks",
						OnExit:     config.OnExitKill,
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

	paneID := getPaneID(t, cfg.Session, "editor-main")
	paneName := strings.TrimSpace(tmuxOutput(t, "display-message", "-p", "-t", paneID, "#{@agentmux_name}"))
	if paneName != "editor-main" {
		t.Fatalf("pane metadata name = %q, want %q", paneName, "editor-main")
	}

	captured := waitForPaneContains(t, paneID, "pane-one-ready")
	if !strings.Contains(captured, "pane-one-ready") {
		t.Fatalf("capture-pane output %q does not include pane-one-ready", captured)
	}
	if strings.Contains(captured, "cd '") || strings.Contains(captured, "echo 'pane-one-ready") {
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
		Session: "agentmux-status",
		Windows: []resolve.Window{
			{
				Name: "editor",
				Panes: []resolve.Pane{
					{Name: "editor", SourceType: "command", Command: "printf ready", Title: "Editor", OnExit: config.OnExitShell},
				},
			},
			{
				Name: "review",
				Panes: []resolve.Pane{
					{Name: "checks", SourceType: "agent", AgentName: "reviewer", Command: "printf checks", Title: "Checks", OnExit: config.OnExitRestart},
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
	if panes[0].WindowName != "editor" || panes[1].WindowName != "review" {
		t.Fatalf("unexpected pane windows: %#v", panes)
	}
	if panes[0].SourceType != "command" || panes[1].SourceType != "agent" {
		t.Fatalf("unexpected pane source types: %#v", panes)
	}
	if panes[0].Dead || panes[1].Dead {
		t.Fatalf("expected live panes, got %#v", panes)
	}
	if panes[1].AgentName != "reviewer" {
		t.Fatalf("pane agent name = %q, want %q", panes[1].AgentName, "reviewer")
	}
	if panes[0].OnExit != config.OnExitShell {
		t.Fatalf("pane on_exit = %q, want %q", panes[0].OnExit, config.OnExitShell)
	}
	if panes[1].OnExit != config.OnExitRestart {
		t.Fatalf("pane on_exit = %q, want %q", panes[1].OnExit, config.OnExitRestart)
	}
	if panes[0].CurrentCmd == "" || panes[1].CurrentCmd == "" {
		t.Fatalf("expected current command metadata, got %#v", panes)
	}

	if err := FocusPaneByName(cfg.Session, "checks", false); err != nil {
		t.Fatalf("FocusPaneByName returned error: %v", err)
	}

	activeName := strings.TrimSpace(tmuxOutput(t, "display-message", "-p", "-t", cfg.Session+":review", "#{pane_id}\t#{@agentmux_name}"))
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
		if !strings.Contains(message, "agentmux status agentmux-status") {
			t.Fatalf("missing-pane error %q does not include status hint", message)
		}
	}
}

func TestSessionConfigPathRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-session-meta.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-session-meta",
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

	wantPath := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := SetSessionConfigPath(cfg.Session, wantPath, false); err != nil {
		t.Fatalf("SetSessionConfigPath returned error: %v", err)
	}

	gotPath, err := SessionConfigPath(cfg.Session, false)
	if err != nil {
		t.Fatalf("SessionConfigPath returned error: %v", err)
	}
	if gotPath != wantPath {
		t.Fatalf("SessionConfigPath = %q, want %q", gotPath, wantPath)
	}
}

func TestExecuteRemainOnExitKeepsPaneAvailable(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-remain.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-remain",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "remain",
						SourceType: "command",
						Command:    "printf 'remain-done\\n' && sleep 0.2",
						Title:      "Remain",
						OnExit:     config.OnExitRemain,
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

	paneID := getPaneID(t, cfg.Session, "remain")
	if out := waitForPaneContains(t, paneID, "remain-done"); !strings.Contains(out, "remain-done") {
		t.Fatalf("remain output %q does not include remain-done", out)
	}

	if got := strings.TrimSpace(tmuxOutput(t, "show-options", "-p", "-v", "-t", paneID, "remain-on-exit")); got != "on" {
		t.Fatalf("remain-on-exit = %q, want on", got)
	}

	panes, err := Status(cfg.Session)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if len(panes) != 1 || panes[0].Name != "remain" {
		t.Fatalf("status panes = %#v, want remain pane", panes)
	}
	if panes[0].OnExit != config.OnExitRemain {
		t.Fatalf("pane on_exit = %q, want %q", panes[0].OnExit, config.OnExitRemain)
	}
	waitForPaneDead(t, cfg.Session, "remain", true, 2*time.Second)
}

func TestExecuteRestartOnExitRestartsCommand(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-auto-restart.sock")
	t.Setenv(envTmuxSocket, socket)

	counterFile := filepath.Join(t.TempDir(), "counter.txt")
	commandText := fmt.Sprintf(
		"count=$(cat %s 2>/dev/null || echo 0); count=$((count+1)); echo \"$count\" > %s; printf 'auto-run-%%s\\n' \"$count\"",
		singleQuote(counterFile),
		singleQuote(counterFile),
	)

	cfg := resolve.Config{
		Session: "agentmux-auto-restart",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "runner",
						SourceType: "command",
						Command:    commandText,
						Title:      "Runner",
						OnExit:     config.OnExitRestart,
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

	paneID := getPaneID(t, cfg.Session, "runner")
	out := waitForPaneContainsWithin(t, paneID, "auto-run-2", 4*time.Second)
	if !strings.Contains(out, "auto-run-2") {
		t.Fatalf("restart output %q does not include auto-run-2", out)
	}

	panes, err := Status(cfg.Session)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if len(panes) != 1 || panes[0].OnExit != config.OnExitRestart {
		t.Fatalf("status panes = %#v, want restart pane", panes)
	}
}

func TestExecuteKillOnExitRemovesPaneAfterCommandExit(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-auto-kill.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-auto-kill",
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
						Command:    "printf 'drop\\n'",
						Title:      "Drop",
						OnExit:     config.OnExitKill,
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

	waitForPaneCount(t, cfg.Session, 1, 3*time.Second)

	panes, err := Status(cfg.Session)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if len(panes) != 1 || panes[0].Name != "keep" {
		t.Fatalf("status panes = %#v, want only keep pane", panes)
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

	t.Setenv("TMUX", socket+",123,0")
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
		"echo x >> %s; printf 'run-'; cat %s | wc -l | tr -d ' '; printf '\\n'",
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

	paneID := getPaneID(t, cfg.Session, "runner")
	initial := waitForPaneContains(t, paneID, "run-1")
	if !strings.Contains(initial, "run-1") {
		t.Fatalf("initial output %q does not include run-1", initial)
	}

	if err := RestartPaneByName(cfg.Session, "runner", false); err != nil {
		t.Fatalf("RestartPaneByName returned error: %v", err)
	}
	restarted := waitForPaneContains(t, paneID, "run-2")
	if !strings.Contains(restarted, "run-2") {
		t.Fatalf("restarted output %q does not include run-2", restarted)
	}

	if err := RerunPaneByName(cfg.Session, "runner", false); err != nil {
		t.Fatalf("RerunPaneByName returned error: %v", err)
	}
	rerun := waitForPaneContains(t, paneID, "run-3")
	if !strings.Contains(rerun, "run-3") {
		t.Fatalf("rerun output %q does not include run-3", rerun)
	}

	name := strings.TrimSpace(tmuxOutput(t, "display-message", "-p", "-t", paneID, "#{@agentmux_name}"))
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

	paneID := getPaneID(t, cfg.Session, "runner")
	if out := waitForPaneContains(t, paneID, "ready"); !strings.Contains(out, "ready") {
		t.Fatalf("initial output %q does not include ready", out)
	}

	if err := SendCommandToPaneByName(cfg.Session, "runner", "printf 'sent-from-test\\n'", true, false); err != nil {
		t.Fatalf("SendCommandToPaneByName returned error: %v", err)
	}

	if out := waitForPaneContains(t, paneID, "sent-from-test"); !strings.Contains(out, "sent-from-test") {
		t.Fatalf("sent output %q does not include sent-from-test", out)
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

	if err := SendCommandToPaneByName(cfg.Session, "runner", "printf 'captured-from-test\\n'", true, false); err != nil {
		t.Fatalf("SendCommandToPaneByName returned error: %v", err)
	}

	captured := waitForCaptureContains(t, cfg.Session, "runner", 0, "captured-from-test")
	if !strings.Contains(captured, "captured-from-test") {
		t.Fatalf("captured output %q does not include captured-from-test", captured)
	}
}

func TestCaptureNamedPaneTailLines(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-capture-tail.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-capture-tail",
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

	if err := SendCommandToPaneByName(cfg.Session, "runner", "for i in $(seq 1 20); do echo \"tail-line-$i\"; done", true, false); err != nil {
		t.Fatalf("SendCommandToPaneByName returned error: %v", err)
	}

	captured := waitForCaptureContains(t, cfg.Session, "runner", 10, "tail-line-20")
	if !strings.Contains(captured, "tail-line-20") {
		t.Fatalf("captured output %q does not include tail-line-20", captured)
	}
	if strings.Contains(captured, "tail-line-1\n") {
		t.Fatalf("captured tail output %q still includes tail-line-1", captured)
	}
}

func TestCaptureNamedPaneStripEmpty(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-capture-strip-empty.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-capture-strip-empty",
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

	if err := SendCommandToPaneByName(cfg.Session, "runner", "printf 'line-1\\n\\nline-2\\n'", true, false); err != nil {
		t.Fatalf("SendCommandToPaneByName returned error: %v", err)
	}

	captured := waitForCaptureContainsOptions(t, cfg.Session, "runner", 0, true, "line-2")
	if !strings.Contains(captured, "line-1") || !strings.Contains(captured, "line-2") {
		t.Fatalf("captured output %q does not include expected lines", captured)
	}
	if strings.Contains(captured, "\n\n") {
		t.Fatalf("captured output %q still includes blank lines", captured)
	}
}

func TestSendCommandToNamedPaneWithoutEnter(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-send-no-enter.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-send-no-enter",
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

	if err := SendCommandToPaneByName(cfg.Session, "runner", "printf 'queued-only\\n'", false, false); err != nil {
		t.Fatalf("SendCommandToPaneByName returned error: %v", err)
	}

	paneID := getPaneID(t, cfg.Session, "runner")
	captured := waitForPaneContainsWithin(t, paneID, "printf 'queued-only\\n'", 2*time.Second)
	if !strings.Contains(captured, "printf 'queued-only\\n'") {
		t.Fatalf("captured output %q does not include queued command text", captured)
	}
	if strings.Contains(captured, "queued-only\n") {
		t.Fatalf("captured output %q unexpectedly includes executed command output", captured)
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

	paneID := getPaneID(t, cfg.Session, "runner")
	if out := waitForPaneContains(t, paneID, "running"); !strings.Contains(out, "running") {
		t.Fatalf("initial output %q does not include running", out)
	}

	if err := StopPaneByName(cfg.Session, "runner", false); err != nil {
		t.Fatalf("StopPaneByName returned error: %v", err)
	}
	if err := SendCommandToPaneByName(cfg.Session, "runner", "printf 'after-stop\\n'", true, false); err != nil {
		t.Fatalf("SendCommandToPaneByName after stop returned error: %v", err)
	}

	afterStop := waitForPaneContains(t, paneID, "after-stop")
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

func getPaneID(t *testing.T, session string, name string) string {
	t.Helper()
	p, err := StatusPaneByName(session, name)
	if err != nil {
		t.Fatalf("getPaneID(%q, %q): %v", session, name, err)
	}
	return p.PaneID
}

func waitForPaneContains(t *testing.T, target string, want string) string {
	t.Helper()

	return waitForPaneContainsWithin(t, target, want, 2*time.Second)
}

func waitForPaneContainsWithin(t *testing.T, target string, want string, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
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

func waitForPaneCount(t *testing.T, session string, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	lastCount := -1
	for time.Now().Before(deadline) {
		panes, err := Status(session)
		if err != nil {
			if isTmuxPermissionError(err) {
				t.Skipf("tmux integration unavailable in this environment: %v", err)
			}
			t.Fatalf("Status returned error: %v", err)
		}
		lastCount = len(panes)
		if lastCount == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("pane count = %d, want %d", lastCount, want)
}

func waitForPaneDead(t *testing.T, session string, paneName string, want bool, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var last PaneStatus
	found := false
	for time.Now().Before(deadline) {
		panes, err := Status(session)
		if err != nil {
			if isTmuxPermissionError(err) {
				t.Skipf("tmux integration unavailable in this environment: %v", err)
			}
			t.Fatalf("Status returned error: %v", err)
		}
		for _, pane := range panes {
			if pane.Name == paneName {
				last = pane
				found = true
				if pane.Dead == want {
					return
				}
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !found {
		t.Fatalf("pane %q not found in session %q", paneName, session)
	}
	t.Fatalf("pane %q dead = %t, want %t", paneName, last.Dead, want)
}

func waitForCaptureContains(t *testing.T, session string, paneName string, tailLines int, want string) string {
	t.Helper()

	return waitForCaptureContainsOptions(t, session, paneName, tailLines, false, want)
}

func waitForCaptureContainsOptions(t *testing.T, session string, paneName string, tailLines int, stripEmpty bool, want string) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		out, err := CapturePaneByName(session, paneName, tailLines, stripEmpty, false)
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
func TestNonDestructiveReloadReconcilesPanes(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-reconcile.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg1 := resolve.Config{
		Session: "agentmux-reconcile",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "p1",
						SourceType: "command",
						Command:    "printf 'unique-history-123\\n' && sleep 3600",
						Title:      "Original Title",
						OnExit:     config.OnExitKill,
					},
					{
						Name:       "p2",
						SourceType: "command",
						Command:    "echo $MY_VAR && sleep 3600",
						Env:        map[string]string{"MY_VAR": "v1"},
						Title:      "P2",
						OnExit:     config.OnExitKill,
					},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg1, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	panes1, err := Status(cfg1.Session)
	if err != nil {
		t.Fatalf("Status after Execute: %v", err)
	}
	if len(panes1) != 2 {
		t.Fatalf("Expected 2 panes, got %d", len(panes1))
	}

	// wait for output in pane 1
	waitForPaneContains(t, panes1[0].PaneID, "unique-history-123")

	// Change config
	cfg2 := resolve.Config{
		Session: "agentmux-reconcile",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "p1",
						SourceType: "command",
						Command:    "printf 'unique-history-123\\n' && sleep 3600",
						Title:      "New Title",
						OnExit:     config.OnExitKill,
					},
					{
						Name:       "p2",
						SourceType: "command",
						Command:    "echo $MY_VAR && sleep 3600",
						Env:        map[string]string{"MY_VAR": "v2"},
						Title:      "P2",
						OnExit:     config.OnExitKill,
					},
				},
			},
		},
	}

	if err := Reconcile(cfg2, Options{Verbose: true}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	panes, err := Status(cfg2.Session)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}

	// Verify p1
	if panes[0].Title != "New Title" {
		t.Fatalf("pane 1 title not updated, got %q", panes[0].Title)
	}
	out1 := tmuxOutput(t, "capture-pane", "-J", "-p", "-t", panes[0].PaneID)
	if !strings.Contains(out1, "unique-history-123") {
		t.Fatalf("pane 1 history lost, respawned unexpectedly")
	}

	// Verify p2
	if !strings.Contains(panes[1].StartupCmd, "MY_VAR='v2'") {
		t.Fatalf("pane 2 startup cmd not updated, got %q", panes[1].StartupCmd)
	}
}
func TestReconcileSelfRespawnIsDeferred(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-self.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg1 := resolve.Config{
		Session: "agentmux-self",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "p1",
						SourceType: "command",
						Command:    "printf 'p1-v1\\n' && sleep 3600",
						Title:      "P1",
						OnExit:     config.OnExitKill,
					},
					{
						Name:       "p2",
						SourceType: "command",
						Command:    "printf 'p2-v1\\n' && sleep 3600",
						Title:      "P2",
						OnExit:     config.OnExitKill,
					},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg1, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	panes1, err := Status(cfg1.Session)
	if err != nil {
		t.Fatalf("Status after Execute: %v", err)
	}

	// wait for output in p2
	waitForPaneContains(t, panes1[1].PaneID, "p2-v1")

	// Change config: both p1 and p2 change
	cfg2 := resolve.Config{
		Session: "agentmux-self",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "p1",
						SourceType: "command",
						Command:    "printf 'p1-v2\\n' && sleep 3600",
						Title:      "P1",
						OnExit:     config.OnExitKill,
					},
					{
						Name:       "p2",
						SourceType: "command",
						Command:    "printf 'p2-v2\\n' && sleep 3600",
						Title:      "P2",
						OnExit:     config.OnExitKill,
					},
				},
			},
		},
	}

	// Simulate running from inside p1
	t.Setenv("TMUX_PANE", panes1[0].PaneID)

	if err := Reconcile(cfg2, Options{Verbose: true}); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	// Verify p2 was respawned (p2-v2)
	waitForPaneContains(t, panes1[1].PaneID, "p2-v2")

	// Verify p1 was respawned (p1-v2) - it should happen last but it should happen!
	waitForPaneContains(t, panes1[0].PaneID, "p1-v2")
}
func TestReconcileReturnsErrStructuralChangeOnWindowCountMismatch(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-structural.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg1 := resolve.Config{
		Session: "agentmux-structural",
		Windows: []resolve.Window{
			{Name: "w1", Panes: []resolve.Pane{{Name: "p1", Command: "sleep 3600"}}},
			{Name: "w2", Panes: []resolve.Pane{{Name: "p2", Command: "sleep 3600"}}},
		},
	}

	t.Cleanup(func() {
		_ = command("kill-server").Run()
	})

	if err := Execute(cfg1, Options{Replace: true}); err != nil {
		if isTmuxPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	cfg2 := resolve.Config{
		Session: "agentmux-structural",
		Windows: []resolve.Window{
			{Name: "w1", Panes: []resolve.Pane{{Name: "p1", Command: "sleep 3600"}}},
		},
	}

	err := Reconcile(cfg2, Options{})
	if !errors.Is(err, ErrStructuralChange) {
		t.Fatalf("Reconcile error = %v, want ErrStructuralChange", err)
	}
}
func TestStatusShowsExitCodeForDeadPanes(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-status-exit.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-status-exit",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "p1",
						SourceType: "command",
						Command:    "exit 42",
						OnExit:     config.OnExitRemain,
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

	// wait for pane to die
	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		panes, _ := Status(cfg.Session)
		if len(panes) > 0 && panes[0].Dead {
			if panes[0].ExitStatus != 42 {
				t.Fatalf("pane exit status = %d, want 42", panes[0].ExitStatus)
			}
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !found {
		t.Fatal("pane did not die or exit status not captured")
	}
}
func TestBlackboardKVStore(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-kv.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-kv",
		Windows: []resolve.Window{
			{Name: "main", Panes: []resolve.Pane{{Name: "p1", Command: "sleep 3600"}}},
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

	// 1. Set
	if err := SetKV(cfg.Session, "task", "coding", false); err != nil {
		t.Fatalf("SetKV failed: %v", err)
	}

	// 2. Get
	val, err := GetKV(cfg.Session, "task", false)
	if err != nil {
		t.Fatalf("GetKV failed: %v", err)
	}
	if val != "coding" {
		t.Fatalf("GetKV task = %q, want coding", val)
	}

	// 3. Set another with spaces
	if err := SetKV(cfg.Session, "target_file", "./src/main.go", false); err != nil {
		t.Fatalf("SetKV target_file failed: %v", err)
	}

	// 4. List
	kv, err := ListKV(cfg.Session, false)
	if err != nil {
		t.Fatalf("ListKV failed: %v", err)
	}
	if len(kv) != 2 {
		t.Fatalf("ListKV count = %d, want 2", len(kv))
	}
	if kv["task"] != "coding" || kv["target_file"] != "./src/main.go" {
		t.Fatalf("ListKV content mismatch: %v", kv)
	}

	// 5. Delete
	if err := DelKV(cfg.Session, "task", false); err != nil {
		t.Fatalf("DelKV failed: %v", err)
	}
	val, _ = GetKV(cfg.Session, "task", false)
	if val != "" {
		t.Fatalf("GetKV after DelKV returned %q, want empty", val)
	}
}
func TestWaitCondition(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-wait.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-wait",
		Windows: []resolve.Window{
			{Name: "main", Panes: []resolve.Pane{{Name: "p1", Command: "exec $SHELL"}}},
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

	// In a separate goroutine, send the "FINISHED" message after a short delay
	go func() {
		time.Sleep(1 * time.Second)
		_ = SendCommandToPaneByName(cfg.Session, "p1", "echo 'Work is FINISHED'", true, false)
	}()

	// Wait for the pattern
	match, err := WaitForPattern(cfg.Session, "p1", "FINISHED", 5*time.Second, WaitScopeOutput, false)
	if err != nil {
		t.Fatalf("WaitForPattern failed: %v", err)
	}
	if match != "FINISHED" {
		t.Fatalf("WaitForPattern matched %q, want FINISHED", match)
	}
}

func TestWaitTimeout(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-timeout.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-timeout",
		Windows: []resolve.Window{
			{Name: "main", Panes: []resolve.Pane{{Name: "p1", Command: "exec $SHELL"}}},
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

	// Wait for a pattern that will never appear with a short timeout
	_, err := WaitForPattern(cfg.Session, "p1", "NEVER", 1*time.Second, WaitScopeOutput, false)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout error message, got %q", err.Error())
	}
}

func TestWaitScopeOutputIgnoresQueuedInput(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-wait-output.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-wait-output",
		Windows: []resolve.Window{
			{Name: "main", Panes: []resolve.Pane{{Name: "p1", Command: "exec $SHELL"}}},
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

	if err := SendCommandToPaneByName(cfg.Session, "p1", "echo WAIT_FALSE_POSITIVE", false, false); err != nil {
		t.Fatalf("SendCommandToPaneByName returned error: %v", err)
	}

	if match, err := WaitForPattern(cfg.Session, "p1", "WAIT_FALSE_POSITIVE", 500*time.Millisecond, WaitScopeVisible, false); err != nil {
		t.Fatalf("WaitForPattern visible returned error: %v", err)
	} else if match != "WAIT_FALSE_POSITIVE" {
		t.Fatalf("visible match = %q, want %q", match, "WAIT_FALSE_POSITIVE")
	}

	if _, err := WaitForPattern(cfg.Session, "p1", "WAIT_FALSE_POSITIVE", 1*time.Second, WaitScopeOutput, false); err == nil {
		t.Fatal("WaitForPattern output returned nil error for queued-only text, want timeout")
	}
	target, err := findPaneByName(cfg.Session, "p1")
	if err != nil {
		t.Fatalf("findPaneByName returned error: %v", err)
	}
	if err := run(false, "send-keys", "-t", target.PaneID, "Enter"); err != nil {
		t.Fatalf("send Enter returned error: %v", err)
	}

	if match, err := WaitForPattern(cfg.Session, "p1", "WAIT_FALSE_POSITIVE", 5*time.Second, WaitScopeOutput, false); err != nil {
		t.Fatalf("WaitForPattern output after enter returned error: %v", err)
	} else if match != "WAIT_FALSE_POSITIVE" {
		t.Fatalf("output match = %q, want %q", match, "WAIT_FALSE_POSITIVE")
	}
}
func TestScaleAgent(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "agentmux-scale.sock")
	t.Setenv(envTmuxSocket, socket)

	cfg := resolve.Config{
		Session: "agentmux-scale",
		Agents: map[string]resolve.Agent{
			"worker": {
				Provider: "command",
				Command:  "sleep 3600",
				OnExit:   config.OnExitKill,
			},
		},
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{
						Name:       "worker",
						AgentName:  "worker",
						SourceType: "agent",
						Command:    "sleep 3600",
						OnExit:     config.OnExitKill,
					},
				},
				Layout: "even-horizontal",
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

	// 1. Scale Up to 3
	if err := ScaleAgent(cfg.Session, "worker", 3, "", cfg, Options{Verbose: true}); err != nil {
		t.Fatalf("ScaleAgent UP failed: %v", err)
	}

	panes, err := Status(cfg.Session)
	if err != nil {
		t.Fatalf("Status after scale up: %v", err)
	}

	workerPanes := 0
	names := make(map[string]bool)
	for _, p := range panes {
		if p.AgentName == "worker" {
			workerPanes++
			if p.Name != "" {
				names[p.Name] = true
			}
		}
	}

	if workerPanes != 3 {
		t.Fatalf("Expected 3 worker panes, got %d", workerPanes)
	}
	if !names["worker"] || !names["worker-2"] || !names["worker-3"] {
		t.Fatalf("Expected pane names worker, worker-2, worker-3, got %v", names)
	}

	// 2. Scale Down to 1
	if err := ScaleAgent(cfg.Session, "worker", 1, "", cfg, Options{Verbose: true}); err != nil {
		t.Fatalf("ScaleAgent DOWN failed: %v", err)
	}

	panes, err = Status(cfg.Session)
	if err != nil {
		t.Fatalf("Status after scale down: %v", err)
	}

	workerPanes = 0
	for _, p := range panes {
		if p.AgentName == "worker" {
			workerPanes++
		}
	}

	if workerPanes != 1 {
		t.Fatalf("Expected 1 worker pane, got %d", workerPanes)
	}
}
