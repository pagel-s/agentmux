package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkflowJSONRejectsDuplicateKeysCaseAliasesAndDeepNesting(t *testing.T) {
	for _, input := range []string{`{"version":1,"version":2}`, `{"Goal":"one","goal":"two"}`, `{"version":1,"verſion":2}`, `{"checks":[],"checKs":[]}`, `{"env":{"KEY":"one","key":"two"}}`, strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66), `{"version":1} {"version":2}`, `{"version":1,"unknown":2}`} {
		var p Plan
		if err := DecodeJSON([]byte(input), &p); err == nil {
			t.Fatalf("ambiguous/malformed JSON accepted: %s", input)
		}
	}
	var p Plan
	if err := DecodeJSON([]byte(`{"version":1,"goal":"one"}`), &p); err != nil || p.Goal != "one" {
		t.Fatalf("valid input: %+v %v", p, err)
	}
}

func TestCandidateConfigurationIsFullyValidatedBeforeAnyExecution(t *testing.T) {
	repo, root := fixtureRepo(t)
	marker := filepath.Join(root, "must-not-run")
	encoded, _ := json.Marshal(marker)
	for name, bad := range map[string]Candidate{
		"environment":      {ID: "invalid", Harness: "command", Command: []string{"python3", "-c", "print('unused')"}, Env: map[string]string{"BAD=KEY": "value"}},
		"command_metadata": {ID: "invalid", Harness: "command", Command: []string{"python3", "-c", "print('unused')"}, Model: "misconfigured"},
		"native_endpoint":  {ID: "invalid", Harness: "antigravity", BaseURL: "https://example.invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			p := auditPlan(repo, []string{"python3", "-c", "from pathlib import Path; Path(" + string(encoded) + ").write_text('bad')"})
			p.Candidates = append(p.Candidates, bad)
			output := filepath.Join(root, name)
			if _, err := Run(context.Background(), p, output, true); err == nil {
				t.Fatal("invalid peer configuration accepted")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("peer process ran before whole-plan validation")
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("invalid plan created output")
			}
		})
	}
}

func TestRunRendererFailureIsDurablyFailedRatherThanCompleted(t *testing.T) {
	repo, root := fixtureRepo(t)
	output := filepath.Join(root, "run")
	html, _ := json.Marshal(filepath.Join(output, "report.html"))
	p := auditPlan(repo, []string{"python3", "-c", "from pathlib import Path; Path(" + string(html) + ").write_text('occupied'); print('done')"})
	r, err := Run(context.Background(), p, output, false)
	if err == nil || r.State != "failed" || !strings.Contains(r.Error, "render workflow report") {
		t.Fatalf("renderer failure mislabeled: %+v %v", r, err)
	}
	stored, loadErr := LoadReport(output)
	if loadErr != nil || stored.State != "failed" || stored.Error != r.Error {
		t.Fatalf("durable result contradicts return: %+v %v", stored, loadErr)
	}
}

func TestLoadReportRejectsInventedSuccessfulEvidence(t *testing.T) {
	dir, r := evidenceRun(t)
	for _, change := range []func(*Report){
		func(r *Report) { r.State = "invented" }, func(r *Report) { r.Candidates[0].State = "uncertain" }, func(r *Report) { r.Candidates[0].Verification = nil }, func(r *Report) { r.Output = filepath.Dir(r.Output) }, func(r *Report) { r.Candidates = append(r.Candidates, r.Candidates[0]) },
	} {
		bytes, _ := json.Marshal(r)
		var modified Report
		if err := json.Unmarshal(bytes, &modified); err != nil {
			t.Fatal(err)
		}
		change(&modified)
		if err := saveJSON(filepath.Join(dir, "report.json"), modified); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadReport(dir); err == nil {
			t.Fatal("invented successful record accepted")
		}
	}
}

