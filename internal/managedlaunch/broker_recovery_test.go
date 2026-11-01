package managedlaunch

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStartupWaitsForExplicitNativeBrokerRecovery(t *testing.T) {
	for _, scenario := range []string{"native-recovery", "native-attention", "managed-attention"} {
		t.Run(scenario, func(t *testing.T) {
			state := t.TempDir()
			l := &Launcher{options: Options{StateDir: state}}
			runtime := Runtime{Version: 1, AttemptID: "run_owned", Session: "owned", State: "attention", BrokerRecovering: scenario != "native-attention", Agents: map[string]AgentPane{}}
			if err := writeRuntime(state, runtime); err != nil {
				t.Fatal(err)
			}
			if scenario == "native-recovery" {
				done := make(chan error, 1)
				go func(running Runtime) {
					time.Sleep(30 * time.Millisecond)
					running.State, running.BrokerRecovering = "running", false
					done <- writeRuntime(state, running)
				}(runtime)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				result, err := l.awaitStartup(ctx, runtime, true)
				if e := <-done; e != nil {
					t.Fatal(e)
				}
				if err != nil || result.State != "running" || result.BrokerRecovering {
					t.Fatalf("recoverable startup was reported as failed: %+v %v", result, err)
				}
				return
			}
			_, err := l.awaitStartup(context.Background(), runtime, scenario != "managed-attention")
			if err == nil {
				t.Fatal("non-recoverable startup attention was ignored")
			}
		})
	}
}

func TestNativeStartupTimeoutCompletesOrderedOwnedShutdown(t *testing.T) {
	state := t.TempDir()
	l := &Launcher{options: Options{StateDir: state}}
	runtime := Runtime{Version: 1, AttemptID: "run_owned", Session: "owned", State: "attention", BrokerRecovering: true, SupervisorPane: "%0", BrokerPane: "%2", Agents: map[string]AgentPane{"first": {PaneID: "%1", State: "running"}}}
	if err := writeRuntime(state, runtime); err != nil {
		t.Fatal(err)
	}
	dead := map[string]bool{}
	var order []string
	l.run = func(_ context.Context, args ...string) (string, error) {
		switch args[4] {
		case "show-options":
			return "run_owned", nil
		case "list-panes":
			var rows []string
			for _, pane := range []struct{ id, role, agent string }{{"%0", "supervisor", ""}, {"%1", "worker", "first"}, {"%2", "broker", ""}} {
				stopped := "0"
				if dead[pane.id] {
					stopped = "1"
				}
				rows = append(rows, pane.id+"\t"+stopped+"\trun_owned\t"+pane.role+"\t"+pane.agent)
			}
			return strings.Join(rows, "\n"), nil
		case "send-keys":
			pane := args[6]
			dead[pane] = true
			order = append(order, pane)
		case "kill-session":
			order = append(order, "session")
		}
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	result, err := l.awaitStartup(ctx, runtime, true)
	if err == nil || result.State != "stopped" || result.BrokerRecovering {
		t.Fatalf("startup failure left a recovering runtime: %+v %v", result, err)
	}
	if !reflect.DeepEqual(order, []string{"%1", "%2", "%0", "session"}) {
		t.Fatalf("startup shutdown was not ordered: %v", order)
	}
	if !shouldStop(state, runtime.AttemptID) {
		t.Fatal("startup failure did not fence later supervisor work")
	}
}

func TestNativeBrokerRecoveryBackoffIsBounded(t *testing.T) {
	var recovery brokerRecovery
	now := time.Now()
	for _, delay := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second} {
		recovery.deferAttempt(now)
		if recovery.delay != delay || recovery.next.Sub(now) != delay {
			t.Fatalf("unbounded broker retry: %+v", recovery)
		}
	}
}

func TestNativeBrokerRespawnTouchesOnlyExactOwnedDeadPane(t *testing.T) {
	for _, scenario := range []string{"owned-dead", "live", "foreign-attempt", "worker-role", "shutdown"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			runtime := Runtime{Session: "owned", AttemptID: "run_owned", BrokerPane: "%2"}
			l := &Launcher{options: Options{StateDir: root}}
			var mutations [][]string
			l.run = func(_ context.Context, args ...string) (string, error) {
				if args[4] == "show-options" {
					return "run_owned", nil
				}
				mutations = append(mutations, append([]string(nil), args[4:]...))
				return "", nil
			}
			pane := paneInfo{ID: "%2", Attempt: "run_owned", Role: "broker", Dead: true}
			switch scenario {
			case "live":
				pane.Dead = false
			case "foreign-attempt":
				pane.Attempt = "foreign"
			case "worker-role":
				pane.Role = "worker"
			case "shutdown":
				if err := writeStop(root, runtime.AttemptID); err != nil {
					t.Fatal(err)
				}
			}
			argv := []string{"/pinned/controller ' literal", "managed", "serve", "--state", root}
			err := l.restartBrokerPane(context.Background(), &runtime, []paneInfo{pane}, argv)
			if scenario != "owned-dead" {
				if err == nil || len(mutations) > 0 {
					t.Fatalf("unsafe automatic restart for %s: %v %v", scenario, err, mutations)
				}
				return
			}
			want := [][]string{{"respawn-pane", "-t", "%2", "exec " + ShellCommand(argv)}}
			if err != nil || !reflect.DeepEqual(mutations, want) {
				t.Fatalf("broker recovery changed unrelated processes: %v %v", err, mutations)
			}
			if strings.Contains(strings.Join(mutations[0], " "), " -k ") {
				t.Fatal("recovery can kill a concurrently revived process")
			}
		})
	}
}

func TestNativeBrokerRecoveryRejectsForeignSession(t *testing.T) {
	l := &Launcher{options: Options{StateDir: t.TempDir()}}
	l.run = func(_ context.Context, args ...string) (string, error) {
		if args[4] != "show-options" {
			t.Fatal("mutated foreign session")
		}
		return "foreign", nil
	}
	if err := l.restartBrokerPane(context.Background(), &Runtime{Session: "owned", AttemptID: "run_owned"}, nil, []string{"/pinned/controller"}); err == nil {
		t.Fatal("restarted broker in foreign session")
	}
}
