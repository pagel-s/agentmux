package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateSelectionKeepsPinnedIgnoresAfterAmbientChanges(t *testing.T) {
	repo, root := fixtureRepo(t)
	home := filepath.Join(root, "owner-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(root, "global-ignore")
	fixtureWrite(t, global, []byte("global-secret\nproject-override\n"))
	fixtureWrite(t, filepath.Join(home, ".gitconfig"), []byte("[core]\n excludesfile = "+global+"\n"))
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	fixtureWrite(t, filepath.Join(repo, ".git", "info", "exclude"), []byte("local-secret\nstaged-ignored\n"))
	fixtureWrite(t, filepath.Join(repo, ".gitignore"), []byte("project-secret\n!project-override\n"))
	for _, name := range []string{"global-secret", "local-secret", "project-secret", "project-override", "staged-ignored"} {
		fixtureWrite(t, filepath.Join(repo, name), []byte(name+"\n"))
	}
	fixtureGit(t, repo, "add", "-f", "staged-ignored")
	ctx, err := PrepareSnapshotStorage(context.Background(), StoragePrivate, repo, filepath.Join(root, "private.git"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Capture(ctx, repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	fork := filepath.Join(root, "fork")
	if err = CreateWorktree(ctx, s, fork); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"global-secret", "local-secret", "project-secret"} {
		if _, err := os.Stat(filepath.Join(fork, name)); !os.IsNotExist(err) {
			t.Fatalf("excluded owner file %s entered private worktree: %v", name, err)
		}
	}
	for _, name := range []string{"project-override", "staged-ignored"} {
		if data, err := os.ReadFile(filepath.Join(fork, name)); err != nil || string(data) != name+"\n" {
			t.Fatalf("selected path %s missing: %q %v", name, data, err)
		}
	}
	type state struct{ tree, raw string }
	before := map[string]state{}
	for _, repository := range []string{repo, fork} {
		fixtureWrite(t, filepath.Join(repository, "later-file"), []byte("must remain selected by the pinned policy\n"))
		tree, raw, err := contentState(ctx, repository)
		if err != nil {
			t.Fatal(err)
		}
		storage, err := privateStorageForRepository(ctx, repository)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = privateCommandOutput(ctx, storage.Store, privateGitArgs(storage.Store), nil, "cat-file", "-e", tree+":later-file"); err != nil {
			t.Fatalf("new file absent from private fingerprint: %v", err)
		}
		before[repository] = state{tree, raw}
	}
	// Neither new global rules nor removed source-local rules may replace the
	// policy captured before untrusted work began.
	fixtureWrite(t, global, []byte("later-file\n"))
	fixtureWrite(t, filepath.Join(repo, ".git", "info", "exclude"), []byte("later-file\n"))
	assertUnchanged := func() {
		t.Helper()
		for _, repository := range []string{repo, fork} {
			tree, raw, err := contentState(ctx, repository)
			if err != nil {
				t.Fatal(err)
			}
			if got := (state{tree, raw}); got != before[repository] {
				t.Fatalf("ambient ignore drift changed selection for %s: before=%+v after=%+v", repository, before[repository], got)
			}
		}
	}
	assertUnchanged()
	otherHome := filepath.Join(root, "different-home")
	if err := os.Mkdir(otherHome, 0700); err != nil {
		t.Fatal(err)
	}
	otherIgnore := filepath.Join(root, "different-ignore")
	fixtureWrite(t, otherIgnore, []byte("later-file\nproject-override\nstaged-ignored\n"))
	fixtureWrite(t, filepath.Join(otherHome, ".gitconfig"), []byte("[core]\n excludesfile = "+otherIgnore+"\n"))
	t.Setenv("HOME", otherHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(otherHome, ".config"))
	assertUnchanged()
}

func TestPrivateSourceSelectionDoesNotAdoptLaterLocalExcludes(t *testing.T) {
	repo, root := fixtureRepo(t)
	fixtureWrite(t, filepath.Join(repo, ".git", "info", "exclude"), []byte("owner-ignored-secret\n"))
	fixtureWrite(t, filepath.Join(repo, "owner-ignored-secret"), []byte("must remain outside retained snapshots\n"))
	ctx, err := PrepareSnapshotStorage(context.Background(), StoragePrivate, repo, filepath.Join(root, "private.git"))
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, filepath.Join(repo, ".git", "info", "exclude"), nil)
	s, err := Capture(ctx, repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	fork := filepath.Join(root, "fork")
	if err = CreateWorktree(ctx, s, fork); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fork, "owner-ignored-secret")); !os.IsNotExist(err) {
		t.Fatalf("removed source info/exclude copied owner secret: %v", err)
	}
}

func TestWorkflowIsolationRealPinnedSelectionSurvivesExternalIgnoreChanges(t *testing.T) {
	requireFullWorkflowIsolation(t)
	repo, root := fixtureRepo(t)
	home := filepath.Join(root, "owner-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(home, "ignore")
	fixtureWrite(t, global, []byte("global-owner-secret\n"))
	fixtureWrite(t, filepath.Join(home, ".gitconfig"), []byte("[core]\n excludesfile = "+global+"\n"))
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	fixtureWrite(t, filepath.Join(repo, ".git", "info", "exclude"), []byte("local-owner-secret\n"))
	fixtureWrite(t, filepath.Join(repo, "global-owner-secret"), []byte("globally ignored owner bytes\n"))
	fixtureWrite(t, filepath.Join(repo, "local-owner-secret"), []byte("locally ignored owner bytes\n"))
	p := comparePlan(repo, pythonCandidate("writer", "from pathlib import Path; assert not Path('global-owner-secret').exists(); assert not Path('local-owner-secret').exists(); Path('file.txt').write_text('fixed\\n')"))
	p.Candidates[0].Command[0] = "/usr/bin/python3"
	p.Checks[0].Command[0] = "/usr/bin/python3"
	p.Storage = StoragePrivate
	p.Isolation = &IsolationOptions{}
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), true)
	if err != nil || len(r.Candidates) != 1 || r.Candidates[0].State != "verified" {
		t.Fatalf("isolated run failed: %+v %v", r, err)
	}
	candidate := r.Candidates[0]
	fixtureWrite(t, global, []byte("later-file\n"))
	fixtureWrite(t, filepath.Join(repo, ".git", "info", "exclude"), nil)
	later := filepath.Join(candidate.Worktree, "later-file")
	fixtureWrite(t, later, []byte("new candidate evidence cannot be hidden by a later global rule\n"))
	if fresh, err := IsFresh(context.Background(), candidate.Worktree, *candidate.Verification); err != nil || fresh {
		t.Fatalf("external ignore hid changed candidate evidence: %v %v", fresh, err)
	}
	if err := os.Remove(later); err != nil {
		t.Fatal(err)
	}
	otherHome := filepath.Join(root, "different-home")
	if err := os.Mkdir(otherHome, 0700); err != nil {
		t.Fatal(err)
	}
	otherIgnore := filepath.Join(otherHome, "ignore")
	fixtureWrite(t, otherIgnore, []byte("later-file\n"))
	fixtureWrite(t, filepath.Join(otherHome, ".gitconfig"), []byte("[core]\n excludesfile = "+otherIgnore+"\n"))
	t.Setenv("HOME", otherHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(otherHome, ".config"))
	if fresh, err := IsFresh(context.Background(), candidate.Worktree, *candidate.Verification); err != nil || !fresh {
		t.Fatalf("external policy change invalidated unchanged pinned evidence: %v %v", fresh, err)
	}
	// The application lock is the one intentional source metadata artifact.
	fixtureWrite(t, filepath.Join(repo, ".git", "agentmux-apply.lock"), nil)
	before := privateSourceMetadata(t, repo)
	sourceLater := filepath.Join(repo, "later-file")
	fixtureWrite(t, sourceLater, []byte("new source evidence cannot be hidden by a later global rule\n"))
	if applied, err := Apply(context.Background(), r.Output, "writer"); err == nil || applied.Applied {
		t.Fatalf("external ignore hid source drift before application: %+v %v", applied, err)
	}
	if data, err := os.ReadFile(filepath.Join(repo, "file.txt")); err != nil || string(data) != "base\n" {
		t.Fatalf("source drift refusal modified application target: %q %v", data, err)
	}
	if err := os.Remove(sourceLater); err != nil {
		t.Fatal(err)
	}
	if applied, err := Apply(context.Background(), r.Output, "writer"); err != nil || !applied.Applied || applied.Uncertain {
		t.Fatalf("pinned source application after external policy changes: %+v %v", applied, err)
	}
	if privateSourceMetadata(t, repo) != before {
		t.Fatal("pinned application changed source Git metadata")
	}
}
