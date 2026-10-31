//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeFilesOnlyMountPrivateSelectedCopies(t *testing.T) {
	f := setup(t)
	source := filepath.Join(f.root, "credential-copy.json")
	if err := os.WriteFile(source, []byte(`{"fixture":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	for target := range homeTargets {
		f.opts.HomeFiles = []HomeFile{{Source: source, Target: target}}
		wrapped, err := Wrap(f.spec, f.opts)
		if err != nil || !hasMount(wrapped.Args, "--ro-bind", source, target) {
			t.Fatalf("private credential target %s: %v", target, err)
		}
	}
	for _, file := range []HomeFile{
		{Source: source, Target: "/home/agent/.claude/settings.json"},
		{Source: f.opts.CredentialFile, Target: "/home/agent/.claude.json"},
		{Source: f.opts.Workspace, Target: "/home/agent/.claude.json"},
	} {
		f.opts.HomeFiles = []HomeFile{file}
		if _, err := Wrap(f.spec, f.opts); err == nil {
			t.Fatalf("accepted unsafe home file %+v", file)
		}
	}
	alias := filepath.Join(f.root, "alias.json")
	if err := os.Symlink(source, alias); err != nil {
		t.Fatal(err)
	}
	f.opts.HomeFiles = []HomeFile{{Source: alias, Target: "/home/agent/.claude.json"}}
	if _, err := Wrap(f.spec, f.opts); err == nil {
		t.Fatal("accepted a symlink credential copy")
	}
	if err := os.Chmod(source, 0644); err != nil {
		t.Fatal(err)
	}
	f.opts.HomeFiles[0].Source = source
	if _, err := Wrap(f.spec, f.opts); err == nil {
		t.Fatal("accepted a nonprivate credential copy")
	}
}
