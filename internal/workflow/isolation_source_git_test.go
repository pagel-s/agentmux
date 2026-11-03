package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/harness"
)

func linkedSourceFixture(t *testing.T) (main, linked, root string) {
	t.Helper()
	main, root = fixtureRepo(t)
	linked = filepath.Join(root, "linked")
	fixtureGit(t, main, "worktree", "add", "-q", "-b", "linked", linked)
	return main, linked, root
}

func TestIsolatedLinkedSourceGitDiscoveryAndNativeDependencyRefusal(t *testing.T) {
	main, linked, root := linkedSourceFixture(t)
	paths, err := isolatedSourceGitPaths(context.Background(), linked, false)
	if err != nil {
		t.Fatal(err)
	}
	common := filepath.Join(main, ".git")
	admin := filepath.Join(common, "worktrees", "linked")
	if len(paths) != 2 || paths[0] != admin || paths[1] != common {
		t.Fatalf("wrong exact Git metadata roots: %v", paths)
	}
	r := &isolationRuntime{protectedGitPaths: paths}
	alias := filepath.Join(root, "metadata-alias")
	if err := os.Symlink(common, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{admin, common, filepath.Join(common, "config"), main, alias} {
		if err := r.validateNativeGitDependencies([]string{path}); err == nil {
			t.Fatalf("native dependency exposes original Git metadata: %s", path)
		}
	}
	if err := r.validateNativeGitDependencies([]string{"/usr/bin/python3"}); err != nil {
		t.Fatal(err)
	}
}

func TestIsolationRefusesRuntimeSourceRunAndGitMetadata(t *testing.T) {
	repo, root := fixtureRepo(t)
	alias := filepath.Join(root, "runtime-source")
	if err := os.Symlink("/usr", alias); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"/usr/local/project", "/etc/ssl", alias} {
		p := Plan{Repository: source, Storage: StoragePrivate, Isolation: &IsolationOptions{}}
		if err := ValidateIsolation(p); err == nil || !strings.Contains(err.Error(), "runtime mounts") {
			t.Fatalf("runtime source accepted: %s %v", source, err)
		}
	}
	if _, _, err := prepareIsolation(context.Background(), &IsolationOptions{}, repo, "/usr/local/private-run", nil, false, nil); err == nil || !strings.Contains(err.Error(), "runtime mounts") {
		t.Fatalf("runtime run state accepted: %v", err)
	}
	parent := &isolationRuntime{protectedGitPaths: []string{alias}}
	if _, _, err := prepareIsolation(context.Background(), &IsolationOptions{}, repo, filepath.Join(root, "run"), nil, false, parent); err == nil || !strings.Contains(err.Error(), "runtime mounts") {
		t.Fatalf("inherited runtime Git metadata accepted: %v", err)
	}
	tools := filepath.Join(root, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+":/usr/bin:/bin")
	for _, metadata := range []string{"/usr", "/etc/ssl", alias} {
		// Discovery is fixed read-only plumbing. A tiny Git fixture supplies
		// candidate roots without creating repositories in system directories.
		if err := os.WriteFile(filepath.Join(tools, "git"), []byte("#!/usr/bin/python3\nprint("+fmt.Sprintf("%q", metadata)+")\n"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := isolatedSourceGitPaths(context.Background(), repo, false); err == nil || !strings.Contains(err.Error(), "runtime mounts") {
			t.Fatalf("runtime Git metadata accepted: %s %v", metadata, err)
		}
	}
}

func TestWorkflowIsolationRealLinkedSourceRunApplyPreservesGit(t *testing.T) {
	requireFullWorkflowIsolation(t)
	main, linked, root := linkedSourceFixture(t)
	common := filepath.Join(main, ".git")
	admin := filepath.Join(common, "worktrees", "linked")
	// Include a staged/working difference so reading the main worktree index
	// instead of this linked worktree's index cannot satisfy the regression.
	fixtureWrite(t, filepath.Join(linked, "file.txt"), []byte("staged\n"))
	fixtureGit(t, linked, "add", "file.txt")
	fixtureWrite(t, filepath.Join(linked, "file.txt"), []byte("working\n"))
	beforeCommon := privateSourceMetadata(t, main)
	beforePointer := privateSourceMetadata(t, linked)
	visibility := fmt.Sprintf(`from pathlib import Path
for p in [%q, %q, %q, %q]:
    assert not Path(p).exists(), p
    try:
        Path(p, 'config').read_bytes()
    except OSError: pass
    else: raise AssertionError('original Git metadata readable: '+p)
`, common, admin, "/proc/1/root"+common, linked)
	script := "import sys,json\nsys.stdin.read()\n" + visibility + `assert Path('file.txt').read_text()=='working\n'
assert Path('.git').is_dir()
Path('file.txt').write_text('fixed\n')
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'isolated linked source fixed'}}))
print('{"type":"turn.completed"}')
`
	p := comparePlan(linked, Candidate{ID: "writer", Harness: "codex", Command: []string{"/usr/bin/python3", "-c", script}})
	p.Storage = StoragePrivate
	p.Isolation = &IsolationOptions{}
	p.Checks = []Check{{Name: "linked-source-acceptance", Command: []string{"/usr/bin/python3", "-c", visibility + "assert Path('file.txt').read_text()=='fixed\\n'"}}}
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), true)
	if err != nil || len(r.Candidates) != 1 || r.Candidates[0].State != "verified" {
		t.Fatalf("isolated linked source failed: %+v %v", r, err)
	}
	if privateSourceMetadata(t, main) != beforeCommon || privateSourceMetadata(t, linked) != beforePointer {
		t.Fatal("run/capture changed source pointer or common Git bytes/modes")
	}
	if data, err := os.ReadFile(filepath.Join(linked, "file.txt")); err != nil || string(data) != "working\n" {
		t.Fatalf("run changed original source worktree: %q %v", data, err)
	}
	// Source application coordination deliberately creates this persistent
	// lock. Precreate it so every other byte/mode remains in the comparison.
	fixtureWrite(t, filepath.Join(common, "agentmux-apply.lock"), nil)
	beforeCommon = privateSourceMetadata(t, main)
	applied, err := Apply(context.Background(), r.Output, "writer")
	if err != nil || !applied.Applied || applied.Uncertain {
		t.Fatalf("isolated linked source application: %+v %v", applied, err)
	}
	inspected, err := InspectApply(context.Background(), r.Output, "writer")
	if err != nil || inspected.State != "applied" {
		t.Fatalf("isolated linked source inspection: %+v %v", inspected, err)
	}
	recovered, err := RecoverApply(context.Background(), r.Output, "writer")
	if err != nil || recovered.State != "applied" {
		t.Fatalf("isolated linked source recovery: %+v %v", recovered, err)
	}
	if privateSourceMetadata(t, main) != beforeCommon || privateSourceMetadata(t, linked) != beforePointer {
		t.Fatal("apply/inspect/recover changed source pointer, index, config, refs or objects")
	}
	if data, err := os.ReadFile(filepath.Join(linked, "file.txt")); err != nil || string(data) != "fixed\n" {
		t.Fatalf("application did not write the exact linked source content: %q %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(main, "file.txt")); err != nil || string(data) != "base\n" {
		t.Fatalf("application changed the primary worktree: %q %v", data, err)
	}
	// Explicit dependency grants cannot export the controller-only common
	// directory to either native workers or acceptance checks.
	ctx, cleanup, err := ReportContext(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := gitText(ctx, linked, nil, "update-ref", "refs/heads/isolation-must-not-write", "HEAD"); err == nil {
		t.Fatal("trusted controller could write original common Git metadata")
	}
	runtime := isolationFromContext(ctx)
	runtime.opts.ReadOnlyPaths = []string{common}
	for _, check := range []bool{false, true} {
		request := harness.Request{Harness: "command", Command: []string{"/usr/bin/true"}, Workdir: r.Candidates[0].Worktree}
		var closeRequest func()
		if check {
			request, closeRequest, err = IsolationCheckRequest(ctx, request.Workdir, request)
		} else {
			request, closeRequest, err = IsolationRequest(ctx, Candidate{ID: "writer", Harness: "command"}, request, false)
		}
		if err == nil {
			_, err = request.Wrap(harness.Spec{Name: "command", Binary: "/usr/bin/true", Workdir: request.Workdir})
		}
		closeRequest()
		if err == nil || !strings.Contains(err.Error(), "original source Git metadata") {
			t.Fatalf("metadata dependency accepted for check=%v: %v", check, err)
		}
	}
	if privateSourceMetadata(t, main) != beforeCommon || privateSourceMetadata(t, linked) != beforePointer {
		t.Fatal("source Git write/dependency refusal changed original metadata")
	}
}
