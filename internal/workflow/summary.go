package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

type PhaseSummary struct {
	SkipReason string `json:"skip_reason,omitempty"`
	ID         string `json:"id"`
	State      string `json:"state"`
	Harness    string `json:"harness,omitempty"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}
type ChangedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}
type WorkSummary struct {
	EarlyApproval         *EarlyApproval   `json:"early_approval,omitempty"`
	ID                    string           `json:"id"`
	Goal                  string           `json:"goal"`
	State                 string           `json:"state"`
	Outcome               string           `json:"outcome"`
	Repository            string           `json:"repository"`
	ProcessContainment    string           `json:"process_containment"`
	Storage               string           `json:"storage"`
	Isolation             string           `json:"isolation,omitempty"`
	Directory             string           `json:"directory"`
	StartedAt             string           `json:"started_at"`
	FinishedAt            string           `json:"finished_at,omitempty"`
	Error                 string           `json:"error,omitempty"`
	ReportPath            string           `json:"report_path,omitempty"`
	Phases                []PhaseSummary   `json:"phases"`
	ChecksPassed          int              `json:"checks_passed"`
	ChecksTotal           int              `json:"checks_total"`
	SourceFresh           *bool            `json:"source_fresh,omitempty"`
	ResultFresh           *bool            `json:"result_fresh,omitempty"`
	SourceFreshnessError  string           `json:"source_freshness_error,omitempty"`
	ResultFreshnessError  string           `json:"result_freshness_error,omitempty"`
	CanApply              bool             `json:"can_apply"`
	NextAction            string           `json:"next_action"`
	NextCommand           string           `json:"next_command"`
	Files                 []ChangedFile    `json:"files"`
	FilesTruncated        bool             `json:"files_truncated,omitempty"`
	FilesError            string           `json:"files_error,omitempty"`
	PatchPath             string           `json:"patch_path,omitempty"`
	PatchPreview          string           `json:"patch_preview,omitempty"`
	PatchPreviewTruncated bool             `json:"patch_preview_truncated,omitempty"`
	PatchPreviewError     string           `json:"patch_preview_error,omitempty"`
	Review                string           `json:"review,omitempty"`
	ReviewState           string           `json:"review_state"`
	ReviewTruncated       bool             `json:"review_truncated,omitempty"`
	Findings              []ReviewFinding  `json:"findings,omitempty"`
	Application           *ApplyInspection `json:"application,omitempty"`
}

// Summarize describes recorded work and currently inspectable evidence. Saved
// "running" state is not treated as a live-process heartbeat. Git fingerprints
// can update Git's object cache but never alter the source index or files.
func Summarize(ctx context.Context, directory string) (WorkSummary, error) {
	r, err := LoadReport(directory)
	if err != nil {
		return WorkSummary{}, err
	}
	return summarizeReport(ctx, r), nil
}

func summarizeReport(ctx context.Context, r Report) (s WorkSummary) {
	s = WorkSummary{ID: r.ID, Goal: r.Goal, State: r.State, Repository: r.Repository, Directory: r.Output, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Error: r.Error, Phases: []PhaseSummary{}, Files: []ChangedFile{}, ReviewState: "not_recorded", NextCommand: "amx flow explain " + shellWord(r.Output)}
	s.ProcessContainment = r.ProcessContainment
	s.EarlyApproval = r.EarlyApproval
	s.Storage = r.Storage
	if s.Storage == "" {
		s.Storage = StorageShared
	}
	if r.Isolation != nil {
		s.Isolation = r.Isolation.Mode
	}
	source := SourceSnapshot(r)
	if s.ProcessContainment == "" {
		s.ProcessContainment = "process-group"
	}
	defer func() {
		if !safeCommandText(s.NextCommand) {
			s.NextCommand = ""
			s.NextAction += " Exact paths contain control characters; inspect JSON evidence instead of copying a displayed command."
		}
	}()
	if info, err := os.Lstat(filepath.Join(r.Output, "report.html")); err == nil && info.Mode().IsRegular() {
		s.ReportPath = filepath.Join(r.Output, "report.html")
	}
	for _, step := range r.Steps {
		s.Phases = append(s.Phases, PhaseSummary{ID: step.ID, State: step.State, SkipReason: step.SkipReason, Harness: step.Harness, Result: bounded(step.Result, 16<<10), Error: step.Error})
		if step.Review != nil {
			s.ReviewState = step.Review.Decision
			s.Review = step.Review.Summary
			s.Findings = append([]ReviewFinding(nil), step.Review.Findings...)
			s.ReviewTruncated = step.ReviewTruncated
		}
	}
	for _, c := range r.Candidates {
		s.Phases = append(s.Phases, PhaseSummary{ID: c.ID, State: c.State, Harness: c.Harness, Result: bounded(c.Result, 16<<10), Error: c.Error})
	}
	var final *Snapshot
	var verification *Verification
	var worktree, patch, patchDigest string
	patchEmpty := false
	if r.Mode == "mission" {
		final, verification, worktree, patch, patchDigest = r.FinalSnapshot, r.Verification, r.Worktree, r.Patch, r.PatchSHA256
	} else if len(r.Candidates) == 1 {
		c := r.Candidates[0]
		final, verification, worktree, patch, patchDigest = c.FinalSnapshot, c.Verification, c.Worktree, c.Patch, c.PatchSHA256
	}
	if patch != "" {
		s.PatchPath = patch
		data, err := readPatch(r.Output, patch, patchDigest)
		if err != nil {
			s.PatchPreviewError = err.Error()
		} else {
			patchEmpty = len(data) == 0
			s.PatchPreview, s.PatchPreviewTruncated = boundedPatchPreview(data)
		}
	}
	addChecks := func(v *Verification) {
		if v == nil {
			return
		}
		s.ChecksTotal += len(v.Checks)
		for _, c := range v.Checks {
			if c.Passed {
				s.ChecksPassed++
			}
		}
	}
	if verification != nil {
		addChecks(verification)
	} else if r.Mode == "compare" {
		for _, c := range r.Candidates {
			addChecks(c.Verification)
		}
	} else {
		for i := len(r.Steps) - 1; i >= 0; i-- {
			if r.Steps[i].Verification != nil {
				addChecks(r.Steps[i].Verification)
				break
			}
		}
	}
	prepared, closeContext, containmentErr := ReportContext(ctx, r)
	if containmentErr != nil {
		s.SourceFreshnessError = containmentErr.Error()
		s.ResultFreshnessError = containmentErr.Error()
		s.Outcome = "Required execution context is unavailable"
		s.NextAction = "Inspect saved evidence and restore the recorded storage and isolation before checking freshness or applying work. " + containmentErr.Error()
		return s
	}
	defer closeContext()
	ctx = prepared
	if objectID(source.Tree) && digestID(source.ContentSHA256) {
		tree, raw, err := contentState(ctx, r.Repository)
		if err != nil {
			s.SourceFreshnessError = err.Error()
		} else {
			head, headErr := gitText(ctx, r.Repository, nil, "rev-parse", "HEAD")
			if headErr != nil {
				s.SourceFreshnessError = headErr.Error()
			} else {
				fresh := tree == source.Tree && raw == source.ContentSHA256 && head == source.BaseCommit
				s.SourceFresh = &fresh
			}
		}
	}
	if verification != nil {
		fresh, err := IsFresh(ctx, worktree, *verification)
		if err != nil {
			s.ResultFreshnessError = err.Error()
		} else {
			s.ResultFresh = &fresh
		}
	}
	if final == nil && r.Mode == "mission" {
		for i := len(r.Steps) - 1; i >= 0; i-- {
			if r.Steps[i].FinalSnapshot != nil {
				final = r.Steps[i].FinalSnapshot
				break
			}
		}
	}
	if final != nil && objectID(final.Tree) && objectID(source.Tree) {
		files, truncated, err := changedFiles(ctx, SnapshotObjectRepository(*final), source.Tree, final.Tree)
		s.Files, s.FilesTruncated = files, truncated
		if err != nil {
			s.FilesError = err.Error()
		}
	}
	switch r.State {
	case "running":
		s.Outcome = "Recorded in progress"
		s.NextAction = "This is saved progress, not a live heartbeat. Check the original command before cancelling or starting another run."
		return s
	case "failed", "interrupted":
		s.Outcome = "Work stopped before acceptance"
		s.NextAction = "Inspect the recorded error and partial changes before starting new work. Uncertain native turns are never replayed automatically."
		if s.ReviewState == "request_changes" {
			s.Outcome = "Review requested changes"
			s.NextAction = "Read the review findings and inspect the retained changes. The source has not been integrated by this run."
		}
		for _, step := range r.Steps {
			if step.State == "review_modified" {
				s.Outcome = "Reviewer changed its snapshot"
				s.NextAction = "Inspect the review step's patch. Its changed content is not approved for application."
			}
		}
		return s
	}
	if r.Mode == "compare" && len(r.Candidates) != 1 {
		s.Outcome = "Alternatives checked"
		s.NextAction = "Compare the retained candidates and choose one explicitly. No winner or source integration is automatic."
		return s
	}
	if verification == nil || !verification.Passed {
		s.Outcome = "Acceptance checks are incomplete"
		s.NextAction = "Inspect the recorded checks before integration."
		return s
	}
	if s.ResultFresh == nil || !*s.ResultFresh {
		s.Outcome = "Checked result needs inspection"
		s.NextAction = "The result changed or its current content could not be confirmed. Inspect the retained worktree and evidence."
		return s
	}
	if s.FilesError != "" {
		s.Outcome = "Changed files need inspection"
		s.NextAction = s.FilesError
		return s
	}
	if final == nil {
		s.Outcome = "Final artifact needs inspection"
		s.NextAction = "No final checked snapshot is available for integration. Inspect the retained evidence."
		return s
	}
	if reviewErr := RequireApprovedReview(r); reviewErr != nil {
		s.Outcome = "Final review is not approved"
		s.NextAction = reviewErr.Error() + ". Inspect the review findings before continuing."
		return s
	}
	application, inspection := recordedApplication(ctx, r)
	s.Application = inspection
	if application == "unknown" {
		s.Outcome = "Application history needs inspection"
		s.NextAction = "Application receipts could not be fully confirmed. Inspect the retained receipts and source before any retry."
		s.NextCommand = "git -C " + shellWord(r.Repository) + " status --short"
		if r.Kind == "work" {
			s.NextCommand = "amx work recover " + shellWord(r.Output) + " --repo " + shellWord(r.Repository)
		}
		return s
	}
	if application == "uncertain" {
		s.Outcome = "Application needs inspection"
		s.NextAction = "An application receipt records uncertainty. Inspect source files and the retained receipt before any retry."
		s.NextCommand = "git -C " + shellWord(r.Repository) + " status --short"
		if r.Kind == "work" {
			s.NextCommand = "amx work recover " + shellWord(r.Output) + " --repo " + shellWord(r.Repository)
		}
		return s
	}
	if s.SourceFresh == nil || !*s.SourceFresh {
		if s.SourceFresh == nil {
			s.Outcome = "Source freshness needs inspection"
			s.NextAction = "The current source content could not be confirmed. Inspect the project and retained evidence before continuing."
		} else if application == "applied" {
			s.Outcome = "Applied to source"
			s.NextAction = "Inspect the changed files, including new untracked files, and review their contents or diff before deciding when to commit. Agentmux has not committed or pushed these changes."
		} else {
			s.Outcome = "Source changed since this run"
			s.NextAction = "Preserve current source work and reconcile the retained patch manually; application will refuse stale source content."
		}
		s.NextCommand = "git -C " + shellWord(r.Repository) + " status --short"
		return s
	}
	if s.PatchPreviewError != "" {
		s.Outcome = "Patch integrity needs inspection"
		s.NextAction = s.PatchPreviewError
		return s
	}
	if len(s.Files) == 0 {
		if final.Tree != source.Tree || final.ContentSHA256 != source.ContentSHA256 {
			s.Outcome = "Changes need manual inspection"
			s.NextAction = "The checked content changed without a representable Git file diff. Inspect the retained worktree before integration."
			return s
		}
		if !patchEmpty || patch == "" {
			s.Outcome = "Patch integrity needs inspection"
			s.NextAction = "The saved patch and recorded absence of file changes do not agree. Inspect the retained artifact before continuing."
			return s
		}
		s.Outcome = "No file changes to apply"
		s.NextAction = "The declared checks completed without creating a source patch. No application is needed."
		if r.Kind == "work" {
			s.Outcome = "No changes needed"
			s.NextAction = "Acceptance checks and final review completed without changing the project. No application is needed; existing source work is preserved."
		}
		s.NextCommand = "git -C " + shellWord(r.Repository) + " status --short"
		return s
	}
	if patchEmpty {
		s.Outcome = "Patch integrity needs inspection"
		s.NextAction = "Recorded file changes have an empty saved patch. Inspect the retained artifacts before integration."
		return s
	}
	s.CanApply = true
	s.Outcome = "Ready for your review"
	if r.Kind == "work" {
		s.NextAction = "Review the changes, recorded approval and passing checks. Apply explicitly when satisfied."
		s.NextCommand = "amx work apply " + shellWord(r.Output) + " --repo " + shellWord(r.Repository)
	} else {
		s.NextAction = "Inspect the checked changes, then explicitly apply the result when satisfied."
		s.NextCommand = "amx flow apply " + shellWord(r.Output)
	}
	return s
}

// The complete artifact is hash-checked before this bounded display is built.
// Controls and invalid text bytes are shown visibly, not sent to terminals.
// Git binary patches remain their encoded patch representation.
func boundedPatchPreview(data []byte) (string, bool) {
	const limit = 64 << 10
	var b strings.Builder
	for len(data) > 0 {
		r, n := utf8.DecodeRune(data)
		var token string
		if r == utf8.RuneError && n == 1 {
			token = fmt.Sprintf("\\x%02x", data[0])
		} else if unicode.IsControl(r) && r != '\n' && r != '\t' || r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			token = fmt.Sprintf("\\u%04x", r)
		} else {
			token = string(r)
		}
		if b.Len()+len(token) > limit {
			return b.String(), true
		}
		b.WriteString(token)
		data = data[n:]
	}
	return b.String(), false
}

func changedFiles(ctx context.Context, repo, base, tree string) ([]ChangedFile, bool, error) {
	data, err := gitOutput(ctx, repo, nil, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", base, tree, "--")
	if err != nil {
		return nil, false, err
	}
	fields := bytes.Split(data, []byte{0})
	result := []ChangedFile{}
	truncated := false
	for i := 0; i < len(fields) && len(fields[i]) > 0; i += 2 {
		if i+1 >= len(fields) || len(fields[i+1]) == 0 {
			return nil, false, errors.New("malformed changed-file evidence")
		}
		if !utf8.Valid(fields[i+1]) {
			return result, false, errors.New("a changed filename is not valid UTF-8; inspect the retained patch with Git")
		}
		if len(result) >= 100 {
			truncated = true
			continue
		}
		result = append(result, ChangedFile{Status: string(fields[i]), Path: string(fields[i+1])})
	}
	return result, truncated, nil
}

func safeCommandText(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return false
		}
	}
	return true
}

func shortGoal(value string, maximum int) string {
	runes := []rune(value)
	if len(runes) > maximum {
		return string(runes[:maximum]) + "…"
	}
	return value
}

func recordedApplication(ctx context.Context, r Report) (string, *ApplyInspection) {
	id := ""
	if r.Mode == "compare" && len(r.Candidates) == 1 {
		id = r.Candidates[0].ID
	}
	inspection, err := InspectApply(ctx, r.Output, id)
	if err != nil {
		return "unknown", &inspection
	}
	switch inspection.State {
	case "unknown", "uncertain":
		return inspection.State, &inspection
	case "applied":
		if inspection.SourceObservation == "matches_final" {
			return "applied", &inspection
		}
	}
	return "", &inspection
}
