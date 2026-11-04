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

const fixtureCleanApproval = `{"decision":"approve","summary":"Independently inspected the actual fixed file against the acceptance criterion.","findings":[]}`

func earlyApprovalFixture(t *testing.T) (Plan, string, string) {
	t.Helper()
	options, root := guidedFixtureOptions(t)
	coder := "import sys; from pathlib import Path; prompt=sys.stdin.read(); assert 'Current mission step: implement' in prompt; Path('file.txt').write_text('fixed\\n'); print('IMPLEMENTATION_NARRATIVE_NOT_FOR_REVIEW')"
	reviewer := "import sys; from pathlib import Path; prompt=sys.stdin.read(); assert 'Current mission step: review\\n' in prompt; assert 'IMPLEMENTATION_NARRATIVE_NOT_FOR_REVIEW' not in prompt; assert 'Acceptance criteria:' in prompt; assert Path('file.txt').read_text()=='fixed\\n'; print(" + fmt.Sprintf("%q", fixtureCleanApproval) + ")"
	options.Coder = &Candidate{Harness: "command", Command: []string{"python3", "-c", coder}}
	options.Reviewer = &Candidate{Harness: "command", Command: []string{"python3", "-c", reviewer}}
	checkCount := filepath.Join(root, "check-count")
	options.CheckCommand = []string{"python3", "-c", "from pathlib import Path; assert Path('file.txt').read_text()=='fixed\\n'; p=Path(" + fmt.Sprintf("%q", checkCount) + "); p.write_text(str(int(p.read_text())+1) if p.exists() else '1')"}
	options.MaxAttempts = 1
	options.TimeoutSeconds = 30
	preview, err := PrepareGuided(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if preview.MinNativeTurns != 2 || preview.MaxNativeTurns != 4 || preview.Plan.ReviewPolicy != ReviewPolicyEarlyApproval {
		t.Fatalf("incorrect conditional turn bounds: %+v", preview)
	}
	return preview.Plan, preview.Output, checkCount
}

func TestEarlyApprovalSkipsOnlyRedundantTurnsAndRetainsExactEvidence(t *testing.T) {
	plan, output, checkCount := earlyApprovalFixture(t)
	r, err := RunMission(context.Background(), plan, output, false)
	if err != nil {
		t.Fatalf("clean approval: %v %+v", err, r)
	}
	if r.EarlyApproval == nil || len(r.Steps) != 5 || r.Steps[0].Attempts != 1 || r.Steps[1].Attempts != 1 {
		t.Fatalf("missing original execution and promoted review evidence: %+v", r)
	}
	for _, i := range []int{2, 3} {
		step := r.Steps[i]
		if step.State != "skipped" || step.SkipReason != earlyApprovalSkipReason || step.Attempts != 0 || step.Harness != "" || step.Review != nil || step.Verification != nil {
			t.Fatalf("skipped phase falsely records a turn: %+v", step)
		}
		var retained StepResult
		if err := DecodeFile(step.EvidencePath, &retained); err != nil || retained.State != "skipped" || retained.SkipReason != step.SkipReason {
			t.Fatalf("missing durable skip record: %+v %v", retained, err)
		}
	}
	if data, err := os.ReadFile(checkCount); err != nil || string(data) != "2" {
		t.Fatalf("implementation and final checks must both run exactly once: %q %v", data, err)
	}
	stored, err := LoadReport(output)
	if err != nil || RequireApprovedReview(stored) != nil {
		t.Fatalf("retained promotion invalid: %v", err)
	}
	summary, err := Summarize(context.Background(), output)
	if err != nil || !summary.CanApply || summary.EarlyApproval == nil || summary.Phases[3].State != "skipped" || summary.Phases[3].SkipReason == "" {
		t.Fatalf("summary hides skipped review or lost application readiness: %+v %v", summary, err)
	}
	html, err := os.ReadFile(filepath.Join(output, "report.html"))
	if err != nil || !strings.Contains(string(html), "response and second review were skipped; final acceptance checks still ran") {
		t.Fatalf("HTML did not explain actual execution: %v", err)
	}
	if result, err := Apply(context.Background(), output, ""); err != nil || !result.Applied {
		t.Fatalf("exact checked reviewed result not applicable: %+v %v", result, err)
	}
}

func cloneEarlyApprovalReport(t *testing.T, r Report) Report {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var copy Report
	if err := json.Unmarshal(data, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

func TestEarlyApprovalRejectsForgedMissingOrStalePromotion(t *testing.T) {
	plan, output, _ := earlyApprovalFixture(t)
	r, err := RunMission(context.Background(), plan, output, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*Report)
	}{
		{"missing-policy", func(r *Report) { r.ReviewPolicy = "" }},
		{"unknown-policy", func(r *Report) { r.ReviewPolicy = "early-approval-v2" }},
		{"missing-promotion", func(r *Report) { r.EarlyApproval = nil }},
		{"unknown-promotion-version", func(r *Report) { r.EarlyApproval.Version = 2 }},
		{"wrong-original-review", func(r *Report) { r.EarlyApproval.ReviewStep = "final-review" }},
		{"wrong-original-implementation", func(r *Report) { r.EarlyApproval.ImplementationStep = "address-review" }},
		{"wrong-promoted-content", func(r *Report) { r.EarlyApproval.ContentSHA256 = strings.Repeat("a", 64) }},
		{"wrong-review-start", func(r *Report) { r.Steps[1].StartingSnapshot.Tree = strings.Repeat("0", 40) }},
		{"wrong-review-end", func(r *Report) { r.Steps[1].FinalSnapshot.ContentSHA256 = strings.Repeat("a", 64) }},
		{"review-is-writable", func(r *Report) { r.Steps[1].ReadOnly = false }},
		{"review-context-missing", func(r *Report) { r.Steps[1].ReviewContext = "" }},
		{"original-review-not-fresh", func(r *Report) { r.ReviewContext = "" }},
		{"review-invalid", func(r *Report) { r.Steps[1].Result = "null" }},
		{"review-null-findings", func(r *Report) { r.Steps[1].Review.Findings = nil }},
		{"review-requested-changes", func(r *Report) { r.Steps[1].Review.Decision = "request_changes" }},
		{"review-text-differs", func(r *Report) { r.Steps[1].Result = strings.Replace(r.Steps[1].Result, "actual", "different", 1) }},
		{"review-modified", func(r *Report) { r.Steps[1].State = "review_modified" }},
		{"review-gate-changed", func(r *Report) { r.Steps[1].GateReview = true }},
		{"implementation-check-failed", func(r *Report) { r.Steps[0].Verification.Passed = false }},
		{"implementation-unchecked", func(r *Report) { r.Steps[0].State = "completed" }},
		{"skip-reason-missing", func(r *Report) { r.Steps[2].SkipReason = "" }},
		{"skip-counts-as-turn", func(r *Report) { r.Steps[2].Attempts = 1 }},
		{"skip-false-verification", func(r *Report) { r.Steps[2].Verification = r.Verification }},
		{"skip-false-review", func(r *Report) { r.Steps[3].Review = r.Steps[1].Review }},
		{"skip-marked-completed", func(r *Report) { r.Steps[3].State = "completed" }},
		{"skip-gate-removed", func(r *Report) { r.Steps[3].GateReview = false }},
		{"skip-phase-renamed", func(r *Report) { r.Steps[3].ID = "unchecked" }},
		{"phase-reordered", func(r *Report) { r.Steps[2], r.Steps[3] = r.Steps[3], r.Steps[2] }},
		{"final-check-missing", func(r *Report) { r.Steps = r.Steps[:4] }},
		{"final-check-failed", func(r *Report) { r.Steps[4].Verification.Passed = false }},
		{"final-check-dependencies", func(r *Report) { r.Steps[4].DependsOn = []string{"implement"} }},
		{"final-check-ran-agent", func(r *Report) { r.Steps[4].Harness = "command" }},
		{"final-check-command-changed", func(r *Report) { r.Steps[4].Verification.Checks[0].Command = []string{"true"} }},
		{"final-check-name-changed", func(r *Report) { r.Steps[4].Verification.Checks[0].Name = "different" }},
		{"final-verification-missing", func(r *Report) { r.Verification = nil }},
		{"final-snapshot-drift", func(r *Report) { r.FinalSnapshot.ContentSHA256 = strings.Repeat("b", 64) }},
		{"additional-writer", func(r *Report) {
			r.Steps = append(r.Steps, StepResult{ID: "writer", CandidateResult: CandidateResult{State: "completed"}})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := cloneEarlyApprovalReport(t, r)
			test.mutate(&copy)
			if err := RequireApprovedReview(copy); err == nil {
				t.Fatal("forged promotion authorized application")
			}
			if err := saveJSON(filepath.Join(output, "report.json"), &copy); err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(context.Background(), output, ""); err == nil {
				t.Fatal("saved forged promotion applied")
			}
		})
	}
}

func TestEarlyApprovalRejectsPlanGateMutations(t *testing.T) {
	p, _, _ := earlyApprovalFixture(t)
	for _, mutate := range []func(*Plan){
		func(p *Plan) { p.Steps[0].Verify = false },
		func(p *Plan) { p.Steps[1].Review = false },
		func(p *Plan) { p.Steps[1].ReadOnly = false },
		func(p *Plan) { p.Steps[1].Candidate = p.Steps[0].Candidate },
		func(p *Plan) { p.Steps[2].DependsOn = []string{"implement"} },
		func(p *Plan) { p.Steps[3].GateReview = false },
		func(p *Plan) { p.Steps[4].Candidate = p.Steps[0].Candidate },
		func(p *Plan) { p.Steps = p.Steps[:4] },
		func(p *Plan) { p.ReviewPolicy = "unknown" },
	} {
		copy := p
		copy.Steps = append([]Step(nil), p.Steps...)
		mutate(&copy)
		if err := ValidatePlan(copy); err == nil {
			t.Fatal("changed guided gates admitted with early-approval policy")
		}
	}
}

func TestEarlyApprovalStillRefusesStaleSourceOrCheckedResult(t *testing.T) {
	for _, source := range []bool{true, false} {
		t.Run(fmt.Sprint("source=", source), func(t *testing.T) {
			plan, output, _ := earlyApprovalFixture(t)
			r, err := RunMission(context.Background(), plan, output, false)
			if err != nil {
				t.Fatal(err)
			}
			target := r.Worktree
			if source {
				target = plan.Repository
			}
			fixtureWrite(t, filepath.Join(target, "file.txt"), []byte("changed since checks\n"))
			if _, err := Apply(context.Background(), output, ""); err == nil {
				t.Fatal("early approval bypassed source/result freshness")
			}
		})
	}
}

func TestEarlyApprovalCancellationAtFinalCheckRemainsInspectable(t *testing.T) {
	plan, output, checkCount := earlyApprovalFixture(t)
	marker := filepath.Join(filepath.Dir(checkCount), "final-check-started")
	plan.Checks[0].Command = []string{"python3", "-c", "from pathlib import Path; import time; p=Path(" + fmt.Sprintf("%q", checkCount) + "); prior=p.exists(); p.write_text('1');\nif prior:\n Path(" + fmt.Sprintf("%q", marker) + ").write_text('started'); time.sleep(30)"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		report Report
		err    error
	}
	done := make(chan outcome, 1)
	go func() { r, err := RunMission(ctx, plan, output, false); done <- outcome{r, err} }()
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case result := <-done:
			t.Fatalf("ended before final check: %v %+v", result.err, result.report)
		case <-deadline:
			t.Fatal("final check did not start")
		case <-ticker.C:
		}
	}
	live, err := LoadReport(output)
	if err != nil || live.EarlyApproval == nil || len(live.Steps) != 5 || live.Steps[4].State != "running" {
		t.Fatalf("live fast path not inspectable: %v %+v", err, live)
	}
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) || result.report.State != "interrupted" {
			t.Fatalf("lost cancellation: %v %+v", result.err, result.report)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("final check did not stop")
	}
	if saved, err := LoadReport(output); err != nil || saved.State != "interrupted" {
		t.Fatalf("cancelled promotion not inspectable: %v %+v", err, saved)
	}
	if _, err := Apply(context.Background(), output, ""); err == nil {
		t.Fatal("cancelled final checks accepted")
	}
}

