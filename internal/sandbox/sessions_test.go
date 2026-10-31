//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/harness"
)

func TestSessionMountIsPrivateNarrowAndSeparateFromCredentials(t *testing.T) {
	f := setup(t)
	dir := filepath.Join(f.opts.StateDir, "sessions", "worker", "g-1")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := "/home/agent/.claude/projects"
	f.spec.SessionDirs = []harness.SessionDir{{Source: dir, Target: target}}
	wrapped, err := Wrap(f.spec, f.opts)
	if err != nil || !hasMount(wrapped.Args, "--bind", dir, target) {
		t.Fatalf("private transcript mount failed: %v", err)
	}
	for _, unsafe := range []string{f.opts.StateDir, filepath.Join(f.opts.StateDir, "credentials"), f.opts.Workspace} {
		f.spec.SessionDirs[0].Source = unsafe
		if _, err := Wrap(f.spec, f.opts); err == nil {
			t.Fatalf("exposed unsafe session directory %s", unsafe)
		}
	}
	f.spec.SessionDirs[0].Source = dir
	f.spec.SessionDirs[0].Target = "/home/agent/.claude"
	if _, err := Wrap(f.spec, f.opts); err == nil {
		t.Fatal("session directory replaced credential/config root")
	}
	f.spec.SessionDirs[0].Target = target
	if err := os.Symlink(f.opts.CredentialFile, filepath.Join(dir, "credential-alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := Wrap(f.spec, f.opts); err == nil {
		t.Fatal("session store admitted a credential symlink")
	}
}
