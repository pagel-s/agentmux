//go:build unix

package harness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBoundedTurnReapsDetachedStdioDescendantsAfterSuccess(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required for process regression")
	}
	for _, reap := range []bool{false, true} {
		t.Run(strconv.FormatBool(reap), func(t *testing.T) {
			root := t.TempDir()
			ready := filepath.Join(root, "child-ready")
			sentinel := filepath.Join(root, "late-write")
			child := "import pathlib,time; pathlib.Path(" + strconv.Quote(ready) + ").write_text('ready'); time.sleep(1); pathlib.Path(" + strconv.Quote(sentinel) + ").write_text('late')"
			parent := "import pathlib,subprocess,sys,time\nsubprocess.Popen([sys.executable,'-c'," + strconv.Quote(child) + "],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)\nfor _ in range(300):\n if pathlib.Path(" + strconv.Quote(ready) + ").exists(): break\n time.sleep(.01)\nelse: raise RuntimeError('child did not start')\nprint('parent exited')\n"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := Run(ctx, Request{Harness: "command", Command: []string{python, "-c", parent}, Workdir: root, ReapDescendants: reap})
			if err != nil || !strings.Contains(result.Text, "parent exited") {
				t.Fatalf("headless parent failed: %+v %v", result, err)
			}
			if reap {
				time.Sleep(1300 * time.Millisecond)
				if _, err = os.Stat(sentinel); !os.IsNotExist(err) {
					t.Fatalf("opted-in child survived successful turn: %v", err)
				}
			} else {
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					if _, err = os.Stat(sentinel); err == nil {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if _, err = os.Stat(sentinel); err != nil {
					t.Fatalf("default lifecycle unexpectedly killed its child: %v", err)
				}
			}
		})
	}
}

func TestBoundedTurnOptionIsHeadlessAndRejectsUnstartedCleanup(t *testing.T) {
	if _, err := BuildInteractive(Request{Harness: "command", Command: []string{"/bin/true"}, ReapDescendants: true}); err == nil {
		t.Fatal("interactive lifecycle accepted headless-only cleanup")
	}
	if err := reapDescendants(&exec.Cmd{}); err == nil {
		t.Fatal("unstarted process cleanup accepted")
	}
	if err := reapDescendants(&exec.Cmd{Process: &os.Process{Pid: 0}}); err == nil {
		t.Fatal("current-group process cleanup accepted")
	}
}
