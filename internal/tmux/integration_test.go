package tmux

import (
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
					{Name: "checks", SourceType: "command", Command: "printf checks", Title: "Checks"},
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

	if err := FocusPaneByName(cfg.Session, "checks", false); err != nil {
		t.Fatalf("FocusPaneByName returned error: %v", err)
	}

	activeName := strings.TrimSpace(tmuxOutput(t, "display-message", "-p", "-t", cfg.Session+":main", "#{pane_id}\t#{@agentmux_name}"))
	if !strings.HasSuffix(activeName, "\tchecks") {
		t.Fatalf("active pane metadata = %q, want checks", activeName)
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

func isTmuxPermissionError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "Operation not permitted") || strings.Contains(message, "permission denied")
}
