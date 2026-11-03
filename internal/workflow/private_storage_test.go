package workflow

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateStoreRejectsObjectDirectorySwapAtGitLaunch(t *testing.T) {
	ctx, repo, root, baseline := preparePrivateFixture(t)
	candidate := filepath.Join(root, "candidate")
	if err := CreateWorktree(ctx, baseline, candidate); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(candidate, ".git")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "wrapper")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	wrapper := "#!/bin/sh\nfor arg in \"$@\"; do\n if [ \"$arg\" = fast-import ] && [ ! -e \"$PRIVATE_TEST_STORE/objects-before\" ]; then\n mv \"$PRIVATE_TEST_STORE/objects\" \"$PRIVATE_TEST_STORE/objects-before\" || exit 90\n ln -s \"$PRIVATE_TEST_SOURCE/.git/objects\" \"$PRIVATE_TEST_STORE/objects\" || exit 91\n fi\ndone\nexec \"$PRIVATE_TEST_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRIVATE_TEST_STORE", store)
	t.Setenv("PRIVATE_TEST_SOURCE", repo)
	t.Setenv("PRIVATE_TEST_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	fixtureWrite(t, filepath.Join(candidate, "file.txt"), []byte("new candidate bytes absent from source object database\n"))
	before := privateSourceMetadata(t, repo)
	if _, _, err := contentState(ctx, candidate); err == nil {
		t.Fatal("metadata swap published a successful fingerprint")
	}
	if after := privateSourceMetadata(t, repo); after != before {
		t.Fatal("controller Git wrote through replaced objects directory into source metadata")
	}
}

