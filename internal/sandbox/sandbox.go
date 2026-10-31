// Package sandbox wraps a managed harness process in an optional Linux
// bubblewrap boundary. Local mode is a trusted host process and provides no
// filesystem, process, or network isolation. Bubblewrap is not a VM boundary.
package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"agentmux/internal/harness"
)

type Options struct {
	Mode           string
	Network        string
	Workspace      string
	StateDir       string
	CredentialFile string
	SocketPath     string
	ReadOnlyPaths  []string
	Limits         Limits
	HomeFiles      []HomeFile
}

const (
	credentialTarget = "/run/agentmux/credential.json"
	socketTarget     = "/run/agentmux/broker.sock"
	privateHome      = "/home/agent"
	workspaceTarget  = "/workspace"
)

// Wrap never falls back from unavailable isolation to local execution. The
// returned Spec.Env remains a complete, explicit environment: callers must not
// append the controller's os.Environ(). Host-network mode permits unrestricted
// network access; it is not an endpoint allowlist.
func Wrap(spec harness.Spec, opts Options) (harness.Spec, error) {
	if opts.Mode == "" {
		opts.Mode = "local"
	}
	if opts.Network == "" {
		opts.Network = "host"
		if opts.Mode == "bubblewrap" {
			opts.Network = "none"
		}
	}
	if opts.Mode == "local" {
		if opts.Limits.Enabled() || len(opts.HomeFiles) != 0 || len(spec.SessionDirs) != 0 {
			return harness.Spec{}, fmt.Errorf("resource limits and isolated home files require bubblewrap mode")
		}
		if opts.Network != "host" {
			return harness.Spec{}, fmt.Errorf("local mode is trusted and cannot enforce network isolation")
		}
		return spec, nil
	}
	if opts.Mode != "bubblewrap" {
		return harness.Spec{}, fmt.Errorf("unsupported sandbox mode %q", opts.Mode)
	}
	if runtime.GOOS != "linux" {
		return harness.Spec{}, fmt.Errorf("bubblewrap sandbox requires Linux")
	}
	if opts.Network != "none" && opts.Network != "host" {
		return harness.Spec{}, fmt.Errorf("unsupported bubblewrap network mode %q", opts.Network)
	}
	if err := opts.Limits.Validate(); err != nil {
		return harness.Spec{}, err
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return harness.Spec{}, fmt.Errorf("bubblewrap sandbox requires bwrap: %w", err)
	}
	workspace, err := realPath(opts.Workspace, "workspace", true)
	if err != nil {
		return harness.Spec{}, err
	}
	state, err := realPath(opts.StateDir, "state directory", true)
	if err != nil {
		return harness.Spec{}, err
	}
	credential, err := realPath(opts.CredentialFile, "agent credential", false)
	if err != nil {
		return harness.Spec{}, err
	}
	credentialInfo, err := os.Stat(credential)
	if err != nil || !credentialInfo.Mode().IsRegular() {
		return harness.Spec{}, fmt.Errorf("agent credential must be a regular file")
	}
	socket, err := realPath(opts.SocketPath, "broker socket", false)
	if err != nil {
		return harness.Spec{}, err
	}
	socketInfo, err := os.Stat(socket)
	if err != nil || socketInfo.Mode()&os.ModeSocket == 0 {
		return harness.Spec{}, fmt.Errorf("broker socket must be a Unix socket")
	}
	if containsPath(workspace, state) || containsPath(workspace, credential) || containsPath(workspace, socket) {
		return harness.Spec{}, fmt.Errorf("workspace must not contain coordination state, credentials, or the broker socket")
	}
	if exposesWholeHome(workspace) || broadHostPath(workspace) {
		return harness.Spec{}, fmt.Errorf("workspace must not expose the host home or one of its ancestors")
	}
	workdir := workspace
	if spec.Workdir != "" {
		workdir = spec.Workdir
		if !filepath.IsAbs(workdir) {
			workdir = filepath.Join(workspace, workdir)
		}
		workdir, err = realPath(workdir, "working directory", true)
		if err != nil {
			return harness.Spec{}, err
		}
	}
	if !containsPath(workspace, workdir) {
		return harness.Spec{}, fmt.Errorf("working directory must stay inside the dedicated workspace")
	}
	relativeWorkdir, _ := filepath.Rel(workspace, workdir)
	childWorkdir := filepath.Join(workspaceTarget, relativeWorkdir)

	env, err := isolatedEnv(spec.Env, childWorkdir)
	if err != nil {
		return harness.Spec{}, err
	}
	for _, file := range spec.HomeFiles {
		if file.Target == privateHome+"/.pi/agent/models.json" {
			for i, value := range env {
				if strings.HasPrefix(value, "PI_CODING_AGENT_DIR=") {
					env[i] = "PI_CODING_AGENT_DIR=" + privateHome + "/.pi/agent"
				}
			}
		}
	}
	executable, err := resolveExecutable(spec.Binary, workdir, env)
	if err != nil {
		return harness.Spec{}, err
	}
	if err := safeMountSource(executable, workspace, state, credential, socket); err != nil {
		return harness.Spec{}, fmt.Errorf("harness executable: %w", err)
	}
	args := []string{"--die-with-parent", "--new-session", "--unshare-all", "--cap-drop", "ALL"}
	if opts.Network == "host" {
		args = append(args, "--share-net")
	}
	args = append(args, runtimeMounts()...)
	args = append(args,
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
		"--dir", "/home", "--tmpfs", privateHome,
		"--dir", "/run", "--dir", "/run/agentmux",
		"--bind", workspace, workspaceTarget,
	)
	allHomeFiles := append(append([]HomeFile(nil), opts.HomeFiles...), spec.HomeFiles...)
	homeArgs, err := homeMounts(allHomeFiles, workspace, state, credential, socket)
	if err != nil {
		return harness.Spec{}, err
	}
	args = append(args, homeArgs...)
	sessionArgs, err := sessionMounts(spec.SessionDirs, workspace, state, credential, socket)
	if err != nil {
		return harness.Spec{}, err
	}
	args = append(args, sessionArgs...)

	// Read-only dependencies retain their absolute paths, so runtime-generated
	// Pi config and packages referenced by wrapper scripts remain addressable.
	// Only the dedicated workspace is writable; no controller state is mounted.
	paths := append(append([]string(nil), opts.ReadOnlyPaths...), spec.ReadOnlyPaths...)
	if !coveredByRuntime(executable) && !containsPath(workspace, executable) {
		paths = append(paths, executable)
	}
	seen := make(map[string]bool)
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return harness.Spec{}, fmt.Errorf("read-only path: %w", err)
		}
		source, err := realPath(absolute, "read-only path", false)
		if err != nil {
			return harness.Spec{}, err
		}
		if err := safeMountSource(source, workspace, state, credential, socket); err != nil {
			return harness.Spec{}, fmt.Errorf("read-only path: %w", err)
		}
		if exposesWholeHome(source) || reservedTarget(source) || reservedTarget(absolute) {
			return harness.Spec{}, fmt.Errorf("read-only path would expose a host home or replace a reserved sandbox path")
		}
		if err := validateDependencyTree(source); err != nil {
			return harness.Spec{}, err
		}
		if !seen[absolute] {
			args = append(args, "--ro-bind", source, absolute)
			seen[absolute] = true
		}
	}
	childExecutable := executable
	if containsPath(workspace, executable) {
		relativeExecutable, _ := filepath.Rel(workspace, executable)
		childExecutable = filepath.Join(workspaceTarget, relativeExecutable)
	}
	args = append(args,
		"--ro-bind", credential, credentialTarget,
		"--ro-bind", socket, socketTarget,
		"--chdir", childWorkdir,
		"--unsetenv", "XDG_RUNTIME_DIR", "--unsetenv", "DBUS_SESSION_BUS_ADDRESS",
		"--", childExecutable,
	)
	args = append(args, spec.Args...)
	// Cmd.Dir is interpreted on the host before bwrap starts. The namespace's
	// actual working directory is selected by --chdir above.
	return wrapResources(harness.Spec{Name: spec.Name, Binary: bwrap, Args: args, Env: env, Workdir: workspace}, opts.Limits)
}

