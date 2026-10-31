//go:build linux

package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/harness"
)

func TestResourceLimitsValidateAndFailClosed(t *testing.T) {
	if !(Limits{}).Empty() || (Limits{}).Enabled() {
		t.Fatal("empty limits must preserve current behavior")
	}
	for _, limits := range []Limits{{MemoryBytes: -1}, {MemoryBytes: 1}, {MemoryBytes: 1 << 41}, {CPUPercent: -1}, {CPUPercent: 6401}, {Processes: 1}, {Processes: 65537}, {OpenFiles: 15}, {OpenFiles: 1048577}, {CPUSeconds: 86401}, {AddressSpaceBytes: 1}} {
		if err := limits.Validate(); err == nil {
			t.Fatalf("accepted invalid limits %+v", limits)
		}
	}
	f := setup(t)
	f.opts.Limits = Limits{MemoryBytes: 128 << 20, CPUPercent: 50, Processes: 64, OpenFiles: 128, CPUSeconds: 10}
	wrapped, err := Wrap(f.spec, f.opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"--scope", "--expand-environment=no", "--property=MemoryMax=134217728", "--property=MemorySwapMax=0", "--property=OOMPolicy=kill", "--property=TasksMax=64", "--property=CPUQuota=50%", "--nofile=128:128", "--cpu=10:10", "literal $(expression)", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
		if !hasArg(wrapped.Args, required) {
			t.Errorf("resource wrapper omitted %s", required)
		}
	}
	if filepath.Base(wrapped.Binary) != "systemd-run" || envMap(wrapped.Env)["XDG_RUNTIME_DIR"] == "" || envMap(wrapped.Env)["OWNER_ONLY_SECRET"] != "" {
		t.Fatal("resource launcher environment or executable is incorrect")
	}
	f.opts.Mode = "local"
	if _, err := Wrap(f.spec, f.opts); err == nil {
		t.Fatal("resources were accepted without the required PID namespace")
	}
	f.opts.Mode = "bubblewrap"
	t.Setenv("PATH", filepath.Join(f.root, "tools"))
	if _, err := Wrap(f.spec, f.opts); err == nil {
		t.Fatal("missing resource executables silently fell back")
	}
}

func realResourceFixture(t *testing.T) fixture {
	t.Helper()
	if os.Getenv("AGENTMUX_RESOURCE_INTEGRATION") != "1" || os.Getenv("AGENTMUX_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_RESOURCE_INTEGRATION=1 and AGENTMUX_SANDBOX_INTEGRATION=1 for real user cgroup tests")
	}
	f := setup(t)
	t.Setenv("PATH", "/usr/bin:/bin")
	f.opts.Limits = Limits{MemoryBytes: 128 << 20, CPUPercent: 100, Processes: 64, OpenFiles: 256}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Probe(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ProbeLimits(ctx, f.opts.Limits); err != nil {
		t.Fatal(err)
	}
	return f
}

