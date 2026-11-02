package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"agentmux/internal/harness"
)

// WrapProcessTree confines forked descendants to a Linux PID namespace. A
// descendant creating a new session or process group remains in that namespace
// and is terminated when its init exits. This is deliberately a lifetime
// boundary, not a filesystem or network sandbox: trusted host authority and
// configured native authentication remain available. Requests to independent
// host services are not forked descendants and are outside this guarantee.
//
// Callers must run ProbeProcessTree before admitting work. An unavailable
// boundary always fails; this function never substitutes process groups.
func WrapProcessTree(spec harness.Spec) (harness.Spec, error) {
	if runtime.GOOS != "linux" {
		return harness.Spec{}, errors.New("PID namespace containment requires Linux and Bubblewrap")
	}
	binary, err := exec.LookPath("bwrap")
	if err != nil {
		return harness.Spec{}, fmt.Errorf("PID namespace containment requires Bubblewrap: %w", err)
	}
	for _, value := range append([]string{spec.Binary, spec.Workdir}, spec.Args...) {
		if strings.ContainsRune(value, 0) {
			return harness.Spec{}, errors.New("PID namespace process arguments must not contain NUL")
		}
	}
	if spec.Binary == "" {
		return harness.Spec{}, errors.New("PID namespace containment requires a process executable")
	}
	for _, entry := range spec.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || strings.ContainsRune(key, 0) || strings.ContainsRune(value, 0) {
			return harness.Spec{}, errors.New("invalid process environment entry")
		}
		// These variables execute code in the launcher before the PID namespace
		// exists. Permitting them would bypass the requested lifetime boundary.
		if strings.HasPrefix(key, "LD_") || key == "GCONV_PATH" || key == "LOCPATH" || key == "GLIBC_TUNABLES" {
			return harness.Spec{}, fmt.Errorf("environment variable %q is unsafe for the containment launcher", key)
		}
	}
	args := []string{"--die-with-parent", "--new-session", "--unshare-user", "--unshare-pid", "--cap-drop", "ALL", "--bind", "/", "/", "--dev-bind", "/dev", "/dev", "--proc", "/proc"}
	if spec.Workdir != "" {
		args = append(args, "--chdir", spec.Workdir)
	}
	args = append(args, "--", spec.Binary)
	args = append(args, spec.Args...)
	spec.Binary, spec.Args = binary, args
	return spec, nil
}

// ProbeProcessTree verifies that a harmless process actually enters another
// PID namespace. Merely finding the Bubblewrap executable does not establish
// that the kernel or an outer container permits it. The probe has bounded time,
// output and environment and starts no model provider.
func ProbeProcessTree(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return errors.New("PID namespace containment requires Linux and Bubblewrap")
	}
	host, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return fmt.Errorf("read current PID namespace: %w", err)
	}
	readlink, err := exec.LookPath("readlink")
	if err != nil {
		return errors.New("PID namespace preflight requires a system readlink executable")
	}
	readlink, err = filepath.EvalSymlinks(readlink)
	if err != nil || !coveredByRuntime(readlink) {
		return errors.New("PID namespace preflight requires a system readlink executable")
	}
	spec, err := WrapProcessTree(harness.Spec{Name: "command", Binary: readlink, Args: []string{"/proc/self/ns/pid"}, Workdir: "/", Env: []string{"PATH=/usr/bin:/bin", "LANG=C"}})
	if err != nil {
		return err
	}
	output, err := runSandboxProbe(ctx, spec.Binary, spec.Args)
	if err != nil {
		return fmt.Errorf("PID namespace containment is unavailable; no work was started: %w: %s", err, strings.TrimSpace(string(output)))
	}
	child := strings.TrimSpace(string(output))
	if child == host || !strings.HasPrefix(child, "pid:[") || !strings.HasSuffix(child, "]") {
		return errors.New("PID namespace preflight did not establish a distinct namespace; no work was started")
	}
	return nil
}
