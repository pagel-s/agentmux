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

func TestReviewAuditRequiresMatchingStartingTree(t *testing.T) {
	plan, output := reviewFixturePlan(t, `{"decision":"approve","summary":"Inspected the implementation.","findings":[]}`, false)
	report, err := RunMission(context.Background(), plan, output, false)
	if err != nil {
		t.Fatal(err)
	}
	before := *report.Steps[1].StartingSnapshot
	before.Tree = strings.Repeat("0", 40)
	report.Steps[1].StartingSnapshot = &before
	if err := RequireApprovedReview(report); err == nil {
		t.Fatal("review of a different starting Git tree approved the final result")
	}
}

func TestReviewAuditPreservesExplicitEmptyFindingsAndRejectsNull(t *testing.T) {
	plan, output := reviewFixturePlan(t, `{"decision":"approve","summary":"Inspected the implementation.","findings":[]}`, false)
	if _, err := RunMission(context.Background(), plan, output, false); err != nil {
		t.Fatal(err)
	}
	stored, err := LoadReport(output)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Steps[1].Review == nil || stored.Steps[1].Review.Findings == nil {
		t.Fatal("aggregate compaction changed explicit findings [] to null")
	}
	stored.Steps[1].Review.Findings = nil
	if err := RequireApprovedReview(stored); err == nil {
		t.Fatal("malformed null findings silently became an approval")
	}
}

func TestReviewAuditCompleteFindingsReachAddressingTurn(t *testing.T) {
	repo, root := fixtureRepo(t)
	findings := make([]ReviewFinding, 30)
	for i := range findings {
		findings[i] = ReviewFinding{File: "file.txt", Line: 1, Message: strings.Repeat("actionable finding ", 60)}
	}
	const sentinel = "FINAL_ACTIONABLE_FINDING_MUST_REACH_CODER"
	findings[len(findings)-1].Message += sentinel
	verdict := ReviewResult{Decision: "request_changes", Summary: "Address all recorded findings.", Findings: findings}
	data, err := json.Marshal(verdict)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= 16<<10 {
		t.Fatal("fixture does not exceed generic dependency excerpt limit")
	}
	verdictPath := filepath.Join(root, "owner-review.json")
	fixtureWrite(t, verdictPath, data)
	approved := `{"decision":"approve","summary":"The findings were addressed; the inspected implementation satisfies the fixture.","findings":[]}`
	coder := "import sys; from pathlib import Path; prompt=sys.stdin.read();\nif 'Current mission step: address' in prompt:\n assert " + fmt.Sprintf("%q", sentinel) + " in prompt\n print('FULL_FINDINGS_RECEIVED')\nPath('file.txt').write_text('fixed\\n')\nprint('Implementation is ready')"
	reviewer := "import sys; from pathlib import Path; prompt=sys.stdin.read(); print(" + fmt.Sprintf("%q", approved) + " if 'Current mission step: final-review' in prompt else Path(" + fmt.Sprintf("%q", verdictPath) + ").read_text())"
	plan := Plan{Kind: "work", Version: 1, Execution: "local", Mode: "mission", Repository: repo, Goal: "Address the review of the supplied file", TimeoutSeconds: 30, MaxAttempts: 1,
		Candidates: []Candidate{{ID: "coder", Harness: "command", Command: []string{"python3", "-c", coder}}, {ID: "reviewer", Harness: "command", Command: []string{"python3", "-c", reviewer}}},
		Checks:     []Check{{Name: "fixture", Command: []string{"python3", "-c", "from pathlib import Path; assert Path('file.txt').read_text()=='fixed\\n'"}}},
		Steps: []Step{
			{ID: "implement", Candidate: "coder", Verify: true},
			{ID: "review", Candidate: "reviewer", DependsOn: []string{"implement"}, Review: true, ReadOnly: true, RequireResult: true},
			{ID: "address", Candidate: "coder", DependsOn: []string{"implement", "review"}, Verify: true},
			{ID: "final-review", Candidate: "reviewer", DependsOn: []string{"address"}, Review: true, ReadOnly: true, RequireResult: true, GateReview: true},
			{ID: "check", Verify: true, DependsOn: []string{"final-review"}},
		},
	}
	report, err := RunMission(context.Background(), plan, filepath.Join(root, "run"), false)
	if err != nil {
		t.Fatalf("feedback loop failed: %v %+v", err, report)
	}
	if len(report.Steps) != 5 || !strings.Contains(report.Steps[2].Result, "FULL_FINDINGS_RECEIVED") {
		t.Fatal("addressing turn did not receive the final review finding")
	}
	if !report.Steps[1].ReviewTruncated || len(report.Steps[1].Review.Findings) != 8 {
		t.Fatal("aggregate fixture did not exercise finding compaction")
	}
	var complete CandidateResult
	if err := DecodeFile(report.Steps[1].EvidencePath, &complete); err != nil {
		t.Fatal(err)
	}
	if complete.Review == nil || len(complete.Review.Findings) != len(findings) {
		t.Fatal("complete review findings were not retained")
	}
}

