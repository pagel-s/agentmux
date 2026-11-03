package workflow

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentmux/internal/harness"
)

type Check struct {
	Name           string   `json:"name"`
	Command        []string `json:"command"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}
type CheckResult struct {
	CredentialsRedacted bool     `json:"credentials_redacted,omitempty"`
	Name                string   `json:"name"`
	Command             []string `json:"command"`
	Passed              bool     `json:"passed"`
	ExitCode            int      `json:"exit_code"`
	Output              string   `json:"output,omitempty"`
	Error               string   `json:"error,omitempty"`
	DurationMillis      int64    `json:"duration_millis"`
}
type Verification struct {
	Storage             string        `json:"storage,omitempty"`
	Store               string        `json:"store,omitempty"`
	IgnoreSHA256        string        `json:"ignore_sha256,omitempty"`
	SelectionSHA256     string        `json:"selection_sha256,omitempty"`
	Isolation           string        `json:"isolation,omitempty"`
	ProcessContainment  string        `json:"process_containment,omitempty"`
	Repository          string        `json:"repository"`
	Tree                string        `json:"tree"`
	EndingTree          string        `json:"ending_tree"`
	ContentSHA256       string        `json:"content_sha256"`
	EndingContentSHA256 string        `json:"ending_content_sha256"`
	Passed              bool          `json:"passed"`
	StartedAt           string        `json:"started_at"`
	FinishedAt          string        `json:"finished_at"`
	Checks              []CheckResult `json:"checks"`
}

// Verify runs explicit owner-configured argv checks locally, with bounded
// duration and output. It does not interpret shell syntax or certify correctness
// beyond these checks. A check that modifies selected project content invalidates
// the result. Ignored build artifacts do not change a content fingerprint.
func Verify(ctx context.Context, repository string, checks []Check) (Verification, error) {
	return VerifyWithContainment(ctx, repository, checks, ProcessGroups)
}

// VerifyWithContainment verifies the requested process-tree boundary before
// launching owner checks or their Git helpers. It never weakens the policy
// after a failed probe; PID containment retains trusted host filesystem access.
func VerifyWithContainment(ctx context.Context, repository string, checks []Check, policy string) (Verification, error) {
	if err := validateVerificationChecks(checks); err != nil {
		return Verification{}, err
	}
	prepared, err := PrepareProcessContainment(ctx, policy)
	if err != nil {
		return Verification{}, err
	}
	prepared, err = SnapshotContextForRepository(prepared, repository)
	if err != nil {
		return Verification{}, err
	}
	return verifyPrepared(prepared, repository, checks)
}

func validateVerificationChecks(checks []Check) error {
	if len(checks) == 0 || len(checks) > 32 {
		return errors.New("verification requires between 1 and 32 checks")
	}
	names := map[string]bool{}
	for _, check := range checks {
		if strings.TrimSpace(check.Name) == "" || len(check.Name) > 128 || names[check.Name] {
			return errors.New("check names must be unique, nonempty and at most 128 bytes")
		}
		names[check.Name] = true
		if len(check.Command) == 0 || len(check.Command) > 64 || check.Command[0] == "" {
			return errors.New("check requires an executable argv with at most 64 arguments")
		}
		for _, arg := range check.Command {
			if len(arg) > 16384 || strings.ContainsRune(arg, 0) {
				return errors.New("invalid check argument")
			}
		}
		if check.TimeoutSeconds < 0 || check.TimeoutSeconds > 600 {
			return errors.New("check timeout must be between 1 and 600 seconds (0 uses 60)")
		}
	}
	return nil
}

func verifyPrepared(ctx context.Context, repository string, checks []Check) (v Verification, verifyErr error) {
	defer func() {
		if verifyErr != nil {
			v.Passed = false
		}
		verifyErr = redactEarlyIsolationError(ctx, verifyErr)
		if _, err := sanitizeIsolationEvidence(ctx, &v); err != nil {
			v.Passed = false
			verifyErr = errors.Join(verifyErr, err)
		}
		verifyErr = redactEarlyIsolationError(ctx, verifyErr)
	}()
	if err := validateVerificationChecks(checks); err != nil {
		return v, err
	}
	repo, err := repositoryRoot(ctx, repository)
	if err != nil {
		return v, err
	}
	ctx, err = SnapshotContextForRepository(ctx, repo)
	if err != nil {
		return v, err
	}
	tree, content, err := contentState(ctx, repo)
	if err != nil {
		return v, err
	}
	v = Verification{Storage: SnapshotStoragePolicy(ctx), Store: SnapshotObjectStore(ctx), IgnoreSHA256: SnapshotIgnoreSHA256(ctx), SelectionSHA256: snapshotSelectionSHA256(ctx), Isolation: IsolationMode(ctx), ProcessContainment: contextProcessContainment(ctx), Repository: repo, Tree: tree, EndingTree: tree, ContentSHA256: content, EndingContentSHA256: content, Passed: true, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Checks: []CheckResult{}}
	for _, check := range checks {
		seconds := check.TimeoutSeconds
		if seconds == 0 {
			seconds = 60
		}
		checkCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
		start := time.Now()
		request := harness.Request{Harness: "command", Command: append([]string(nil), check.Command...), Workdir: repo, ReapDescendants: true, Wrap: processContainmentWrapper(contextProcessContainment(ctx))}
		request, closeRequest, prepareErr := IsolationCheckRequest(checkCtx, repo, request)
		if prepareErr != nil {
			cancel()
			return v, prepareErr
		}
		result, runErr := harness.Run(checkCtx, request)
		result, redacted, redactErr := redactIsolationNativeResult(checkCtx, result)
		runErr = errors.Join(runErr, redactErr)
		if runErr != nil {
			original := runErr.Error()
			runErr = RedactIsolationError(checkCtx, "", runErr)
			redacted = redacted || runErr.Error() != original
		}
		closeRequest()
		cancel()
		r := CheckResult{CredentialsRedacted: redacted, Name: check.Name, Command: append([]string(nil), check.Command...), Passed: runErr == nil, DurationMillis: time.Since(start).Milliseconds(), Output: truncateDiagnostic(result.Text), ExitCode: 0}
		if runErr != nil {
			r.ExitCode = -1
			var exit *exec.ExitError
			if errors.As(runErr, &exit) {
				r.ExitCode = exit.ExitCode()
			}
			r.Error = truncateDiagnostic(runErr.Error())
			v.Passed = false
		}
		v.Checks = append(v.Checks, r)
		ending, endingContent, treeErr := contentState(ctx, repo)
		if treeErr != nil {
			v.Passed = false
			v.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			return v, fmt.Errorf("fingerprint after check: %w", treeErr)
		}
		v.EndingTree = ending
		v.EndingContentSHA256 = endingContent
		if ending != tree || endingContent != content {
			v.Passed = false
			v.Checks[len(v.Checks)-1].Passed = false
			v.Checks[len(v.Checks)-1].Error = "check changed repository contents; verification is invalid"
			break
		}
		if ctx.Err() != nil {
			v.Passed = false
			v.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			return v, ctx.Err()
		}
	}
	v.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return v, nil
}

// IsFresh checks content identity, not whether a prior successful command is a
// proof of correctness. Failed and mutated verification never becomes current.
func IsFresh(ctx context.Context, repository string, v Verification) (bool, error) {
	if !v.Passed || v.Tree == "" || v.Tree != v.EndingTree || len(v.ContentSHA256) != 64 || v.ContentSHA256 != v.EndingContentSHA256 || len(v.Checks) == 0 {
		return false, nil
	}
	for _, check := range v.Checks {
		if !check.Passed {
			return false, nil
		}
	}
	prepared, err := PrepareProcessContainment(ctx, v.ProcessContainment)
	if err != nil {
		return false, err
	}
	ctx = prepared
	if v.Isolation != "" {
		if v.Isolation != "bubblewrap" || v.Storage != StoragePrivate || v.Store == "" {
			return false, errors.New("verification requires an unavailable or incomplete filesystem isolation policy")
		}
		if existing := isolationFromContext(ctx); existing != nil {
			if existing.opts.Mode != v.Isolation {
				return false, errors.New("verification isolation differs from its active required boundary")
			}
		} else {
			// A freshness check starts no provider and needs no credentials or
			// network. Bind only its own recorded private repository/cache. A
			// normal repository's .git lives within its selected worktree, so
			// use that worktree's private parent as the containment state root.
			state := filepath.Dir(v.Store)
			if filepath.Base(v.Store) == ".git" {
				state = filepath.Dir(state)
			}
			var closeIsolation func()
			ctx, closeIsolation, err = prepareIsolation(ctx, &IsolationOptions{Mode: v.Isolation}, repository, state, nil, true, nil)
			if err != nil {
				return false, err
			}
			defer closeIsolation()
		}
	}
	if v.Storage == StoragePrivate {
		if v.Store == "" || !digestID(v.IgnoreSHA256) || !digestID(v.SelectionSHA256) {
			return false, errors.New("private verification lacks its pinned store, ignore policy or path selection")
		}
		ctx, err = SnapshotContext(ctx, Snapshot{Repository: repository, Store: v.Store, IgnoreSHA256: v.IgnoreSHA256, SelectionSHA256: v.SelectionSHA256})
		if err != nil {
			return false, err
		}
	}
	ctx, err = SnapshotContextForRepository(ctx, repository)
	if err != nil {
		return false, err
	}
	repo, err := repositoryRoot(ctx, repository)
	if err != nil {
		return false, err
	}
	if repo != v.Repository {
		return false, nil
	}
	tree, content, err := contentState(ctx, repo)
	return tree == v.Tree && content == v.ContentSHA256, err
}

func truncateDiagnostic(value string) string {
	const maximum = 64 << 10
	if len(value) > maximum {
		return value[:maximum] + "\n[output truncated]"
	}
	return value
}
