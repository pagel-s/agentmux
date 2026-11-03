package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAdversarialCaptureIncludesStagedNowIgnoredFile(t *testing.T) {
	repo, root := fixtureRepo(t)
	fixtureWrite(t, filepath.Join(repo, "new.txt"), []byte("selected and staged\n"))
	fixtureGit(t, repo, "add", "new.txt")
	fixtureWrite(t, filepath.Join(repo, ".gitignore"), []byte("ignored-secret\nnew.txt\n"))
	s, err := Capture(context.Background(), repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "candidate")
	if err = CreateWorktree(context.Background(), s, wt); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(wt, "new.txt"))
	if err != nil || string(b) != "selected and staged\n" {
		t.Fatalf("staged file silently omitted: %q %v", b, err)
	}
}

func TestAdversarialCapturePreservesLiteralTrackedPathnames(t *testing.T) {
	repo, root := fixtureRepo(t)
	for _, name := range []string{"literal[1].txt", ":(exclude)odd", "line\nbreak.txt"} {
		fixtureWrite(t, filepath.Join(repo, name), []byte(name))
		fixtureGit(t, repo, "--literal-pathspecs", "add", "--", name)
	}
	s, err := Capture(context.Background(), repo, filepath.Join(root, "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "candidate")
	if err = CreateWorktree(context.Background(), s, wt); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"literal[1].txt", ":(exclude)odd", "line\nbreak.txt"} {
		b, err := os.ReadFile(filepath.Join(wt, name))
		if err != nil || string(b) != name {
			t.Fatalf("literal path changed %q: %q %v", name, b, err)
		}
	}
}

func TestAdversarialCancelledGitKillsFilterDescendants(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix process group guarantee")
	}
	repo, root := fixtureRepo(t)
	pids := filepath.Join(root, "pids")
	marker := filepath.Join(root, "escaped")
	script := filepath.Join(root, "filter.py")
	code := "import os, pathlib, subprocess, sys, time\np = subprocess.Popen([sys.executable, '-c', " + strconv.Quote("import pathlib,time; time.sleep(1); pathlib.Path("+strconv.Quote(marker)+").write_text('escaped')") + "])\npathlib.Path(" + strconv.Quote(pids) + ").write_text(str(os.getpid())+' '+str(p.pid))\ntime.sleep(60)\n"
	fixtureWrite(t, script, []byte(code))
	fixtureWrite(t, filepath.Join(repo, ".gitattributes"), []byte("file.txt filter=slow\n"))
	fixtureGit(t, repo, "config", "filter.slow.clean", "python3 "+shellWord(script))
	fixtureGit(t, repo, "config", "filter.slow.required", "true")
	t.Cleanup(func() {
		data, _ := os.ReadFile(pids)
		for _, field := range strings.Fields(string(data)) {
			pid, _ := strconv.Atoi(field)
			if pid > 0 {
				p, _ := os.FindProcess(pid)
				if p != nil {
					_ = p.Kill()
				}
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Capture(ctx, repo, filepath.Join(root, "snapshot")); err == nil {
		t.Fatal("cancelled filter capture succeeded")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("Git descendant cancellation exceeded bound")
	}
	if _, err := os.Stat(pids); err != nil {
		t.Fatalf("filter never reached descendant setup: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Git filter descendant survived cancellation")
	}
}

func TestAdversarialVerificationDetectsEOLBytes(t *testing.T) {
	repo, _ := fixtureRepo(t)
	fixtureWrite(t, filepath.Join(repo, ".gitattributes"), []byte("file.txt text\n"))
	fixtureGit(t, repo, "add", ".gitattributes")
	fixtureGit(t, repo, "commit", "-qm", "attributes")
	v, err := Verify(context.Background(), repo, []Check{{Name: "check", Command: []string{"/bin/true"}}})
	if err != nil || !v.Passed {
		t.Fatalf("verify: %+v %v", v, err)
	}
	fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("base\r\n"))
	if fresh, err := IsFresh(context.Background(), repo, v); err != nil || fresh {
		t.Fatalf("byte-changed verification was fresh: %v %v", fresh, err)
	}
}

func TestAdversarialCheckMutationCannotHideBehindEOLNormalization(t *testing.T) {
	repo, _ := fixtureRepo(t)
	fixtureWrite(t, filepath.Join(repo, ".gitattributes"), []byte("file.txt text\n"))
	fixtureGit(t, repo, "add", ".gitattributes")
	fixtureGit(t, repo, "commit", "-qm", "attributes")
	v, err := Verify(context.Background(), repo, []Check{{Name: "mutation", Command: []string{"python3", "-c", "from pathlib import Path; Path('file.txt').write_bytes(b'base\\r\\n')"}}})
	if err != nil || v.Passed || v.ContentSHA256 == v.EndingContentSHA256 {
		t.Fatalf("normalized mutation accepted: %+v %v", v, err)
	}
}

func TestAdversarialApplyPreviewRejectsNormalizedResult(t *testing.T) {
	repo, root := fixtureRepo(t)
	fixtureWrite(t, filepath.Join(repo, ".gitattributes"), []byte("file.txt text\n"))
	fixtureGit(t, repo, "add", ".gitattributes")
	fixtureGit(t, repo, "commit", "-qm", "attributes")
	p := Plan{Version: 1, Repository: repo, Execution: "local", Goal: "Write fixed CRLF", TimeoutSeconds: 30, Candidates: []Candidate{{ID: "fix", Harness: "command", Command: []string{"python3", "-c", "from pathlib import Path; Path('file.txt').write_bytes(b'fixed\\r\\n')"}}}, Checks: []Check{{Name: "bytes", Command: []string{"python3", "-c", "from pathlib import Path; assert Path('file.txt').read_bytes() == b'fixed\\r\\n'"}}}}
	p.Storage = StorageShared // Legacy conversions must still be refused.
	dir := filepath.Join(root, "run")
	if _, err := Run(context.Background(), p, dir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(context.Background(), dir, "fix"); err == nil || !strings.Contains(err.Error(), "exact bytes") {
		t.Fatalf("normalized integration accepted: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(repo, "file.txt"))
	if err != nil || string(b) != "base\n" {
		t.Fatalf("source changed: %q %v", b, err)
	}
}

func TestAdversarialApplyReportsConcurrentPostApplyMutationAsUncertain(t *testing.T) {
	dir, report := evidenceRun(t)
	root := filepath.Dir(dir)
	shimDir := filepath.Join(root, "shim")
	if err := os.Mkdir(shimDir, 0700); err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env python3\nimport pathlib, subprocess, sys\nargs=sys.argv[1:]\nresult=subprocess.run([" + strconv.Quote(realGit) + "]+args)\nif result.returncode == 0 and 'apply' in args and '--check' not in args and '-C' in args and args[args.index('-C')+1] == " + strconv.Quote(report.Repository) + ":\n pathlib.Path(" + strconv.Quote(filepath.Join(report.Repository, "concurrent.txt")) + ").write_text('concurrent edit')\nsys.exit(result.returncode)\n"
	shim := filepath.Join(shimDir, "git")
	fixtureWrite(t, shim, []byte(script))
	if err = os.Chmod(shim, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	result, err := Apply(context.Background(), dir, "fix")
	if err == nil || !result.Applied || !result.Uncertain {
		t.Fatalf("concurrent source edit was called verified: %+v %v", result, err)
	}
	b, readErr := os.ReadFile(filepath.Join(report.Repository, "file.txt"))
	if readErr != nil || string(b) != "fixed\n" {
		t.Fatalf("applied changes not retained: %q %v", b, readErr)
	}
}