func TestMissionCancellationPreservesContextIdentityAndStopsDescendants(t *testing.T) {
	repo, root := fixtureRepo(t)
	marker := filepath.Join(root, "started")
	q, _ := json.Marshal(marker)
	p := auditPlan(repo, []string{"python3", "-c", "from pathlib import Path; import time; Path(" + string(q) + ").write_text('started'); time.sleep(30)"})
	p.Mode = "mission"
	p.Steps = []Step{{ID: "slow", Candidate: "review"}, {ID: "after", Candidate: "review", DependsOn: []string{"slow"}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		end := time.Now().Add(5 * time.Second)
		for time.Now().Before(end) {
			if _, err := os.Stat(marker); err == nil {
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	r, err := RunMission(ctx, p, filepath.Join(root, "mission"), false)
	if !errors.Is(err, context.Canceled) || r.State != "interrupted" || len(r.Steps) != 1 || r.Steps[0].ID != "slow" {
		t.Fatalf("cancelled mission: %+v %v", r, err)
	}
	stored, err := LoadReport(r.Output)
	if err != nil || stored.State != "interrupted" {
		t.Fatalf("persisted interruption: %+v %v", stored, err)
	}
}

func TestRepairAttemptBeforeLaunchRecordDoesNotClaimPriorCompletion(t *testing.T) {
	repo, root := fixtureRepo(t)
	marker := filepath.Join(root, "second-started")
	q, _ := json.Marshal(marker)
	script := "from pathlib import Path; import time; p=Path('attempt.txt'); n=int(p.read_text())+1 if p.exists() else 1; p.write_text(str(n)); Path('file.txt').write_text('fixed' if n==2 else 'wrong')\nif n==2: Path(" + string(q) + ").write_text('started'); time.sleep(0.5)\nprint('turn completed')"
	p := auditPlan(repo, []string{"python3", "-c", script})
	p.MaxAttempts = 2
	p.Checks = []Check{pythonCheck("from pathlib import Path; assert Path('file.txt').read_text()=='fixed'")}
	output := filepath.Join(root, "run")
	observed := make(chan CandidateResult, 1)
	go func() {
		end := time.Now().Add(4 * time.Second)
		for time.Now().Before(end) {
			if _, err := os.Stat(marker); err == nil {
				var record CandidateResult
				_ = decodeFile(filepath.Join(output, "candidates", "review", "attempt-2.json"), &record)
				observed <- record
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		observed <- CandidateResult{}
	}()
	r, err := Run(context.Background(), p, output, false)
	record := <-observed
	if err != nil || r.Candidates[0].Attempts != 2 {
		t.Fatalf("repair failed: %+v %v", r, err)
	}
	if record.State != "running" || record.Result != "" || record.Verification != nil || record.AttemptTree == "" || record.AttemptStartedAt == "" || record.AttemptFinishedAt != "" {
		t.Fatalf("before-launch record retained prior completion: %+v", record)
	}
}

func TestSelectedWatchRevisionCannotBeReplacedBeforeModelAdmission(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := auditPlan(repo, []string{"python3", "-c", "raise RuntimeError('must never start')"})
	tree, raw, err := contentState(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	p.expectedTree = tree
	p.expectedContent = raw
	fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("changed"))
	r, err := Run(context.Background(), p, filepath.Join(root, "changed"), false)
	if err == nil || len(r.Candidates) != 0 || !strings.Contains(err.Error(), "no candidate was started") {
		t.Fatalf("changed selected snapshot admitted: %+v %v", r, err)
	}
}

func TestLargeMissionAggregateStaysReadableAndRetainsExactEvidence(t *testing.T) {
	repo, root := fixtureRepo(t)
	output := filepath.Join(root, "large")
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	input := strings.Repeat("i", 256<<10)
	diagnostic := strings.Repeat("\x00", 64<<10)
	c := CandidateResult{State: "completed", Worktree: repo, Input: input, Result: strings.Repeat("r", 128<<10), EvidencePath: filepath.Join(output, "full-evidence.json"), Verification: &Verification{Checks: []CheckResult{}}}
	for i := 0; i < 32; i++ {
		c.Verification.Checks = append(c.Verification.Checks, CheckResult{Name: fmt.Sprintf("check-%d", i), Output: diagnostic, Command: []string{"fixture"}})
	}
	if err := saveJSON(c.EvidencePath, c); err != nil {
		t.Fatal(err)
	}
	r := Report{Version: 1, ID: "flow_large", Mode: "mission", Execution: "local", State: "failed", Error: "fixture terminal failure", Repository: repo, Output: output, Goal: "large supported diagnostics", StartedAt: timestamp(), FinishedAt: timestamp()}
	for i := 0; i < 64; i++ {
		r.Steps = append(r.Steps, StepResult{ID: fmt.Sprintf("step-%d", i), CandidateResult: c})
	}
	if err := saveJSON(filepath.Join(output, "report.json"), r); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(output, "report.json"))
	if err != nil || info.Size() > 32<<20 {
		t.Fatalf("unreadable aggregate: %v %v", info, err)
	}
	loaded, err := LoadReport(output)
	if err != nil || !loaded.DiagnosticsTruncated || !loaded.Steps[0].InputTruncated {
		t.Fatalf("lost truncation disclosure: %+v %v", loaded, err)
	}
	var full CandidateResult
	if err := DecodeFile(c.EvidencePath, &full); err != nil || full.Input != input || full.Verification.Checks[0].Output != diagnostic {
		t.Fatalf("exact evidence was compacted: %v", err)
	}
}

func TestLabRefusesToContinueFromMutatedBaseline(t *testing.T) {
	repo, root := fixtureRepo(t)
	output := filepath.Join(root, "lab")
	baseline, _ := json.Marshal(filepath.Join(output, "baseline", "file.txt"))
	p := auditPlan(repo, []string{"python3", "-c", "from pathlib import Path; Path(" + string(baseline) + ").write_text('contaminated baseline'); print('completed')"})
	r, err := Lab(context.Background(), p, output, false, 2)
	if err == nil || r.State != "failed" || len(r.Runs) != 1 || !strings.Contains(err.Error(), "baseline changed") {
		t.Fatalf("contaminated trial continued: %+v %v", r, err)
	}
}

func TestWatchDetectsRawChangesEvenWhenGitNormalizationKeepsSameTree(t *testing.T) {
	repo, root := fixtureRepo(t)
	fixtureWrite(t, filepath.Join(repo, ".gitattributes"), []byte("file.txt text eol=lf\n"))
	fixtureGit(t, repo, "add", ".gitattributes")
	fixtureGit(t, repo, "commit", "-qm", "normalization")
	marker := filepath.Join(root, "started")
	q, _ := json.Marshal(marker)
	p := auditPlan(repo, []string{"python3", "-c", "from pathlib import Path; import time; Path(" + string(q) + ").write_text('started'); time.sleep(.3); print('reviewed')"})
	before, raw, err := contentState(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	changed := make(chan error, 1)
	go func() {
		end := time.Now().Add(4 * time.Second)
		for time.Now().Before(end) {
			if _, err := os.Stat(marker); err == nil {
				changed <- os.WriteFile(filepath.Join(repo, "file.txt"), []byte("base\r\n"), 0600)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		changed <- context.DeadlineExceeded
	}()
	r, err := Watch(context.Background(), p, filepath.Join(root, "watch"), false, WatchOptions{IntervalSeconds: 1, MaxRuns: 1, TimeoutSeconds: 5})
	if changeErr := <-changed; changeErr != nil {
		t.Fatal(changeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	after, rawAfter, err := contentState(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || raw == rawAfter {
		t.Fatalf("fixture did not isolate raw-only changes: %s %s %s %s", before, after, raw, rawAfter)
	}
	if len(r.Runs) != 1 || !r.Runs[0].Superseded {
		t.Fatalf("raw-stale review shown current: %+v", r)
	}
}

func TestMaximumMissionFanInKeepsAllReferencesWithinPromptLimit(t *testing.T) {
	p := Plan{Goal: strings.Repeat("g", 128<<10)}
	candidate := Candidate{Prompt: strings.Repeat("p", 128<<10)}
	step := Step{ID: "final", Goal: strings.Repeat("s", 128<<10)}
	completed := map[string]StepResult{}
	for i := 0; i < 63; i++ {
		id := fmt.Sprintf("predecessor-%d", i)
		step.DependsOn = append(step.DependsOn, id)
		completed[id] = StepResult{ID: id, CandidateResult: CandidateResult{Result: strings.Repeat("r", 128<<10)}}
	}
	goal, err := missionStepGoal(p, step, candidate, completed)
	if err != nil {
		t.Fatal(err)
	}
	if len(goal)+len(candidate.Prompt)+1024 > 1<<20 {
		t.Fatal("assembled task exceeds native prompt limit")
	}
	for _, id := range step.DependsOn {
		if !strings.Contains(goal, "Prior step "+id+" (") {
			t.Fatalf("omitted source %s", id)
		}
	}
	if !strings.Contains(goal, "[truncated]") {
		t.Fatal("dependency excerpts were not disclosed")
	}
}

func TestInterruptedVerificationRetainsFinalAttemptFailureEvidence(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := auditPlan(repo, []string{"python3", "-c", "print('turn completed')"})
	p.TimeoutSeconds = 1
	p.Checks = []Check{{Name: "slow-check", Command: []string{"python3", "-c", "import time; time.sleep(30)"}, TimeoutSeconds: 30}}
	r, err := Run(context.Background(), p, filepath.Join(root, "interrupted-check"), false)
	if !errors.Is(err, context.DeadlineExceeded) || r.State != "interrupted" {
		t.Fatalf("wrong interrupted outcome: %+v %v", r, err)
	}
	var full CandidateResult
	if err := DecodeFile(r.Candidates[0].EvidencePath, &full); err != nil {
		t.Fatal(err)
	}
	if full.State == "running" || full.Verification == nil || full.Verification.Passed || len(full.Verification.Checks) != 1 || full.Verification.Checks[0].Error == "" {
		t.Fatalf("final check evidence lost: %+v", full)
	}
}
