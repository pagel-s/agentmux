//go:build unix

package managedlaunch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func TestNativeWorkspaceBrokerRecoveryPreservesTerminalAndUncertainTask(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("requires explicit disposable tmux integration")
	}
	executable := os.Getenv("AGENTMUX_TEST_BINARY")
	if executable == "" {
		executable = "../../bin/amx"
	}
	executable, _ = filepath.Abs(executable)
	root, err := os.MkdirTemp("", "amx-native-broker-")
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "s")
	l, err := New(Options{StateDir: state, Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if _, err := l.Down(ctx); err != nil {
			t.Error(err)
			return
		}
		_ = os.RemoveAll(root)
	})
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "native-broker-recovery", InteractiveWorkspace: true, Agents: map[string]managedconfig.Agent{}}
	for _, id := range []string{"first", "peer"} {
		cfg.Agents[id] = managedconfig.Agent{Harness: "command", Interaction: "interactive", Sandbox: "local", Network: "host", Command: []string{"/bin/sh", "-c", "printf 'NATIVE_READY\\n'; while IFS= read -r line; do printf 'INPUT:%s\\n' \"$line\"; done"}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	runtime, err := l.Up(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(label string, check func() bool) {
		t.Helper()
		end := time.Now().Add(12 * time.Second)
		for time.Now().Before(end) {
			if check() {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
		snapshot, _ := Snapshot(state)
		supervisor, _ := l.tmux(ctx, "capture-pane", "-p", "-t", runtime.SupervisorPane, "-S", "-30")
		t.Fatalf("timed out %s: %+v; supervisor: %s", label, snapshot, supervisor)
	}
	owner, _ := managed.LoadCredential(managed.CredentialPath(state, ""))
	ownerClient := managed.NewClient(owner)
	agent, _ := managed.LoadCredential(managed.CredentialPath(state, "first"))
	agentClient := managed.NewClient(agent)
	call := func(client *managed.Client, op string, args any) json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(args)
		out, problem := client.Call(ctx, managed.Request{Operation: op, RequestID: fmt.Sprintf("broker-recovery-%d", time.Now().UnixNano()), Args: raw})
		if problem != nil {
			t.Fatal(problem)
		}
		return out
	}
	var task struct {
		Task managed.Task `json:"task"`
	}
	if err := json.Unmarshal(call(ownerClient, "instruct", map[string]any{"to": []string{"first"}, "text": "Uncertain coordination fixture"}), &task); err != nil {
		t.Fatal(err)
	}
	call(agentClient, "task.claim", map[string]any{})
	pids := map[string]string{}
	for id, pane := range runtime.Agents {
		out, err := l.tmux(ctx, "display-message", "-p", "-t", pane.PaneID, "#{pane_pid}")
		if err != nil {
			t.Fatal(err)
		}
		pids[id] = strings.TrimSpace(out)
	}
	pidText, err := l.tmux(ctx, "display-message", "-p", "-t", runtime.BrokerPane, "#{pane_pid}")
	if err != nil {
		t.Fatal(err)
	}
	brokerPID, err := strconv.Atoi(strings.TrimSpace(pidText))
	if err != nil {
		t.Fatal(err)
	}
	// Temporarily hide only this fixture's socket. tmux resumes stopped pane
	// processes itself, so SIGSTOP cannot reliably simulate registry outages.
	brokerSocket := managed.SocketPath(state)
	pausedSocket := brokerSocket + ".unavailable"
	if err := os.Rename(brokerSocket, pausedSocket); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(pausedSocket, brokerSocket) })
	wait("registry outage attention", func() bool { snapshot, _ := Snapshot(state); return snapshot.State == "attention" })
	_, _ = l.tmux(ctx, "send-keys", "-t", runtime.Agents["peer"].PaneID, "-l", "WHILE_BROKER_PAUSED")
	_, _ = l.tmux(ctx, "send-keys", "-t", runtime.Agents["peer"].PaneID, "Enter")
	wait("native input while registry offline", func() bool {
		out, _ := l.tmux(ctx, "capture-pane", "-p", "-t", runtime.Agents["peer"].PaneID)
		return strings.Contains(out, "INPUT:WHILE_BROKER_PAUSED")
	})
	if err := os.Rename(pausedSocket, brokerSocket); err != nil {
		t.Fatal(err)
	}
	wait("registry recovered without broker replacement", func() bool {
		snapshot, _ := Snapshot(state)
		out, _ := l.tmux(ctx, "display-message", "-p", "-t", runtime.BrokerPane, "#{pane_pid}")
		return snapshot.State == "running" && strings.TrimSpace(out) == strings.TrimSpace(pidText)
	})
	if err := syscall.Kill(brokerPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	wait("only broker restarted", func() bool {
		snapshot, _ := Snapshot(state)
		if snapshot.State != "running" || snapshot.BrokerRecovering || snapshot.BrokerPane != runtime.BrokerPane {
			return false
		}
		out, _ := l.tmux(ctx, "display-message", "-p", "-t", runtime.BrokerPane, "#{pane_pid}")
		if strings.TrimSpace(out) == strings.TrimSpace(pidText) {
			return false
		}
		probe, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		_, problem := ownerClient.Call(probe, managed.Request{Operation: "identity"})
		return problem == nil
	})
	var recovered struct {
		Task managed.Task `json:"task"`
	}
	if err := json.Unmarshal(call(ownerClient, "task.get", map[string]any{"id": task.Task.ID}), &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Task.Delivery != "uncertain" || recovered.Task.State != "running" {
		t.Fatalf("broker replayed or lost task after restart: %+v", recovered.Task)
	}
	call(ownerClient, "task.resolve", map[string]any{"id": task.Task.ID, "version": recovered.Task.Version, "resolution": "fail"})
	for id, pane := range runtime.Agents {
		out, err := l.tmux(ctx, "display-message", "-p", "-t", pane.PaneID, "#{pane_pid}\t#{pane_dead}")
		if err != nil || strings.TrimSpace(out) != pids[id]+"\t0" {
			t.Fatalf("native worker changed across broker recovery: %s %q %v", id, out, err)
		}
	}
	_, _ = l.tmux(ctx, "send-keys", "-t", runtime.Agents["peer"].PaneID, "-l", "AFTER_BROKER_RECOVERY")
	_, _ = l.tmux(ctx, "send-keys", "-t", runtime.Agents["peer"].PaneID, "Enter")
	wait("native input after recovery", func() bool {
		out, _ := l.tmux(ctx, "capture-pane", "-p", "-t", runtime.Agents["peer"].PaneID)
		return strings.Contains(out, "INPUT:AFTER_BROKER_RECOVERY")
	})
}
