package managedlaunch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func relaunchNativeFixture(t *testing.T, sandbox string) (*Launcher, *managed.Store, *managed.Client, Runtime, string) {
	t.Helper()
	root, err := os.MkdirTemp("", "amx-relaunch-override-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root); os.Remove(root + ".launch.lock") })
	state := filepath.Join(root, "s")
	selected := filepath.Join(root, "public launcher ' $(literal)")
	if err := os.Symlink("/bin/true", selected); err != nil {
		t.Fatal(err)
	}
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "relaunch-override", InteractiveWorkspace: true, Agents: map[string]managedconfig.Agent{
		"native": {Harness: "command", Interaction: "interactive", Sandbox: sandbox, Command: []string{filepath.Join(root, "removed-old-version"), "--owner-argument"}},
		"peer":   {Harness: "command", Interaction: "interactive", Command: []string{"/bin/true"}},
	}}
	store, err := managed.Init(state, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan error, 1)
	go func() { done <- managed.Serve(ctx, store) }()
	t.Cleanup(func() { cancel(); <-done; store.Close() })
	credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		t.Fatal(err)
	}
	client := managed.NewClient(credential)
	for {
		if _, problem := client.Call(ctx, managed.Request{Operation: "identity"}); problem == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("fixture broker did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	l, err := New(Options{StateDir: state, Executable: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{Version: 1, RelaunchVersion: 1, NativeLauncherVersion: 1, AttemptID: "run_override", Session: cfg.Session, State: "running", Agents: map[string]AgentPane{
		"native": {PaneID: "%3", State: "exited"}, "peer": {PaneID: "%4", State: "running"},
	}}
	if err := writeRuntime(state, runtime); err != nil {
		t.Fatal(err)
	}
	l.run = func(_ context.Context, args ...string) (string, error) {
		switch args[4] {
		case "show-options":
			return runtime.AttemptID, nil
		case "list-panes":
			return "%3\t1\trun_override\tworker\tnative\n%4\t0\trun_override\tworker\tpeer\n", nil
		}
		return "", nil
	}
	return l, store, client, runtime, selected
}

