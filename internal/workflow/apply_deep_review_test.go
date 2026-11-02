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

func installApplyHEADMutation(t *testing.T, report Report, atSource bool) string {
	t.Helper()
	root := filepath.Dir(report.Output)
	shimDir := filepath.Join(root, "head-shim")
	if err := os.Mkdir(shimDir, 0700); err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "head-moved")
	script := "#!/usr/bin/env python3\nimport pathlib, subprocess, sys\nargs=sys.argv[1:]\nresult=subprocess.run([" + strconv.Quote(realGit) + "]+args)\nsource=" + strconv.Quote(report.Repository) + "\nmarker=pathlib.Path(" + strconv.Quote(marker) + ")\n"
	script += "if result.returncode == 0 and 'apply' in args and '--check' not in args and '-C' in args and not marker.exists():\n repo=args[args.index('-C')+1]\n"
	condition := "repo != source"
	if atSource {
		condition = "repo == source"
	}
	script += " if " + condition + ":\n  subprocess.run([" + strconv.Quote(realGit) + ", '-C', source, '-c', 'core.hooksPath=/dev/null', '-c', 'commit.gpgsign=false', 'commit', '--allow-empty', '-qm', 'concurrent commit'], check=True)\n  marker.write_text('moved')\nsys.exit(result.returncode)\n"
	shim := filepath.Join(shimDir, "git")
	fixtureWrite(t, shim, []byte(script))
	if err = os.Chmod(shim, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

func TestApplyRejectsHEADChangedDuringPreview(t *testing.T) {
	dir, report := evidenceRun(t)
	marker := installApplyHEADMutation(t, report, false)
	result, err := Apply(context.Background(), dir, "fix")
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("fixture did not move HEAD: %v", statErr)
	}
	if err == nil || result.Applied || !strings.Contains(err.Error(), "HEAD changed") {
		t.Fatalf("applied onto a different source HEAD: %+v %v", result, err)
	}
	data, readErr := os.ReadFile(filepath.Join(report.Repository, "file.txt"))
	if readErr != nil || string(data) != "base\n" {
		t.Fatalf("source content changed after stale HEAD: %q %v", data, readErr)
	}
}

func TestApplyMarksPostApplyHEADChangeUncertain(t *testing.T) {
	dir, report := evidenceRun(t)
	marker := installApplyHEADMutation(t, report, true)
	result, err := Apply(context.Background(), dir, "fix")
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("fixture did not move HEAD: %v", statErr)
	}
	if err == nil || !result.Applied || !result.Uncertain {
		t.Fatalf("concurrent HEAD change called verified: %+v %v", result, err)
	}
	data, readErr := os.ReadFile(filepath.Join(report.Repository, "file.txt"))
	if readErr != nil || string(data) != "fixed\n" {
		t.Fatalf("applied content was lost: %q %v", data, readErr)
	}
}
