//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func brokerExecutable(t *testing.T, f fixture) string {
	t.Helper()
	path := filepath.Join(f.root, "tools", "agentmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWrapBrokerMountsOnlyDedicatedStateWritable(t *testing.T) {
	f := setup(t)
	binary := brokerExecutable(t, f)
	t.Setenv("LD_PRELOAD", "/owner/library.so")
	t.Setenv("OPENAI_API_KEY", "owner-key-must-not-leak")
	spec, err := WrapBroker(binary, f.opts.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--unshare-all", "--die-with-parent", "--new-session", "--cap-drop", "ALL"} {
		if !hasArg(spec.Args, flag) {
			t.Errorf("missing namespace restriction %s", flag)
		}
	}
	if hasArg(spec.Args, "--share-net") || hasArg(spec.Args, "--sandbox") {
		t.Fatal("broker must have isolated network and nonrecursive child invocation")
	}
	writable := 0
	for i, arg := range spec.Args {
		if arg == "--bind" {
			writable++
			if i+2 >= len(spec.Args) || spec.Args[i+1] != f.opts.StateDir || spec.Args[i+2] != f.opts.StateDir {
				t.Fatalf("unexpected persistent writable mount: %v", spec.Args[i:])
			}
		}
	}
	if writable != 1 || !hasMount(spec.Args, "--ro-bind", binary, binary) {
		t.Fatalf("state mounts=%d, missing exact read-only executable mount", writable)
	}
	expectedTail := []string{"--", binary, "managed", "serve", "--state", f.opts.StateDir}
	if !reflect.DeepEqual(spec.Args[len(spec.Args)-len(expectedTail):], expectedTail) {
		t.Fatalf("child argv=%v", spec.Args)
	}
	if spec.Workdir != f.opts.StateDir {
		t.Fatal("host cwd must remain the dedicated state directory")
	}
	env := envMap(spec.Env)
	if env["HOME"] != privateHome || env["TMPDIR"] != "/tmp" || env["PWD"] != f.opts.StateDir {
		t.Fatalf("broker environment paths are not private: %v", env)
	}
	for _, key := range []string{"OWNER_ONLY_SECRET", "OPENAI_API_KEY", "LD_PRELOAD", "AGENTMUX_CREDENTIAL_FILE"} {
		if _, exists := env[key]; exists {
			t.Errorf("broker inherited forbidden environment variable %s", key)
		}
	}
}

func TestWrapBrokerPreservesExecutableAliasAndResolvedTarget(t *testing.T) {
	f := setup(t)
	binary := brokerExecutable(t, f)
	alias := filepath.Join(f.root, "broker-link")
	if err := os.Symlink(binary, alias); err != nil {
		t.Fatal(err)
	}
	spec, err := WrapBroker(alias, f.opts.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasMount(spec.Args, "--ro-bind", binary, alias) || !hasMount(spec.Args, "--ro-bind", binary, binary) {
		t.Fatal("both executable paths must be available without mounting their containing directory")
	}
	if hasMount(spec.Args, "--ro-bind", filepath.Dir(binary), filepath.Dir(binary)) {
		t.Fatal("broker must not expose its containing workspace or tools directory")
	}
}

func TestWrapBrokerRejectsNonprivateOrAliasedState(t *testing.T) {
	f := setup(t)
	binary := brokerExecutable(t, f)
	alias := filepath.Join(f.root, "state-link")
	if err := os.Symlink(f.opts.StateDir, alias); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"", "/", "/tmp", os.Getenv("HOME"), f.root, alias, filepath.Join(f.root, "missing")} {
		if _, err := WrapBroker(binary, state); err == nil {
			t.Errorf("unsafe state accepted: %q", state)
		}
	}
	if err := os.Chmod(f.opts.StateDir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := WrapBroker(binary, f.opts.StateDir); err == nil {
		t.Fatal("nonprivate state must be rejected")
	}
}

func TestWrapBrokerRejectsMutableOrInvalidExecutable(t *testing.T) {
	f := setup(t)
	inside := filepath.Join(f.opts.StateDir, "agentmux")
	if err := os.WriteFile(inside, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(f.root, "alias-to-mutable-binary")
	if err := os.Symlink(inside, alias); err != nil {
		t.Fatal(err)
	}
	for _, binary := range []string{"", inside, alias, f.root, filepath.Join(f.root, "missing")} {
		if _, err := WrapBroker(binary, f.opts.StateDir); err == nil {
			t.Errorf("unsafe executable accepted: %q", binary)
		}
	}
	outside := brokerExecutable(t, f)
	stateAlias := filepath.Join(f.opts.StateDir, "alias-to-external-binary")
	if err := os.Symlink(outside, stateAlias); err != nil {
		t.Fatal(err)
	}
	if _, err := WrapBroker(stateAlias, f.opts.StateDir); err == nil {
		t.Fatal("even an executable alias must not be writable through coordination state")
	}
	if err := os.Chmod(outside, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := WrapBroker(outside, f.opts.StateDir); err == nil {
		t.Fatal("non-executable file accepted")
	}
}

func TestWrapBrokerFailsWithoutBubblewrap(t *testing.T) {
	f := setup(t)
	binary := brokerExecutable(t, f)
	t.Setenv("PATH", filepath.Join(f.root, "nothing-installed"))
	if _, err := WrapBroker(binary, f.opts.StateDir); err == nil || !strings.Contains(err.Error(), "requires bwrap") {
		t.Fatalf("missing bwrap must not produce an unisolated spec: %v", err)
	}
}
