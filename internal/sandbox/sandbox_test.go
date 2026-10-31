//go:build linux

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"agentmux/internal/harness"
)

type fixture struct {
	root string
	opts Options
	spec harness.Spec
}

func setup(t *testing.T) fixture {
	t.Helper()
	// Unix socket paths must fit sockaddr_un even for long test names.
	root, err := os.MkdirTemp("", "amx-sandbox-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	state := filepath.Join(root, "state")
	workspace := filepath.Join(state, "workspaces", "worker")
	home := filepath.Join(root, "home")
	tools := filepath.Join(root, "tools")
	for _, dir := range []string{workspace, home, tools} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	credential := filepath.Join(state, "worker.json")
	if err := os.WriteFile(credential, []byte(`{"token":"private-agent-token"}`), 0600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(state, "broker.sock")
	// Argument construction only needs the inode type, not a live listener.
	// This also keeps unit tests independent of network sandbox permissions.
	if err := syscall.Mknod(socket, syscall.S_IFSOCK|0600, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "bwrap"), []byte("#!/bin/sh\nprintf '%s' 'user namespaces denied' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", tools+":/usr/bin:/bin")
	t.Setenv("OWNER_ONLY_SECRET", "must-not-leak")
	return fixture{
		root: root,
		opts: Options{Mode: "bubblewrap", Workspace: workspace, StateDir: state, CredentialFile: credential, SocketPath: socket},
		spec: harness.Spec{Name: "command", Binary: "/bin/echo", Args: []string{"literal $(expression)"}, Workdir: workspace, Env: []string{"PATH=/usr/bin:/bin", "ALLOWED=explicit-value"}},
	}
}

func envMap(env []string) map[string]string {
	result := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		result[key] = value
	}
	return result
}

func hasArg(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}

func hasMount(args []string, mode, source, target string) bool {
	for i := 0; i+2 < len(args); i++ {
		if args[i] == mode && args[i+1] == source && args[i+2] == target {
			return true
		}
	}
	return false
}

func TestWrapBuildsPrivateNamespaceWithExplicitBridge(t *testing.T) {
	f := setup(t)
	spec, err := Wrap(f.spec, f.opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"--unshare-all", "--die-with-parent", "--new-session", "--cap-drop"} {
		if !hasArg(spec.Args, required) {
			t.Errorf("missing %s", required)
		}
	}
	if hasArg(spec.Args, "--share-net") {
		t.Fatal("default sandbox must not share the network namespace")
	}
	for _, mount := range [][3]string{
		{"--bind", f.opts.Workspace, "/workspace"},
		{"--ro-bind", f.opts.CredentialFile, credentialTarget},
		{"--ro-bind", f.opts.SocketPath, socketTarget},
	} {
		if !hasMount(spec.Args, mount[0], mount[1], mount[2]) {
			t.Errorf("missing mount %v", mount)
		}
	}
	if hasMount(spec.Args, "--bind", f.opts.StateDir, f.opts.StateDir) || hasMount(spec.Args, "--ro-bind", f.opts.StateDir, f.opts.StateDir) {
		t.Fatal("coordination state must not be mounted")
	}
	env := envMap(spec.Env)
	if env["HOME"] != privateHome || env["AGENTMUX_SOCKET"] != socketTarget || env["AGENTMUX_CREDENTIAL_FILE"] != credentialTarget || env["PWD"] != "/workspace" {
		t.Fatalf("environment paths not isolated: %v", env)
	}
	if _, exists := env["OWNER_ONLY_SECRET"]; exists || env["ALLOWED"] != "explicit-value" {
		t.Fatal("must retain only explicit harness environment and minimal defaults")
	}
	if strings.Contains(strings.Join(spec.Args, " "), "private-agent-token") {
		t.Fatal("credential content must not enter argv")
	}
	if spec.Workdir != f.opts.Workspace || !hasArg(spec.Args, "literal $(expression)") {
		t.Fatal("wrapper host cwd or literal child argv changed")
	}
}

func TestLocalIsUnchangedAndCannotClaimNetworkIsolation(t *testing.T) {
	spec := harness.Spec{Name: "command", Binary: "/bin/echo", Args: []string{"x"}, Env: []string{"EXPLICIT=value"}, Workdir: "/tmp"}
	wrapped, err := Wrap(spec, Options{Mode: "local", Network: "host"})
	if err != nil || !reflect.DeepEqual(wrapped, spec) {
		t.Fatalf("local wrapping changed spec: %+v, %v", wrapped, err)
	}
	if _, err := Wrap(spec, Options{Mode: "local", Network: "none"}); err == nil {
		t.Fatal("trusted local mode cannot enforce a network restriction")
	}
	if _, err := Wrap(spec, Options{Mode: "docker"}); err == nil {
		t.Fatal("unsupported mode must not fall back")
	}
}

func TestHostNetworkingMustBeExplicit(t *testing.T) {
	f := setup(t)
	f.opts.Network = "host"
	spec, err := Wrap(f.spec, f.opts)
	if err != nil || !hasArg(spec.Args, "--share-net") {
		t.Fatalf("host network mode not honored: %v", err)
	}
	f.opts.Network = "allowlist"
	if _, err := Wrap(f.spec, f.opts); err == nil {
		t.Fatal("unsupported restrictions must not silently downgrade")
	}
}

func TestWorkspaceWorkingDirectoryIsMappedAndCannotEscape(t *testing.T) {
	f := setup(t)
	subdir := filepath.Join(f.opts.Workspace, "src")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	f.spec.Workdir = subdir
	wrapped, err := Wrap(f.spec, f.opts)
	if err != nil {
		t.Fatal(err)
	}
	if envMap(wrapped.Env)["PWD"] != "/workspace/src" || !hasArg(wrapped.Args, "/workspace/src") {
		t.Fatal("child cwd must be remapped inside the workspace")
	}
	f.spec.Workdir = f.opts.StateDir
	if _, err := Wrap(f.spec, f.opts); err == nil {
		t.Fatal("working directory must not escape the workspace")
	}
	if err := os.Symlink(f.opts.StateDir, filepath.Join(f.opts.Workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	f.spec.Workdir = "escape"
	if _, err := Wrap(f.spec, f.opts); err == nil {
		t.Fatal("working directory symlink must not escape the workspace")
	}
}

func TestWorkspaceMustNotContainCoordinationState(t *testing.T) {
	f := setup(t)
	for _, workspace := range []string{f.root, f.opts.StateDir, filepath.Dir(f.opts.CredentialFile)} {
		f.opts.Workspace = workspace
		f.spec.Workdir = workspace
		if _, err := Wrap(f.spec, f.opts); err == nil {
			t.Errorf("unsafe workspace %q accepted", workspace)
		}
	}
}

func TestReadOnlyDependenciesCannotExposeSecretsOrBroadDirectories(t *testing.T) {
	f := setup(t)
	private := filepath.Join(f.opts.StateDir, "database.json")
	if err := os.WriteFile(private, []byte("private coordination data"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{f.opts.StateDir, f.opts.CredentialFile, f.opts.SocketPath, private, filepath.Join(f.root, "home"), "/tmp", "/etc", "/dev"} {
		f.opts.ReadOnlyPaths = []string{path}
		if _, err := Wrap(f.spec, f.opts); err == nil {
			t.Errorf("unsafe read-only path %q accepted", path)
		}
	}
	symlink := filepath.Join(f.root, "alias-to-state")
	if err := os.Symlink(f.opts.StateDir, symlink); err != nil {
		t.Fatal(err)
	}
	f.opts.ReadOnlyPaths = []string{symlink}
	if _, err := Wrap(f.spec, f.opts); err == nil {
		t.Fatal("symlink must not hide an unsafe read-only mount")
	}
}

func TestReadOnlyDependenciesCannotExposeOtherDaemonSockets(t *testing.T) {
	f := setup(t)
	packageDir := filepath.Join(f.root, "unsafe-package")
	if err := os.Mkdir(packageDir, 0700); err != nil {
		t.Fatal(err)
	}
	daemonSocket := filepath.Join(packageDir, "docker.sock")
	if err := syscall.Mknod(daemonSocket, syscall.S_IFSOCK|0600, 0); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{packageDir, daemonSocket} {
		f.opts.ReadOnlyPaths = []string{path}
		if _, err := Wrap(f.spec, f.opts); err == nil {
			t.Errorf("dependency %q must not expose a daemon socket", path)
		}
	}
}

func TestPrivateDependencyMountAndExecutableOutsideSystemRuntime(t *testing.T) {
	f := setup(t)
	packageDir := filepath.Join(f.root, "agy-package")
	if err := os.Mkdir(packageDir, 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(packageDir, "agy")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	piConfigDir := filepath.Join(f.root, "pi-config")
	if err := os.Mkdir(piConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	f.spec.Binary = executable
	f.spec.ReadOnlyPaths = []string{piConfigDir}
	f.opts.ReadOnlyPaths = []string{packageDir}
	wrapped, err := Wrap(f.spec, f.opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{packageDir, piConfigDir, executable} {
		if !hasMount(wrapped.Args, "--ro-bind", path, path) {
			t.Errorf("missing exact dependency mount %q", path)
		}
	}
}

func TestWorkspaceExecutableUsesWorkspaceMount(t *testing.T) {
	f := setup(t)
	executable := filepath.Join(f.opts.Workspace, "tool")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	f.spec.Binary = "./tool"
	wrapped, err := Wrap(f.spec, f.opts)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArg(wrapped.Args, "/workspace/tool") || hasMount(wrapped.Args, "--ro-bind", executable, executable) {
		t.Fatal("workspace executable must use its existing remapped mount")
	}
}

func TestUnsafeLauncherEnvironmentAndMissingInputsFailClosed(t *testing.T) {
	f := setup(t)
	for _, entry := range []string{"LD_PRELOAD=/workspace/library.so", "GCONV_PATH=/workspace/gconv", "GLIBC_TUNABLES=x", "not-an-assignment"} {
		f.spec.Env = []string{entry}
		if _, err := Wrap(f.spec, f.opts); err == nil {
			t.Errorf("unsafe env entry %q accepted", entry)
		}
	}
	f.spec.Env = nil
	f.spec.Binary = "definitely-not-an-installed-agentmux-harness"
	if _, err := Wrap(f.spec, f.opts); err == nil {
		t.Fatal("missing executable must not fall back to a shell")
	}
	f.spec.Binary = "/bin/echo"
	f.opts.SocketPath = f.opts.CredentialFile
	if _, err := Wrap(f.spec, f.opts); err == nil {
		t.Fatal("non-socket bridge must be rejected")
	}
}

func TestProbeReportsNamespaceDenialWithoutFallback(t *testing.T) {
	setup(t)
	err := Probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "user namespaces denied") {
		t.Fatalf("expected honest probe failure, got %v", err)
	}
}
