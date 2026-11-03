package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type WatchOptions struct {
	IntervalSeconds int `json:"interval_seconds"`
	MaxRuns         int `json:"max_runs"`
	TimeoutSeconds  int `json:"timeout_seconds"`
}

type WatchEntry struct {
	Tree          string `json:"tree"`
	ContentSHA256 string `json:"content_sha256"`
	Output        string `json:"output"`
	State         string `json:"state"`
	Superseded    bool   `json:"superseded"`
	Error         string `json:"error,omitempty"`
}

type WatchReport struct {
	Version              int               `json:"version"`
	Storage              string            `json:"storage,omitempty"`
	Isolation            *IsolationOptions `json:"isolation,omitempty"`
	ProcessContainment   string            `json:"process_containment,omitempty"`
	Repository           string            `json:"repository"`
	Output               string            `json:"output"`
	State                string            `json:"state"`
	StartedAt            string            `json:"started_at"`
	FinishedAt           string            `json:"finished_at,omitempty"`
	Options              WatchOptions      `json:"options"`
	Runs                 []WatchEntry      `json:"runs"`
	Error                string            `json:"error,omitempty"`
	CurrentTree          string            `json:"current_tree,omitempty"`
	CurrentContentSHA256 string            `json:"current_content_sha256,omitempty"`
}