func TestPrivateApplyPreservesReviewedWhitespaceDespiteSourceConfig(t *testing.T) {
	repo, root := fixtureRepo(t)
	fixtureGit(t, repo, "config", "apply.whitespace", "fix")
	p := comparePlan(repo, pythonCandidate("fix", "from pathlib import Path; Path('file.txt').write_bytes(b'checked trailing whitespace '+bytes([10])); print('changed')"))
	p.Checks = []Check{pythonCheck("from pathlib import Path; assert Path('file.txt').read_bytes()==b'checked trailing whitespace '+bytes([10])")}
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(context.Background(), r.Output, "fix")
	if err != nil || !result.Applied || result.Uncertain {
		t.Fatalf("private raw apply: %+v %v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "file.txt"))
	if err != nil || string(data) != "checked trailing whitespace \n" {
		t.Fatalf("apply changed reviewed bytes: %q %v", data, err)
	}
}

func privateSourceMetadata(t *testing.T, repo string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(filepath.Join(repo, ".git"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(repo, path)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", relative, info.Mode())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			h.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func preparePrivateFixture(t *testing.T) (context.Context, string, string, Snapshot) {
	t.Helper()
	repo, root := fixtureRepo(t)
	ctx, err := PrepareSnapshotStorage(context.Background(), StoragePrivate, repo, filepath.Join(root, "private.git"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Capture(ctx, repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	return ctx, repo, root, s
}

func TestPrivateSnapshotsLeaveSourceGitUntouchedAndForkIndependentCandidates(t *testing.T) {
	repo, root := fixtureRepo(t)
	fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("staged\n"))
	fixtureGit(t, repo, "add", "file.txt")
	fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("working\r\n"))
	fixtureWrite(t, filepath.Join(repo, "new.bin"), []byte{0, 1, 2, 255})
	fixtureWrite(t, filepath.Join(repo, "ignored-secret"), []byte("not selected"))
	marker := filepath.Join(root, "filter-ran")
	fixtureWrite(t, filepath.Join(repo, ".gitattributes"), []byte("file.txt text eol=lf filter=spy\n"))
	fixtureGit(t, repo, "config", "filter.spy.clean", "touch "+shellWord(marker))
	fixtureGit(t, repo, "config", "filter.spy.required", "true")
	before := privateSourceMetadata(t, repo)
	ctx, err := PrepareSnapshotStorage(context.Background(), StoragePrivate, repo, filepath.Join(root, "private.git"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Capture(ctx, repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Store != filepath.Join(root, "private.git") || s.SelectedFiles < 3 {
		t.Fatalf("private snapshot metadata: %+v", s)
	}
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, dest := range []string{first, second} {
		if err = CreateWorktree(ctx, s, dest); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dest, "file.txt"))
		if err != nil || string(data) != "working\r\n" {
			t.Fatalf("raw checkout bytes changed: %q %v", data, err)
		}
		if _, err = os.Stat(filepath.Join(dest, "ignored-secret")); !os.IsNotExist(err) {
			t.Fatal("ignored secret copied")
		}
	}
	fixtureWrite(t, filepath.Join(first, "candidate-only-secret"), []byte("private candidate value"))
	final, err := Capture(ctx, first, filepath.Join(root, "first-final"))
	if err != nil {
		t.Fatal(err)
	}
	if final.Store != filepath.Join(first, ".git") {
		t.Fatalf("candidate shares baseline or rival store: %+v", final)
	}
	object, err := privateCommandOutput(ctx, final.Store, privateGitArgs(final.Store), nil, "rev-parse", final.Tree+":candidate-only-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = privateCommandOutput(ctx, filepath.Join(second, ".git"), privateGitArgs(filepath.Join(second, ".git")), nil, "cat-file", "-e", strings.TrimSpace(string(object))); err == nil {
		t.Fatal("rival candidate can resolve another candidate's new object")
	}
	if got := privateSourceMetadata(t, repo); got != before {
		t.Fatal("private capture/worktree/final fingerprint changed source Git metadata")
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("source clean filter was executed")
	}
	for _, store := range []string{s.Store, final.Store, filepath.Join(second, ".git")} {
		info, err := os.Stat(store)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("private store permissions: %s %v %v", store, info, err)
		}
		if _, err = os.Lstat(filepath.Join(store, "objects", "info", "alternates")); !os.IsNotExist(err) {
			t.Fatal("private store depends on object alternates")
		}
	}
}

func TestPrivateSnapshotSurvivesSourceGCAndSourceRemoval(t *testing.T) {
	ctx, repo, root, s := preparePrivateFixture(t)
	fixtureGit(t, repo, "gc", "--prune=now")
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "after-source-removal")
	if err := CreateWorktree(ctx, s, dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "file.txt"))
	if err != nil || string(data) != "base\n" {
		t.Fatalf("snapshot depended on source objects: %q %v", data, err)
	}
}

func TestPrivateSnapshotRejectsSymlinkedAncestorsWithoutCopyingOutsideData(t *testing.T) {
	repo, root := fixtureRepo(t)
	if err := os.Mkdir(filepath.Join(repo, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, filepath.Join(repo, "dir", "value"), []byte("inside"))
	fixtureGit(t, repo, "add", "dir/value")
	fixtureGit(t, repo, "commit", "-qm", "nested")
	ctx, err := PrepareSnapshotStorage(context.Background(), StoragePrivate, repo, filepath.Join(root, "private.git"))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err = os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	secret := []byte("outside private material never selected")
	fixtureWrite(t, filepath.Join(outside, "value"), secret)
	if err = os.RemoveAll(filepath.Join(repo, "dir")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(repo, "dir")); err != nil {
		t.Fatal(err)
	}
	if _, err = Capture(ctx, repo, filepath.Join(root, "bad-snapshot")); err == nil {
		t.Fatal("private capture followed an outside ancestor symlink")
	}
	header := []byte(fmt.Sprintf("blob %d\x00", len(secret)))
	sum := sha1.Sum(append(header, secret...))
	oid := hex.EncodeToString(sum[:])
	if _, err = privateCommandOutput(ctx, filepath.Join(root, "private.git"), privateGitArgs(filepath.Join(root, "private.git")), nil, "cat-file", "-e", oid); err == nil {
		t.Fatal("outside data was imported before refusing the path")
	}
}

func TestPrivateStoreRejectsRedirectedMetadataBeforeControllerGit(t *testing.T) {
	for _, kind := range []string{"include", "worktree", "alternates", "object-link", "object-hardlink", "attributes"} {
		t.Run(kind, func(t *testing.T) {
			ctx, repo, root, s := preparePrivateFixture(t)
			switch kind {
			case "include":
				f, err := os.OpenFile(filepath.Join(s.Store, "config"), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.WriteString("\n[include]\n path = " + filepath.Join(root, "outside-config") + "\n")
				_ = f.Close()
			case "worktree":
				f, err := os.OpenFile(filepath.Join(s.Store, "config"), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = f.WriteString("\n[core]\n worktree = " + root + "\n")
				_ = f.Close()
			case "alternates":
				fixtureWrite(t, filepath.Join(s.Store, "objects", "info", "alternates"), []byte(filepath.Join(repo, ".git", "objects")+"\n"))
			case "object-link":
				if err := os.Symlink(filepath.Join(repo, ".git", "objects"), filepath.Join(s.Store, "objects", "outside")); err != nil {
					t.Fatal(err)
				}
			case "object-hardlink":
				source := filepath.Join(root, "outside-object")
				fixtureWrite(t, source, []byte("outside"))
				if err := os.Link(source, filepath.Join(s.Store, "objects", "outside")); err != nil {
					t.Fatal(err)
				}
			case "attributes":
				fixtureWrite(t, filepath.Join(s.Store, "info", "attributes"), []byte("* filter=outside\n"))
			}
			if _, err := SnapshotContext(ctx, s); err == nil {
				t.Fatal("redirected private metadata accepted")
			}
		})
	}
}

func TestPrivateRunAndApplyKeepSourceObjectsRefsAndStagingUntouched(t *testing.T) {
	repo, root := fixtureRepo(t)
	fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("staged owner work\n"))
	fixtureGit(t, repo, "add", "file.txt")
	fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("working owner work\n"))
	indexBefore, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	objectsBefore := fixtureGit(t, repo, "count-objects", "-v")
	refsBefore := fixtureGit(t, repo, "show-ref")
	headBefore := fixtureGit(t, repo, "rev-parse", "HEAD")
	metadataBefore := privateSourceMetadata(t, repo)
	p := comparePlan(repo, pythonCandidate("fix", "from pathlib import Path; Path('file.txt').write_text('fixed\\n'); print('changed')"))
	p.Storage = StoragePrivate
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), false)
	if err != nil {
		t.Fatalf("private run: %+v %v", r, err)
	}
	if r.Storage != StoragePrivate || r.Snapshot.Store != filepath.Join(r.Output, "git") || r.Candidates[0].FinalSnapshot.Store != filepath.Join(r.Candidates[0].Worktree, ".git") {
		t.Fatalf("private run stores: %+v", r)
	}
	if privateSourceMetadata(t, repo) != metadataBefore {
		t.Fatal("private run changed source Git metadata")
	}
	result, err := Apply(context.Background(), r.Output, "fix")
	if err != nil || !result.Applied || result.Uncertain {
		t.Fatalf("private apply: %+v %v", result, err)
	}
	if objects := fixtureGit(t, repo, "count-objects", "-v"); objects != objectsBefore {
		t.Fatalf("private apply created source objects: before=%s after=%s", objectsBefore, objects)
	}
	if refs := fixtureGit(t, repo, "show-ref"); refs != refsBefore {
		t.Fatal("private apply changed source refs")
	}
	if fixtureGit(t, repo, "rev-parse", "HEAD") != headBefore {
		t.Fatal("private apply changed source HEAD")
	}
	indexAfter, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil || string(indexAfter) != string(indexBefore) {
		t.Fatal("private apply changed staging")
	}
	data, err := os.ReadFile(filepath.Join(repo, "file.txt"))
	if err != nil || string(data) != "fixed\n" {
		t.Fatalf("private patch content: %q %v", data, err)
	}
	inspection, err := InspectApply(context.Background(), r.Output, "fix")
	if err != nil || inspection.State != "applied" {
		t.Fatalf("private receipt: %+v %v", inspection, err)
	}
}

func TestPrivateConcurrentFingerprintingKeepsExactIndependentEvidence(t *testing.T) {
	ctx, repo, _, s := preparePrivateFixture(t)
	type result struct {
		tree, raw string
		err       error
	}
	results := make(chan result, 8)
	for i := 0; i < 8; i++ {
		go func() { tree, raw, err := contentState(ctx, repo); results <- result{tree, raw, err} }()
	}
	for i := 0; i < 8; i++ {
		v := <-results
		if v.err != nil || v.tree != s.Tree || v.raw != s.ContentSHA256 {
			t.Fatalf("concurrent private fingerprint drift: %+v", v)
		}
	}
}

func TestPrivateSnapshotsPreserveLiteralPathsAndDisableCheckoutConversions(t *testing.T) {
	repo, root := fixtureRepo(t)
	names := []string{"literal[1].txt", ":(exclude)odd", "line\nbreak.txt", ".hidden.txt", "nested/value.txt", "non-ascii-λ.txt"}
	if err := os.Mkdir(filepath.Join(repo, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		fixtureWrite(t, filepath.Join(repo, name), []byte("$Id$ "+name+"\r\n"))
	}
	fixtureWrite(t, filepath.Join(repo, ".gitattributes"), []byte("* text eol=lf ident working-tree-encoding=UTF-16LE filter=unavailable\n"))
	ctx, err := PrepareSnapshotStorage(context.Background(), StoragePrivate, repo, filepath.Join(root, "private.git"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Capture(ctx, repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "fork")
	if err = CreateWorktree(ctx, s, dest); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || string(data) != "$Id$ "+name+"\r\n" {
			t.Fatalf("private literal path/conversion drift %q: %q %v", name, data, err)
		}
	}
}

func TestPrivateSHA256RepositoryUsesIndependentMatchingObjectFormat(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, repo, "init", "--object-format=sha256", "-q")
	fixtureGit(t, repo, "config", "user.name", "Fixture")
	fixtureGit(t, repo, "config", "user.email", "fixture@example.invalid")
	fixtureGit(t, repo, "config", "commit.gpgsign", "false")
	fixtureWrite(t, filepath.Join(repo, "value"), []byte("raw\r\n"))
	fixtureGit(t, repo, "add", "value")
	fixtureGit(t, repo, "commit", "-qm", "base")
	before := privateSourceMetadata(t, repo)
	ctx, err := PrepareSnapshotStorage(context.Background(), StoragePrivate, repo, filepath.Join(root, "private.git"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Capture(ctx, repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tree) != 64 || len(s.Commit) != 64 {
		t.Fatalf("private object format changed: %+v", s)
	}
	if err = CreateWorktree(ctx, s, filepath.Join(root, "fork")); err != nil {
		t.Fatal(err)
	}
	if privateSourceMetadata(t, repo) != before {
		t.Fatal("SHA256 capture changed source metadata")
	}
}

func TestPrivateForkPinsGlobalAndLocalIgnoreRulesWithProjectPrecedence(t *testing.T) {
	repo, root := fixtureRepo(t)
	home := filepath.Join(root, "owner-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(root, "global-ignore")
	fixtureWrite(t, global, []byte("global-output\nproject-override\n"))
	fixtureWrite(t, filepath.Join(home, ".gitconfig"), []byte("[core]\n excludesfile = "+global+"\n"))
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	fixtureWrite(t, filepath.Join(repo, ".git", "info", "exclude"), []byte("local-output\n"))
	fixtureWrite(t, filepath.Join(repo, ".gitignore"), []byte("ignored-secret\n!project-override\n"))
	fixtureWrite(t, filepath.Join(repo, "global-output"), []byte("ignored global data"))
	fixtureWrite(t, filepath.Join(repo, "local-output"), []byte("ignored local data"))
	fixtureWrite(t, filepath.Join(repo, "project-override"), []byte("must be selected"))
	ctx, err := PrepareSnapshotStorage(context.Background(), StoragePrivate, repo, filepath.Join(root, "private.git"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Capture(ctx, repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "fork")
	if err = CreateWorktree(ctx, s, dest); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"global-output", "local-output"} {
		if _, err := os.Stat(filepath.Join(dest, name)); !os.IsNotExist(err) {
			t.Fatalf("ignored source file %s copied", name)
		}
	}
	if data, err := os.ReadFile(filepath.Join(dest, "project-override")); err != nil || string(data) != "must be selected" {
		t.Fatalf("project negation lost precedence: %q %v", data, err)
	}
	// A private HOME must not change the controller's selected ignore policy.
	t.Setenv("HOME", filepath.Join(root, "unavailable-home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "unavailable-xdg"))
	v, err := Verify(ctx, dest, []Check{pythonCheck("from pathlib import Path; Path('global-output').write_text('build'); Path('local-output').write_text('build')")})
	if err != nil || !v.Passed {
		t.Fatalf("pinned ignored build output invalidated verification: %+v %v", v, err)
	}
	markerPath := filepath.Join(dest, ".git", privateStoreMarkerName)
	var marker privateStoreMarker
	if err = DecodeFile(markerPath, &marker); err != nil {
		t.Fatal(err)
	}
	// Even a self-consistent rewrite of worker-owned metadata cannot replace
	// the ignore identity recorded in the controller's original snapshot.
	if _, err = setPrivateIgnorePolicy(ctx, filepath.Join(dest, ".git"), []byte("hidden-new-file\n")); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, filepath.Join(dest, "hidden-new-file"), []byte("must not silently disappear"))
	if _, err = Capture(ctx, dest, filepath.Join(root, "tampered")); err == nil {
		t.Fatal("worker replaced the pinned ignore policy")
	}
}
