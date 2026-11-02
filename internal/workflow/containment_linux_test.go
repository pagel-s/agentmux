//go:build linux

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func requireWorkflowPIDContainment(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENTMUX_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_SANDBOX_INTEGRATION=1 for real contained workflows")
	}
	if _, err := PrepareProcessContainment(context.Background(), PIDNamespace); err != nil {
		t.Fatal(err)
	}
}

func TestProcessContainmentRealWorkflowRecordsPolicyAndContainsGitFilterDescendants(t *testing.T) {
	requireWorkflowPIDContainment(t)
	repo, root := fixtureRepo(t)
	// Commit the attribute before configuring its helper so fixture setup never
	// executes the helper outside the requested workflow boundary.
	fixtureWrite(t, filepath.Join(repo, ".gitattributes"), []byte("*.txt filter=workflow-lifetime\n"))
	fixtureGit(t, repo, "add", ".gitattributes")
	fixtureGit(t, repo, "commit", "-qm", "select filter")
	ready := filepath.Join(root, "filter-child-ready")
	marker := filepath.Join(root, "filter-child-must-not-survive")
	readyJSON, _ := json.Marshal(ready)
	markerJSON, _ := json.Marshal(marker)
	child := "from pathlib import Path; import time; Path(" + string(readyJSON) + ").write_text('ready'); time.sleep(0.6); Path(" + string(markerJSON) + ").write_text('escaped')"
	childJSON, _ := json.Marshal(child)
	helper := "import pathlib,subprocess,sys,time\nsubprocess.Popen([sys.executable,'-c'," + string(childJSON) + "],start_new_session=True,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)\nwhile not pathlib.Path(" + string(readyJSON) + ").exists(): time.sleep(0.002)\nsys.stdout.buffer.write(sys.stdin.buffer.read())\n"
	filter := filepath.Join(root, "filter.py")
	fixtureWrite(t, filter, []byte(helper))
	fixtureGit(t, repo, "config", "filter.workflow-lifetime.clean", "python3 "+shellWord(filter))
	p := comparePlan(repo, pythonCandidate("coder", "from pathlib import Path; Path('file.txt').write_text('fixed\\n'); print('done')"))
	p.ProcessContainment = PIDNamespace
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), false)
	if err != nil {
		t.Fatalf("contained workflow: %+v %v", r, err)
	}
	if r.ProcessContainment != PIDNamespace || len(r.Candidates) != 1 || r.Candidates[0].Verification == nil || r.Candidates[0].Verification.ProcessContainment != PIDNamespace {
		t.Fatalf("actual containment policy was not retained: %+v", r)
	}
	if _, err = os.Stat(ready); err != nil {
		t.Fatal("source Git helper did not execute its session-escaping descendant")
	}
	time.Sleep(750 * time.Millisecond)
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("source Git helper descendant survived snapshot execution: %v", err)
	}
	if _, err = LoadReport(r.Output); err != nil {
		t.Fatal(err)
	}
	r.Candidates[0].Verification.ProcessContainment = ProcessGroups
	if err = saveJSON(filepath.Join(r.Output, "report.json"), r); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadReport(r.Output); err == nil {
		t.Fatal("a PID-contained report accepted weaker verification evidence")
	}
}

func TestProcessContainmentRealFreshnessRefusesUnavailableRecordedPolicy(t *testing.T) {
	requireWorkflowPIDContainment(t)
	repo, _ := fixtureRepo(t)
	v, err := VerifyWithContainment(context.Background(), repo, []Check{{Name: "unchanged", Command: []string{"/bin/true"}}}, PIDNamespace)
	if err != nil || !v.Passed {
		t.Fatalf("contained verification: %+v %v", v, err)
	}
	t.Setenv("PATH", t.TempDir())
	fresh, err := IsFresh(context.Background(), repo, v)
	if err == nil || fresh {
		t.Fatalf("freshness silently abandoned recorded containment: fresh=%t err=%v", fresh, err)
	}
}

func TestProcessContainmentRealCancellationPreservesPolicyInRecoveryContext(t *testing.T) {
	requireWorkflowPIDContainment(t)
	prepared, err := PrepareProcessContainment(context.Background(), PIDNamespace)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(prepared)
	cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("fixture cancellation did not occur")
	}
	recovery, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer stop()
	if contextProcessContainment(recovery) != PIDNamespace {
		t.Fatal("bounded recovery discarded requested PID containment")
	}
	if again, err := PrepareProcessContainment(recovery, ProcessGroups); err != nil || contextProcessContainment(again) != PIDNamespace {
		t.Fatalf("nested verification weakened parent containment: %v", err)
	}
}
