package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requireCaseDistinctPrivatePaths(t *testing.T, directory string) {
	t.Helper()
	lower := filepath.Join(directory, "case-distinct-probe")
	upper := filepath.Join(directory, "CASE-DISTINCT-PROBE")
	fixtureWrite(t, lower, nil)
	t.Cleanup(func() { _ = os.Remove(lower) })
	f, err := os.OpenFile(upper, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		t.Skip("filesystem cannot represent distinct paths differing only in case")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(upper) })
}

func TestPrivateRunBindsCaseDistinctPathsDespiteMutableGitConfig(t *testing.T) {
	repo, root := fixtureRepo(t)
	requireCaseDistinctPrivatePaths(t, root)
	fixtureGit(t, repo, "config", "core.ignorecase", "true")
	// The persistent application coordination file is the only intentional
	// source metadata addition; include it in the preservation oracle.
	fixtureWrite(t, filepath.Join(repo, ".git", "agentmux-apply.lock"), nil)
	before := privateSourceMetadataWithTimes(t, repo)
	script := `from pathlib import Path
import subprocess
for key, value in [('ignorecase', 'true'), ('precomposeunicode', 'true'), ('filemode', 'false'), ('symlinks', 'false')]:
    subprocess.run(['git', 'config', 'core.'+key, value], check=True)
Path('file.txt').write_bytes(b'fixed\n')
Path('FILE.TXT').write_bytes(b'case-distinct checked bytes\r\n')
print('updated both case-distinct paths')
`
	p := comparePlan(repo, pythonCandidate("writer", script))
	p.Storage = StoragePrivate
	p.Checks = []Check{pythonCheck("from pathlib import Path; assert Path('file.txt').read_bytes()==b'fixed\\n'; assert Path('FILE.TXT').read_bytes()==b'case-distinct checked bytes\\r\\n'")}
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), false)
	if err != nil || len(r.Candidates) != 1 || r.Candidates[0].State != "verified" {
		t.Fatalf("case-distinct workflow: %+v %v", r, err)
	}
	c := r.Candidates[0]
	if c.FinalSnapshot == nil || c.Verification == nil || c.FinalSnapshot.SelectedFiles != r.Snapshot.SelectedFiles+1 || c.FinalSnapshot.Tree != c.Verification.Tree || c.FinalSnapshot.ContentSHA256 != c.Verification.ContentSHA256 {
		t.Fatalf("case-distinct path absent from checked snapshot identity: %+v", c)
	}
	patch, err := os.ReadFile(c.Patch)
	if err != nil || !strings.Contains(string(patch), "b/FILE.TXT") || !strings.Contains(string(patch), "b/file.txt") {
		t.Fatalf("patch omits checked case-distinct paths: %s %v", patch, err)
	}
	if got := fixtureGit(t, c.Worktree, "config", "--get", "core.ignorecase"); got != "true" {
		t.Fatalf("controller changed worker configuration instead of overriding it: %q", got)
	}
	if privateSourceMetadataWithTimes(t, repo) != before {
		t.Fatal("private run changed source Git metadata")
	}
	upper := filepath.Join(c.Worktree, "FILE.TXT")
	fixtureWrite(t, upper, []byte("changed after independent checks\n"))
	if fresh, err := IsFresh(context.Background(), c.Worktree, *c.Verification); err != nil || fresh {
		t.Fatalf("case-distinct candidate mutation escaped freshness: %v %v", fresh, err)
	}
	if applied, err := Apply(context.Background(), r.Output, c.ID); err == nil || applied.Applied {
		t.Fatalf("applied candidate with changed case-distinct checked bytes: %+v %v", applied, err)
	}
	if data, err := os.ReadFile(filepath.Join(repo, "file.txt")); err != nil || string(data) != "base\n" {
		t.Fatalf("stale candidate refusal changed source bytes: %q %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(repo, "FILE.TXT")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale candidate refusal created the case-distinct source file: %v", err)
	}
	fixtureWrite(t, upper, []byte("case-distinct checked bytes\r\n"))
	if fresh, err := IsFresh(context.Background(), c.Worktree, *c.Verification); err != nil || !fresh {
		t.Fatalf("restored exact case-distinct candidate is not fresh: %v %v", fresh, err)
	}
	if applied, err := Apply(context.Background(), r.Output, c.ID); err != nil || !applied.Applied || applied.Uncertain {
		t.Fatalf("apply exact case-distinct result: %+v %v", applied, err)
	}
	for path, want := range map[string]string{"file.txt": "fixed\n", "FILE.TXT": "case-distinct checked bytes\r\n"} {
		if data, err := os.ReadFile(filepath.Join(repo, path)); err != nil || string(data) != want {
			t.Fatalf("applied source differs from checked %s bytes: %q %v", path, data, err)
		}
	}
	if privateSourceMetadataWithTimes(t, repo) != before {
		t.Fatal("freshness, refusal or application changed source Git objects, refs, index or config")
	}
}

func TestPrivateSourceSelectionIncludesCaseDistinctUntrackedPath(t *testing.T) {
	repo, root := fixtureRepo(t)
	requireCaseDistinctPrivatePaths(t, root)
	fixtureGit(t, repo, "config", "core.ignorecase", "true")
	fixtureWrite(t, filepath.Join(repo, "FILE.TXT"), []byte("untracked owner bytes\r\n"))
	before := privateSourceMetadataWithTimes(t, repo)
	ctx, err := PrepareSnapshotStorage(context.Background(), StoragePrivate, repo, filepath.Join(root, "private.git"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Capture(ctx, repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if s.SelectedFiles != 3 {
		t.Fatalf("initial snapshot omitted a case-distinct selected path: %+v", s)
	}
	fork := filepath.Join(root, "fork")
	if err := CreateWorktree(ctx, s, fork); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"file.txt": "base\n", "FILE.TXT": "untracked owner bytes\r\n"} {
		if data, err := os.ReadFile(filepath.Join(fork, path)); err != nil || string(data) != want {
			t.Fatalf("initial snapshot lost selected %s bytes: %q %v", path, data, err)
		}
	}
	if privateSourceMetadataWithTimes(t, repo) != before {
		t.Fatal("initial case-distinct selection changed source Git metadata")
	}
}
