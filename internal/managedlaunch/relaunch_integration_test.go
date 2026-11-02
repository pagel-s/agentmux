package managedlaunch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
)

func TestIndividualNativeRelaunchPreservesPeerAndAttempt(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("requires explicit real tmux opt-in")
	}
	exe := os.Getenv("AGENTMUX_TEST_BINARY")
	if exe == "" {
		exe = "../../bin/amx"
	}
	exe, _ = filepath.Abs(exe)
	state, err := os.MkdirTemp("", "amx-relaunch-")
	if err != nil {
		t.Fatal(err)
	}
	launcher, err := New(Options{StateDir: state, Executable: exe})
	if err != nil {
		t.Fatal(err)
	}
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "individual-relaunch", InteractiveWorkspace: true, Agents: map[string]managedconfig.Agent{}}
	for _, id := range []string{"first", "peer"} {
		cfg.Agents[id] = managedconfig.Agent{Harness: "command", Interaction: "interactive", Sandbox: "local", Network: "host", Command: []string{"/bin/sh", "-c", "printf 'NATIVE_READY\\n'; while IFS= read -r line; do printf 'INPUT:%s\\n' \"$line\"; done"}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	t.Cleanup(func() {
		clean, stop := context.WithTimeout(context.Background(), 25*time.Second)
		defer stop()
		if _, err := launcher.Down(clean); err != nil {
			t.Error(err)
			return
		}
		_ = os.RemoveAll(state)
		_ = os.RemoveAll(state + ".bin")
		_ = os.Remove(state + ".launch.lock")
	})
	runtime, err := launcher.Up(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(7 * time.Second)
		for time.Now().Before(deadline) {
			if fn() {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
		t.Fatal("terminal condition not observed")
	}
	first := runtime.Agents["first"].PaneID
	peer := runtime.Agents["peer"].PaneID
	wait(func() bool {
		text, _ := launcher.tmux(ctx, "capture-pane", "-p", "-t", first)
		return strings.Contains(text, "NATIVE_READY")
	})
	peerPID, err := launcher.tmux(ctx, "display-message", "-p", "-t", peer, "#{pane_pid}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := launcher.Relaunch(ctx, "peer", ""); err == nil {
		t.Fatal("live native session restarted")
	}
	if _, err := launcher.tmux(ctx, "send-keys", "-t", first, "C-d"); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { r, _ := launcher.Describe(ctx); return r.Agents["first"].State == "exited" })
	resumed, err := launcher.Relaunch(ctx, "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.AttemptID != runtime.AttemptID || resumed.Agents["first"].Launches != 1 || resumed.Agents["peer"].PaneID != peer {
		t.Fatalf("unrelated lifecycle changed: %+v", resumed)
	}
	afterPID, err := launcher.tmux(ctx, "display-message", "-p", "-t", peer, "#{pane_pid}")
	if err != nil || afterPID != peerPID {
		t.Fatal("peer process was restarted")
	}
	wait(func() bool {
		text, _ := launcher.tmux(ctx, "capture-pane", "-p", "-t", resumed.Agents["first"].PaneID)
		return strings.Contains(text, "NATIVE_READY")
	})
	if _, err := launcher.tmux(ctx, "send-keys", "-t", peer, "-l", "still interactive"); err != nil {
		t.Fatal(err)
	}
	_, _ = launcher.tmux(ctx, "send-keys", "-t", peer, "Enter")
	wait(func() bool {
		text, _ := launcher.tmux(ctx, "capture-pane", "-p", "-t", peer)
		return strings.Contains(text, "INPUT:still interactive")
	})
}
