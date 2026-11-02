package managedlaunch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

// This is an actual public CLI -> owner request -> supervisor -> native worker
// acceptance. The native executables are shell fixtures; no provider is called.
func TestNativeLauncherPublicCLIRecoversRemovedVersionWithoutRestartingPeer(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_MANAGED_INTEGRATION=1 for real tmux native recovery")
	}
	exe := os.Getenv("AGENTMUX_TEST_BINARY")
	if exe == "" {
		exe = "../../bin/amx"
	}
	exe, _ = filepath.Abs(exe)
	root, err := os.MkdirTemp("", "amx-native-updater-")
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "s")
	launcher, err := New(Options{StateDir: state, Executable: exe})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean, stop := context.WithTimeout(context.Background(), 25*time.Second)
		defer stop()
		if _, err := launcher.Down(clean); err != nil {
			t.Error(err)
			return
		}
		os.RemoveAll(root)
	})
	old, updated, public := filepath.Join(root, "native-v1"), filepath.Join(root, "native-v2"), filepath.Join(root, "public native ' launcher")
	for path, program := range map[string]string{
		old:     "#!/bin/sh\nprintf 'UPDATER_CLOSED_NATIVE\\n'\nexit 0\n",
		updated: "#!/bin/sh\nprintf 'UPDATED_NATIVE_READY\\nARGS:%s\\n' \"$*\"\nwhile IFS= read -r line; do printf 'INPUT:%s\\n' \"$line\"; done\n",
	} {
		if err := os.WriteFile(path, []byte(program), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(old, public); err != nil {
		t.Fatal(err)
	}
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "native-updater-recovery", InteractiveWorkspace: true, Agents: map[string]managedconfig.Agent{
		"native": {Harness: "command", Interaction: "interactive", Sandbox: "local", Network: "host", Command: []string{old, "--owner-prefix", "value with spaces"}},
		"peer":   {Harness: "command", Interaction: "interactive", Sandbox: "local", Network: "host", Command: []string{"/bin/sh", "-c", "printf 'PEER_READY\\n'; while IFS= read -r line; do printf 'PEER:%s\\n' \"$line\"; done"}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	runtime, err := launcher.Up(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(label string, check func() bool) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if check() {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatal("timed out: " + label)
	}
	wait("native update exit", func() bool {
		current, err := launcher.Describe(ctx)
		return err == nil && current.Agents["native"].State == "exited"
	})
	wait("peer is interactive", func() bool {
		text, _ := launcher.tmux(ctx, "capture-pane", "-p", "-t", runtime.Agents["peer"].PaneID)
		return strings.Contains(text, "PEER_READY")
	})
	exitText, err := launcher.tmux(ctx, "capture-pane", "-p", "-t", runtime.Agents["native"].PaneID)
	if err != nil || !strings.Contains(exitText, "exited normally (exit 0)") || !strings.Contains(exitText, "F1") || strings.Contains(exitText, `"schema_version"`) {
		t.Fatalf("native exit was not readable: err=%v text=%s", err, exitText)
	}
	peerPID, err := launcher.tmux(ctx, "display-message", "-p", "-t", runtime.Agents["peer"].PaneID, "#{pane_pid}")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(public); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(updated, public); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, exe, "relaunch", "native", "--state", state, "--native-launcher", public)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "saved definition is unchanged") {
		t.Fatalf("public launcher recovery failed: err=%v output=%s", err, output)
	}
	current, err := Snapshot(state)
	if err != nil || current.AttemptID != runtime.AttemptID || current.Agents["native"].Launches != 1 || current.Agents["peer"].PaneID != runtime.Agents["peer"].PaneID {
		t.Fatalf("public recovery changed unrelated lifecycle state: %+v err=%v", current, err)
	}
	wait("updated native remains interactive with owner arguments", func() bool {
		text, _ := launcher.tmux(ctx, "capture-pane", "-p", "-t", current.Agents["native"].PaneID)
		return strings.Contains(text, "UPDATED_NATIVE_READY") && strings.Contains(text, "ARGS:--owner-prefix value with spaces")
	})
	afterPID, err := launcher.tmux(ctx, "display-message", "-p", "-t", runtime.Agents["peer"].PaneID, "#{pane_pid}")
	if err != nil || afterPID != peerPID {
		t.Fatal("public recovery restarted the peer process")
	}
	store, err := managed.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	agent, err := store.Agent("native")
	if err != nil || !reflect.DeepEqual(agent.Command, cfg.Agents["native"].Command) {
		t.Fatalf("public recovery rewrote the saved old definition: %+v err=%v", agent, err)
	}
}
