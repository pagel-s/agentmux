package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"agentmux/internal/harness"
)

// WorkflowOptions grants only a dedicated private worktree, its own Git index,
// explicitly selected read-only code dependencies and filtered login copies.
// Unlike managed Wrap it exposes no broker credential or coordination socket.
// Network is none or allowlist; unrestricted host networking is never accepted.
type WorkflowOptions struct {
	Workspace, StateDir string
	// ControllerSource is reserved for fixed controller Git plumbing. That
	// exact source input is read-only; native tools must leave this empty.
	ControllerSource string
	// ControllerSourceGitPaths names only the original source's canonical Git
	// admin/common directories. These remain read-only even during integration,
	// and are never passed to native or check process wrappers.
	ControllerSourceGitPaths []string
	// ProtectedSourceGitPaths is a deny-only capability for native/check
	// wrappers. It never mounts these directories into the namespace.
	ProtectedSourceGitPaths []string
	// ProtectedSourcePaths denies dependency mounts of original or inherited
	// input trees. The assigned workspace itself remains the execution grant.
	ProtectedSourcePaths                   []string
	ControllerWrite                        bool
	ControllerMetadata                     string
	ControllerWorkspace                    string
	ReadOnlyWorkspace                      bool
	ReadOnlyPaths                          []string
	HomeFiles                              []HomeFile
	Network, EgressSocket, RelayExecutable string
	EgressCA                               string
	HomeBytes, TempBytes                   int64
}

const defaultWorkflowPrivateBytes int64 = 64 << 20

// ProbeWorkflow verifies namespaces and kernel-backed private home/temp bounds.
func ProbeWorkflow(ctx context.Context) error {
	if err := Probe(ctx); err != nil {
		return err
	}
	if err := ProbeCapsule(ctx); err != nil {
		return err
	}
	return probeWorkflowCredentialFD(ctx)
}

