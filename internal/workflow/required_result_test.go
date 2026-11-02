package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWatchRejectsEmptyReviewerResultWithoutReplay(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := auditPlan(repo, []string{"python3", "-c", "print('  \\t')"})
	p.MaxAttempts = 3
	r, err := Watch(context.Background(), p, filepath.Join(root, "watch"), false, WatchOptions{IntervalSeconds: 1, MaxRuns: 1, TimeoutSeconds: 5})
	if err == nil || r.State != "failed" || len(r.Runs) != 1 || r.Runs[0].State != "empty_review" {
		t.Fatalf("empty review accepted: %+v %v", r, err)
	}
	nested, err := LoadReport(r.Runs[0].Output)
	if err != nil {
		t.Fatal(err)
	}
	if nested.State != "failed" || len(nested.Candidates) != 1 || nested.Candidates[0].State != "empty_result" || nested.Candidates[0].Attempts != 1 || strings.TrimSpace(nested.Candidates[0].Result) != "" {
		t.Fatalf("silent reviewer replayed or mislabeled: %+v", nested)
	}
}

func TestMissionRequiresReviewResultBeforeDependentSteps(t *testing.T) {
	repo, root := fixtureRepo(t)
	marker := filepath.Join(root, "must-not-run")
	q, _ := json.Marshal(marker)
	p := auditPlan(repo, []string{"python3", "-c", "print('  \\t')"})
	p.Mode = "mission"
	p.MaxAttempts = 3
	p.Candidates = append(p.Candidates, Candidate{ID: "dependent", Harness: "command", Command: []string{"python3", "-c", "from pathlib import Path; Path(" + string(q) + ").write_text('executed')"}})
	p.Steps = []Step{{ID: "review", Candidate: "review", RequireResult: true}, {ID: "after", Candidate: "dependent", DependsOn: []string{"review"}}}
	r, err := RunMission(context.Background(), p, filepath.Join(root, "mission"), false)
	if err == nil || r.State != "failed" || len(r.Steps) != 1 || r.Steps[0].State != "empty_result" || r.Steps[0].Attempts != 1 {
		t.Fatalf("silent mission review accepted/replayed: %+v %v", r, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("dependent step ran without required review result")
	}
	var full CandidateResult
	if err := DecodeFile(r.Steps[0].EvidencePath, &full); err != nil || full.State != "empty_result" {
		t.Fatalf("empty-result failure not durable: %+v %v", full, err)
	}
}

func TestMissionAllowsToolOnlyStepWhenNoResultRequired(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := auditPlan(repo, []string{"python3", "-c", "from pathlib import Path; Path('tool-output.txt').write_text('done')"})
	p.Mode = "mission"
	p.Steps = []Step{{ID: "write", Candidate: "review"}}
	r, err := RunMission(context.Background(), p, filepath.Join(root, "tool-only"), false)
	if err != nil || r.State != "completed" || len(r.Steps) != 1 || r.Steps[0].State != "completed" || r.Steps[0].Result != "" {
		t.Fatalf("tool-only step unnecessarily rejected: %+v %v", r, err)
	}
}

func TestVerifierOnlyStepCannotRequireNativeResult(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := auditPlan(repo, []string{"/bin/true"})
	p.Mode = "mission"
	p.Steps = []Step{{ID: "verify", Verify: true, RequireResult: true}}
	output := filepath.Join(root, "invalid")
	if _, err := RunMission(context.Background(), p, output, false); err == nil {
		t.Fatal("accepted impossible result requirement")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("invalid plan created output")
	}
}