func TestReviewAuditFinalVerifierIsNotRunTwice(t *testing.T) {
	plan, output := reviewFixturePlan(t, `{"decision":"approve","summary":"Inspected the implementation.","findings":[]}`, false)
	counter := filepath.Join(filepath.Dir(output), "owner-check-count")
	plan.Checks[0].Command = []string{"python3", "-c", "from pathlib import Path; assert Path('file.txt').read_text()=='fixed\\n'; p=Path(" + fmt.Sprintf("%q", counter) + "); p.write_text(str(int(p.read_text())+1) if p.exists() else '1')"}
	if _, err := RunMission(context.Background(), plan, output, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(counter)
	if err != nil || string(data) != "2" {
		t.Fatalf("expected implementation check and one final check, got %q %v", data, err)
	}
}

func TestReviewAuditReadOnlyCatchesNormalizedByteChanges(t *testing.T) {
	approved := `{"decision":"approve","summary":"Inspected the implementation.","findings":[]}`
	plan, output := reviewFixturePlan(t, approved, false)
	fixtureWrite(t, filepath.Join(plan.Repository, ".gitattributes"), []byte("file.txt text\n"))
	fixtureGit(t, plan.Repository, "add", ".gitattributes")
	fixtureGit(t, plan.Repository, "commit", "-qm", "normalize file.txt")
	plan.Candidates[1].Command = []string{"python3", "-c", "import sys; from pathlib import Path; sys.stdin.read(); Path('file.txt').write_bytes(b'fixed\\r\\n'); print(" + fmt.Sprintf("%q", approved) + ")"}
	report, err := RunMission(context.Background(), plan, output, false)
	if err == nil || len(report.Steps) != 2 || report.Steps[1].State != "review_modified" {
		t.Fatalf("read-only review accepted changed raw bytes: %v %+v", err, report)
	}
	if _, err := Apply(context.Background(), output, ""); err == nil {
		t.Fatal("byte-mutating reviewer result applied")
	}
}

func TestReviewAuditRunningStepAndCancellationRemainInspectable(t *testing.T) {
	plan, output := reviewFixturePlan(t, `{"decision":"approve","summary":"Inspected the implementation.","findings":[]}`, false)
	marker := filepath.Join(filepath.Dir(output), "owned-turn-started")
	plan.Candidates[0].Command = []string{"python3", "-c", "from pathlib import Path; import time; Path(" + fmt.Sprintf("%q", marker) + ").write_text('started'); time.sleep(30)"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		report Report
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := RunMission(ctx, plan, output, false)
		done <- outcome{r, err}
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case result := <-done:
			t.Fatalf("turn ended before progress could be inspected: %v", result.err)
		case <-deadline.C:
			t.Fatal("owned test turn did not start")
		case <-poll.C:
		}
	}
	live, err := LoadReport(output)
	if err != nil || live.State != "running" || len(live.Steps) != 1 || live.Steps[0].ID != "implement" || live.Steps[0].State != "running" {
		t.Fatalf("active step not durably inspectable: %+v %v", live, err)
	}
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) || result.report.State != "interrupted" {
			t.Fatalf("cancellation lost its identity: %+v %v", result.report, result.err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("owned process did not stop after cancellation")
	}
	stored, err := LoadReport(output)
	if err != nil || stored.State != "interrupted" || stored.Error == "" {
		t.Fatalf("interrupted result not inspectable: %+v %v", stored, err)
	}
	if _, err := Apply(context.Background(), output, ""); err == nil {
		t.Fatal("interrupted unreviewed work was applied")
	}
}

func TestReviewAuditVerificationCannotBypassStructuredGate(t *testing.T) {
	changes := `{"decision":"request_changes","summary":"A real defect remains.","findings":[{"file":"file.txt","line":1,"message":"The result still needs correction"}]}`
	plan, output := reviewFixturePlan(t, changes, false)
	// Generic mission gates must still mean what they declare. Combining an
	// ordinary check with review cannot turn a rejecting verdict into success.
	plan.Kind = ""
	plan.Steps[1].Verify = true
	report, err := RunMission(context.Background(), plan, output, false)
	if err == nil {
		t.Errorf("passing command checks bypassed a rejecting review gate: state=%s", report.State)
	}
	if applied, err := Apply(context.Background(), output, ""); err == nil || applied.Applied {
		t.Errorf("a result with a rejecting declared review gate was applied: %+v %v", applied, err)
	}
}

func TestReviewAuditOversizedOutputCannotApproveItsTruncatedPrefix(t *testing.T) {
	approved := `{"decision":"approve","summary":"Inspected the implementation.","findings":[]}`
	plan, output := reviewFixturePlan(t, approved, false)
	// The original response is not a single JSON verdict. Its approval prefix
	// becomes valid only if the engine discards the contradictory suffix first.
	plan.Candidates[1].Command = []string{"python3", "-c", "import sys; sys.stdin.read(); print(" + fmt.Sprintf("%q", approved) + "+' '*" + fmt.Sprint(resultLimit) + "+'{\"decision\":\"request_changes\"}')"}
	report, err := RunMission(context.Background(), plan, output, false)
	if err == nil || len(report.Steps) != 2 || report.Steps[1].State != "review_invalid" || !report.Steps[1].Truncated {
		t.Fatalf("oversized ambiguous review accepted: %v %+v", err, report)
	}
	if _, err := Apply(context.Background(), output, ""); err == nil {
		t.Fatal("truncated review approved application")
	}
}