func TestNativeLauncherRelaunchOwnerQueueAndSupervisorPreserveDefinition(t *testing.T) {
	l, store, client, runtime, selected := relaunchNativeFixture(t, "local")
	before, err := store.Agent("native")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var respawns []string
	baseRun := l.run
	l.run = func(ctx context.Context, args ...string) (string, error) {
		if args[4] == "respawn-pane" {
			mu.Lock()
			respawns = append(respawns, strings.Join(args[5:], "\n"))
			mu.Unlock()
		}
		return baseRun(ctx, args...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		for {
			if _, err := os.Stat(relaunchPath(l.options.StateDir, "native")); err == nil {
				done <- l.processRelaunches(ctx, &runtime, client)
				return
			}
			if ctx.Err() != nil {
				done <- ctx.Err()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	current, err := l.RelaunchWithLauncher(ctx, "native", "", selected)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	expected := "-t\n%3\nexec " + ShellCommand([]string{l.options.Executable, "managed", "worker", "--state", l.options.StateDir, "--agent", "native", "--native-launcher", selected})
	mu.Lock()
	got := append([]string(nil), respawns...)
	mu.Unlock()
	if len(got) != 1 || got[0] != expected {
		t.Fatalf("recovery did not preserve one literal launcher argument or tried to kill a pane: %q", got)
	}
	if current.AttemptID != "run_override" || current.Agents["native"].Launches != 1 || current.Agents["peer"].PaneID != "%4" {
		t.Fatalf("recovery changed peer/attempt identity: %+v", current)
	}
	after, err := store.Agent("native")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("owner relaunch rewrote persisted configuration: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestNativeLauncherRelaunchRejectsOldControllerWithoutQueuing(t *testing.T) {
	l, _, _, runtime, selected := relaunchNativeFixture(t, "local")
	runtime.NativeLauncherVersion = 0
	if err := writeRuntime(l.options.StateDir, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := l.RelaunchWithLauncher(context.Background(), "native", "", selected); err == nil || !strings.Contains(err.Error(), "does not support native launcher recovery") {
		t.Fatalf("old controller accepted an override it could ignore: %v", err)
	}
	if _, err := os.Stat(relaunchPath(l.options.StateDir, "native")); !os.IsNotExist(err) {
		t.Fatalf("old-controller rejection still queued a relaunch: %v", err)
	}
}

func TestNativeLauncherRelaunchRechecksSupervisorInputs(t *testing.T) {
	for _, scenario := range []string{"isolated", "removed-executable", "old-controller"} {
		t.Run(scenario, func(t *testing.T) {
			sandbox := "local"
			if scenario == "isolated" {
				sandbox = "bubblewrap"
			}
			l, _, client, runtime, selected := relaunchNativeFixture(t, sandbox)
			if scenario == "isolated" {
				if _, err := l.RelaunchWithLauncher(context.Background(), "native", "", selected); err == nil || !strings.Contains(err.Error(), "trusted-local") {
					t.Fatalf("owner queued an isolated override: %v", err)
				}
			}
			request := relaunchRequest{ID: "request_recheck", Agent: "native", Attempt: runtime.AttemptID, Pane: "%3", NativeLauncher: selected}
			if err := writePrivateJSON(relaunchPath(l.options.StateDir, "native"), request); err != nil {
				t.Fatal(err)
			}
			if scenario == "removed-executable" {
				if err := os.Remove(selected); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "old-controller" {
				runtime.NativeLauncherVersion = 0
			}
			baseRun := l.run
			l.run = func(ctx context.Context, args ...string) (string, error) {
				if args[4] == "respawn-pane" || args[4] == "new-window" {
					t.Fatal("supervisor started a process from an invalid override")
				}
				return baseRun(ctx, args...)
			}
			if err := l.processRelaunches(context.Background(), &runtime, client); err != nil {
				t.Fatal(err)
			}
			data, err := readPrivateFile(relaunchPath(l.options.StateDir, "native") + ".result")
			var result relaunchResult
			if err != nil || json.Unmarshal(data, &result) != nil || result.Error == "" || result.Pane != "" {
				t.Fatalf("invalid override did not leave a clear refusal: result=%+v err=%v", result, err)
			}
		})
	}
}

func TestNativeLauncherMissingPaneRelaunchSyncsNewWindowAfterManifest(t *testing.T) {
	l, _, client, runtime, selected := relaunchNativeFixture(t, "local")
	request := relaunchRequest{ID: "request_missing", Agent: "native", Attempt: runtime.AttemptID, Pane: "%3", NativeLauncher: selected}
	if err := writePrivateJSON(relaunchPath(l.options.StateDir, "native"), request); err != nil {
		t.Fatal(err)
	}
	synced := false
	l.run = func(_ context.Context, args ...string) (string, error) {
		switch args[4] {
		case "show-options":
			return runtime.AttemptID, nil
		case "list-panes":
			if strings.Contains(args[len(args)-1], "#{window_id}") {
				saved, err := Snapshot(l.options.StateDir)
				if err != nil || saved.Agents["native"].PaneID != "%8" {
					t.Fatal("new window was synced before the manifest recorded its identity")
				}
				return "%8\t@8\trun_override\tworker\tnative\n%4\t@4\trun_override\tworker\tpeer\n", nil
			}
			return "%4\t0\trun_override\tworker\tpeer\n", nil
		case "new-window":
			return "%8", nil
		case "list-sessions":
			return "private_view\trun_override\n", nil
		case "list-windows":
			return "@1\t0\n@4\t1\n", nil
		case "link-window":
			if strings.Join(args[5:], " ") == "-d -s @8 -t private_view:" {
				synced = true
			}
		}
		return "", nil
	}
	if err := l.processRelaunches(context.Background(), &runtime, client); err != nil {
		t.Fatal(err)
	}
	if runtime.Agents["native"].PaneID != "%8" || !synced {
		t.Fatalf("missing-pane recovery did not link the replacement window into the owned view: %+v synced=%v", runtime, synced)
	}
}
