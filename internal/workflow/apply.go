package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type ApplyResult struct {
	Run         string `json:"run"`
	Candidate   string `json:"candidate"`
	Repository  string `json:"repository"`
	PatchSHA256 string `json:"patch_sha256"`
	Applied     bool   `json:"applied"`
	Uncertain   bool   `json:"uncertain,omitempty"`
	Preview     string `json:"preview,omitempty"`
	Note        string `json:"note"`
	Phase       string `json:"phase,omitempty"`
	Receipt     string `json:"receipt,omitempty"`
}

// Apply is an explicit owner operation. It never chooses a winner, commits or
// pushes. Concurrent writers must be settled before integrating a result.
func Apply(ctx context.Context, directory, candidateID string) (ApplyResult, error) {
	r, err := LoadReport(directory)
	if err != nil {
		return ApplyResult{}, err
	}
	ctx, err = PrepareProcessContainment(ctx, r.ProcessContainment)
	if err != nil {
		return ApplyResult{}, err
	}
	c, err := selectResult(r, candidateID)
	if err != nil {
		return ApplyResult{}, err
	}
	result := ApplyResult{Run: r.ID, Candidate: c.ID, Repository: r.Repository, PatchSHA256: c.PatchSHA256}
	if err = RequireApprovedReview(r); err != nil {
		return result, err
	}
	if r.FinishedAt == "" {
		return result, errors.New("run is unfinished; wait before applying a result")
	}
	if c.Verification == nil || !c.Verification.Passed || c.FinalSnapshot == nil || c.FinalSnapshot.Tree != c.Verification.Tree || c.Verification.Tree != c.Verification.EndingTree || c.FinalSnapshot.ContentSHA256 != c.Verification.ContentSHA256 {
		return result, errors.New("result needs passing independent checks bound to its exact final tree")
	}
	fresh, err := IsFresh(ctx, c.Worktree, *c.Verification)
	if err != nil {
		return result, err
	}
	if !fresh {
		return result, errors.New("candidate has changed since verification; run checks again")
	}
	patch, err := readPatch(directory, c.Patch, c.PatchSHA256)
	if err != nil {
		return result, err
	}
	if err = validateSnapshot(ctx, r.Repository, r.Snapshot); err != nil {
		return result, err
	}
	if err = validateSnapshot(ctx, c.Worktree, *c.FinalSnapshot); err != nil {
		return result, err
	}
	canonical, err := gitOutput(ctx, r.Repository, nil, "diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", r.Snapshot.Commit, c.FinalSnapshot.Tree, "--")
	if err != nil {
		return result, err
	}
	if !bytes.Equal(canonical, patch) {
		return result, errors.New("patch does not represent the recorded starting and final content")
	}
	// An exclusive integration lock serializes Agentmux apply operations. Native
	// tools do not participate: the source is checked again just before apply.
	root, err := filepath.Abs(directory)
	if err != nil {
		return result, err
	}
	lease, err := lockSourceApplication(ctx, r.Repository)
	if err != nil {
		return result, err
	}
	defer lease.Close()
	ctx = context.WithValue(ctx, applicationLeaseContextKey{}, lease)
	receipts, err := readApplicationReceipts(r)
	if err != nil {
		return result, fmt.Errorf("application history needs inspection before retrying: %w", err)
	}
	for _, receipt := range receipts {
		if receipt.result.PatchSHA256 == c.PatchSHA256 && receipt.result.Uncertain {
			return result, errors.New("an earlier application is unresolved; inspect and explicitly recover its receipt before retrying")
		}
	}
	head, err := applyGit(ctx, r.Repository, nil, "rev-parse", "HEAD")
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(string(head)) != r.Snapshot.BaseCommit {
		return result, errors.New("source HEAD changed since the run; review and integrate the patch manually")
	}
	current, content, err := contentState(ctx, r.Repository)
	if err != nil {
		return result, err
	}
	if current != r.Snapshot.Tree || content != r.Snapshot.ContentSHA256 {
		return result, errors.New("source files changed since the run; preserve current work and reconcile the patch manually")
	}
	if len(patch) == 0 {
		if c.FinalSnapshot.ContentSHA256 != r.Snapshot.ContentSHA256 {
			return result, errors.New("checked result changes raw bytes that the Git patch cannot represent; integrate it manually")
		}
		result.Note = "No changes to apply."
		return result, nil
	}
	// Git attributes may normalize patch bytes. Apply to a private pinned copy
	// first so such conversions cannot silently change the checked result.
	preview, err := os.MkdirTemp(root, "integration-")
	if err != nil {
		return result, err
	}
	if err = os.Remove(preview); err != nil {
		return result, err
	}
	if err = CreateWorktree(ctx, r.Snapshot, preview); err != nil {
		return result, err
	}
	result.Preview = preview
	if _, err = applyGit(ctx, preview, patch, "apply", "--binary", "-"); err != nil {
		return result, fmt.Errorf("integration preview: %w", err)
	}
	previewTree, previewContent, err := contentState(ctx, preview)
	if err != nil {
		return result, err
	}
	if previewTree != c.FinalSnapshot.Tree || previewContent != c.FinalSnapshot.ContentSHA256 {
		return result, errors.New("patch does not reproduce the checked result's exact bytes; source was not changed")
	}
	if _, err = applyGit(ctx, r.Repository, patch, "apply", "--check", "--binary", "-"); err != nil {
		return result, err
	}
	current, content, err = contentState(ctx, r.Repository)
	if err != nil {
		return result, err
	}
	if current != r.Snapshot.Tree || content != r.Snapshot.ContentSHA256 {
		return result, errors.New("source changed during integration review; nothing was applied")
	}
	// An empty commit or a branch switch can move HEAD without changing the
	// selected files. File fingerprints alone cannot establish source freshness.
	if matches, headErr := sourceHEADMatches(ctx, r.Repository, r.Snapshot.BaseCommit); headErr != nil {
		return result, headErr
	} else if !matches {
		return result, errors.New("source HEAD changed during integration review; nothing was applied")
	}
	// Persist intent before the first source-writing process is admitted. A hard
	// interruption leaves a discoverable unresolved record instead of an absent
	// receipt which could be mistaken for permission to replay the application.
	result.Phase = "intent"
	result.Uncertain = true
	result.Note = "Application admitted; source completion is not yet confirmed."
	result.Receipt = filepath.Join(root, fmt.Sprintf("applied-%d.json", time.Now().UnixNano()))
	if err = writePrivateJSON(result.Receipt, result); err != nil {
		return result, fmt.Errorf("persist application intent before source mutation: %w", err)
	}
	result.Phase = "finished"
	if _, err = applyGit(ctx, r.Repository, patch, "apply", "--binary", "-"); err != nil {
		result.Uncertain = true
		result.Note = "Patch application did not confirm completion. Inspect the source before retrying; some files may have changed."
		inspectCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		observedTree, observedContent, inspectErr := contentState(inspectCtx, r.Repository)
		headMatches, headErr := sourceHEADMatches(inspectCtx, r.Repository, r.Snapshot.BaseCommit)
		if inspectErr == nil && headErr == nil && headMatches {
			if observedTree == r.Snapshot.Tree && observedContent == r.Snapshot.ContentSHA256 {
				result.Uncertain = false
				result.Note = "Patch application failed; selected source content remains at the recorded starting snapshot."
			}
			if observedTree == c.FinalSnapshot.Tree && observedContent == c.FinalSnapshot.ContentSHA256 {
				result.Applied = true
				if syncErr := syncAppliedSource(inspectCtx, r, *c.FinalSnapshot); syncErr == nil {
					finalTree, finalRaw, finalErr := contentState(inspectCtx, r.Repository)
					finalHEAD, finalHEADErr := sourceHEADMatches(inspectCtx, r.Repository, r.Snapshot.BaseCommit)
					if finalErr == nil && finalHEADErr == nil && finalHEAD && finalTree == c.FinalSnapshot.Tree && finalRaw == c.FinalSnapshot.ContentSHA256 {
						result.Uncertain = false
						result.Note = "The application process did not confirm completion, but persisted source content matches the checked result. Do not repeat the application."
					}
				}
			}
		}
		cancel()
		if receiptErr := saveJSON(result.Receipt, result); receiptErr != nil {
			result.Uncertain = true
			result.Note += " Receipt could not be saved: " + receiptErr.Error()
			err = errors.Join(err, receiptErr)
		}
		return result, err
	}
	result.Applied = true
	result.Uncertain = false
	result.Note = "Applied selected patch to the source working tree; index, commits and remote are unchanged."
	persistErr := syncAppliedSource(ctx, r, *c.FinalSnapshot)
	current, content, checkErr := contentState(ctx, r.Repository)
	headMatches, headErr := sourceHEADMatches(ctx, r.Repository, r.Snapshot.BaseCommit)
	var finalErr error
	if persistErr != nil || checkErr != nil || headErr != nil || !headMatches || current != c.FinalSnapshot.Tree || content != c.FinalSnapshot.ContentSHA256 {
		result.Uncertain = true
		result.Note = "The patch was applied, but source HEAD, content or persistence could not be confirmed as the recorded result. Preserve the applied changes and inspect concurrent edits before continuing."
		finalErr = errors.Join(errors.New("patch was applied but final source verification is uncertain; inspect the applied content before continuing"), persistErr, checkErr, headErr)
	}
	// The receipt is separate from immutable execution evidence. Failure to
	// retain it must not be reported as though a successful apply did not occur.
	if err := saveJSON(result.Receipt, result); err != nil {
		result.Uncertain = true
		result.Note += " Receipt could not be saved: " + err.Error()
		finalErr = errors.Join(finalErr, fmt.Errorf("application occurred but its durable receipt could not be confirmed: %w", err))
	}
	return result, finalErr
}

