package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSnapshotUsageIsReadOnlyAndIncludesTrackedIgnoredAndStagedDeletion(t *testing.T) {
	repo, root := fixtureRepo(t)
	base, err := SnapshotUsage(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, filepath.Join(repo, "new.bin"), []byte{0, 1, 2, 3})
	fixtureWrite(t, filepath.Join(repo, "ignored-secret"), []byte("private"))
	fixtureGit(t, repo, "rm", "--cached", "file.txt") // working file remains selected through HEAD
	objects := fixtureGit(t, repo, "count-objects", "-v")
	index := fixtureGit(t, repo, "diff", "--cached", "--binary")
	usage, err := SnapshotUsage(context.Background(), repo)
	if err != nil || usage.Files != base.Files+1 || usage.Bytes != base.Bytes+4 {
		t.Fatalf("unexpected selection: %+v, base=%+v, err=%v", usage, base, err)
	}
	if fixtureGit(t, repo, "count-objects", "-v") != objects || fixtureGit(t, repo, "diff", "--cached", "--binary") != index {
		t.Fatal("usage inspection wrote Git objects or modified the index")
	}
	fixtureGit(t, repo, "add", "--force", "ignored-secret")
	usage, err = SnapshotUsage(context.Background(), repo)
	if err != nil || usage.Files != base.Files+2 || usage.Bytes != base.Bytes+11 {
		t.Fatalf("tracked ignored content was omitted: %+v %v", usage, err)
	}
	snapshot, err := Capture(context.Background(), repo, filepath.Join(root, "snapshot"))
	if err != nil || snapshot.SelectedFiles != usage.Files || snapshot.SelectedBytes != usage.Bytes {
		t.Fatalf("snapshot accounting differs from exact selection: %+v %+v %v", snapshot, usage, err)
	}
}

func TestSnapshotAdmissionRefusesOversizedContentBeforeGitAdd(t *testing.T) {
	repo, root := fixtureRepo(t)
	large, err := os.Create(filepath.Join(repo, "oversized.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err = large.Truncate(MaxSnapshotBytes + 1); err != nil {
		large.Close()
		t.Fatal(err)
	}
	if err = large.Close(); err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(root, "bin")
	if err = os.Mkdir(shimDir, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "git-add-started")
	script := "#!/usr/bin/env python3\nimport os,pathlib,sys\nif 'add' in sys.argv[1:]:\n pathlib.Path(" + strconv.Quote(marker) + ").write_text('unsafe admission')\n sys.exit(97)\nos.execv(" + strconv.Quote(realGit) + ", [" + strconv.Quote(realGit) + "]+sys.argv[1:])\n"
	shim := filepath.Join(shimDir, "git")
	fixtureWrite(t, shim, []byte(script))
	if err = os.Chmod(shim, 0700); err != nil {
		t.Fatal(err)
	}
	objects := fixtureGit(t, repo, "count-objects", "-v")
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err = Capture(context.Background(), repo, filepath.Join(root, "snapshot")); err == nil || !strings.Contains(err.Error(), "512 MiB") {
		t.Fatalf("oversized source was not refused during admission: %v", err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("git add started before selected-content admission")
	}
	if got := fixtureGit(t, repo, "count-objects", "-v"); got != objects {
		t.Fatal("oversized snapshot wrote objects before refusal")
	}
}

func TestSnapshotSelectionBoundsSymlinksFileCountAndCancellation(t *testing.T) {
	dir := t.TempDir()
	fixtureWrite(t, filepath.Join(dir, "one"), []byte("abc"))
	fixtureWrite(t, filepath.Join(dir, "two"), []byte("de"))
	if err := os.Symlink("destination", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	paths := [][]byte{[]byte("one\x00two\x00link\x00"), []byte("one\x00deleted\x00")}
	usage, err := measureSelectedPaths(context.Background(), dir, paths, 3, 16)
	if err != nil || usage.Files != 3 || usage.Bytes != 16 {
		t.Fatalf("incorrect bounded selection: %+v %v", usage, err)
	}
	if _, err = measureSelectedPaths(context.Background(), dir, paths, 2, 100); err == nil {
		t.Fatal("accepted excessive file count")
	}
	if _, err = measureSelectedPaths(context.Background(), dir, paths, 3, 15); err == nil {
		t.Fatal("symlink target bytes bypassed the content bound")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = measureSelectedPaths(ctx, dir, paths, 3, 100); err != context.Canceled {
		t.Fatalf("selection ignored cancellation: %v", err)
	}
	if _, err = measureSelectedPaths(context.Background(), dir, [][]byte{[]byte("../outside\x00")}, 3, 100); err == nil {
		t.Fatal("accepted a path outside the project")
	}
}
