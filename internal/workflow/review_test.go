package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reviewFixturePlan(t *testing.T, verdict string, edit bool) (Plan, string) {
	t.Helper()
	repo, root := fixtureRepo(t)
	write := "from pathlib import Path; Path('file.txt').write_text('fixed\\n'); print('file.txt implementation ready')"
	review := "import sys; sys.stdin.read(); "
	if edit {
		review += "from pathlib import Path; Path('file.txt').write_text('reviewer edit\\n'); "
	}
	review += "print(" + fmt.Sprintf("%q", verdict) + ")"
	plan := Plan{Kind: "work", Version: 1, Execution: "local", Mode: "mission", Repository: repo, Goal: "Replace base with fixed", TimeoutSeconds: 30, MaxAttempts: 1, Candidates: []Candidate{{ID: "writer", Harness: "command", Command: []string{"python3", "-c", write}}, {ID: "reviewer", Harness: "command", Command: []string{"python3", "-c", review}}}, Checks: []Check{{Name: "fixed", Command: []string{"python3", "-c", "from pathlib import Path; assert Path('file.txt').read_text()=='fixed\\n'"}}}, Steps: []Step{{ID: "implement", Candidate: "writer", Verify: true}, {ID: "review", Candidate: "reviewer", DependsOn: []string{"implement"}, ReadOnly: true, Review: true, RequireResult: true, GateReview: true}, {ID: "check", Verify: true, DependsOn: []string{"review"}}}}
	return plan, filepath.Join(root, "run")
}

func TestReviewedWorkRequiresExactApprovedUnmodifiedRevision(t *testing.T) {
	approved := `{"decision":"approve","summary":"Inspected the implementation; no actionable findings.","findings":[]}`
	plan, output := reviewFixturePlan(t, approved, false)
	report, err := RunMission(context.Background(), plan, output, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = RequireApprovedReview(report); err != nil {
		t.Fatal(err)
	}
	stored, err := LoadReport(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = RequireApprovedReview(stored); err != nil {
		t.Fatal(err)
	}
	if stored.Steps[1].Review == nil || stored.Steps[1].Review.Decision != "approve" {
		t.Fatal("structured review missing")
	}
	result, err := Apply(context.Background(), output, "")
	if err != nil || !result.Applied {
		t.Fatalf("application %+v %v", result, err)
	}
	data, _ := os.ReadFile(filepath.Join(plan.Repository, "file.txt"))
	if string(data) != "fixed\n" {
		t.Fatalf("wrong applied contents %q", data)
	}
	stored.FinalSnapshot.ContentSHA256 = strings.Repeat("a", 64)
	if err = RequireApprovedReview(stored); err == nil {
		t.Fatal("approval accepted for a different final revision")
	}
}

func TestGuidedReviewFailureBlocksApplication(t *testing.T) {
	for _, test := range []struct {
		name, verdict, state string
		edit                 bool
	}{
		{"requested", `{"decision":"request_changes","summary":"A defect remains.","findings":[{"file":"file.txt","line":1,"message":"Needs further work"}]}`, "changes_requested", false},
		{"malformed", "Looks good", "review_invalid", false},
		{"modified", `{"decision":"approve","summary":"Looks good","findings":[]}`, "review_modified", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, dir := reviewFixturePlan(t, test.verdict, test.edit)
			r, err := RunMission(context.Background(), plan, dir, false)
			if err == nil {
				t.Fatal("review failure accepted")
			}
			if len(r.Steps) != 2 || r.Steps[1].State != test.state {
				t.Fatalf("incorrect failed review: %+v", r.Steps)
			}
			if _, err = Apply(context.Background(), dir, ""); err == nil {
				t.Fatal("unapproved work applied")
			}
			b, _ := os.ReadFile(filepath.Join(plan.Repository, "file.txt"))
			if string(b) != "base\n" {
				t.Fatal("review changed source")
			}
		})
	}
}

func TestStructuredReviewRejectsAmbiguousOrInconsistentDecisions(t *testing.T) {
	for _, input := range []string{
		`{"decision":"approve","summary":"ok"}`,
		`{"decision":"approve","summary":"ok","findings":null}`,
		`{"decision":"approve","summary":"ok","findings":[{"file":"file.txt","line":1,"message":"bad"}]}`,
		`{"decision":"request_changes","summary":"bad","findings":[]}`,
		`{"decision":"approve","decision":"request_changes","summary":"ok","findings":[]}`,
		`{"decision":"request_changes","summary":"bad","findings":[{"file":"../secret","line":1,"message":"bad"}]}`,
	} {
		if _, err := ParseReviewResult(input); err == nil {
			t.Fatalf("invalid verdict accepted %s", input)
		}
	}
	valid := ReviewResult{Decision: "approve", Summary: "Checked", Findings: []ReviewFinding{}}
	encoded, _ := json.Marshal(valid)
	if _, err := ParseReviewResult(string(encoded)); err != nil {
		t.Fatal(err)
	}
}

func TestGuidedPlanRefusesWritersAfterReviewGate(t *testing.T) {
	p, _ := reviewFixturePlan(t, `{"decision":"approve","summary":"ok","findings":[]}`, false)
	p.Steps = append(p.Steps, Step{ID: "later-writer", Candidate: "writer", DependsOn: []string{"check"}})
	if err := ValidatePlan(p); err == nil {
		t.Fatal("writer accepted after final approval")
	}
}
