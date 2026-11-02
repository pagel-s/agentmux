//go:build linux

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentmux/internal/harness"
)

func TestProcessTreeWrapperPreservesTrustedHostAuthorityAndRejectsUnsafeLauncher(t *testing.T) {
	tools := t.TempDir()
	bwrap := filepath.Join(tools, "bwrap")
	if err := os.WriteFile(bwrap, []byte("#!/bin/sh\nexit 125\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools)
	original := harness.Spec{Name: "command", Binary: "/bin/echo", Args: []string{"literal $(expression)"}, Env: []string{"PATH=/usr/bin:/bin", "HOME=/owner/auth", "TOKEN=selected"}, Workdir: "/owner/project"}
	wrapped, err := WrapProcessTree(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"--die-with-parent", "--new-session", "--unshare-user", "--unshare-pid", "--proc", "--chdir", "/owner/project", "/bin/echo", "literal $(expression)"} {
		if !hasArg(wrapped.Args, required) {
			t.Errorf("missing containment argument %q", required)
		}
	}
	if filepath.Base(wrapped.Binary) != "bwrap" || wrapped.Workdir != original.Workdir || strings.Join(wrapped.Env, "\n") != strings.Join(original.Env, "\n") {
		t.Fatal("process containment silently changed native authentication or host working directory")
	}
	for _, entry := range []string{"LD_PRELOAD=/owner/library", "LD_LIBRARY_PATH=/owner/lib", "GCONV_PATH=/owner/modules", "GLIBC_TUNABLES=unsafe", "LOCPATH=/owner/locale", "invalid", "KEY=bad\x00value"} {
		bad := original
		bad.Env = append(append([]string(nil), original.Env...), entry)
		if _, err := WrapProcessTree(bad); err == nil {
			t.Fatalf("unsafe launcher environment accepted: %q", entry)
		}
	}
	t.Setenv("PATH", filepath.Join(tools, "missing"))
	if _, err := WrapProcessTree(original); err == nil {
		t.Fatal("missing Bubblewrap silently fell back to an uncontained process")
	}
}

func TestProcessTreeProbeCannotMistakeHostNamespaceForContainment(t *testing.T) {
	host, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	if err = os.WriteFile(filepath.Join(tools, "bwrap"), []byte("#!/bin/sh\nprintf '%s\\n' '"+host+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+":/usr/bin:/bin")
	if err := ProbeProcessTree(context.Background()); err == nil || !strings.Contains(err.Error(), "distinct namespace") {
		t.Fatalf("host namespace was presented as contained: %v", err)
	}
}

func requireProcessTreeBoundary(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENTMUX_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_SANDBOX_INTEGRATION=1 for real PID namespace descendant checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ProbeProcessTree(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestProcessTreeRealStopsSessionEscapedDescendantAfterCompletionAndCancellation(t *testing.T) {
	requireProcessTreeBoundary(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	for _, cancelTurn := range []bool{false, true} {
		name := "completion"
		if cancelTurn {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			ready := filepath.Join(root, "session-escaped-ready")
			marker := filepath.Join(root, "must-not-survive")
			child := "import pathlib,sys,time; pathlib.Path(sys.argv[1]).write_text('ready'); time.sleep(0.6); pathlib.Path(sys.argv[2]).write_text('escaped')"
			parent := "import pathlib,subprocess,sys,time; subprocess.Popen([sys.executable,'-c',sys.argv[1],sys.argv[2],sys.argv[3]],start_new_session=True,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)\nwhile not pathlib.Path(sys.argv[2]).exists(): time.sleep(0.005)\n"
			if cancelTurn {
				parent += "time.sleep(30)\n"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if cancelTurn {
				go func() {
					deadline := time.Now().Add(3 * time.Second)
					for time.Now().Before(deadline) {
						if _, err := os.Stat(ready); err == nil {
							cancel()
							return
						}
						time.Sleep(5 * time.Millisecond)
					}
					cancel()
				}()
			}
			_, err := harness.Run(ctx, harness.Request{Harness: "command", Command: []string{python, "-c", parent, child, ready, marker}, Workdir: root, Wrap: WrapProcessTree, ReapDescendants: true})
			if cancelTurn && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation identity lost: %v", err)
			}
			if !cancelTurn && err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(ready); err != nil {
				t.Fatal("session-escaping descendant was never started")
			}
			time.Sleep(750 * time.Millisecond)
			if _, err = os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("descendant survived the requested PID boundary: %v", err)
			}
		})
	}
}

// This child is intentionally killed without cancellation or group cleanup.
// Bubblewrap must observe the abrupt owner death itself.
func TestProcessTreeOwnerProcess(t *testing.T) {
	if os.Getenv("AGENTMUX_PROCESS_TREE_OWNER_HELPER") != "1" {
		return
	}
	root := os.Getenv("AGENTMUX_PROCESS_TREE_OWNER_ROOT")
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(root, "session-escaped-ready")
	marker := filepath.Join(root, "must-not-survive")
	child := "import pathlib,sys,time; pathlib.Path(sys.argv[1]).write_text('ready'); time.sleep(0.6); pathlib.Path(sys.argv[2]).write_text('escaped')"
	parent := "import pathlib,subprocess,sys,time; subprocess.Popen([sys.executable,'-c',sys.argv[1],sys.argv[2],sys.argv[3]],start_new_session=True,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)\nwhile not pathlib.Path(sys.argv[2]).exists(): time.sleep(0.005)\ntime.sleep(30)\n"
	spec, err := WrapProcessTree(harness.Spec{Name: "command", Binary: python, Args: []string{"-c", parent, child, ready, marker}, Workdir: root, Env: []string{"PATH=" + os.Getenv("PATH"), "LANG=C"}})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(spec.Binary, spec.Args...)
	cmd.Env, cmd.Dir = spec.Env, spec.Workdir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "namespace-owner.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		_ = cmd.Process.Kill()
		t.Fatal(err)
	}
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestProcessTreeRealStopsEscapedDescendantAfterOwnerSIGKILL(t *testing.T) {
	requireProcessTreeBoundary(t)
	root := t.TempDir()
	log, err := os.OpenFile(filepath.Join(root, "owner.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessTreeOwnerProcess$")
	cmd.Env = append(os.Environ(), "AGENTMUX_PROCESS_TREE_OWNER_HELPER=1", "AGENTMUX_PROCESS_TREE_OWNER_ROOT="+root)
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
			}
		}
		// On a regression, clean up only our recorded Bubblewrap process after
		// confirming its command line still names this fixture directory.
		data, readErr := os.ReadFile(filepath.Join(root, "namespace-owner.pid"))
		if readErr == nil {
			pid, parseErr := strconv.Atoi(string(data))
			args, argsErr := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
			if parseErr == nil && argsErr == nil && bytes.Contains(args, []byte(root)) {
				process, _ := os.FindProcess(pid)
				_ = process.Kill()
			}
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(root, "session-escaped-ready")); err == nil {
			break
		}
		select {
		case err = <-done:
			waited = true
			data, _ := os.ReadFile(filepath.Join(root, "owner.log"))
			t.Fatalf("owner exited before its escaped descendant started: %v: %s", err, data)
		default:
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(filepath.Join(root, "owner.log"))
			t.Fatalf("owner did not start a session-escaping descendant: %s", data)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Kill only the controller. No context is canceled and no process-group
	// signal is sent, so survival is controlled by the kernel/namespace path.
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		waited = true
	case <-time.After(3 * time.Second):
		t.Fatal("SIGKILL did not stop fixture owner")
	}
	time.Sleep(750 * time.Millisecond)
	if _, err = os.Stat(filepath.Join(root, "must-not-survive")); !os.IsNotExist(err) {
		t.Fatalf("session-escaped descendant survived abrupt owner death: %v", err)
	}
}
