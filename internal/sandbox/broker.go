package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"agentmux/internal/harness"
)

// WrapBroker isolates the coordination service itself. Its only persistent
// writable mount is the dedicated private state directory; the absolute path
// stays unchanged so credential files and host-side clients address the same
// Unix socket. The returned argv already contains the child managed serve
// invocation. Callers must use Spec.Env exactly and must not append --sandbox
// to the child, which would recurse. No isolation fallback is provided.
func WrapBroker(binary, stateDir string) (harness.Spec, error) {
	if runtime.GOOS != "linux" {
		return harness.Spec{}, fmt.Errorf("bubblewrap broker sandbox requires Linux")
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return harness.Spec{}, fmt.Errorf("bubblewrap broker sandbox requires bwrap: %w", err)
	}
	stateAbs, err := filepath.Abs(stateDir)
	if err != nil || stateDir == "" || strings.ContainsRune(stateDir, 0) {
		return harness.Spec{}, fmt.Errorf("broker state requires a valid dedicated directory")
	}
	state, err := realPath(stateAbs, "broker state directory", true)
	if err != nil {
		return harness.Spec{}, err
	}
	if state != stateAbs {
		return harness.Spec{}, fmt.Errorf("broker state directory must have a canonical path without symlinks")
	}
	info, err := os.Stat(state)
	if err != nil || info.Mode().Perm() != 0700 {
		return harness.Spec{}, fmt.Errorf("broker state directory must be private (mode 0700)")
	}
	if broadHostPath(state) || exposesWholeHome(state) || coveredByRuntime(state) || reservedTarget(state) {
		return harness.Spec{}, fmt.Errorf("broker state must be a dedicated directory outside host runtime and reserved sandbox paths")
	}
	if len(filepath.Join(state, "broker.sock")) > 100 {
		return harness.Spec{}, fmt.Errorf("broker state path is too long for its Unix socket")
	}

	if binary == "" || strings.ContainsRune(binary, 0) {
		return harness.Spec{}, fmt.Errorf("broker executable requires a valid path")
	}
	if !strings.ContainsRune(binary, filepath.Separator) {
		binary, err = exec.LookPath(binary)
		if err != nil {
			return harness.Spec{}, fmt.Errorf("broker executable not found: %w", err)
		}
	}
	binaryAbs, err := filepath.Abs(binary)
	if err != nil {
		return harness.Spec{}, fmt.Errorf("resolve broker executable: %w", err)
	}
	executable, err := realPath(binaryAbs, "broker executable", false)
	if err != nil {
		return harness.Spec{}, err
	}
	info, err = os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return harness.Spec{}, fmt.Errorf("broker executable must be an executable regular file")
	}
	if containsPath(state, binaryAbs) || containsPath(state, executable) {
		return harness.Spec{}, fmt.Errorf("broker executable must not be inside writable coordination state")
	}
	for _, path := range []string{binaryAbs, executable} {
		if reservedTarget(path) || exposesWholeHome(path) {
			return harness.Spec{}, fmt.Errorf("broker executable cannot replace a reserved sandbox path")
		}
	}

	args := []string{"--unshare-all", "--die-with-parent", "--new-session", "--cap-drop", "ALL"}
	args = append(args, runtimeMounts()...)
	args = append(args,
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
		"--dir", "/home", "--tmpfs", privateHome,
	)
	// Preserve both an owner-selected executable alias and its canonical
	// target, without mounting their containing workspace or user home.
	seen := map[string]bool{}
	for _, target := range []string{binaryAbs, executable} {
		if !coveredByRuntime(target) && !seen[target] {
			args = append(args, "--ro-bind", executable, target)
			seen[target] = true
		}
	}
	args = append(args,
		"--bind", state, state,
		"--chdir", state,
		"--", executable, "managed", "serve", "--state", state,
	)
	return harness.Spec{
		Name: "broker", Binary: bwrap, Args: args, Workdir: state,
		Env: []string{
			"PATH=/usr/local/bin:/usr/bin:/bin", "LANG=C.UTF-8", "TERM=dumb",
			"HOME=" + privateHome, "XDG_CONFIG_HOME=" + privateHome + "/.config",
			"XDG_CACHE_HOME=" + privateHome + "/.cache", "XDG_DATA_HOME=" + privateHome + "/.local/share",
			"TMPDIR=/tmp", "PWD=" + state,
		},
	}, nil
}