// Probe starts a harmless process inside real namespaces. Merely having bwrap
// installed is insufficient: kernels and enclosing containers may deny userns.
// An error is reported honestly; no privileged or weaker fallback is attempted.
func Probe(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("bubblewrap sandbox requires Linux")
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return fmt.Errorf("bubblewrap unavailable: %w", err)
	}
	truth, err := exec.LookPath("true")
	if err != nil {
		return fmt.Errorf("bubblewrap probe requires true: %w", err)
	}
	truth, err = filepath.EvalSymlinks(truth)
	if err != nil || !coveredByRuntime(truth) {
		return fmt.Errorf("bubblewrap probe requires a system true executable")
	}
	args := []string{"--die-with-parent", "--new-session", "--unshare-all", "--cap-drop", "ALL"}
	args = append(args, runtimeMounts()...)
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--", truth)
	cmd := exec.CommandContext(ctx, bwrap, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bubblewrap namespace probe failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func realPath(path, kind string, requireDir bool) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("%s requires a nonempty valid path", kind)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", kind, err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", kind, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", kind, err)
	}
	if requireDir && !info.IsDir() {
		return "", fmt.Errorf("%s must be a directory", kind)
	}
	return canonical, nil
}

func containsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func exposesWholeHome(path string) bool {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		if canonical, err := filepath.EvalSymlinks(home); err == nil {
			home = canonical
		}
		if containsPath(path, home) {
			return true
		}
	}
	return filepath.Clean(path) == "/" || filepath.Clean(path) == "/home" || filepath.Clean(path) == "/root"
}