func TestEarlyApprovalRequestedChangesAndLegacyPlansKeepFullLoop(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint("legacy=", legacy), func(t *testing.T) {
			plan, output, count := earlyApprovalFixture(t)
			if legacy {
				plan.ReviewPolicy = ""
			}
			plan.Candidates[0].Command = []string{"python3", "-c", "import sys; from pathlib import Path; prompt=sys.stdin.read(); assert 'Current mission step: implement' in prompt or 'Current mission step: address-review' in prompt; Path('file.txt').write_text('fixed\\n'); print('Implemented and checked the requested work')"}
			initial := `{"decision":"request_changes","summary":"Please confirm the requested behavior.","findings":[{"file":"file.txt","line":1,"message":"Confirm the acceptance criterion before final review."}]}`
			if legacy {
				initial = fixtureCleanApproval
			}
			plan.Candidates[1].Command = []string{"python3", "-c", "import sys; prompt=sys.stdin.read(); assert 'Prior step' not in prompt; print(" + fmt.Sprintf("%q", fixtureCleanApproval) + " if 'Current mission step: final-review' in prompt else " + fmt.Sprintf("%q", initial) + ")"}
			r, err := RunMission(context.Background(), plan, output, false)
			if err != nil {
				t.Fatalf("full loop failed: %v %+v", err, r)
			}
			if r.EarlyApproval != nil || len(r.Steps) != 5 {
				t.Fatal("legacy or requested-changes review unexpectedly promoted")
			}
			for i := 0; i < 4; i++ {
				if r.Steps[i].Attempts != 1 || r.Steps[i].State == "skipped" {
					t.Fatalf("four-turn path lost a real turn: %+v", r.Steps[i])
				}
			}
			if data, err := os.ReadFile(count); err != nil || string(data) != "3" {
				t.Fatalf("full loop must check implement, response and final revision: %q %v", data, err)
			}
			if err := RequireApprovedReview(r); err != nil {
				t.Fatalf("valid original final review rejected: %v", err)
			}
			if _, err := LoadReport(output); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEarlyApprovalInvalidReviewOrFailedFinalChecksNeverApply(t *testing.T) {
	for _, kind := range []string{"malformed", "null", "null-findings", "edited", "final-check-failed", "final-check-edited"} {
		t.Run(kind, func(t *testing.T) {
			plan, output, count := earlyApprovalFixture(t)
			verdict, edit := fixtureCleanApproval, ""
			switch kind {
			case "malformed":
				verdict = "Approved, looks good"
			case "null":
				verdict = "null"
			case "null-findings":
				verdict = `{"decision":"approve","summary":"Inspected the file.","findings":null}`
			case "edited":
				edit = "from pathlib import Path; Path('file.txt').write_text('review changed this\\n'); "
			}
			plan.Candidates[1].Command = []string{"python3", "-c", "import sys; sys.stdin.read(); " + edit + "print(" + fmt.Sprintf("%q", verdict) + ")"}
			if strings.HasPrefix(kind, "final-check-") {
				fail := "raise SystemExit(1)"
				if kind == "final-check-edited" {
					fail = "Path('file.txt').write_text('changed during final check\\n')"
				}
				plan.Checks[0].Command = []string{"python3", "-c", "from pathlib import Path; p=Path(" + fmt.Sprintf("%q", count) + "); prior=p.exists(); p.write_text('1');\nif prior: " + fail}
			}
			r, err := RunMission(context.Background(), plan, output, false)
			if err == nil {
				t.Fatalf("invalid %s journey accepted: %+v", kind, r)
			}
			if strings.HasPrefix(kind, "final-check-") && r.EarlyApproval == nil {
				t.Fatal("fixture did not reach promoted review before failed final check")
			}
			if _, err := LoadReport(output); err != nil {
				t.Fatalf("failed journey not inspectable: %v", err)
			}
			if _, err := Apply(context.Background(), output, ""); err == nil {
				t.Fatal("invalid review or final check applied")
			}
			if data, _ := os.ReadFile(filepath.Join(plan.Repository, "file.txt")); string(data) != "base\n" {
				t.Fatal("failed journey changed original source")
			}
		})
	}
}

func TestEarlyApprovalUsesSuccessfulImplementationRetryIdentity(t *testing.T) {
	plan, output, count := earlyApprovalFixture(t)
	plan.MaxAttempts = 2
	plan.Candidates[0].Command = []string{"python3", "-c", "import sys; from pathlib import Path; prompt=sys.stdin.read(); repaired='The prior turn completed' in prompt; Path('file.txt').write_text('fixed\\n' if repaired else 'broken\\n'); print('Attempt completed')"}
	r, err := RunMission(context.Background(), plan, output, false)
	if err != nil || r.EarlyApproval == nil || r.Steps[0].Attempts != 2 || r.Steps[1].Attempts != 1 {
		t.Fatalf("retry lost exact successful revision identity: %v %+v", err, r)
	}
	if data, err := os.ReadFile(count); err != nil || string(data) != "2" {
		// The failing acceptance command asserts before incrementing this counter;
		// exactly the successful implementation check and final check increment it.
		t.Fatalf("final check missing or repeated after retry: %q %v", data, err)
	}
	if err := RequireApprovedReview(r); err != nil {
		t.Fatal(err)
	}
}

func TestEarlyApprovalSelectionRejectsWorktreeDrift(t *testing.T) {
	plan, output, _ := earlyApprovalFixture(t)
	r, err := RunMission(context.Background(), plan, output, false)
	if err != nil {
		t.Fatal(err)
	}
	r.Steps = r.Steps[:2]
	r.EarlyApproval = nil
	fixtureWrite(t, filepath.Join(r.Worktree, "file.txt"), []byte("changed after first approval\n"))
	if e, err := selectEarlyApproval(context.Background(), r); err == nil || e != nil {
		t.Fatal("stale initial approval skipped remaining reviewers")
	}
}

func TestEarlyApprovalCannotDowngradeToSyntheticFinalReview(t *testing.T) {
	plan, output, _ := earlyApprovalFixture(t)
	original, err := RunMission(context.Background(), plan, output, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, stripPolicy := range []bool{false, true} {
		t.Run(fmt.Sprint("strip-policy=", stripPolicy), func(t *testing.T) {
			r := cloneEarlyApprovalReport(t, original)
			r.EarlyApproval = nil
			if stripPolicy {
				r.ReviewPolicy = ""
			}
			r.Steps[2].State, r.Steps[2].SkipReason = "completed", ""
			r.Steps[3].State, r.Steps[3].SkipReason = "completed", ""
			r.Steps[3].Review = r.Steps[1].Review
			r.Steps[3].ReviewContext = "fresh"
			r.Steps[3].StartingSnapshot = r.Steps[1].StartingSnapshot
			r.Steps[3].FinalSnapshot = r.Steps[1].FinalSnapshot
			r.Steps = r.Steps[:4]
			if !stripPolicy && RequireApprovedReview(r) == nil {
				t.Fatal("policy-bearing synthetic final review passed pure authority check")
			}
			if err := saveJSON(filepath.Join(output, "report.json"), &r); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadReport(output); err == nil {
				t.Fatal("selectively downgraded report loaded with original retained plan")
			}
			if _, err := Apply(context.Background(), output, ""); err == nil {
				t.Fatal("selectively downgraded synthetic review applied")
			}
		})
	}
}

func TestEarlyApprovalRetainsFullWhitespaceAndLargeVerdicts(t *testing.T) {
	for _, kind := range []string{"whitespace", "large-summary", "large-findings"} {
		t.Run(kind, func(t *testing.T) {
			plan, output, count := earlyApprovalFixture(t)
			initial, final := fixtureCleanApproval, fixtureCleanApproval
			if kind == "whitespace" {
				initial = strings.Repeat(" ", 70<<10) + initial
			} else if kind == "large-summary" {
				verdict := ReviewResult{Decision: "approve", Summary: strings.Repeat("Inspected current file. ", 500), Findings: []ReviewFinding{}}
				encoded, err := json.Marshal(verdict)
				if err != nil {
					t.Fatal(err)
				}
				initial = string(encoded)
			} else {
				findings := make([]ReviewFinding, 30)
				for i := range findings {
					findings[i] = ReviewFinding{File: "file.txt", Line: 1, Message: fmt.Sprint("Confirm behavior for case ", i, ": ", strings.Repeat("actionable detail ", 100))}
				}
				encoded, err := json.Marshal(ReviewResult{Decision: "request_changes", Summary: "Address every retained finding.", Findings: findings})
				if err != nil {
					t.Fatal(err)
				}
				initial = string(encoded)
				final = strings.Repeat(" ", 70<<10) + final
				plan.Candidates[0].Command = []string{"python3", "-c", "import sys; from pathlib import Path; prompt=sys.stdin.read();\nif 'Current mission step: address-review' in prompt: assert 'Confirm behavior for case 29:' in prompt\nPath('file.txt').write_text('fixed\\n'); print('Implemented and checked')"}
			}
			initialPath, finalPath := filepath.Join(filepath.Dir(output), "initial-review.txt"), filepath.Join(filepath.Dir(output), "final-review.txt")
			fixtureWrite(t, initialPath, []byte(initial))
			fixtureWrite(t, finalPath, []byte(final))
			plan.Candidates[1].Command = []string{"python3", "-c", "import sys; from pathlib import Path; prompt=sys.stdin.read(); print(Path(" + fmt.Sprintf("%q", finalPath) + ").read_text() if 'Current mission step: final-review' in prompt else Path(" + fmt.Sprintf("%q", initialPath) + ").read_text())"}
			r, err := RunMission(context.Background(), plan, output, false)
			if err != nil {
				t.Fatalf("valid large review failed: %v %+v", err, r)
			}
			stored, err := LoadReport(output)
			if err != nil {
				t.Fatalf("compaction made review unreadable: %v", err)
			}
			if stored.Steps[1].Truncated || stored.Steps[1].ReviewTruncated || strings.TrimSuffix(stored.Steps[1].Result, "\n") != initial {
				t.Fatal("policy review authority bytes were compacted")
			}
			if kind == "large-findings" {
				if stored.EarlyApproval != nil || len(stored.Steps[1].Review.Findings) != 30 || len(stored.Steps[1].Review.Findings[29].Message) <= 1024 || stored.Steps[3].Truncated || stored.Steps[3].ReviewTruncated {
					t.Fatal("fallback lost complete findings or final verdict")
				}
				if data, _ := os.ReadFile(count); string(data) != "3" {
					t.Fatalf("fallback checks missing: %q", data)
				}
			} else if stored.EarlyApproval == nil {
				t.Fatal("valid fully retained clean review did not promote")
			}
			if err := RequireApprovedReview(stored); err != nil {
				t.Fatal(err)
			}
			if result, err := Apply(context.Background(), output, ""); err != nil || !result.Applied {
				t.Fatalf("full verdict not applicable: %+v %v", result, err)
			}
		})
	}
}

func TestEarlyApprovalBindsRetainedPlanAndExecutionArtifacts(t *testing.T) {
	plan, output, _ := earlyApprovalFixture(t)
	original, err := RunMission(context.Background(), plan, output, false)
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(output, "plan.json")
	paths := []string{planPath, original.Steps[0].EvidencePath, original.Steps[1].EvidencePath, original.Steps[2].EvidencePath, original.Steps[3].EvidencePath, original.Steps[4].EvidencePath, filepath.Join(output, "verification.json")}
	baseline := map[string][]byte{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		baseline[path] = data
	}
	for _, kind := range []string{"all-report-checks-changed", "synthetic-fallback", "planned-profile-changed", "planned-role-swapped", "missing-attempt", "attempt-result-changed", "attempt-input-changed", "missing-skip", "skip-record-changed", "final-check-record-changed", "final-result-record-changed"} {
		t.Run(kind, func(t *testing.T) {
			for path, data := range baseline {
				fixtureWrite(t, path, data)
			}
			r := cloneEarlyApprovalReport(t, original)
			switch kind {
			case "all-report-checks-changed":
				for _, verification := range []*Verification{r.Steps[0].Verification, r.Steps[4].Verification, r.Verification} {
					verification.Checks[0].Command = []string{"true"}
				}
			case "synthetic-fallback":
				r.EarlyApproval = nil
				r.Steps[2].SkipReason, r.Steps[3].SkipReason = "", ""
				r.Steps[2].CandidateResult = r.Steps[0].CandidateResult
				r.Steps[2].StartingSnapshot = r.Steps[0].FinalSnapshot
				r.Steps[2].EvidencePath = filepath.Join(output, "steps", "address-review", "attempt-1.json")
				r.Steps[3].CandidateResult = r.Steps[1].CandidateResult
				r.Steps[3].EvidencePath = filepath.Join(output, "steps", "final-review", "attempt-1.json")
			case "planned-profile-changed":
				var p Plan
				if err := DecodeFile(planPath, &p); err != nil {
					t.Fatal(err)
				}
				p.Candidates[0].Command = []string{"true"}
				if err := saveJSON(planPath, &p); err != nil {
					t.Fatal(err)
				}
			case "planned-role-swapped":
				r.Steps[0].CandidateID = r.Steps[1].CandidateID
				r.Steps[0].ProfileSHA256 = r.Steps[1].ProfileSHA256
			case "missing-attempt":
				if err := os.Remove(r.Steps[0].EvidencePath); err != nil {
					t.Fatal(err)
				}
			case "attempt-result-changed", "attempt-input-changed":
				var retained CandidateResult
				if err := DecodeFile(r.Steps[1].EvidencePath, &retained); err != nil {
					t.Fatal(err)
				}
				if kind == "attempt-result-changed" {
					retained.Result = strings.Replace(retained.Result, "actual", "other", 1)
				} else {
					retained.InputSHA256 = strings.Repeat("a", 64)
				}
				if err := saveJSON(r.Steps[1].EvidencePath, &retained); err != nil {
					t.Fatal(err)
				}
			case "missing-skip":
				if err := os.Remove(r.Steps[2].EvidencePath); err != nil {
					t.Fatal(err)
				}
			case "skip-record-changed":
				var retained StepResult
				if err := DecodeFile(r.Steps[2].EvidencePath, &retained); err != nil {
					t.Fatal(err)
				}
				retained.SkipReason = "different_reason"
				if err := saveJSON(r.Steps[2].EvidencePath, &retained); err != nil {
					t.Fatal(err)
				}
			case "final-check-record-changed", "final-result-record-changed":
				path := r.Steps[4].EvidencePath
				if kind == "final-result-record-changed" {
					path = filepath.Join(output, "verification.json")
				}
				var retained Verification
				if err := DecodeFile(path, &retained); err != nil {
					t.Fatal(err)
				}
				retained.Checks[0].Command = []string{"true"}
				if err := saveJSON(path, &retained); err != nil {
					t.Fatal(err)
				}
			}
			if err := saveJSON(filepath.Join(output, "report.json"), &r); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadReport(output); err == nil {
				t.Fatal("inconsistent retained artifacts loaded")
			}
			if _, err := Apply(context.Background(), output, ""); err == nil {
				t.Fatal("inconsistent retained artifacts applied")
			}
		})
	}
}

func TestEarlyApprovalRetainedBindingAllowsIntendedAggregateExcerpts(t *testing.T) {
	plan, output, _ := earlyApprovalFixture(t)
	plan.Goal = strings.Repeat("Confirm the requested behavior. ", 2500)
	plan.Candidates[0].Command = []string{"python3", "-c", "import sys; from pathlib import Path; sys.stdin.read(); Path('file.txt').write_text('fixed\\n'); print('N'*70000)"}
	plan.Checks[0].Command = []string{"python3", "-c", "from pathlib import Path; assert Path('file.txt').read_text()=='fixed\\n'; print('check log '*1000)"}
	r, err := RunMission(context.Background(), plan, output, false)
	if err != nil {
		t.Fatalf("legitimate excerpts rejected: %v", err)
	}
	if !r.Steps[0].InputTruncated || !r.Steps[0].Truncated || len(r.Steps[0].Verification.Checks[0].Output) <= 4096 {
		t.Fatal("fixture did not exercise compacted input, result and check diagnostics")
	}
	if _, err := LoadReport(output); err != nil {
		t.Fatal(err)
	}
	if result, err := Apply(context.Background(), output, ""); err != nil || !result.Applied {
		t.Fatalf("excerpted evidence did not apply: %+v %v", result, err)
	}
}

func TestPolicyCandidateUsageJSONFormattingIsSemantic(t *testing.T) {
	a := CandidateResult{ID: "implement", State: "verified", Usage: json.RawMessage(`{"input_tokens":0,"output_tokens":1}`), UsageRecords: []AttemptUsage{{Attempt: 1, Usage: json.RawMessage(`{"a":1,"b":[2,3]}`)}}}
	b := a
	b.Usage = json.RawMessage("{\n  \"output_tokens\": 1,\n  \"input_tokens\": 0\n}")
	b.UsageRecords = []AttemptUsage{{Attempt: 1, Usage: json.RawMessage("{\n \"b\": [2,3], \"a\": 1\n}")}}
	if !samePolicyCandidate(a, b) {
		t.Fatal("equivalent nested usage JSON failed artifact comparison")
	}
	b.Usage = json.RawMessage(`{"input_tokens":0,"output_tokens":2}`)
	if samePolicyCandidate(a, b) {
		t.Fatal("changed usage value accepted")
	}
	b = a
	b.Result = "different actual result"
	if samePolicyCandidate(a, b) {
		t.Fatal("changed result accepted while normalizing usage")
	}
	a.Usage = json.RawMessage(`{"tokens":9007199254740992}`)
	b = a
	b.Usage = json.RawMessage(`{"tokens":9007199254740993}`)
	if samePolicyCandidate(a, b) {
		t.Fatal("changed usage integer above2^53 lost precision")
	}
}

func TestPolicyIncompleteStatesNeverAuthorizeApplication(t *testing.T) {
	plan, output, _ := earlyApprovalFixture(t)
	original, err := RunMission(context.Background(), plan, output, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"failed", "interrupted", "running"} {
		for _, synthetic := range []bool{false, true} {
			t.Run(fmt.Sprint(state, "/synthetic=", synthetic), func(t *testing.T) {
				r := cloneEarlyApprovalReport(t, original)
				r.State = state
				if state == "running" {
					r.FinishedAt = ""
				} else {
					r.Error = "Recorded work stopped before acceptance"
				}
				if synthetic {
					r.EarlyApproval = nil
					r.Steps[2].SkipReason, r.Steps[3].SkipReason = "", ""
					r.Steps[2].CandidateResult = r.Steps[0].CandidateResult
					r.Steps[2].StartingSnapshot = r.Steps[0].FinalSnapshot
					r.Steps[2].EvidencePath = filepath.Join(output, "steps", "address-review", "attempt-1.json")
					r.Steps[3].CandidateResult = r.Steps[1].CandidateResult
					r.Steps[3].EvidencePath = filepath.Join(output, "steps", "final-review", "attempt-1.json")
				}
				if err := saveJSON(filepath.Join(output, "report.json"), &r); err != nil {
					t.Fatal(err)
				}
				if _, err := LoadReport(output); err != nil {
					t.Fatalf("incomplete saved work should remain inspectable: %v", err)
				}
				if summary, err := Summarize(context.Background(), output); err != nil || summary.CanApply {
					t.Fatalf("incomplete work advertised application: %+v %v", summary, err)
				}
				if _, err := Apply(context.Background(), output, ""); err == nil {
					t.Fatal("incomplete policy work applied")
				}
			})
		}
	}
	if data, _ := os.ReadFile(filepath.Join(plan.Repository, "file.txt")); string(data) != "base\n" {
		t.Fatal("incomplete report changed source")
	}
}

func TestPolicyFailedVerificationPrefixesRemainInspectable(t *testing.T) {
	plan, output, _ := earlyApprovalFixture(t)
	plan.Checks = []Check{{Name: "mutating-check", Command: []string{"python3", "-c", "from pathlib import Path; Path('file.txt').write_text('check changed contents\\n')"}}, {Name: "never-reached", Command: []string{"/usr/bin/true"}}}
	r, err := RunMission(context.Background(), plan, output, false)
	if err == nil || r.State != "failed" || r.Steps[0].Verification == nil || len(r.Steps[0].Verification.Checks) != 1 {
		t.Fatalf("did not retain failed check prefix: %v %+v", err, r)
	}
	if _, err := LoadReport(output); err != nil {
		t.Fatalf("failed prefix not inspectable: %v", err)
	}
	if _, err := Apply(context.Background(), output, ""); err == nil {
		t.Fatal("failed prefix applied")
	}
	// A preparation failure can retain the initial fingerprint before any
	// check result exists. Simulate that saved record without invoking a sandbox.
	r.Steps[0].Verification.Checks = []CheckResult{}
	r.Steps[0].Verification.Passed = false
	if err := saveJSON(filepath.Join(output, "report.json"), &r); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReport(output); err != nil {
		t.Fatalf("zero-check failed preparation not inspectable: %v", err)
	}
	if _, err := Apply(context.Background(), output, ""); err == nil {
		t.Fatal("zero-check failed preparation applied")
	}
}

func TestPolicyVerificationPrefixBindingCannotChangeOrderOrClaimPass(t *testing.T) {
	checks := []Check{{Name: "first", Command: []string{"/usr/bin/true"}}, {Name: "second", Command: []string{"/usr/bin/false"}}}
	prefix := Verification{Passed: false, Checks: []CheckResult{{Name: "first", Command: []string{"/usr/bin/true"}}}}
	if !verificationMatchesPlan(&prefix, checks, true) {
		t.Fatal("unsuccessful exact prefix rejected")
	}
	if verificationMatchesPlan(&prefix, checks, false) {
		t.Fatal("completed run accepted partial checks")
	}
	prefix.Passed = true
	if verificationMatchesPlan(&prefix, checks, true) {
		t.Fatal("passing verification accepted partial checks")
	}
	prefix.Passed = false
	prefix.Checks[0].Name = "second"
	if verificationMatchesPlan(&prefix, checks, true) {
		t.Fatal("reordered failed prefix accepted")
	}
	prefix.Checks[0].Name = "first"
	prefix.Checks[0].Command = []string{"true"}
	if verificationMatchesPlan(&prefix, checks, true) {
		t.Fatal("changed failed prefix argv accepted")
	}
}

func TestPolicyCancelledVerificationPrefixRemainsInspectable(t *testing.T) {
	plan, output, _ := earlyApprovalFixture(t)
	marker := filepath.Join(filepath.Dir(output), "second-check-started")
	plan.Checks = []Check{{Name: "first", Command: []string{"/usr/bin/true"}}, {Name: "second", Command: []string{"python3", "-c", "from pathlib import Path; import time; Path(" + fmt.Sprintf("%q", marker) + ").write_text('started'); time.sleep(30)"}}, {Name: "never-reached", Command: []string{"/usr/bin/true"}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		report Report
		err    error
	}
	done := make(chan outcome, 1)
	go func() { r, err := RunMission(ctx, plan, output, false); done <- outcome{r, err} }()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(20 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case result := <-done:
			t.Fatalf("ended before cancellation: %v", result.err)
		case <-deadline:
			t.Fatal("second check did not start")
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) || result.report.State != "interrupted" || result.report.Steps[0].Verification == nil || len(result.report.Steps[0].Verification.Checks) != 2 {
			t.Fatalf("cancelled prefix not retained: %v %+v", result.err, result.report)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cancelled checks did not stop")
	}
	if _, err := LoadReport(output); err != nil {
		t.Fatalf("cancelled prefix not inspectable: %v", err)
	}
	if _, err := Apply(context.Background(), output, ""); err == nil {
		t.Fatal("cancelled prefix applied")
	}
}

func TestPolicyWholeReportCompactionKeepsSkippedPhasesAndBindings(t *testing.T) {
	plan, output, _ := earlyApprovalFixture(t)
	r, err := RunMission(context.Background(), plan, output, false)
	if err != nil {
		t.Fatal(err)
	}
	// Build bounded recorded check argv whose JSON escaping forces the global
	// 24 MiB fallback. These size fixtures are not executed as project checks.
	command := []string{"/usr/bin/true"}
	for i := 0; i < 63; i++ {
		command = append(command, strings.Repeat("\x01", 16<<10))
	}
	shorter := append([]string(nil), command[:32]...)
	plan.Checks = []Check{{Name: "large-recorded-check", Command: command}, {Name: "second-recorded-check", Command: shorter}}
	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("size fixture exceeds accepted check argv bounds: %v", err)
	}
	if err := saveJSON(filepath.Join(output, "plan.json"), &plan); err != nil {
		t.Fatal(err)
	}
	checks := []CheckResult{{Name: plan.Checks[0].Name, Command: command, Passed: true}, {Name: plan.Checks[1].Name, Command: shorter, Passed: true}}
	var implementation CandidateResult
	if err := DecodeFile(r.Steps[0].EvidencePath, &implementation); err != nil {
		t.Fatal(err)
	}
	implementation.Verification.Checks = checks
	if err := saveJSON(r.Steps[0].EvidencePath, &implementation); err != nil {
		t.Fatal(err)
	}
	r.Steps[0].CandidateResult = implementation
	r.Steps[4].Verification.Checks = checks
	r.Verification.Checks = checks
	if err := saveJSON(r.Steps[4].EvidencePath, r.Steps[4].Verification); err != nil {
		t.Fatal(err)
	}
	if err := saveJSON(filepath.Join(output, "verification.json"), r.Verification); err != nil {
		t.Fatal(err)
	}
	if err := saveJSON(filepath.Join(output, "report.json"), r); err != nil {
		t.Fatal(err)
	}
	var compacted Report
	if err := DecodeFile(filepath.Join(output, "report.json"), &compacted); err != nil {
		t.Fatal(err)
	}
	if !compacted.DiagnosticsTruncated || compacted.Steps[0].Input != "" || !compacted.Steps[0].InputTruncated {
		t.Fatal("fixture did not force global diagnostic omission")
	}
	for _, i := range []int{2, 3} {
		if compacted.Steps[i].InputTruncated || compacted.Steps[i].Truncated || compacted.Steps[i].Attempts != 0 {
			t.Fatal("global compaction invented skipped execution/input evidence")
		}
	}
	if _, err := LoadReport(output); err != nil {
		t.Fatalf("whole-report projection rejected supported global compaction: %v", err)
	}
}

func TestPolicyUsageCompactionBoundariesSurviveRetainedFormatting(t *testing.T) {
	for _, kind := range []string{"indentation", "html-escaping"} {
		t.Run(kind, func(t *testing.T) {
			plan, output, _ := earlyApprovalFixture(t)
			r, err := RunMission(context.Background(), plan, output, false)
			if err != nil {
				t.Fatal(err)
			}
			var raw json.RawMessage
			if kind == "indentation" {
				usage := map[string]string{}
				for i := 0; i < 400; i++ {
					usage[fmt.Sprintf("field-%03d", i)] = "v"
				}
				raw, err = json.Marshal(usage)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				raw = json.RawMessage(`{"html":"` + strings.Repeat("<", 1400) + `"}`)
			}
			if len(raw) >= 8192 {
				t.Fatal("fixture raw usage is not below compaction limit")
			}
			var retained CandidateResult
			if err := DecodeFile(r.Steps[0].EvidencePath, &retained); err != nil {
				t.Fatal(err)
			}
			retained.Usage = raw
			retained.UsageRecords = []AttemptUsage{{Attempt: 1, Usage: raw}}
			if err := saveJSON(retained.EvidencePath, &retained); err != nil {
				t.Fatal(err)
			}
			var formatted CandidateResult
			if err := DecodeFile(retained.EvidencePath, &formatted); err != nil {
				t.Fatal(err)
			}
			if len(formatted.Usage) <= 8192 {
				t.Fatalf("fixture stored usage did not cross limit: %d", len(formatted.Usage))
			}
			r.Steps[0].CandidateResult = retained
			if err := saveJSON(filepath.Join(output, "report.json"), r); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadReport(output)
			if err != nil {
				t.Fatalf("formatting changed usage retention decision: %v", err)
			}
			if kind == "indentation" {
				if len(loaded.Steps[0].Usage) == 0 || len(loaded.Steps[0].UsageRecords) != 1 || len(loaded.Steps[0].UsageRecords[0].Usage) == 0 {
					t.Fatal("small canonical usage became unknown after indentation")
				}
			} else if len(loaded.Steps[0].Usage) != 0 || len(loaded.Steps[0].UsageRecords) != 1 || string(loaded.Steps[0].UsageRecords[0].Usage) != "null" || !loaded.DiagnosticsTruncated {
				t.Fatal("over-limit HTML-escaped canonical usage did not remain explicitly unknown")
			}
			if result, err := Apply(context.Background(), output, ""); err != nil || !result.Applied {
				t.Fatalf("usage formatting changed checked application authority: %+v %v", result, err)
			}
		})
	}
}

func TestPolicyUnknownUsageNullRemainsUnknownAfterReload(t *testing.T) {
	nilUsage := CandidateResult{UsageRecords: []AttemptUsage{{Attempt: 1}}}
	loaded := CandidateResult{Usage: json.RawMessage(`null`), UsageRecords: []AttemptUsage{{Attempt: 1, Usage: json.RawMessage(`null`)}}}
	if !samePolicyCandidate(nilUsage, loaded) {
		t.Fatal("unknown usage null did not retain nil/unknown meaning after JSON reload")
	}
	canonical, err := canonicalUsageJSON(json.RawMessage(`null`))
	if err != nil || len(canonical) != 0 {
		t.Fatal("unknown usage became a recorded value")
	}
}
