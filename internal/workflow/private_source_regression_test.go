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
	"testing"
)

// A file transport exercises real promisor objects without depending on an
// external server. The upload-pack wrapper proves transport was available and
// records any attempted lazy retrieval, including an unsuccessful attempt.
func TestPrivateSourcePartialCloneNeverFetchesOrChangesGitMetadata(t *testing.T) {
	origin, root := fixtureRepo(t)
	fixtureGit(t, origin, "config", "uploadpack.allowFilter", "true")
	partial := filepath.Join(root, "partial")
	fixtureGit(t, root, "clone", "--quiet", "--filter=blob:none", "--no-checkout", "file://"+origin, partial)
	if got := fixtureGit(t, partial, "config", "--local", "--get", "remote.origin.promisor"); got != "true" {
		t.Fatalf("fixture is not a promisor clone: %q", got)
	}
	missing := fixtureGit(t, partial, "rev-list", "--objects", "--all", "--missing=print")
	if !strings.Contains(missing, "\n?") {
		t.Fatalf("partial-clone fixture unexpectedly has all blobs: %s", missing)
	}
	uploadPack, err := exec.LookPath("git-upload-pack")
	if err != nil {
		t.Fatal(err)
	}
	transport := filepath.Join(root, "transport-ran")
	wrapper := filepath.Join(root, "upload-pack")
	script := "#!/bin/sh\nprintf 'transport\\n' >> " + shellWord(transport) + "\nexec " + shellWord(uploadPack) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, partial, "config", "remote.origin.uploadpack", wrapper)
	before := privateSourceMetadataWithTimes(t, partial)
	store := filepath.Join(root, "private.git")
	_, err = PrepareSnapshotStorage(context.Background(), StoragePrivate, partial, store)
	if err == nil || !strings.Contains(err.Error(), "partial-clone or promisor") {
		t.Fatalf("private preparation did not refuse unsupported partial source: %v", err)
	}
	var marker privateStoreMarker
	if err := DecodeFile(filepath.Join(store, privateStoreMarkerName), &marker); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), snapshotStorageContextKey{}, &snapshotStorage{Policy: StoragePrivate, Source: partial, Store: store, IgnoreSHA: marker.IgnoreSHA256})
	if _, err := Capture(ctx, partial, filepath.Join(root, "snapshot")); err == nil || !strings.Contains(err.Error(), "partial-clone or promisor") {
		t.Fatalf("private capture did not refuse unsupported partial source: %v", err)
	}
	checkRan := filepath.Join(root, "check-ran")
	checks := []Check{pythonCheck("from pathlib import Path; Path(" + fmt.Sprintf("%q", checkRan) + ").write_text('started')")}
	if v, err := Verify(ctx, partial, checks); err == nil || v.Passed {
		t.Fatalf("private verification accepted unsupported partial source: %+v %v", v, err)
	}
	if _, err := os.Stat(checkRan); !os.IsNotExist(err) {
		t.Fatalf("check started before partial-source admission: %v", err)
	}
	if got := privateSourceMetadataWithTimes(t, partial); got != before {
		t.Fatal("private preparation, capture or verification changed source Git metadata")
	}
	if _, err := os.Stat(transport); !os.IsNotExist(err) {
		t.Fatalf("private source observation invoked remote transport: %v", err)
	}
	// Prove this oracle would detect the original regression, rather than
	// relying on an unreachable remote or an unexecuted wrapper.
	fixtureGit(t, partial, "fetch", "--no-write-fetch-head", "origin")
	if data, err := os.ReadFile(transport); err != nil || !strings.Contains(string(data), "transport") {
		t.Fatalf("transport oracle was not armed: %q %v", data, err)
	}
}