func safeMountSource(path, workspace, state, credential, socket string) error {
	if containsPath(path, credential) || containsPath(path, socket) || containsPath(path, state) {
		return fmt.Errorf("path would expose coordination state or credentials")
	}
	if containsPath(state, path) && !containsPath(workspace, path) {
		return fmt.Errorf("path is private coordination state outside the workspace")
	}
	return nil
}

func reservedTarget(path string) bool {
	for _, reserved := range []string{workspaceTarget, privateHome, "/run/agentmux", "/proc", "/dev"} {
		if containsPath(path, reserved) || containsPath(reserved, path) {
			return true
		}
	}
	// Mounting a whole system configuration, temporary directory, or state tree
	// is never a narrow executable/package dependency.
	return broadHostPath(path)
}

func broadHostPath(path string) bool {
	switch path {
	case "/", "/etc", "/tmp", "/var", "/run", "/home", "/root", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/proc", "/dev", "/sys":
		return true
	}
	return false
}

func validateDependencyTree(path string) error {
	return filepath.WalkDir(path, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("inspect read-only dependency: %w", err)
		}
		// A read-only socket mount still permits connecting to a privileged
		// daemon. Package resources must contain only directories, regular
		// files, and symlinks, never sockets, device nodes, or named pipes.
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || entry.Type().IsRegular() {
			return nil
		}
		return fmt.Errorf("read-only dependency contains a socket, device, or named pipe: %s", current)
	})
}

func runtimeMounts() []string {
	var args []string
	for _, path := range []string{"/usr", "/bin", "/lib", "/lib64", "/sbin", "/etc/ssl/certs", "/etc/pki/tls/certs", "/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/ld.so.cache", "/etc/localtime"} {
		if _, err := os.Stat(path); err == nil {
			args = append(args, "--ro-bind", path, path)
		}
	}
	return args
}

func coveredByRuntime(path string) bool {
	for _, base := range []string{"/usr", "/bin", "/lib", "/lib64", "/sbin"} {
		if containsPath(base, path) {
			return true
		}
	}
	return false
}

// RuntimeExposes reports whether the default read-only system mounts contain a
// canonical host path. Credential provisioning uses this to avoid placing a
// selected original login in a location visible to every sandbox.
func RuntimeExposes(path string) bool {
	args := runtimeMounts()
	for i := 0; i+2 < len(args); i += 3 {
		if containsPath(args[i+1], path) {
			return true
		}
	}
	return false
}

func isolatedEnv(input []string, workdir string) ([]string, error) {
	env := map[string]string{"PATH": "/usr/local/bin:/usr/bin:/bin", "LANG": "C.UTF-8", "TERM": "dumb"}
	for _, entry := range input {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || strings.ContainsAny(key, "\x00=") || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("invalid harness environment entry")
		}
		// These variables can execute host libraries in bwrap itself, before it
		// establishes the namespace. They cannot be passed through safely.
		if strings.HasPrefix(key, "LD_") || key == "GCONV_PATH" || key == "LOCPATH" || key == "GLIBC_TUNABLES" {
			return nil, fmt.Errorf("environment variable %q is unsafe for the sandbox launcher", key)
		}
		env[key] = value
	}
	env["HOME"] = privateHome
	env["XDG_CONFIG_HOME"] = privateHome + "/.config"
	env["XDG_CACHE_HOME"] = privateHome + "/.cache"
	env["XDG_DATA_HOME"] = privateHome + "/.local/share"
	env["XDG_STATE_HOME"] = privateHome + "/.local/state"
	env["TMPDIR"] = "/tmp"
	env["TMP"] = "/tmp"
	env["TEMP"] = "/tmp"
	env["PWD"] = workdir
	env["AGENTMUX_SOCKET"] = socketTarget
	env["AGENTMUX_CREDENTIAL_FILE"] = credentialTarget
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+env[key])
	}
	return result, nil
}

func resolveExecutable(binary, workdir string, env []string) (string, error) {
	if binary == "" || strings.ContainsRune(binary, 0) {
		return "", fmt.Errorf("harness executable must not be empty or contain NUL")
	}
	path := binary
	if strings.ContainsRune(binary, filepath.Separator) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(workdir, path)
		}
	} else {
		search := "/usr/local/bin:/usr/bin:/bin"
		for _, entry := range env {
			if strings.HasPrefix(entry, "PATH=") {
				search = strings.TrimPrefix(entry, "PATH=")
			}
		}
		path = ""
		for _, dir := range filepath.SplitList(search) {
			if !filepath.IsAbs(dir) {
				continue
			}
			candidate := filepath.Join(dir, binary)
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
				path = candidate
				break
			}
		}
		if path == "" {
			return "", fmt.Errorf("harness executable %q not found in the explicit PATH", binary)
		}
	}
	resolved, err := realPath(path, "harness executable", false)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return "", fmt.Errorf("harness executable is not an executable regular file")
	}
	return resolved, nil
}