func sourceHEADMatches(ctx context.Context, repository, expected string) (bool, error) {
	head, err := applyGit(ctx, repository, nil, "rev-parse", "--verify", "HEAD^{commit}")
	return strings.TrimSpace(string(head)) == expected, err
}

func applyGit(ctx context.Context, repo string, input []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	argv := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-C", repo}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	configureGitProcess(cmd)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = cleanGitEnvironment()
	if lease, ok := ctx.Value(applicationLeaseContextKey{}).(*os.File); ok {
		// ExtraFiles duplicates this descriptor without close-on-exec. Closing
		// the owner process cannot release the coordination lock while a source
		// writer or its descendants still hold the inherited descriptor.
		cmd.ExtraFiles = append(cmd.ExtraFiles, lease)
	}
	if err := wrapGitProcess(ctx, cmd); err != nil {
		return nil, err
	}
	cmd.WaitDelay = 2 * time.Second
	var output limitedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %.2048s", args[0], err, output.Bytes())
	}
	if output.exceeded {
		return nil, errors.New("git output exceeds workflow size limit")
	}
	return output.Bytes(), nil
}

func cleanGitEnvironment() []string {
	env := []string{}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if !strings.HasPrefix(key, "GIT_") {
			env = append(env, value)
		}
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1")
}

func saveApplyReceipt(directory string, result ApplyResult) error {
	// Implemented through the same private JSON writer as presentation output.
	return writePrivateJSON(filepath.Join(directory, fmt.Sprintf("applied-%d.json", time.Now().UnixNano())), result)
}
