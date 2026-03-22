package tmux

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
						SourceType: "command",
						Command:    "printf pane-one-ready",
						Cwd:        t.TempDir(),
						Title:      "Editor",
						Focus:      true,
					},
					{
						SourceType: "command",
						Command:    "printf pane-two-ready",
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

	captured := tmuxOutput(t, "capture-pane", "-pt", cfg.Session+":editor.1")
	if !strings.Contains(captured, "pane-one-ready") {
		t.Fatalf("capture-pane output %q does not include pane-one-ready", captured)
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

func isTmuxPermissionError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "Operation not permitted") || strings.Contains(message, "permission denied")
}
