package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func fixtureRepo(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, repo, "init", "-q")
	fixtureGit(t, repo, "config", "maintenance.auto", "false")
	fixtureGit(t, repo, "config", "gc.auto", "0")
	fixtureGit(t, repo, "config", "user.email", "fixture@example.test")
	fixtureGit(t, repo, "config", "user.name", "Fixture")
	fixtureGit(t, repo, "config", "commit.gpgsign", "false")
	fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("base\n"))
	fixtureWrite(t, filepath.Join(repo, ".gitignore"), []byte("ignored-secret\n"))
	fixtureGit(t, repo, "add", ".")
	fixtureGit(t, repo, "commit", "-qm", "base")
	return repo, root
}

func fixtureGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo, "-c", "core.hooksPath=/dev/null", "-c", "maintenance.auto=false", "-c", "gc.auto=0"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}
func fixtureWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePreservesSourceIndexAndPinsDirtyAndBinaryContents(t *testing.T) {
	repo, root := fixtureRepo(t)
	ctx := context.Background()
	head := fixtureGit(t, repo, "rev-parse", "HEAD")
	fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("staged\n"))
	fixtureGit(t, repo, "add", "file.txt")
	fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("working\n"))
	fixtureWrite(t, filepath.Join(repo, "new.bin"), []byte{0, 1, 2, 255})
	fixtureWrite(t, filepath.Join(repo, "ignored-secret"), []byte("must not travel"))
	index := fixtureGit(t, repo, "diff", "--cached", "--binary")
	s, err := Capture(ctx, repo, filepath.Join(root, "capture"))
	if err != nil {
		t.Fatal(err)
	}
	if s.BaseCommit != head || s.Commit == head || s.Tree == "" {
		t.Fatalf("bad snapshot: %+v", s)
	}
	if got := fixtureGit(t, repo, "rev-parse", "refs/agentmux/snapshots/"+s.Commit); got != s.Commit {
		t.Fatal("snapshot is not retained against Git garbage collection")
	}
	if got := fixtureGit(t, repo, "rev-parse", "HEAD"); got != head {
		t.Fatal("capture changed source branch")
	}
	if got := fixtureGit(t, repo, "diff", "--cached", "--binary"); got != index {
		t.Fatal("capture changed source index")
	}
	wt := filepath.Join(root, "candidate")
	if err := CreateWorktree(ctx, s, wt); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"file.txt": "working\n", "new.bin": string([]byte{0, 1, 2, 255})} {
		b, err := os.ReadFile(filepath.Join(wt, name))
		if err != nil || string(b) != want {
			t.Fatalf("%s: %q, %v", name, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(wt, "ignored-secret")); !os.IsNotExist(err) {
		t.Fatal("ignored file copied")
	}
	patch, err := os.ReadFile(filepath.Join(root, "capture", "changes.patch"))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(patch)
	if s.PatchSHA256 != hex.EncodeToString(h[:]) {
		t.Fatal("patch digest mismatch")
	}
	for path, want := range map[string]os.FileMode{filepath.Join(root, "capture"): 0700, filepath.Join(root, "capture", "snapshot.json"): 0600, wt: 0700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("permissions %s: %v %v", path, info, err)
		}
	}
}

func TestExportPatchIncludesNewFilesAndCanReproduceCandidate(t *testing.T) {
	repo, root := fixtureRepo(t)
	ctx := context.Background()
	s, err := Capture(ctx, repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, path := range []string{first, second} {
		if err := CreateWorktree(ctx, s, path); err != nil {
			t.Fatal(err)
		}
	}
	fixtureWrite(t, filepath.Join(first, "file.txt"), []byte("fixed\n"))
	fixtureWrite(t, filepath.Join(first, "created.bin"), []byte{0, 42, 255})
	patch := filepath.Join(root, "candidate.patch")
	if err := ExportPatch(ctx, s, first, patch); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, second, "apply", "--binary", patch)
	a, err := contentTree(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := contentTree(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("exported patch did not reproduce candidate contents")
	}
	if err := ExportPatch(ctx, s, first, patch); err == nil {
		t.Fatal("existing patch overwritten")
	}
}

func TestArtifactPathsAndForgedSnapshotAreRefused(t *testing.T) {
	repo, root := fixtureRepo(t)
	ctx := context.Background()
	if _, err := Capture(ctx, repo, filepath.Join(repo, "recursive-output")); err == nil {
		t.Fatal("accepted output inside source")
	}
	s, err := Capture(ctx, repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Capture(ctx, repo, filepath.Join(root, "snapshot")); err == nil {
		t.Fatal("overwrote snapshot")
	}
	if err := CreateWorktree(ctx, s, filepath.Join(repo, "nested")); err == nil {
		t.Fatal("accepted nested worktree")
	}
	s.Tree = strings.Repeat("0", 40)
	if err := CreateWorktree(ctx, s, filepath.Join(root, "forged")); err == nil {
		t.Fatal("accepted forged tree")
	}
}

func TestSnapshotsRefuseGitlinksAndDoNotRunCheckoutHooks(t *testing.T) {
	repo, root := fixtureRepo(t)
	marker := filepath.Join(root, "checkout-hook-ran")
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	fixtureWrite(t, hook, []byte("#!/bin/sh\nprintf executed > '"+marker+"'\n"))
	if err := os.Chmod(hook, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := Capture(context.Background(), repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateWorktree(context.Background(), s, filepath.Join(root, "candidate")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("workflow checkout executed a Git hook")
	}
	nested := filepath.Join(repo, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, nested, "init", "-q")
	fixtureGit(t, nested, "config", "user.email", "fixture@example.test")
	fixtureGit(t, nested, "config", "user.name", "Fixture")
	fixtureWrite(t, filepath.Join(nested, "file"), []byte("nested"))
	fixtureGit(t, nested, "add", ".")
	fixtureGit(t, nested, "-c", "commit.gpgsign=false", "commit", "-qm", "nested")
	if _, err := Capture(context.Background(), repo, filepath.Join(root, "unsupported")); err == nil || !strings.Contains(err.Error(), "gitlinks") {
		t.Fatalf("gitlink not refused: %v", err)
	}
}

func TestConcurrentSnapshotsAndSameNamedWorktrees(t *testing.T) {
	repo, root := fixtureRepo(t)
	const workers = 4
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			parent := filepath.Join(root, fmt.Sprintf("run-%d", i))
			if err := os.Mkdir(parent, 0700); err != nil {
				errors <- err
				return
			}
			s, err := Capture(context.Background(), repo, filepath.Join(parent, "snapshot"))
			if err == nil {
				err = CreateWorktree(context.Background(), s, filepath.Join(parent, "worktree"))
			}
			errors <- err
		}(i)
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRepositoryPathWithTrailingSpaceIsPreserved(t *testing.T) {
	repo, root := fixtureRepo(t)
	spaced := repo + " "
	if err := os.Rename(repo, spaced); err != nil {
		t.Fatal(err)
	}
	s, err := Capture(context.Background(), spaced, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Repository != spaced {
		t.Fatalf("repository path was altered: %q", s.Repository)
	}
}
