package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

type ReviewFinding struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}
type ReviewResult struct {
	Decision string          `json:"decision"`
	Summary  string          `json:"summary"`
	Findings []ReviewFinding `json:"findings"`
}

// Review decisions are agent-authored assessments of a pinned revision. They
// remain distinct from independently executed acceptance checks.
func ParseReviewResult(text string) (ReviewResult, error) {
	var r ReviewResult
	if len(text) > 128<<10 {
		return r, errors.New("review exceeds 128 KiB")
	}
	if err := DecodeJSON([]byte(text), &r); err != nil {
		return r, fmt.Errorf("review must be one JSON verdict: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := DecodeJSON([]byte(text), &fields); err != nil {
		return r, err
	}
	if raw, ok := fields["findings"]; !ok || len(strings.TrimSpace(string(raw))) == 0 || strings.TrimSpace(string(raw))[0] != '[' {
		return r, errors.New("review must explicitly include its findings array")
	}
	if strings.TrimSpace(r.Summary) == "" || len(r.Summary) > 16<<10 || len(r.Findings) > 50 {
		return r, errors.New("review needs a summary and at most 50 findings")
	}
	if r.Decision != "approve" && r.Decision != "request_changes" {
		return r, errors.New("review decision must be approve or request_changes")
	}
	if r.Decision == "approve" && len(r.Findings) != 0 || r.Decision == "request_changes" && len(r.Findings) == 0 {
		return r, errors.New("review decision conflicts with its findings")
	}
	for _, f := range r.Findings {
		clean := path.Clean(f.File)
		if f.File == "" || path.IsAbs(f.File) || clean == ".." || strings.HasPrefix(clean, "../") || clean == ".git" || strings.HasPrefix(clean, ".git/") || strings.ContainsAny(f.File, "\\\x00\r\n") || len(f.File) > 4096 || f.Line < 1 || f.Line > 10_000_000 || strings.TrimSpace(f.Message) == "" || len(f.Message) > 8192 {
			return r, errors.New("review finding needs a project-relative file, line and bounded message")
		}
	}
	return r, nil
}

func RequireApprovedReview(r Report) error {
	if r.Kind != "work" {
		return nil
	}
	if r.FinalSnapshot == nil {
		return errors.New("work has no final checked snapshot")
	}
	var last *StepResult
	for i := range r.Steps {
		if r.Steps[i].GateReview {
			last = &r.Steps[i]
		}
	}
	if last == nil || !last.ReadOnly || last.State != "completed" || last.Review == nil || last.Review.Decision != "approve" {
		return errors.New("work needs an approved final review before application")
	}
	verdict := *last.Review
	encoded, err := json.Marshal(verdict)
	if err != nil {
		return err
	}
	parsed, err := ParseReviewResult(string(encoded))
	if err != nil {
		return err
	}
	if parsed.Decision != "approve" {
		return errors.New("final review is not approved")
	}
	if last.StartingSnapshot == nil || last.FinalSnapshot == nil || last.StartingSnapshot.Tree != r.FinalSnapshot.Tree || last.StartingSnapshot.ContentSHA256 != r.FinalSnapshot.ContentSHA256 || last.FinalSnapshot.ContentSHA256 != r.FinalSnapshot.ContentSHA256 || last.FinalSnapshot.Tree != r.FinalSnapshot.Tree {
		return errors.New("final review does not cover the exact checked result")
	}
	return nil
}