// Watch reviews stable revisions only. Every review runs in private detached
// worktrees. The engine never applies its changes to the watched repository;
// execution:'local' still grants each executable the owner's host authority.
func Watch(ctx context.Context, p Plan, output string, allowModels bool, options WatchOptions) (report WatchReport, runErr error) {
	p = normalized(p)
	ctx, p, closeEarlyCredentials, err := registerIsolationEnvironment(ctx, p)
	closeCredentials := func() {}
	defer func() {
		if _, err := sanitizeIsolationEvidence(ctx, &report); err != nil {
			runErr = errors.Join(runErr, err)
		}
		runErr = redactEarlyIsolationError(ctx, runErr)
		closeCredentials()
		closeEarlyCredentials()
	}()
	if err != nil {
		return report, err
	}
	if p.Mode != "compare" {
		return report, errors.New("watch requires compare mode")
	}
	if err := ValidatePlan(p); err != nil {
		return report, err
	}
	if err := modelPermission(p, allowModels); err != nil {
		return report, err
	}
	ctx, p, closeCredentials, err = prepareIsolationCredentials(ctx, p)
	if err != nil {
		return report, err
	}
	prepared, containmentErr := PrepareProcessContainment(ctx, p.ProcessContainment)
	if containmentErr != nil {
		return report, containmentErr
	}
	ctx = prepared
	p.ProcessContainment = contextProcessContainment(ctx)
	canonicalRepo, rootErr := repositoryRoot(ctx, p.Repository)
	if rootErr != nil {
		return report, rootErr
	}
	p.Repository = canonicalRepo
	if err := ValidateIsolation(p); err != nil {
		return report, err
	}
	if containsIsolationCredential(ctx, []byte(output)) {
		return report, ErrIsolationCredentialLeak
	}
	if options.IntervalSeconds == 0 {
		options.IntervalSeconds = 5
	}
	if options.MaxRuns == 0 {
		options.MaxRuns = 3
	}
	if options.TimeoutSeconds == 0 {
		options.TimeoutSeconds = 600
	}
	if options.IntervalSeconds < 1 || options.IntervalSeconds > 3600 || options.MaxRuns < 1 || options.MaxRuns > 100 || options.TimeoutSeconds < 1 || options.TimeoutSeconds > 86400 {
		return report, errors.New("watch interval must be 1–3600s, max_runs 1–100 and timeout 1–86400s")
	}
	dir, err := newOutput(ctx, p.Repository, output)
	if err != nil {
		return report, err
	}
	repo, err := repositoryRoot(ctx, p.Repository)
	if err != nil {
		return report, err
	}
	report = WatchReport{Version: 1, Storage: p.Storage, Isolation: p.Isolation, ProcessContainment: p.ProcessContainment, Repository: repo, Output: dir, State: "watching", StartedAt: timestamp(), Options: options, Runs: []WatchEntry{}}
	var failures []error
	defer func() {
		runErr = errors.Join(append(failures, runErr)...)
		runErr = redactEarlyIsolationError(ctx, runErr)
		report.FinishedAt = timestamp()
		if runErr != nil {
			report.Error = runErr.Error()
			report.State = "failed"
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				report.State = "interrupted"
			}
		} else {
			report.State = "completed"
		}
		if err := saveIsolationJSON(ctx, filepath.Join(dir, "watch.json"), &report); err != nil {
			runErr = errors.Join(runErr, err)
			report.Error = runErr.Error()
			if report.State != "interrupted" {
				report.State = "failed"
			}
		}
	}()
	if err = saveIsolationJSON(ctx, filepath.Join(dir, "watch.json"), &report); err != nil {
		return report, err
	}
	watchCtx, cancel := context.WithTimeout(ctx, time.Duration(options.TimeoutSeconds)*time.Second)
	defer cancel()
	watchCtx, err = PrepareSnapshotStorage(watchCtx, p.Storage, repo, filepath.Join(dir, "git"))
	if err != nil {
		return report, err
	}
	watchCtx, closeContext, err := PrepareReportIsolation(watchCtx, Report{Repository: repo, Output: dir, Isolation: p.Isolation})
	if err != nil {
		return report, err
	}
	defer closeContext()
	interval := time.Duration(options.IntervalSeconds) * time.Second
	last, pending := "", ""
	for len(report.Runs) < options.MaxRuns {
		if watchCtx.Err() != nil {
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			return report, nil
		}
		tree, raw, err := contentState(watchCtx, repo)
		if err != nil {
			return report, err
		}
		report.CurrentTree = tree
		report.CurrentContentSHA256 = raw
		identity := tree + ":" + raw
		changed := false
		for i := range report.Runs {
			stale := i != len(report.Runs)-1 || report.Runs[i].Tree != tree || report.Runs[i].ContentSHA256 != raw
			if stale != report.Runs[i].Superseded {
				changed = true
			}
			report.Runs[i].Superseded = stale
		}
		if changed {
			if err = saveIsolationJSON(ctx, filepath.Join(dir, "watch.json"), &report); err != nil {
				return report, err
			}
		}
		if identity == pending && identity != last {
			reviewPlan := p
			reviewPlan.MaxAttempts = 1
			reviewPlan.requireResult = true
			reviewPlan.requireReadOnly = true
			reviewPlan.requireFreshReview = true
			reviewPlan.expectedTree = tree
			reviewPlan.expectedContent = raw
			reviewPlan.Goal = "Review this stable repository snapshot. Report actionable findings and cite files and reproductions where available. Do not edit the snapshot.\n\nOwner review request:\n" + p.Goal
			runDir := filepath.Join(dir, fmt.Sprintf("review-%04d", len(report.Runs)+1))
			entryIndex := len(report.Runs)
			entry := WatchEntry{Tree: tree, ContentSHA256: raw, Output: runDir, State: "running"}
			report.Runs = append(report.Runs, entry)
			// Record admission before launching a review so a hard interruption
			// cannot leave the watch summary unaware of its retained child run.
			if err = saveIsolationJSON(ctx, filepath.Join(dir, "watch.json"), &report); err != nil {
				report.Runs[entryIndex].State = "not_started"
				report.Runs[entryIndex].Error = err.Error()
				return report, err
			}
			run, reviewErr := Run(watchCtx, reviewPlan, runDir, allowModels)
			entry.State = run.State
			if entry.State == "" {
				entry.State = "failed"
			}
			if reviewErr != nil {
				entry.Error = reviewErr.Error()
				failures = append(failures, reviewErr)
			}
			if run.Snapshot.Tree != "" {
				entry.Tree = run.Snapshot.Tree
				entry.ContentSHA256 = run.Snapshot.ContentSHA256
			}
			for _, candidate := range run.Candidates {
				if candidate.State == "empty_result" {
					entry.State = "empty_review"
					entry.Error = fmt.Sprintf("reviewer %s returned no review findings or conclusion", candidate.ID)
					failures = append(failures, errors.New(entry.Error))
				}
				if candidate.FinalSnapshot != nil && (candidate.FinalSnapshot.Tree != run.Snapshot.Tree || candidate.FinalSnapshot.ContentSHA256 != run.Snapshot.ContentSHA256) {
					entry.State = "modified_review_snapshot"
					entry.Error = "reviewer modified its private snapshot; findings require inspection"
					failures = append(failures, errors.New(entry.Error))
				}
			}
			for i := range report.Runs {
				report.Runs[i].Superseded = true
			}
			currentTree, currentRaw, currentErr := contentState(watchCtx, repo)
			if currentErr != nil {
				entry.Superseded = true
				failures = append(failures, currentErr)
			} else {
				report.CurrentTree = currentTree
				report.CurrentContentSHA256 = currentRaw
				entry.Superseded = currentTree != entry.Tree || currentRaw != entry.ContentSHA256
			}
			report.Runs[entryIndex] = entry
			last = run.Snapshot.Tree + ":" + run.Snapshot.ContentSHA256
			if err = saveIsolationJSON(ctx, filepath.Join(dir, "watch.json"), &report); err != nil {
				return report, err
			}
			if watchCtx.Err() != nil {
				if ctx.Err() != nil {
					return report, ctx.Err()
				}
				return report, nil
			}
		}
		pending = identity
		if len(report.Runs) >= options.MaxRuns {
			break
		}
		timer := time.NewTimer(interval)
		select {
		case <-watchCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return report, ctx.Err()
			}
			return report, nil
		case <-timer.C:
		}
	}
	return report, nil
}

