package workflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuidedNativeTurnBudgetsShareOverallDeadline(t *testing.T) {
	guidedFakeTools(t, "codex", "claude")
	for _, tc := range []struct {
		name                                 string
		overall, coder, reviewer             int
		wantOverall, wantCoder, wantReviewer int
		wantError                            bool
	}{
		{name: "default budget", wantOverall: 600, wantCoder: 600, wantReviewer: 600},
		{name: "private pilot budget", overall: 435, wantOverall: 435, wantCoder: 435, wantReviewer: 435},
		{name: "short shared budget", overall: 30, wantOverall: 30, wantCoder: 30, wantReviewer: 30},
		{name: "existing per-turn ceiling", overall: 1200, wantOverall: 1200, wantCoder: 600, wantReviewer: 600},
		{name: "explicit role limits", overall: 435, coder: 41, reviewer: 123, wantOverall: 435, wantCoder: 41, wantReviewer: 123},
		{name: "invalid explicit role limit is not clamped", overall: 435, coder: 601, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options, _ := guidedFixtureOptions(t)
			options.TimeoutSeconds = tc.overall
			options.Coder = &Candidate{Harness: "codex", TimeoutSeconds: tc.coder}
			options.Reviewer = &Candidate{Harness: "claude", TimeoutSeconds: tc.reviewer}
			preview, err := PrepareGuided(context.Background(), options)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "time bounds") {
					t.Fatalf("invalid explicit timeout was changed or accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if preview.Plan.TimeoutSeconds != tc.wantOverall || preview.Plan.Candidates[0].TimeoutSeconds != tc.wantCoder || preview.Plan.Candidates[1].TimeoutSeconds != tc.wantReviewer {
				t.Fatalf("overall/role budgets differ: overall=%d candidates=%+v", preview.Plan.TimeoutSeconds, preview.Plan.Candidates)
			}
			if options.Coder.TimeoutSeconds != tc.coder || options.Reviewer.TimeoutSeconds != tc.reviewer {
				t.Fatal("preparing a preview changed the owner's candidate profiles")
			}
			if preview.Plan.MaxParallel != 1 || preview.Plan.ReviewPolicy != ReviewPolicyEarlyApproval || len(preview.Plan.Steps) != 5 || !preview.Plan.Steps[4].Verify {
				t.Fatal("changing the role budget altered execution, review or final-check policy")
			}
			for _, i := range []int{0, 2} {
				if !strings.Contains(preview.Plan.Steps[i].Goal, guidedCheckBudgetInstructions) {
					t.Fatal("a coding phase omitted targeted-check and foreground-completion guidance")
				}
			}
		})
	}
}

func TestGuidedAntigravityTurnLimitsRemainUnchanged(t *testing.T) {
	options, _ := guidedFixtureOptions(t)
	options.Coder, options.Reviewer = nil, nil
	options.TimeoutSeconds = 435
	guidedFakeTools(t, "codex", "agy")
	preview, err := PrepareGuided(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Plan.Candidates[0].TimeoutSeconds != 435 || preview.Plan.Candidates[1].TimeoutSeconds != 90 || !strings.Contains(strings.Join(preview.Plan.Candidates[1].Command, " "), "--print-timeout 90s") {
		t.Fatalf("native Antigravity's own limit changed: %+v", preview.Plan.Candidates)
	}
	bridge, err := guidedCandidate("reviewer", &Candidate{Harness: "antigravity-bridge"})
	if err != nil || bridge.TimeoutSeconds != 180 {
		t.Fatalf("isolated bridge's profile limit changed: %+v %v", bridge, err)
	}
}

func TestEarlierGuidedGoalsAndTurnLimitsRemainInspectable(t *testing.T) {
	plan, output, _ := earlyApprovalFixture(t)
	// Reproduce the published v0.14 coding instructions and role limits. Loading
	// retained work must validate its own plan, not rebuild today's defaults.
	plan.Steps[0].Goal = "Implement the requested work in this worktree. Preserve unrelated work, do not delegate or invoke other models, and use the declared checks to validate the changes. Report what changed and any remaining limitations."
	plan.Steps[2].Goal = "Address the preceding review's actionable findings and rerun the declared checks. If the review approved without findings, preserve the implementation and report that no changes were needed. Do not make unrelated improvements, delegate or invoke other models."
	for i := range plan.Candidates {
		plan.Candidates[i].TimeoutSeconds = 180
	}
	if _, err := RunMission(context.Background(), plan, output, false); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReport(output); err != nil {
		t.Fatalf("historical guided instructions/limits became unreadable: %v", err)
	}
	// Compatibility does not permit selectively changing an executed profile.
	plan.Candidates[0].TimeoutSeconds = 179
	if err := saveJSON(filepath.Join(output, "plan.json"), &plan); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReport(output); err == nil {
		t.Fatal("changing the retained turn limit bypassed executed-profile binding")
	}
}