// WrapWorkflow constructs a fail-closed filesystem, process and network
// boundary. Workspace and selected Git metadata keep their canonical absolute
// paths so native tools can use exact recorded paths without seeing the source
// project or sibling worktrees. The returned environment is complete.
func WrapWorkflow(spec harness.Spec, opts WorkflowOptions) (harness.Spec, error) {
	if runtime.GOOS != "linux" {
		return harness.Spec{}, errors.New("isolated workflows require Linux and Bubblewrap")
	}
	if len(spec.ExtraFiles) != 0 {
		return harness.Spec{}, errors.New("workflow launcher accepts only validated anonymous home descriptors")
	}
	if opts.Network == "" {
		opts.Network = "none"
	}
	if opts.Network != "none" && opts.Network != "allowlist" {
		return harness.Spec{}, errors.New("isolated workflows require network none or exact-origin allowlist")
	}
	if opts.Network == "none" && opts.EgressCA != "" {
		return harness.Spec{}, errors.New("workflow TLS trust requires inspected allowlist egress")
	}
	if opts.Network == "none" && (opts.EgressSocket != "" || opts.RelayExecutable != "") {
		return harness.Spec{}, errors.New("workflow egress requires allowlist networking")
	}
	if opts.HomeBytes == 0 {
		opts.HomeBytes = defaultWorkflowPrivateBytes
	}
	if opts.TempBytes == 0 {
		opts.TempBytes = defaultWorkflowPrivateBytes
	}
	for _, size := range []int64{opts.HomeBytes, opts.TempBytes} {
		if size < 1<<20 || size > 1<<40 {
			return harness.Spec{}, errors.New("isolated home and temporary sizes must be 1 MiB–1 TiB")
		}
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return harness.Spec{}, errors.New("isolated workflows require Bubblewrap")
	}
	if RuntimeOverlaps(opts.StateDir) || RuntimeOverlaps(opts.Workspace) || opts.ControllerSource != "" && RuntimeOverlaps(opts.ControllerSource) {
		return harness.Spec{}, errors.New("isolated workflow source, state and workspace must not overlap system runtime mounts")
	}
	state, err := workflowPrivateDirectory(opts.StateDir, "run state")
	if err != nil {
		return harness.Spec{}, err
	}
	workspace, err := workflowPrivateDirectory(opts.Workspace, "workspace")
	controllerSource := ""
	if opts.ControllerSource != "" {
		controllerSource, err = realPath(opts.ControllerSource, "controller source input", true)
		if err != nil || controllerSource != opts.ControllerSource || exposesWholeHome(controllerSource) || broadHostPath(controllerSource) || containsPath(controllerSource, state) {
			return harness.Spec{}, errors.New("controller source input must be one canonical project outside private run state")
		}
		if opts.Workspace == controllerSource && (opts.ReadOnlyWorkspace || opts.ControllerWrite) && spec.Name == "command" {
			workspace, err = controllerSource, nil
		}
	}
	if err != nil || workspace == state || !containsPath(state, workspace) && (workspace != controllerSource || !(opts.ReadOnlyWorkspace || opts.ControllerWrite) || spec.Name != "command") || exposesWholeHome(workspace) || broadHostPath(workspace) {
		return harness.Spec{}, errors.New("isolated workspace must be a dedicated directory within private run state")
	}
	workdir := spec.Workdir
	if workdir == "" {
		workdir = workspace
	}
	workdir, err = realPath(workdir, "workflow working directory", true)
	if err != nil || !containsPath(workspace, workdir) {
		return harness.Spec{}, errors.New("isolated working directory must remain in its dedicated worktree")
	}
	env, err := workflowEnvironment(spec.Env, workdir)
	if err != nil {
		return harness.Spec{}, err
	}
	executable, err := resolveExecutable(spec.Binary, workdir, env)
	if err != nil {
		return harness.Spec{}, err
	}
	args := []string{"--die-with-parent", "--new-session", "--unshare-all", "--cap-drop", "ALL"}
	args = append(args, runtimeMounts()...)
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--size", fmt.Sprint(opts.TempBytes), "--tmpfs", "/tmp", "--dir", "/home", "--size", fmt.Sprint(opts.HomeBytes), "--tmpfs", privateHome, "--dir", "/run", "--dir", "/run/agentmux")
	for _, path := range []string{".codex", ".config", ".cache", ".local", ".local/share"} {
		args = append(args, "--dir", filepath.Join(privateHome, path))
	}
	mount := "--bind"
	if opts.ReadOnlyWorkspace {
		mount = "--ro-bind"
	}
	args = append(args, mount, workspace, workspace)
	if opts.ControllerWorkspace != "" {
		selected, err := workflowPrivateDirectory(opts.ControllerWorkspace, "controller checkout worktree")
		if err != nil || spec.Name != "command" || !containsPath(state, selected) || selected == state || containsPath(selected, workspace) || containsPath(workspace, selected) {
			return harness.Spec{}, errors.New("controller checkout capability must name one separate private worktree")
		}
		args = append(args, "--bind", selected, selected)
	}
	if controllerSource != "" && controllerSource != workspace {
		args = append(args, "--ro-bind", controllerSource, controllerSource)
	}
	if len(opts.ControllerSourceGitPaths) > 2 {
		return harness.Spec{}, errors.New("controller source Git metadata requires at most two exact directories")
	}
	for _, path := range opts.ControllerSourceGitPaths {
		metadata, err := realPath(path, "controller source Git metadata", true)
		if err != nil || spec.Name != "command" || controllerSource == "" || metadata != path || metadata == controllerSource || RuntimeOverlaps(metadata) || exposesWholeHome(metadata) || broadHostPath(metadata) || containsPath(metadata, controllerSource) || containsPath(metadata, state) || containsPath(state, metadata) {
			return harness.Spec{}, errors.New("controller source Git metadata must name an exact canonical directory outside private run state")
		}
		args = append(args, "--ro-bind", metadata, metadata)
	}
	if opts.ControllerMetadata != "" {
		metadata, err := workflowPrivateDirectory(opts.ControllerMetadata, "integration Git metadata")
		if err != nil || spec.Name != "command" || controllerSource == "" || !containsPath(state, metadata) || metadata == state || containsPath(workspace, metadata) || filepath.Base(metadata) != ".git" {
			return harness.Spec{}, errors.New("integration metadata must be the exact private preview Git directory")
		}
		if err := validateDependencyTree(metadata); err != nil {
			return harness.Spec{}, err
		}
		args = append(args, "--ro-bind", metadata, metadata)
	}
	allHomeFiles := append(append([]HomeFile(nil), opts.HomeFiles...), spec.HomeFiles...)
	// The generic home validator disallows run-state sources. Workflow auth
	// snapshots are created outside run state and never become retained evidence.
	homeArgs, extraFiles, err := workflowHomeMounts(allHomeFiles, workspace, state)
	if err != nil {
		return harness.Spec{}, err
	}
	args = append(args, homeArgs...)
	if len(spec.SessionDirs) != 0 {
		return harness.Spec{}, errors.New("isolated workflow turns do not mount native transcript stores")
	}
	paths := append(append([]string(nil), opts.ReadOnlyPaths...), spec.ReadOnlyPaths...)
	if !coveredByRuntime(executable) && !containsPath(workspace, executable) {
		paths = append(paths, executable)
	}
	var relay string
	if opts.Network == "allowlist" {
		if opts.EgressSocket == "" || opts.RelayExecutable == "" || opts.EgressCA == "" {
			return harness.Spec{}, errors.New("allowlisted workflow networking requires an inspected policy proxy, public CA and trusted relay")
		}
		certificate, err := workflowCertificateSource(opts.EgressCA, workspace, state)
		if err != nil {
			return harness.Spec{}, err
		}
		args = append(args, "--ro-bind", certificate, workflowCertificateTarget)
		env = workflowCertificateEnvironment(env)
		socket, e := realPath(opts.EgressSocket, "workflow egress socket", false)
		info, statErr := os.Lstat(socket)
		if e != nil || statErr != nil || socket != opts.EgressSocket || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0077 != 0 || containsPath(workspace, socket) {
			return harness.Spec{}, errors.New("workflow egress requires an external private Unix socket")
		}
		relay, err = resolveExecutable(opts.RelayExecutable, workdir, env)
		if err != nil || containsPath(workspace, relay) || containsPath(state, relay) {
			return harness.Spec{}, errors.New("workflow egress relay must be a trusted executable outside run state")
		}
		if !coveredByRuntime(relay) {
			paths = append(paths, relay)
		}
		args = append(args, "--ro-bind", socket, "/run/agentmux/egress.sock")
	}
	seen := map[string]bool{}
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return harness.Spec{}, errors.New("invalid isolated read-only dependency")
		}
		source, err := realPath(absolute, "workflow read-only dependency", false)
		if err != nil {
			return harness.Spec{}, err
		}
		if workflowDependencyOverlapsProtected(source, absolute, opts.ProtectedSourceGitPaths) {
			return harness.Spec{}, errors.New("native/check dependency would expose original source Git metadata")
		}
		if workflowDependencyOverlapsProtected(source, absolute, opts.ProtectedSourcePaths) {
			return harness.Spec{}, errors.New("native/check dependency would expose original source/input paths")
		}
		if exposesWholeHome(source) || reservedTarget(source) || reservedTarget(absolute) || containsPath(source, state) || containsPath(state, source) || containsPath(workspace, source) {
			return harness.Spec{}, errors.New("workflow dependency would expose private run state or replace an isolation boundary")
		}
		for _, file := range allHomeFiles {
			if containsPath(source, file.Source) || containsPath(absolute, file.Source) {
				return harness.Spec{}, errors.New("workflow dependency would expose a credential snapshot")
			}
		}
		if err := validateDependencyTree(source); err != nil {
			return harness.Spec{}, err
		}
		if !seen[absolute] {
			args = append(args, "--ro-bind", source, absolute)
			seen[absolute] = true
		}
	}
	args = append(args, "--chdir", workdir, "--unsetenv", "XDG_RUNTIME_DIR", "--unsetenv", "DBUS_SESSION_BUS_ADDRESS", "--")
	if relay != "" {
		args = append(args, relay, "internal-egress", "--socket", "/run/agentmux/egress.sock", "--")
	}
	args = append(args, executable)
	args = append(args, spec.Args...)
	return harness.Spec{Name: spec.Name, Binary: bwrap, Args: args, Env: env, Workdir: workspace, ExtraFiles: extraFiles}, nil
}