type LabEntry struct {
	Repetition int      `json:"repetition"`
	Order      []string `json:"order"`
	Output     string   `json:"output"`
	State      string   `json:"state"`
	Error      string   `json:"error,omitempty"`
}

type LabCandidate struct {
	ID             string `json:"id"`
	Runs           int    `json:"runs"`
	VerifiedRuns   int    `json:"verified_runs"`
	FailedRuns     int    `json:"failed_runs"`
	DurationMillis int64  `json:"duration_millis"`
}

type LabReport struct {
	Version            int               `json:"version"`
	Storage            string            `json:"storage,omitempty"`
	Isolation          *IsolationOptions `json:"isolation,omitempty"`
	ProcessContainment string            `json:"process_containment,omitempty"`
	Repository         string            `json:"repository"`
	Output             string            `json:"output"`
	State              string            `json:"state"`
	StartedAt          string            `json:"started_at"`
	FinishedAt         string            `json:"finished_at,omitempty"`
	Snapshot           Snapshot          `json:"snapshot"`
	Repetitions        int               `json:"repetitions"`
	Runs               []LabEntry        `json:"runs"`
	Candidates         []LabCandidate    `json:"candidates"`
	Interpretation     string            `json:"interpretation"`
	Error              string            `json:"error,omitempty"`
}

