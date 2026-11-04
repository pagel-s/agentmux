package workflow

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
)

// ReviewPolicyEarlyApproval avoids two native turns only when the first fresh
// review approves the exact independently checked revision. It does not replace
// the final acceptance checks or the owner's explicit application decision.
const ReviewPolicyEarlyApproval = "early-approval-v1"

const earlyApprovalSkipReason = "initial_fresh_review_approved"

// EarlyApproval retains the original review's identity. The final review phase
// is explicitly skipped, never represented as another completed model turn.
type EarlyApproval struct {
	Version            int    `json:"version"`
	ImplementationStep string `json:"implementation_step"`
	ReviewStep         string `json:"review_step"`
	Tree               string `json:"tree"`
	ContentSHA256      string `json:"content_sha256"`
}

func validateEarlyApprovalPlan(p Plan, steps []Step) error {
	if p.ReviewPolicy == "" {
		return nil
	}
	if p.ReviewPolicy != ReviewPolicyEarlyApproval || p.Kind != "work" || p.Mode != "mission" || len(steps) != 5 {
		return errors.New("early approval requires the complete guided review-and-check plan")
	}
	coder, reviewer := steps[0].Candidate, steps[1].Candidate
	if coder == "" || reviewer == "" || coder == reviewer {
		return errors.New("early approval requires separate implementation and review candidates")
	}
	want := []Step{
		{ID: "implement", Candidate: coder, Verify: true},
		{ID: "review", Candidate: reviewer, DependsOn: []string{"implement"}, ReadOnly: true, Review: true, RequireResult: true},
		{ID: "address-review", Candidate: coder, DependsOn: []string{"implement", "review"}, Verify: true},
		{ID: "final-review", Candidate: reviewer, DependsOn: []string{"implement", "review", "address-review"}, ReadOnly: true, Review: true, GateReview: true, RequireResult: true},
		{ID: "final-check", DependsOn: []string{"final-review"}, Verify: true},
	}
	for i, step := range steps {
		step.Goal = ""
		if !reflect.DeepEqual(step, want[i]) {
			return errors.New("early approval requires the unmodified guided phase dependencies and review/check gates")
		}
	}
	return nil
}

func sameApprovalRevision(s *Snapshot, tree, raw string) bool {
	return s != nil && s.Tree == tree && s.ContentSHA256 == raw
}

func sameAcceptanceCommands(a, b *Verification) bool {
	if a == nil || b == nil || len(a.Checks) != len(b.Checks) {
		return false
	}
	for i := range a.Checks {
		if a.Checks[i].Name != b.Checks[i].Name || !reflect.DeepEqual(a.Checks[i].Command, b.Checks[i].Command) {
			return false
		}
	}
	return true
}

func realAgentStep(s StepResult, output string) bool {
	return s.Harness != "" && workflowID.MatchString(s.CandidateID) && digestID(s.ProfileSHA256) && s.Attempts >= 1 && s.Attempts <= 5 && s.EvidencePath == filepath.Join(output, "steps", s.ID, "attempt-"+strconv.Itoa(s.Attempts)+".json") && digestID(s.InputSHA256) && s.StartingSnapshot != nil && s.FinalSnapshot != nil && s.Error == ""
}

func reviewedRevision(s StepResult, output, tree, raw string, approved bool) bool {
	if !realAgentStep(s, output) || s.State != "completed" || !s.ReadOnly || s.ReviewContext != "fresh" || s.Attempts != 1 || s.Review == nil || s.Truncated || s.ReviewTruncated || !sameApprovalRevision(s.StartingSnapshot, tree, raw) || !sameApprovalRevision(s.FinalSnapshot, tree, raw) {
		return false
	}
	verdict, err := ParseReviewResult(s.Result)
	return err == nil && reflect.DeepEqual(verdict, *s.Review) && (!approved || verdict.Decision == "approve")
}

