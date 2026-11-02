package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pythonCandidate(id, script string) Candidate {
	return Candidate{ID: id, Harness: "command", Command: []string{"python3", "-c", script}}
}
func pythonCheck(script string) Check {
	return Check{Name: "acceptance", Command: []string{"python3", "-c", script}, TimeoutSeconds: 10}
}
func comparePlan(repo string, candidates ...Candidate) Plan {
	return Plan{Version: 1, Repository: repo, Execution: "local", Goal: "Fix the fixture", TimeoutSeconds: 30, Candidates: candidates, Checks: []Check{pythonCheck("from pathlib import Path; assert Path('file.txt').read_text() == 'fixed\\n'")}}
}

func TestCompareIndependentStartingContentAndVerificationEvidence(t *testing.T) {
	repo, root := fixtureRepo(t)
	fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("working\n"))
	fixtureGit(t, repo, "add", "file.txt")
	staged := fixtureGit(t, repo, "diff", "--cached")
	p := comparePlan(repo,
		pythonCandidate("good", "from pathlib import Path; p=Path('file.txt'); assert p.read_text()=='working\\n'; p.write_text('fixed\\n'); print('implemented')"),
		pythonCandidate("bad", "from pathlib import Path; p=Path('file.txt'); assert p.read_text()=='working\\n'; p.write_text('wrong\\n'); print('implemented differently')"))
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), false)
	if err == nil {
		t.Fatal("failing candidate reported universal success")
	}
	if len(r.Candidates) != 2 || r.Candidates[0].State != "verified" || r.Candidates[1].State != "checks_failed" {
		t.Fatalf("bad results: %+v %v", r, err)
	}
	for _, candidate := range r.Candidates {
		if candidate.PatchSHA256 == "" || candidate.FinalSnapshot == nil || candidate.Verification == nil || candidate.Input == "" {
			t.Fatalf("missing bound evidence: %+v", candidate)
		}
		if candidate.Verification.Tree != candidate.FinalSnapshot.Tree {
			t.Fatal("verification bound to different revision")
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(candidate.Worktree), "attempt-1.json")); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(repo, "file.txt"))
	if string(b) != "working\n" || fixtureGit(t, repo, "diff", "--cached") != staged {
		t.Fatal("source/index changed")
	}
	loaded, err := LoadReport(r.Output)
	if err != nil || loaded.State != "failed" || loaded.Candidates[0].State != "verified" {
		t.Fatalf("durable report: %+v %v", loaded, err)
	}
}

func TestCandidateRepairsOnlyCompletedFailedChecks(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := comparePlan(repo, pythonCandidate("repair", "from pathlib import Path; p=Path('file.txt'); p.write_text('first\\n' if p.read_text()=='base\\n' else 'fixed\\n'); print('turn complete')"))
	p.MaxAttempts = 3
	r, err := Run(context.Background(), p, filepath.Join(root, "repair"), false)
	if err != nil || r.Candidates[0].Attempts != 2 || r.Candidates[0].State != "verified" {
		t.Fatalf("repair result: %+v %v", r, err)
	}
	if !strings.Contains(r.Candidates[0].Input, "independent checks failed") {
		t.Fatal("repair did not receive verifier evidence")
	}
}

