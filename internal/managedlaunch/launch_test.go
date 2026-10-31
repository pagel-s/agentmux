package managedlaunch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestShellCommandKeepsPathsLiteral(t *testing.T) {
	argv := []string{"/tmp/bin's tool", "managed", "worker", "--state", "/tmp/space $(never-run) `literal`", "--agent", "worker"}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("shell is unavailable")
	}
	command := "set -- " + ShellCommand(argv) + "; printf '%s\\000' \"$@\""
	output, err := exec.Command(shell, "-c", command).Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	if !reflect.DeepEqual(got, argv) {
		t.Fatalf("argv changed: %q", got)
	}
}

func TestDedicatedTMuxCommandsAndStopOrder(t *testing.T) {
	var commands [][]string
	workerStopped, brokerStopped := false, false
	l := &Launcher{options: Options{StateDir: "/private/state"}}
	l.run = func(_ context.Context, args ...string) (string, error) {
		commands = append(commands, append([]string(nil), args...))
		if len(args) < 5 || !reflect.DeepEqual(args[:4], []string{"-S", "/private/state/tmux.sock", "-f", "/dev/null"}) {
			t.Fatalf("command can reach default tmux server: %q", args)
		}
		switch args[4] {
		case "show-options":
			return "run_owned\n", nil
		case "list-panes":
			worker, broker := "0", "0"
			if workerStopped {
				worker = "1"
			}
			if brokerStopped {
				broker = "1"
			}
			return "%1\t" + worker + "\trun_owned\tworker\tworker\n%2\t" + broker + "\trun_owned\tbroker\t\n%3\t0\trun_other\tworker\tforeign\n", nil
		case "send-keys":
			if args[6] == "%1" {
				workerStopped = true
			} else if args[6] == "%2" {
				if !workerStopped {
					t.Fatal("broker stopped before worker")
				}
				brokerStopped = true
			} else {
				t.Fatal("attempted to stop a foreign pane")
			}
		}
		return "", nil
	}
	if err := l.stopOwned(context.Background(), Runtime{AttemptID: "run_owned", Session: "team"}); err != nil {
		t.Fatal(err)
	}
	if !workerStopped || !brokerStopped {
		t.Fatal("owned processes were not stopped")
	}
	for _, command := range commands {
		if strings.Contains(strings.Join(command, " "), "kill-server") {
			t.Fatal("launcher may not kill a tmux server")
		}
	}
}

func TestRuntimeManifestPrivateAndStopRequestAttemptBound(t *testing.T) {
	state := t.TempDir()
	runtime := Runtime{Version: 1, AttemptID: "run_new", Session: "team", State: "starting", Agents: map[string]AgentPane{}}
	if err := writeRuntime(state, runtime); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(manifestPath(state))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("manifest mode: %v, %v", info, err)
	}
	if err := writeStop(state, "run_old"); err != nil {
		t.Fatal(err)
	}
	if shouldStop(state, "run_new") {
		t.Fatal("stale stop request stopped a new attempt")
	}
	if err := writeStop(state, "run_new"); err != nil || !shouldStop(state, "run_new") {
		t.Fatal("matching stop request not observed")
	}
	if err := os.Chmod(manifestPath(state), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Snapshot(state); err == nil {
		t.Fatal("accepted readable runtime metadata")
	}
}

func TestDownMissingStateIsIdempotent(t *testing.T) {
	l, err := New(Options{StateDir: filepath.Join(t.TempDir(), "missing"), Executable: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	l.run = func(context.Context, ...string) (string, error) {
		t.Fatal("down of absent state called tmux")
		return "", nil
	}
	runtime, err := l.Down(context.Background())
	if err != nil || runtime.State != "stopped" {
		t.Fatalf("down: %+v, %v", runtime, err)
	}
}
