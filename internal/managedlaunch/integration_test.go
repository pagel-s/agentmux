package managedlaunch

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

// Opt in explicitly: this starts a dedicated tmux server and local /bin/cat
// workers, with no model calls or modification of the default tmux server.
func TestManagedLaunchRealTMuxLifecycle(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_MANAGED_INTEGRATION=1 for the real tmux lifecycle test")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatal(err)
	}
	executable := os.Getenv("AGENTMUX_TEST_BINARY")
	if executable == "" {
		executable = filepath.Join("..", "..", "bin", "agentmux")
	}
	executable, err := filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatal("build bin/agentmux or set AGENTMUX_TEST_BINARY before this opt-in test")
	}
	state, err := os.MkdirTemp("", "amx-launch-")
	if err != nil {
		t.Fatal(err)
	}
	// Init expects an empty directory; the lifecycle lock is adjacent to it.
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "managed-launch-test", Agents: map[string]managedconfig.Agent{"worker": {Harness: "command", Command: []string{"/bin/cat"}}}, Limits: managedconfig.Limits{MaxRuntimeSeconds: 60}}.Normalized()
	launcher, err := New(Options{StateDir: state, Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if _, err := launcher.Down(cleanup); err != nil {
			t.Errorf("cleanup: %v; private state retained at %s", err, state)
			return
		}
		_ = os.RemoveAll(state)
		_ = os.Remove(state + ".launch.lock")
	})
	runtime, err := launcher.Up(ctx, cfg)
	if err != nil {
		t.Fatalf("up: %v; runtime %+v", err, runtime)
	}
	if runtime.State != "running" || len(runtime.Agents) != 1 || runtime.BrokerPane == "" || runtime.DashboardPane == "" {
		t.Fatalf("incomplete runtime: %+v", runtime)
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		t.Fatal(err)
	}
	client := managed.NewClient(credential)
	args, _ := json.Marshal(map[string]any{"to": []string{"worker"}, "text": "literal lifecycle handoff"})
	if _, problem := client.Call(ctx, managed.Request{Operation: "instruct", RequestID: "lifecycle-task", Args: args}); problem != nil {
		t.Fatal(problem)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, problem := client.Call(ctx, managed.Request{Operation: "task.list", Args: json.RawMessage(`{}`)})
		if problem != nil {
			t.Fatal(problem)
		}
		var page struct {
			Tasks []managed.Task `json:"tasks"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Tasks) == 1 && page.Tasks[0].State == "submitted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker did not submit task: %s", raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
	firstAttempt := runtime.AttemptID
	runtime, err = launcher.Down(ctx)
	if err != nil || runtime.State != "stopped" {
		t.Fatalf("down: %+v %v", runtime, err)
	}
	if _, err := launcher.tmux(ctx, "has-session", "-t", cfg.Session); err == nil {
		t.Fatal("owned session survived down")
	}
	if _, err := launcher.Down(ctx); err != nil {
		t.Fatalf("idempotent down: %v", err)
	}
	runtime, err = launcher.Up(ctx, cfg)
	if err != nil || runtime.AttemptID == firstAttempt {
		t.Fatalf("restart attempt: %+v %v", runtime, err)
	}
	raw, problem := client.Call(ctx, managed.Request{Operation: "task.list", Args: json.RawMessage(`{}`)})
	if problem != nil {
		t.Fatal(problem)
	}
	var page struct {
		Tasks []managed.Task `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Tasks) != 1 || page.Tasks[0].State != "submitted" {
		t.Fatalf("restart altered completed work: %s", raw)
	}
}

func TestManagedLaunchSpawnedSandboxWorker(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_MANAGED_INTEGRATION=1 for real tmux/sandbox launch")
	}
	if os.Getenv("AGENTMUX_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_SANDBOX_INTEGRATION=1 on a host with working bubblewrap namespaces")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is unavailable")
	}
	executable := os.Getenv("AGENTMUX_TEST_BINARY")
	if executable == "" {
		executable = filepath.Join("..", "..", "bin", "agentmux")
	}
	executable, err := filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.MkdirTemp("", "amx-spawn-")
	if err != nil {
		t.Fatal(err)
	}
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "managed-spawn-test", Agents: map[string]managedconfig.Agent{"lead": {Harness: "command", Command: []string{"/bin/cat"}}}, Spawn: managedconfig.Spawn{Enabled: true, Templates: map[string]managedconfig.Agent{"helper": {Harness: "command", Command: []string{"/bin/cat"}, Sandbox: "bubblewrap", Network: "none"}}}, Limits: managedconfig.Limits{MaxRuntimeSeconds: 60}}.Normalized()
	launcher, err := New(Options{StateDir: state, Executable: executable})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if _, err := launcher.Down(ctx); err != nil {
			t.Errorf("cleanup: %v; private state retained at %s", err, state)
			return
		}
		_ = os.RemoveAll(state)
		_ = os.Remove(state + ".launch.lock")
	})
	if _, err := launcher.Up(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		t.Fatal(err)
	}
	client := managed.NewClient(credential)
	raw, problem := client.Call(ctx, managed.Request{Operation: "agent.spawn", RequestID: "spawned-worker", Args: json.RawMessage(`{"template":"helper"}`)})
	if problem != nil {
		t.Fatal(problem)
	}
	var spawned struct {
		Agent struct {
			ID string `json:"id"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(raw, &spawned); err != nil || spawned.Agent.ID == "" {
		t.Fatalf("invalid spawn response: %s, %v", raw, err)
	}
	args, _ := json.Marshal(map[string]any{"to": []string{spawned.Agent.ID}, "text": "isolated dynamic worker handoff"})
	if _, problem := client.Call(ctx, managed.Request{Operation: "instruct", RequestID: "spawned-task", Args: args}); problem != nil {
		t.Fatal(problem)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		raw, problem := client.Call(ctx, managed.Request{Operation: "task.list", Args: json.RawMessage(`{}`)})
		if problem != nil {
			t.Fatal(problem)
		}
		var page struct {
			Tasks []managed.Task `json:"tasks"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Tasks) == 1 && page.Tasks[0].State == "submitted" {
			break
		}
		if time.Now().After(deadline) {
			if panes, err := launcher.tmux(ctx, "list-panes", "-a", "-F", "#{pane_id}"); err == nil {
				for _, pane := range strings.Fields(panes) {
					if output, err := launcher.tmux(ctx, "capture-pane", "-p", "-t", pane, "-S", "-25"); err == nil {
						t.Logf("test pane %s: %s", pane, output)
					}
				}
			}
			t.Fatalf("spawned sandbox worker did not submit: %s", raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
	runtime, err := Snapshot(state)
	if err != nil {
		t.Fatal(err)
	}
	if pane, ok := runtime.Agents[spawned.Agent.ID]; !ok || pane.PaneID == "" {
		t.Fatal("supervisor did not track spawned worker pane")
	}
	args, _ = json.Marshal(map[string]string{"id": spawned.Agent.ID})
	if _, problem := client.Call(ctx, managed.Request{Operation: "agent.stop", RequestID: "stop-child", Args: args}); problem != nil {
		t.Fatal(problem)
	}
	for {
		runtime, err := launcher.Describe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if pane := runtime.Agents[spawned.Agent.ID]; pane.State == "exited" || pane.State == "stopped" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("revoked worker did not stop")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