func TestExecutionFailureIsNotAutomaticallyRetried(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := comparePlan(repo, pythonCandidate("broken", "from pathlib import Path; Path('file.txt').write_text('side effect\\n'); raise SystemExit(7)"))
	p.MaxAttempts = 5
	r, err := Run(context.Background(), p, filepath.Join(root, "broken"), false)
	if err == nil || r.Candidates[0].Attempts != 1 || r.Candidates[0].State != "uncertain" {
		t.Fatalf("uncertain execution replayed: %+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(root, "broken", "candidates", "broken", "attempt-2.json")); !os.IsNotExist(err) {
		t.Fatal("second attempt exists")
	}
}

func TestMissionDependencyOrderAndBoundedRepair(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := comparePlan(repo,
		pythonCandidate("implement", "from pathlib import Path; p=Path('file.txt'); p.write_text('first\\n' if p.read_text()=='base\\n' else 'fixed\\n'); print('implementation')"),
		pythonCandidate("review", "from pathlib import Path; assert Path('file.txt').read_text()=='fixed\\n'; print('reviewed exact revision')"))
	p.Mode = "mission"
	p.MaxAttempts = 2
	p.Steps = []Step{{ID: "review", Candidate: "review", DependsOn: []string{"implement"}}, {ID: "verify", Verify: true, DependsOn: []string{"review"}}, {ID: "implement", Candidate: "implement", Verify: true}}
	r, err := RunMission(context.Background(), p, filepath.Join(root, "mission"), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Steps) != 3 || r.Steps[0].ID != "implement" || r.Steps[0].Attempts != 2 || r.Steps[1].ID != "review" || r.Verification == nil || !r.Verification.Passed || r.PatchSHA256 == "" {
		t.Fatalf("mission evidence: %+v", r)
	}
	if !strings.Contains(r.Steps[1].Input, "Prior step implement") {
		t.Fatal("dependent step missing predecessor result")
	}
}

func TestNativePermissionAndValidationFailBeforeCreatingOutput(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := comparePlan(repo, Candidate{ID: "native", Harness: "claude"})
	dir := filepath.Join(root, "forbidden")
	if _, err := Run(context.Background(), p, dir, false); err == nil {
		t.Fatal("native calls allowed without explicit flag")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("validation changed filesystem")
	}
	p = comparePlan(repo, pythonCandidate("command", "print('fixture')"))
	p.Execution = ""
	if ValidatePlan(p) == nil {
		t.Fatal("missing authority choice accepted")
	}
	p.Execution = "local"
	p.Mode = "mission"
	p.Steps = []Step{{ID: "a", Candidate: "command", DependsOn: []string{"b"}}, {ID: "b", Candidate: "command", DependsOn: []string{"a"}}}
	if ValidatePlan(p) == nil {
		t.Fatal("dependency cycle accepted")
	}
	p.Mode = "compare"
	p.Steps = nil
	p.Checks[0].Name = strings.Repeat("x", 129)
	if ValidatePlan(p) == nil {
		t.Fatal("invalid verifier rejected only after execution")
	}
}

func TestParallelCandidatesReachBarrierTogether(t *testing.T) {
	repo, root := fixtureRepo(t)
	a, _ := json.Marshal(filepath.Join(root, "a"))
	b, _ := json.Marshal(filepath.Join(root, "b"))
	program := func(mine, peer []byte) string {
		return "from pathlib import Path; import time; Path(" + string(mine) + ").write_text('ready'); end=time.monotonic()+5\nwhile not Path(" + string(peer) + ").exists() and time.monotonic()<end: time.sleep(.01)\nassert Path(" + string(peer) + ").exists(), 'peer never ran concurrently'\nprint('reviewed')"
	}
	p := comparePlan(repo, pythonCandidate("a", program(a, b)), pythonCandidate("b", program(b, a)))
	p.Checks = []Check{pythonCheck("from pathlib import Path; assert Path('file.txt').read_text()=='base\\n'")}
	r, err := Run(context.Background(), p, filepath.Join(root, "parallel"), false)
	if err != nil {
		t.Fatalf("concurrency barrier: %+v %v", r, err)
	}
}

func TestCancellationRetainsUnknownAttemptAndDoesNotReplay(t *testing.T) {
	repo, root := fixtureRepo(t)
	marker := filepath.Join(root, "started")
	quoted, _ := json.Marshal(marker)
	p := comparePlan(repo, pythonCandidate("slow", "from pathlib import Path; import time; Path("+string(quoted)+").write_text('started'); time.sleep(30)"))
	p.MaxAttempts = 5
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			if _, err := os.Stat(marker); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	r, err := Run(ctx, p, filepath.Join(root, "cancelled"), false)
	<-done
	if !errors.Is(err, context.Canceled) || r.State != "interrupted" || len(r.Candidates) != 1 || r.Candidates[0].Attempts != 1 {
		t.Fatalf("cancel evidence: %+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(root, "cancelled", "candidates", "slow", "attempt-1.json")); err != nil {
		t.Fatal("inflight attempt not durable")
	}
}

func TestSavedPlanOmitsEnvironmentCredentials(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := comparePlan(repo, pythonCandidate("fixture", "from pathlib import Path; Path('file.txt').write_text('fixed\\n')"))
	p.Candidates[0].Env = map[string]string{"PRIVATE_TOKEN": "fixture-secret-value"}
	r, err := Run(context.Background(), p, filepath.Join(root, "redacted"), false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(r.Output, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "fixture-secret-value") {
		t.Fatal("environment value copied into saved plan")
	}
}