// Every policy-bearing completed run must retain the canonical checks and real
// execution evidence, even when no early approval was selected. Removing the
// promotion field cannot downgrade the final gate to legacy validation.
func requirePolicyApprovedReview(r Report) error {
	if len(r.Steps) != 5 || r.FinalSnapshot == nil || !validVerification(r.Verification, r.Worktree) {
		return errors.New("guided review policy requires all five phases and final checked result")
	}
	finalCheck := r.Steps[4]
	if finalCheck.State != "verified" || finalCheck.Harness != "" || finalCheck.Attempts != 0 || finalCheck.Review != nil || finalCheck.EvidencePath == "" || !validVerification(finalCheck.Verification, r.Worktree) || !sameApprovalRevision(r.FinalSnapshot, r.Verification.Tree, r.Verification.ContentSHA256) || finalCheck.Verification.Tree != r.Verification.Tree || finalCheck.Verification.ContentSHA256 != r.Verification.ContentSHA256 || !sameAcceptanceCommands(finalCheck.Verification, r.Verification) {
		return errors.New("guided review policy lacks the exact final independent checks")
	}
	implementation := r.Steps[0]
	if !realAgentStep(implementation, r.Output) || implementation.State != "verified" || !validVerification(implementation.Verification, r.Worktree) || !sameApprovalRevision(implementation.FinalSnapshot, implementation.Verification.Tree, implementation.Verification.ContentSHA256) || !sameAcceptanceCommands(implementation.Verification, r.Verification) {
		return errors.New("guided review policy lacks real independently checked implementation evidence")
	}
	if r.EarlyApproval != nil {
		e := r.EarlyApproval
		if !sameApprovalRevision(r.FinalSnapshot, e.Tree, e.ContentSHA256) || r.Verification.Tree != e.Tree || r.Verification.ContentSHA256 != e.ContentSHA256 {
			return errors.New("early approval does not cover the exact final independently checked result")
		}
		return nil
	}
	response, finalReview := r.Steps[2], r.Steps[3]
	if r.ReviewContext != "fresh" || !reviewedRevision(r.Steps[1], r.Output, implementation.FinalSnapshot.Tree, implementation.FinalSnapshot.ContentSHA256, false) || !realAgentStep(response, r.Output) || response.State != "verified" || !validVerification(response.Verification, r.Worktree) || !sameApprovalRevision(response.StartingSnapshot, implementation.FinalSnapshot.Tree, implementation.FinalSnapshot.ContentSHA256) || !sameApprovalRevision(response.FinalSnapshot, response.Verification.Tree, response.Verification.ContentSHA256) || !sameAcceptanceCommands(response.Verification, r.Verification) || !reviewedRevision(finalReview, r.Output, r.FinalSnapshot.Tree, r.FinalSnapshot.ContentSHA256, true) || !sameApprovalRevision(response.FinalSnapshot, r.FinalSnapshot.Tree, r.FinalSnapshot.ContentSHA256) {
		return errors.New("guided review policy requires the actual checked response and fresh final review")
	}
	return nil
}

func validateEarlyApprovalReview(r Report) error {
	e := r.EarlyApproval
	if e == nil || e.Version != 1 || e.ImplementationStep != "implement" || e.ReviewStep != "review" || !objectID(e.Tree) || !digestID(e.ContentSHA256) || len(r.Steps) < 2 {
		return errors.New("early approval lacks its versioned original review identity")
	}
	implementation, review := r.Steps[0], r.Steps[1]
	if implementation.ID != e.ImplementationStep || implementation.State != "verified" || implementation.ReadOnly || implementation.GateReview || implementation.SkipReason != "" || !validVerification(implementation.Verification, r.Worktree) || !sameApprovalRevision(implementation.FinalSnapshot, e.Tree, e.ContentSHA256) || implementation.Verification.Tree != e.Tree || implementation.Verification.ContentSHA256 != e.ContentSHA256 {
		return errors.New("early approval lacks the exact passing implementation checks")
	}
	if review.ID != e.ReviewStep || !realAgentStep(review, r.Output) || review.State != "completed" || !review.ReadOnly || review.GateReview || review.SkipReason != "" || review.ReviewContext != "fresh" || r.ReviewContext != "fresh" || review.Attempts != 1 || review.Truncated || review.ReviewTruncated || review.Review == nil || review.Review.Decision != "approve" || !sameApprovalRevision(review.StartingSnapshot, e.Tree, e.ContentSHA256) || !sameApprovalRevision(review.FinalSnapshot, e.Tree, e.ContentSHA256) {
		return errors.New("early approval lacks an unmodified fresh original approval")
	}
	verdict, err := ParseReviewResult(review.Result)
	if err != nil || verdict.Decision != "approve" || !reflect.DeepEqual(verdict, *review.Review) {
		return errors.New("early approval disagrees with the retained original review verdict")
	}
	return nil
}