func assertScopeCollected(t *testing.T, wrapped harness.Spec) {
	t.Helper()
	unit := ""
	for _, arg := range wrapped.Args {
		if strings.HasPrefix(arg, "--unit=agentmux-turn-") {
			unit = strings.TrimPrefix(arg, "--unit=")
		}
	}
	if unit == "" {
		t.Fatal("resource scope identity is missing")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		command := exec.CommandContext(ctx, "systemctl", "--user", "show", unit, "--property=LoadState", "--value")
		command.Env = wrapped.Env
		output, err := command.Output()
		cancel()
		if strings.TrimSpace(string(output)) == "not-found" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("scope retained processes after task termination: load=%q error=%v", output, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestResourceRealTreeBoundsAndHomeFileIsolation(t *testing.T) {
	f := realResourceFixture(t)
	f.opts.Limits.CPUSeconds = 10
	f.opts.Limits.AddressSpaceBytes = 2 << 30
	source := filepath.Join(f.root, "native-auth-copy.json")
	if err := os.WriteFile(source, []byte("fixture-auth"), 0600); err != nil {
		t.Fatal(err)
	}
	f.opts.HomeFiles = []HomeFile{{Source: source, Target: "/home/agent/.claude/.credentials.json"}}
	// Kernel tree controls are verified by the trusted pre-exec shim. The
	// payload additionally proves read-only selected auth, no user bus, and
	// process-count exhaustion across forked children.
	program := `import os, resource, signal, time
assert open('/home/agent/.claude/.credentials.json').read() == 'fixture-auth'
assert 'XDG_RUNTIME_DIR' not in os.environ and 'DBUS_SESSION_BUS_ADDRESS' not in os.environ
assert resource.getrlimit(resource.RLIMIT_NOFILE) == (256,256)
assert resource.getrlimit(resource.RLIMIT_CPU) == (10,10)
assert resource.getrlimit(resource.RLIMIT_AS) == (2*1024*1024*1024,2*1024*1024*1024)
try:
    open('/home/agent/.claude/.credentials.json','w')
except OSError:
    pass
else:
    raise AssertionError('credential copy writable')
children=[]
try:
    for i in range(100):
        try: pid=os.fork()
        except OSError:
            print('pids-bound-enforced', flush=True)
            break
        if pid == 0:
            time.sleep(20); os._exit(0)
        children.append(pid)
    else: raise AssertionError('pids limit absent')
finally:
    for pid in children: os.kill(pid,signal.SIGKILL)
    for pid in children: os.waitpid(pid,0)
`
	var wrapped harness.Spec
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := harness.Run(ctx, harness.Request{Harness: "command", Command: []string{"/usr/bin/python3", "-c", program}, Workdir: f.opts.Workspace, Wrap: func(spec harness.Spec) (harness.Spec, error) {
		var err error
		wrapped, err = Wrap(spec, f.opts)
		return wrapped, err
	}})
	if err != nil || !strings.Contains(result.Text, "pids-bound-enforced") {
		t.Fatalf("tree-bound/auth proof failed: %v; %s", err, result.Text)
	}
	assertScopeCollected(t, wrapped)
}

func TestResourceRealMemoryIsAggregateAcrossProcesses(t *testing.T) {
	f := realResourceFixture(t)
	f.opts.Limits.MemoryBytes = 64 << 20
	program := `import subprocess, sys, time
allocation=bytearray(40*1024*1024)
subprocess.Popen([sys.executable,'-c','import time; allocation=bytearray(40*1024*1024); time.sleep(20)'])
time.sleep(20)
`
	var wrapped harness.Spec
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, err := harness.Run(ctx, harness.Request{Harness: "command", Command: []string{"/usr/bin/python3", "-c", program}, Workdir: f.opts.Workspace, Wrap: func(spec harness.Spec) (harness.Spec, error) {
		var err error
		wrapped, err = Wrap(spec, f.opts)
		return wrapped, err
	}})
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("aggregate memory cap did not stop the process tree before deadline: %v", err)
	}
	assertScopeCollected(t, wrapped)
}

func TestResourceRealCancellationReapsSessionEscapingDescendants(t *testing.T) {
	f := realResourceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wrapped := make(chan harness.Spec, 1)
	done := make(chan error, 1)
	go func() {
		_, err := harness.Run(ctx, harness.Request{Harness: "command", Command: []string{"/bin/sh", "-c", "setsid /bin/sleep 30 & echo ready > /workspace/ready; wait"}, Workdir: f.opts.Workspace, Wrap: func(spec harness.Spec) (harness.Spec, error) {
			result, err := Wrap(spec, f.opts)
			wrapped <- result
			return result, err
		}})
		done <- err
	}()
	var spec harness.Spec
	select {
	case spec = <-wrapped:
	case <-ctx.Done():
		t.Fatal("resource wrapper was not built")
	}
	for {
		if _, err := os.Stat(filepath.Join(f.opts.Workspace, "ready")); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("fixture exited before creating its descendant: %v", err)
		case <-ctx.Done():
			t.Fatal("fixture was not ready")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled harness did not stop: %v", err)
	}
	assertScopeCollected(t, spec)
}