func privateSourceMetadataWithTimes(t *testing.T, repo string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(filepath.Join(repo, ".git"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(repo, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00%d\x00", relative, info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			h.Write(data)
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			h.Write([]byte(target))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func privateTransitionFixture(t *testing.T) (string, string) {
	t.Helper()
	repo, root := fixtureRepo(t)
	for _, dir := range []string{"removed/deeper", "directory"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string]string{"removed/deeper/leaf": "deleted ancestor\n", "file": "old file\n", "directory/child": "old child\n", "head-ignored": "HEAD still selects this\n"} {
		fixtureWrite(t, filepath.Join(repo, path), []byte(data))
	}
	fixtureGit(t, repo, "add", ".")
	fixtureGit(t, repo, "commit", "-qm", "tracked transition inputs")
	return repo, root
}

const privateTransitionScript = "from pathlib import Path; import shutil; shutil.rmtree('removed'); Path('file').unlink(); Path('file').mkdir(); Path('file/new').write_bytes(b'new child\\r\\n'); shutil.rmtree('directory'); Path('directory').write_bytes(b'new regular\\x00file\\n')"

const privateTransitionCheck = "from pathlib import Path; assert not Path('removed').exists(); assert Path('file/new').read_bytes()==b'new child\\r\\n'; assert Path('directory').read_bytes()==b'new regular\\x00file\\n'"

func TestPrivateCaptureAndCheckHandleDeletedAncestorsAndTypeTransitions(t *testing.T) {
	repo, root := privateTransitionFixture(t)
	fixtureWrite(t, filepath.Join(repo, ".gitignore"), []byte("ignored-secret\nhead-ignored\nindex-ignored\n"))
	fixtureGit(t, repo, "rm", "--cached", "head-ignored")
	fixtureWrite(t, filepath.Join(repo, "index-ignored"), []byte("staged bytes\n"))
	fixtureGit(t, repo, "add", "-f", "index-ignored")
	fixtureWrite(t, filepath.Join(repo, "index-ignored"), []byte("working bytes win\r\n"))
	if err := os.MkdirAll(filepath.Join(repo, "index-removed", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, filepath.Join(repo, "index-removed", "nested", "leaf"), []byte("staged addition then removed\n"))
	fixtureGit(t, repo, "add", "index-removed/nested/leaf")
	if err := os.RemoveAll(filepath.Join(repo, "index-removed")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "-c", privateTransitionScript)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture transitions: %v %s", err, out)
	}
	before := privateSourceMetadataWithTimes(t, repo)
	ctx, err := PrepareSnapshotStorage(context.Background(), StoragePrivate, repo, filepath.Join(root, "private.git"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Capture(ctx, repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{".gitignore": "ignored-secret\nhead-ignored\nindex-ignored\n", "file.txt": "base\n", "file/new": "new child\r\n", "directory": "new regular\x00file\n", "head-ignored": "HEAD still selects this\n", "index-ignored": "working bytes win\r\n"}
	var wantBytes int64
	for _, data := range want {
		wantBytes += int64(len(data))
	}
	if s.SelectedFiles != len(want) || s.SelectedBytes != wantBytes {
		t.Fatalf("deleted paths or directory placeholders entered selection bounds: %+v, want %d files / %d bytes", s, len(want), wantBytes)
	}
	v, err := Verify(ctx, repo, []Check{pythonCheck(privateTransitionCheck)})
	if err != nil || !v.Passed || v.Tree != s.Tree || v.ContentSHA256 != s.ContentSHA256 {
		t.Fatalf("transition check did not bind the captured raw state: %+v %v", v, err)
	}
	fork := filepath.Join(root, "fork")
	if err := CreateWorktree(ctx, s, fork); err != nil {
		t.Fatal(err)
	}
	for path, data := range want {
		if got, err := os.ReadFile(filepath.Join(fork, path)); err != nil || string(got) != data {
			t.Fatalf("snapshot lost current selected bytes for %s: %q %v", path, got, err)
		}
	}
	for _, path := range []string{"removed", "index-removed"} {
		if _, err := os.Stat(filepath.Join(fork, path)); !os.IsNotExist(err) {
			t.Fatalf("deleted selected ancestor %s appeared in fork: %v", path, err)
		}
	}
	if got := privateSourceMetadataWithTimes(t, repo); got != before {
		t.Fatal("transition capture/check changed source HEAD, index or other Git metadata")
	}
}

func TestPrivateRunAndApplyHandleDeletedAncestorsAndTypeTransitions(t *testing.T) {
	repo, root := privateTransitionFixture(t)
	// The persistent lock is the only intentional source metadata artifact.
	fixtureWrite(t, filepath.Join(repo, ".git", "agentmux-apply.lock"), nil)
	before := privateSourceMetadata(t, repo)
	p := comparePlan(repo, pythonCandidate("transition", privateTransitionScript))
	p.Storage = StoragePrivate
	p.Checks = []Check{pythonCheck(privateTransitionCheck)}
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), false)
	if err != nil || len(r.Candidates) != 1 || r.Candidates[0].State != "verified" {
		t.Fatalf("transition run: %+v %v", r, err)
	}
	if privateSourceMetadata(t, repo) != before {
		t.Fatal("transition run changed source Git metadata")
	}
	result, err := Apply(context.Background(), r.Output, "transition")
	if err != nil || !result.Applied || result.Uncertain {
		t.Fatalf("transition apply: %+v %v", result, err)
	}
	cmd := exec.Command("python3", "-c", privateTransitionCheck)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("applied transition bytes: %v %s", err, out)
	}
	if privateSourceMetadata(t, repo) != before {
		t.Fatal("transition apply changed source objects, HEAD, refs or index")
	}
}
