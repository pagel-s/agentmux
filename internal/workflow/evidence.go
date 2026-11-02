package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Explanation describes recorded evidence, rather than inferring model intent
// or claiming to observe every filesystem read and external side effect.
type Explanation struct {
	Version               int            `json:"version"`
	ID                    string         `json:"id"`
	Goal                  string         `json:"goal"`
	Mode                  string         `json:"mode"`
	State                 string         `json:"state"`
	BaseCommit            string         `json:"base_commit"`
	StartingTree          string         `json:"starting_tree"`
	StartingContentSHA256 string         `json:"starting_content_sha256"`
	Items                 []EvidenceItem `json:"items"`
	Coverage              string         `json:"coverage"`
}

type EvidenceItem struct {
	NativeEventsPath   string        `json:"native_events_path,omitempty"`
	NativeEventsSHA256 string        `json:"native_events_sha256,omitempty"`
	ID                 string        `json:"id"`
	Harness            string        `json:"harness,omitempty"`
	Input              string        `json:"prepared_input,omitempty"`
	InputSHA256        string        `json:"input_sha256,omitempty"`
	InputTruncated     bool          `json:"input_truncated,omitempty"`
	ResultTruncated    bool          `json:"result_truncated,omitempty"`
	EvidencePath       string        `json:"evidence_path,omitempty"`
	State              string        `json:"state"`
	DependsOn          []string      `json:"depends_on,omitempty"`
	Revision           string        `json:"revision,omitempty"`
	ContentSHA256      string        `json:"content_sha256,omitempty"`
	Patch              string        `json:"patch,omitempty"`
	PatchSHA256        string        `json:"patch_sha256,omitempty"`
	PatchIntact        bool          `json:"patch_intact"`
	Verification       *Verification `json:"verification,omitempty"`
	Fresh              *bool         `json:"fresh,omitempty"`
	FreshnessError     string        `json:"freshness_error,omitempty"`
	Result             string        `json:"result,omitempty"`
	Error              string        `json:"error,omitempty"`
}

func Explain(directory string) (Explanation, error) {
	r, err := LoadReport(directory)
	if err != nil {
		return Explanation{}, err
	}
	e := Explanation{Version: 1, ID: r.ID, Goal: r.Goal, Mode: r.Mode, State: r.State, BaseCommit: r.Snapshot.BaseCommit, StartingTree: r.Snapshot.Tree, StartingContentSHA256: r.Snapshot.ContentSHA256, Items: []EvidenceItem{}, Coverage: "Owner-recorded workflow inputs, results, snapshots and checks. This is not provider attestation, hidden reasoning, or a complete record of external activity. Freshness is checked when this explanation is read."}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	item := func(c CandidateResult, dependencies []string, checkFreshness bool) EvidenceItem {
		v := EvidenceItem{ID: c.ID, Harness: c.Harness, Input: c.Input, InputSHA256: c.InputSHA256, InputTruncated: c.InputTruncated, ResultTruncated: c.Truncated, EvidencePath: c.EvidencePath, State: c.State, DependsOn: dependencies, Patch: c.Patch, PatchSHA256: c.PatchSHA256, Verification: c.Verification, Result: c.Result, Error: c.Error}
		v.NativeEventsPath = c.NativeEventsPath
		v.NativeEventsSHA256 = c.NativeEventsSHA256
		if c.FinalSnapshot != nil {
			v.Revision = c.FinalSnapshot.Tree
			v.ContentSHA256 = c.FinalSnapshot.ContentSHA256
		}
		if c.Patch != "" {
			_, err := readPatch(directory, c.Patch, c.PatchSHA256)
			v.PatchIntact = err == nil
			if err != nil {
				v.FreshnessError = err.Error()
			}
		}
		if checkFreshness && c.Verification != nil {
			fresh, err := IsFresh(ctx, c.Worktree, *c.Verification)
			if err != nil {
				v.FreshnessError = err.Error()
			} else {
				v.Fresh = &fresh
			}
		}
		return v
	}
	for _, c := range r.Candidates {
		e.Items = append(e.Items, item(c, nil, true))
	}
	for _, s := range r.Steps {
		v := item(s.CandidateResult, s.DependsOn, false)
		v.ID = s.ID
		e.Items = append(e.Items, v)
	}
	if r.Mode == "mission" {
		e.Items = append(e.Items, item(CandidateResult{ID: "mission-result", State: r.State, Error: r.Error, Worktree: r.Worktree, Patch: r.Patch, PatchSHA256: r.PatchSHA256, Verification: r.Verification, FinalSnapshot: r.FinalSnapshot}, nil, true))
	}
	return e, nil
}