// Lab repeats candidate workflows on a frozen source snapshot and rotates
// execution order. Outcomes are descriptive check results, never a model
// ranking or a claim that passing the selected checks proves correctness.
func Lab(ctx context.Context, p Plan, output string, allowModels bool, repetitions int) (report LabReport, runErr error) {
	p = normalized(p)
	ctx, p, closeEarlyCredentials, err := registerIsolationEnvironment(ctx, p)
	closeCredentials := func() {}
	defer func() {
		if _, err := sanitizeIsolationEvidence(ctx, &report); err != nil {
			runErr = errors.Join(runErr, err)
		}
		runErr = redactEarlyIsolationError(ctx, runErr)
		closeCredentials()
		closeEarlyCredentials()
	}()
	if err != nil {
		return report, err
	}
	if p.Mode != "compare" {
		return report, errors.New("lab requires compare mode")
	}
	if err := ValidatePlan(p); err != nil {
		return report, err
	}
	if err := modelPermission(p, allowModels); err != nil {
		return report, err
	}
	ctx, p, closeCredentials, err = prepareIsolationCredentials(ctx, p)
	if err != nil {
		return report, err
	}
	prepared, containmentErr := PrepareProcessContainment(ctx, p.ProcessContainment)
	if containmentErr != nil {
		return report, containmentErr
	}
	ctx = prepared
	p.ProcessContainment = contextProcessContainment(ctx)
	canonicalRepo, rootErr := repositoryRoot(ctx, p.Repository)
	if rootErr != nil {
		return report, rootErr
	}
	p.Repository = canonicalRepo
	if err := ValidateIsolation(p); err != nil {
		return report, err
	}
	if containsIsolationCredential(ctx, []byte(output)) {
		return report, ErrIsolationCredentialLeak
	}
	if repetitions < 1 || repetitions > 20 {
		return report, errors.New("lab repetitions must be 1–20")
	}
	dir, err := newOutput(ctx, p.Repository, output)
	if err != nil {
		return report, err
	}
	report = LabReport{Version: 1, Storage: p.Storage, Isolation: p.Isolation, ProcessContainment: p.ProcessContainment, Repository: p.Repository, Output: dir, State: "running", StartedAt: timestamp(), Repetitions: repetitions, Runs: []LabEntry{}, Interpretation: "Descriptive results for this task, snapshot and checks. Passing checks does not establish general correctness. Order rotates; provider state and execution timing remain variable. Usage is retained in individual run reports; no inferred dollar totals or automatic winner."}
	defer func() {
		runErr = redactEarlyIsolationError(ctx, runErr)
		report.FinishedAt = timestamp()
		if runErr != nil {
			report.Error = runErr.Error()
			report.State = "failed"
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				report.State = "interrupted"
			}
		} else {
			report.State = "completed"
		}
		if err := saveIsolationJSON(ctx, filepath.Join(dir, "lab.json"), &report); err != nil {
			runErr = errors.Join(runErr, err)
			report.Error = runErr.Error()
			if report.State != "interrupted" {
				report.State = "failed"
			}
		}
	}()
	if err = saveIsolationJSON(ctx, filepath.Join(dir, "lab.json"), &report); err != nil {
		return report, err
	}
	labCtx, cancel := context.WithTimeout(ctx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	labCtx, err = PrepareSnapshotStorage(labCtx, p.Storage, p.Repository, filepath.Join(dir, "git"))
	if err != nil {
		return report, err
	}
	labCtx, closeContext, err := PrepareReportIsolation(labCtx, Report{Repository: p.Repository, Output: dir, Isolation: p.Isolation})
	if err != nil {
		return report, err
	}
	defer closeContext()
	snap, err := Capture(labCtx, p.Repository, filepath.Join(dir, "snapshot"))
	if err != nil {
		return report, err
	}
	report.Snapshot = snap
	baseline := filepath.Join(dir, "baseline")
	if err = CreateWorktree(labCtx, snap, baseline); err != nil {
		return report, err
	}
	summary := map[string]*LabCandidate{}
	for _, c := range p.Candidates {
		summary[c.ID] = &LabCandidate{ID: c.ID}
	}
	var failures []error
	for repetition := 0; repetition < repetitions; repetition++ {
		if err = labCtx.Err(); err != nil {
			return report, errors.Join(append(failures, err)...)
		}
		trial := p
		currentTree, currentRaw, baselineErr := contentState(labCtx, baseline)
		if baselineErr != nil {
			return report, baselineErr
		}
		if currentTree != snap.Tree || currentRaw != snap.ContentSHA256 {
			return report, errors.New("lab baseline changed; remaining trials were not started")
		}
		trial.Repository = baseline
		trial.expectedTree = snap.Tree
		trial.expectedContent = snap.ContentSHA256
		trial.MaxParallel = 1
		trial.Candidates = make([]Candidate, 0, len(p.Candidates))
		for i := range p.Candidates {
			trial.Candidates = append(trial.Candidates, p.Candidates[(i+repetition)%len(p.Candidates)])
		}
		entry := LabEntry{Repetition: repetition + 1, Output: filepath.Join(dir, fmt.Sprintf("trial-%04d", repetition+1)), State: "running"}
		for _, c := range trial.Candidates {
			entry.Order = append(entry.Order, c.ID)
		}
		entryIndex := len(report.Runs)
		report.Runs = append(report.Runs, entry)
		// Persist the pinned snapshot and admitted trial before its processes
		// start; retained aggregate progress must identify an interrupted child.
		if err = saveIsolationJSON(ctx, filepath.Join(dir, "lab.json"), &report); err != nil {
			report.Runs[entryIndex].State = "not_started"
			report.Runs[entryIndex].Error = err.Error()
			return report, err
		}
		run, trialErr := Run(labCtx, trial, entry.Output, allowModels)
		if run.Snapshot.Tree != "" && (run.Snapshot.Tree != snap.Tree || run.Snapshot.ContentSHA256 != snap.ContentSHA256) {
			trialErr = errors.Join(trialErr, errors.New("trial did not start from the frozen lab content"))
		}
		if run.ID != "" && run.Snapshot.Tree == snap.Tree && run.Snapshot.ContentSHA256 == snap.ContentSHA256 {
			// The trial's temporary baseline has the same pinned tree as the
			// original source. Keep integration addressed to that original
			// project, while plan.json retains the actual execution baseline.
			run.Repository = snap.Repository
			run.IntegrationSnapshot = &snap
			if persistErr := saveIsolationJSON(labCtx, filepath.Join(entry.Output, "report.json"), &run); persistErr != nil {
				trialErr = errors.Join(trialErr, persistErr)
			}
			// Run generated this private view before the lab re-addressed the
			// source metadata. Replace that generated view so it agrees with
			// the final report and integration target.
			if removeErr := os.Remove(filepath.Join(entry.Output, "report.html")); removeErr != nil && !os.IsNotExist(removeErr) {
				trialErr = errors.Join(trialErr, removeErr)
			} else if _, renderErr := RenderReportContext(labCtx, entry.Output); renderErr != nil {
				trialErr = errors.Join(trialErr, renderErr)
				if run.State != "interrupted" {
					run.State = "failed"
				}
				run.Error = redactEarlyIsolationError(labCtx, trialErr).Error()
				if persistErr := saveIsolationJSON(labCtx, filepath.Join(entry.Output, "report.json"), &run); persistErr != nil {
					trialErr = errors.Join(trialErr, persistErr)
				}
			}
		}
		entry.State = run.State
		if entry.State == "" {
			entry.State = "failed"
		}
		if trialErr != nil {
			if entry.State == "completed" {
				entry.State = "failed"
			}
			entry.Error = trialErr.Error()
			failures = append(failures, fmt.Errorf("trial %d: %w", repetition+1, trialErr))
		}
		for _, c := range run.Candidates {
			s := summary[c.ID]
			s.Runs++
			s.DurationMillis += c.DurationMillis
			if c.State == "verified" {
				s.VerifiedRuns++
			} else {
				s.FailedRuns++
			}
		}
		report.Runs[entryIndex] = entry
		report.Candidates = nil
		for _, c := range p.Candidates {
			report.Candidates = append(report.Candidates, *summary[c.ID])
		}
		if err = saveIsolationJSON(ctx, filepath.Join(dir, "lab.json"), &report); err != nil {
			return report, err
		}
	}
	return report, errors.Join(failures...)
}
