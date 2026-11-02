//go:build unix

package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestWorkflowTurnsCannotLeaveSameGroupDelayedWriters(t *testing.T) {
	repo, root := fixtureRepo(t)
	// Both an implementation and its checker try to leave a stdio-detached
	// writer. Such a child formerly could mutate the result after verification.
	spawn := func(marker string) string {
		child := "from pathlib import Path; import time; Path(" + strconv.Quote(marker) + ").write_text('ready'); time.sleep(1); Path('file.txt').write_text('late mutation')"
		return "import pathlib,subprocess,sys,time\nsubprocess.Popen([sys.executable,'-c'," + strconv.Quote(child) + "],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)\nfor _ in range(300):\n if pathlib.Path(" + strconv.Quote(marker) + ").exists(): break\n time.sleep(.01)\nelse: raise RuntimeError('child did not start')\n"
	}
	candidate := spawn(filepath.Join(root, "candidate-ready")) + "pathlib.Path('answer.txt').write_text('checked')\n"
	checker := spawn(filepath.Join(root, "checker-ready")) + "assert pathlib.Path('answer.txt').read_text() == 'checked'\n"
	p := Plan{Version: 1, Repository: repo, Execution: "local", Goal: "Produce a stable checked artifact", TimeoutSeconds: 30, Candidates: []Candidate{{ID: "writer", Harness: "command", Command: []string{"python3", "-c", candidate}}}, Checks: []Check{{Name: "content", Command: []string{"python3", "-c", checker}}}}
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1300 * time.Millisecond)
	wt := r.Candidates[0].Worktree
	data, err := os.ReadFile(filepath.Join(wt, "file.txt"))
	if err != nil || string(data) != "base\n" {
		t.Fatalf("a turn left a delayed writer: %q %v", data, err)
	}
	if fresh, err := IsFresh(context.Background(), wt, *r.Candidates[0].Verification); err != nil || !fresh {
		t.Fatalf("checked artifact became stale: %v %v", fresh, err)
	}
}