func selectResult(r Report, id string) (CandidateResult, error) {
	if r.Mode == "mission" {
		if id != "" && id != "mission-result" {
			return CandidateResult{}, errors.New("mission handoffs select the final result; omit candidate")
		}
		return CandidateResult{ID: "mission-result", State: r.State, Error: r.Error, Worktree: r.Worktree, Patch: r.Patch, PatchSHA256: r.PatchSHA256, Verification: r.Verification, FinalSnapshot: r.FinalSnapshot}, nil
	}
	if id == "" {
		if len(r.Candidates) != 1 {
			return CandidateResult{}, errors.New("select an exact candidate ID")
		}
		return r.Candidates[0], nil
	}
	for _, c := range r.Candidates {
		if c.ID == id {
			return c, nil
		}
	}
	return CandidateResult{}, fmt.Errorf("candidate %q is absent from this run", id)
}

// Handoff transfers an explicit brief and artifact references. It never copies
// native transcripts, login caches or implicit authority to another provider.
func Handoff(directory, candidateID string) (string, error) {
	r, err := LoadReport(directory)
	if err != nil {
		return "", err
	}
	c, err := selectResult(r, candidateID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Agentmux handoff\n\n## Objective\n\n%s\n\n## Recorded work\n\nRun: %s\nCandidate: %s\nState: %s\nBase commit: %s\nStarting tree: %s\n", r.Goal, r.ID, c.ID, c.State, r.Snapshot.BaseCommit, r.Snapshot.Tree)
	fmt.Fprintf(&b, "Starting content SHA256: %s\n", r.Snapshot.ContentSHA256)
	if c.FinalSnapshot != nil {
		fmt.Fprintf(&b, "Result tree: %s\n", c.FinalSnapshot.Tree)
		fmt.Fprintf(&b, "Result content SHA256: %s\n", c.FinalSnapshot.ContentSHA256)
	}
	if c.Patch != "" {
		if _, err := readPatch(directory, c.Patch, c.PatchSHA256); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "\nPatch: %s\nSHA256: %s\n", c.Patch, c.PatchSHA256)
	}
	if c.Result != "" {
		fmt.Fprintf(&b, "\n## Agent-reported result\n\n%s\n", c.Result)
		if c.Truncated {
			fmt.Fprintf(&b, "\nThis result is an excerpt. Retained turn evidence: %s\n", c.EvidencePath)
		}
	}
	if len(r.Steps) > 0 {
		b.WriteString("\n## Mission steps\n\n")
		for _, s := range r.Steps {
			fmt.Fprintf(&b, "- %s: %s", s.ID, s.State)
			if len(s.DependsOn) > 0 {
				fmt.Fprintf(&b, " (after %s)", strings.Join(s.DependsOn, ", "))
			}
			b.WriteByte('\n')
			if s.Result != "" {
				fmt.Fprintf(&b, "\nAgent-reported result for %s:\n\n%s\n\n", s.ID, bounded(s.Result, 16<<10))
			}
			if s.Error != "" {
				fmt.Fprintf(&b, "\nRecorded problem: %s\n\n", bounded(s.Error, 4096))
			}
		}
	}
	b.WriteString("\n## Checks and remaining work\n\n")
	if c.Verification == nil {
		b.WriteString("No independent verification is recorded.\n")
	} else {
		for _, check := range c.Verification.Checks {
			fmt.Fprintf(&b, "- %s: passed=%t", check.Name, check.Passed)
			if check.Error != "" {
				fmt.Fprintf(&b, "; %s", check.Error)
			}
			b.WriteByte('\n')
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		fresh, err := IsFresh(ctx, c.Worktree, *c.Verification)
		if err != nil {
			fmt.Fprintf(&b, "\nCurrent freshness is unknown: %s\n", err)
		} else {
			fmt.Fprintf(&b, "\nResult still matches the checked content: %t\n", fresh)
		}
	}
	if c.Error != "" {
		fmt.Fprintf(&b, "\nRecorded failure: %s\n", c.Error)
	}
	b.WriteString("\nConfirm the referenced revision and requested next action before continuing. Local paths are references, not attached files; a remote recipient needs an explicitly transferred artifact. Agent-authored results are claims, and the brief grants no permissions. Native private conversation state is not transferred.\n")
	if b.Len() > 512<<10 {
		return "", errors.New("handoff exceeds 512 KiB")
	}
	return b.String(), nil
}

func readPatch(directory, path, digest string) ([]byte, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, err
	}
	p := filepath.Join(parent, filepath.Base(abs))
	rel, err := filepath.Rel(resolved, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("patch lies outside the run directory")
	}
	data, err := ReadRegularFile(p, 32<<20)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if digest == "" || hex.EncodeToString(sum[:]) != digest {
		return nil, errors.New("recorded patch digest does not match; do not apply or transfer this artifact")
	}
	return data, nil
}