// Called for running/failed reports as well as completed results, so an
// interrupted fast path remains inspectable without legitimizing a forged skip.
func validateEarlyApprovalReport(r Report) error {
	if r.ReviewPolicy != "" && (r.ReviewPolicy != ReviewPolicyEarlyApproval || r.Kind != "work" || r.Mode != "mission") {
		return errors.New("unknown or misplaced review policy")
	}
	if r.ReviewPolicy == ReviewPolicyEarlyApproval {
		if len(r.Steps) > 5 {
			return errors.New("guided review policy has unexpected extra phases")
		}
		ids := []string{"implement", "review", "address-review", "final-review", "final-check"}
		dependencies := [][]string{nil, {"implement"}, {"implement", "review"}, {"implement", "review", "address-review"}, {"final-review"}}
		for i, step := range r.Steps {
			if step.ID != ids[i] || !reflect.DeepEqual(step.DependsOn, dependencies[i]) || step.ReadOnly != (i == 1 || i == 3) || step.GateReview != (i == 3) {
				return errors.New("guided review policy phase order, dependencies or gates changed")
			}
		}
	}
	if r.EarlyApproval != nil {
		if r.ReviewPolicy != ReviewPolicyEarlyApproval || len(r.Steps) > 5 {
			return errors.New("early approval lacks its explicit guided review policy")
		}
		if err := validateEarlyApprovalReview(r); err != nil {
			return err
		}
		for i, step := range r.Steps {
			if (i == 2 || i == 3) && step.State != "skipped" {
				return errors.New("early approval phase order, dependencies or gates changed")
			}
		}
	}
	for _, candidate := range r.Candidates {
		if candidate.State == "skipped" {
			return errors.New("only the guided response and final review may be skipped")
		}
	}
	for i, step := range r.Steps {
		if step.State != "skipped" && step.SkipReason == "" {
			continue
		}
		if r.EarlyApproval == nil || (i != 2 && i != 3) || step.State != "skipped" || step.SkipReason != earlyApprovalSkipReason || step.Attempts != 0 || step.Harness != "" || step.Model != "" || step.Result != "" || step.Error != "" || step.Review != nil || step.Verification != nil || step.StartingSnapshot != nil || step.FinalSnapshot != nil || step.Input != "" || step.NativeEventsPath != "" || step.Patch != "" || step.EvidencePath == "" || step.Worktree != r.Worktree {
			return errors.New("skipped phase lacks the exact original approval evidence or falsely records execution")
		}
		// StepResult's outer ID is canonical JSON; encoding/json does not fill
		// the shadowed embedded CandidateResult.ID when a saved step is loaded.
		if step.CandidateResult.ID != "" && step.CandidateResult.ID != step.ID {
			return errors.New("skipped phase has conflicting step identities")
		}
		withoutExecution := CandidateResult{ID: step.CandidateResult.ID, State: "skipped", Worktree: r.Worktree, StartedAt: step.StartedAt, FinishedAt: step.FinishedAt, EvidencePath: step.EvidencePath}
		if !reflect.DeepEqual(step.CandidateResult, withoutExecution) {
			return errors.New("skipped phase contains fabricated execution or provider usage evidence")
		}
		wantID := "address-review"
		if i == 3 {
			wantID = "final-review"
		}
		if step.ID != wantID || step.ReadOnly != (i == 3) || step.GateReview != (i == 3) {
			return errors.New("early approval skipped an unexpected phase")
		}
	}
	if r.ReviewPolicy == ReviewPolicyEarlyApproval && r.State == "completed" {
		return requirePolicyApprovedReview(r)
	}
	return nil
}

func selectEarlyApproval(ctx context.Context, r Report) (*EarlyApproval, error) {
	if r.ReviewPolicy != ReviewPolicyEarlyApproval || len(r.Steps) != 2 || r.Steps[1].Review == nil || r.Steps[1].Review.Decision != "approve" {
		return nil, nil
	}
	initial := r.Steps[1].StartingSnapshot
	if initial == nil {
		return nil, errors.New("initial approval has no revision identity")
	}
	e := &EarlyApproval{Version: 1, ImplementationStep: "implement", ReviewStep: "review", Tree: initial.Tree, ContentSHA256: initial.ContentSHA256}
	r.EarlyApproval = e
	if err := validateEarlyApprovalReview(r); err != nil {
		return nil, err
	}
	fresh, err := IsFresh(ctx, r.Worktree, *r.Steps[0].Verification)
	if err != nil {
		return nil, err
	}
	if !fresh {
		return nil, errors.New("worktree changed after the initial approved review")
	}
	return e, nil
}
