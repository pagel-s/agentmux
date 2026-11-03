package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowRejectsNativeHistoryBeforeAdmission(t *testing.T) {
	repo, root := fixtureRepo(t)
	for _, profile := range []Candidate{
		{ID: "reviewer", Harness: "antigravity", Command: []string{"agy", "--continue"}},
		{ID: "reviewer", Harness: "antigravity", Command: []string{"agy", "--conversation", "prior-review"}},
		{ID: "reviewer", Harness: "claude", Command: []string{"claude", "--resume=old-context"}},
		{ID: "reviewer", Harness: "claude", Command: []string{"claude", "-pc"}},
		{ID: "reviewer", Harness: "claude", Command: []string{"claude", "-prprior-review"}},
		{ID: "reviewer", Harness: "claude", Command: []string{"claude", "attach", "prior-review"}},
		{ID: "reviewer", Harness: "claude", Command: []string{"claude", "respawn", "prior-review"}},
		{ID: "reviewer", Harness: "claude", Command: []string{"claude", "--bg"}},
		{ID: "reviewer", Harness: "claude", Command: []string{"claude", "--background"}},
		{ID: "reviewer", Harness: "claude", Command: []string{"claude", "--from-pr=123"}},
		{ID: "reviewer", Harness: "claude", Command: []string{"claude", "--teleport", "old-context"}},
		{ID: "reviewer", Harness: "pi", Command: []string{"pi", "--session", "prior.jsonl"}},
		{ID: "reviewer", Harness: "codex", Command: []string{"codex", "--resume=old-context"}},
		{ID: "reviewer", Harness: "codex", Command: []string{"codex", "-C/tmp/wrong-review-project"}},
	} {
		t.Run(profile.Harness, func(t *testing.T) {
			output := filepath.Join(root, "resumed-"+profile.Harness)
			p := comparePlan(repo, profile)
			if _, err := Run(context.Background(), p, output, true); err == nil || !strings.Contains(err.Error(), "conversation selection") {
				t.Fatalf("native history control admitted: %v", err)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("rejected native profile created durable run evidence")
			}
			if profile.Harness != "pi" {
				coder := pythonCandidate("coder", "print('implementation')")
				if _, err := PrepareGuided(context.Background(), GuidedOptions{Repository: repo, Goal: "Review current files", Output: output, Coder: &coder, Reviewer: &profile, CheckCommand: []string{"/usr/bin/true"}}); err == nil || !strings.Contains(err.Error(), "conversation selection") {
					t.Fatalf("guided reviewer admitted native history: %v", err)
				}
			}
		})
	}
}

func TestStructuredReviewsStartWithoutImplementationOrPriorReviewNarratives(t *testing.T) {
	repo, root := fixtureRepo(t)
	const implementation = "IMPLEMENTATION_NARRATIVE_MUST_NOT_REACH_REVIEW"
	const priorReview = "PRIOR_REVIEW_NARRATIVE_MUST_NOT_REACH_FINAL_REVIEW"
	const task = "OWNER_TASK_CRITERION: write fixed followed by newline into file.txt"
	coder := pythonCandidate("coder", "from pathlib import Path; Path('file.txt').write_text('fixed\\n'); print("+fmt.Sprintf("%q", implementation)+")")
	reviewerCode := "import json,sys; from pathlib import Path; prompt=sys.stdin.read(); assert " + fmt.Sprintf("%q", task) + " in prompt; assert 'Acceptance criteria:' in prompt; assert " + fmt.Sprintf("%q", implementation) + " not in prompt; assert " + fmt.Sprintf("%q", priorReview) + " not in prompt; assert Path('file.txt').read_bytes()==b'fixed\\n'; final='Current mission step: final-review' in prompt; print(json.dumps({'decision':'approve','summary':'Independently inspected the final files.' if final else " + fmt.Sprintf("%q", priorReview) + ",'findings':[]}))"
	reviewer := pythonCandidate("reviewer", reviewerCode)
	preview, err := PrepareGuided(context.Background(), GuidedOptions{Repository: repo, Goal: task, Output: filepath.Join(root, "fresh-reviews"), Coder: &coder, Reviewer: &reviewer, CheckCommand: []string{"python3", "-c", "from pathlib import Path; assert Path('file.txt').read_bytes()==b'fixed\\n'"}, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	r, err := RunMission(context.Background(), preview.Plan, preview.Output, false)
	if err != nil {
		t.Fatalf("fresh review journey: %v %+v", err, r)
	}
	for _, index := range []int{1, 3} {
		step := r.Steps[index]
		if step.ReviewContext != "fresh" || strings.Contains(step.Input, implementation) || strings.Contains(step.Input, priorReview) {
			t.Fatalf("review inherited prior narratives: %+v", step)
		}
	}
	if !strings.Contains(r.Steps[2].Input, priorReview) {
		t.Fatal("the implementation correction step lost the review it must address")
	}
	r.Steps[3].ReviewContext = ""
	if err := RequireApprovedReview(r); err == nil {
		t.Fatal("missing fresh-context final review evidence approved application")
	}
}