func workflowDependencyOverlapsProtected(source, destination string, protected []string) bool {
	for _, root := range protected {
		paths, err := runtimeOverlapPaths(root)
		if err != nil {
			return true
		}
		for _, selected := range paths {
			for _, path := range []string{source, destination} {
				if containsPath(selected, path) || containsPath(path, selected) {
					return true
				}
			}
		}
	}
	return false
}

func workflowHomeMounts(files []HomeFile, workspace, state string) ([]string, []*os.File, error) {
	args := []string{}
	descriptors := []*os.File{}
	seen := map[string]bool{}
	for _, file := range files {
		if !homeTargets[file.Target] || seen[file.Target] {
			return nil, nil, errors.New("isolated home files require unique supported targets")
		}
		seen[file.Target] = true
		if file.Descriptor == nil {
			mount, err := homeMounts([]HomeFile{file}, workspace, state, "", "")
			if err != nil {
				return nil, nil, err
			}
			args = append(args, mount...)
			continue
		}
		if _, err := anonymousHomeSource(file); err != nil {
			return nil, nil, err
		}
		args = append(args, "--ro-bind-data", strconv.Itoa(3+len(descriptors)), file.Target)
		descriptors = append(descriptors, file.Descriptor)
	}
	return args, descriptors, nil
}

func workflowPrivateDirectory(path, kind string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil || abs != path {
		return "", fmt.Errorf("isolated %s requires a canonical absolute directory", kind)
	}
	canonical, err := realPath(path, kind, true)
	info, statErr := os.Lstat(path)
	if err != nil || statErr != nil || canonical != path || info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("isolated %s must be a canonical private directory", kind)
	}
	return path, nil
}

func workflowEnvironment(input []string, workdir string) ([]string, error) {
	env, err := isolatedEnv(input, workdir)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, item := range env {
		key, value, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "GIT_") || key == "AGENTMUX_SOCKET" || key == "AGENTMUX_CREDENTIAL_FILE" || key == "CODEX_HOME" || key == "CLAUDE_CONFIG_DIR" || key == "PI_CODING_AGENT_DIR" || key == "SSH_AUTH_SOCK" || key == "DBUS_SESSION_BUS_ADDRESS" || key == "XDG_RUNTIME_DIR" || strings.HasSuffix(key, "_PROXY") || strings.HasSuffix(key, "_proxy") {
			continue
		}
		values[key] = value
	}
	values["GIT_CONFIG_GLOBAL"], values["GIT_CONFIG_NOSYSTEM"], values["GIT_TERMINAL_PROMPT"], values["GIT_OPTIONAL_LOCKS"] = "/dev/null", "1", "0", "0"
	values["CODEX_HOME"], values["CLAUDE_CONFIG_DIR"], values["PI_CODING_AGENT_DIR"] = privateHome+"/.codex", privateHome+"/.claude", privateHome+"/.pi/agent"
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result, nil
}
