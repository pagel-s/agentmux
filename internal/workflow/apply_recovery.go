package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ApplyInspection struct {
	Run               string   `json:"run"`
	Candidate         string   `json:"candidate"`
	Repository        string   `json:"repository"`
	PatchSHA256       string   `json:"patch_sha256"`
	State             string   `json:"state"`
	SourceObservation string   `json:"source_observation"`
	Note              string   `json:"note"`
	Receipts          []string `json:"receipts"`
	Recoverable       bool     `json:"recoverable"`
}

type applicationReceipt struct {
	path   string
	result ApplyResult
}

// InspectApply observes saved integration evidence and current source content.
// It never starts a provider, applies a patch or changes a receipt.
func InspectApply(ctx context.Context, directory, candidateID string) (ApplyInspection, error) {
	r, err := LoadReport(directory)
	if err != nil {
		return ApplyInspection{}, err
	}
	ctx, closeReport, err := ReportContext(ctx, r)
	if err != nil {
		return ApplyInspection{}, err
	}
	defer closeReport()
	c, err := selectResult(r, candidateID)
	if err != nil {
		return ApplyInspection{}, err
	}
	inspection, _, err := inspectApplication(ctx, r, c)
	return inspection, err
}

func inspectApplication(ctx context.Context, r Report, c CandidateResult) (ApplyInspection, []applicationReceipt, error) {
	v := ApplyInspection{Run: r.ID, Candidate: c.ID, Repository: r.Repository, PatchSHA256: c.PatchSHA256, State: "none", SourceObservation: "unknown", Receipts: []string{}}
	all, err := readApplicationReceipts(r)
	if err != nil {
		v.State = "unknown"
		v.Note = "Application history could not be confirmed: " + err.Error()
		return v, nil, nil
	}
	matching := []applicationReceipt{}
	for _, receipt := range all {
		if receipt.result.PatchSHA256 == c.PatchSHA256 {
			matching = append(matching, receipt)
			v.Receipts = append(v.Receipts, receipt.path)
			if receipt.result.Uncertain {
				v.State = "uncertain"
			}
		}
	}
	if len(matching) == 0 {
		v.Note = "No application has been recorded for this patch."
		return v, matching, nil
	}
	if c.FinalSnapshot == nil || !objectID(SourceSnapshot(r).Tree) || !digestID(SourceSnapshot(r).ContentSHA256) || !objectID(c.FinalSnapshot.Tree) || !digestID(c.FinalSnapshot.ContentSHA256) {
		v.State = "unknown"
		v.Note = "Application snapshots cannot establish exact source identity."
		return v, matching, nil
	}
	tree, raw, err := contentState(ctx, r.Repository)
	if err != nil {
		v.State = "unknown"
		v.Note = "Source content could not be confirmed: " + err.Error()
		return v, matching, nil
	}
	headMatches, err := sourceHEADMatches(ctx, r.Repository, SourceSnapshot(r).BaseCommit)
	if err != nil {
		v.State = "unknown"
		v.Note = "Source HEAD could not be confirmed: " + err.Error()
		return v, matching, nil
	}
	v.SourceObservation = "diverged"
	if headMatches {
		if tree == SourceSnapshot(r).Tree && raw == SourceSnapshot(r).ContentSHA256 {
			v.SourceObservation = "matches_start"
		} else if tree == c.FinalSnapshot.Tree && raw == c.FinalSnapshot.ContentSHA256 {
			v.SourceObservation = "matches_final"
		}
	}
	if v.State == "uncertain" {
		v.Recoverable = v.SourceObservation == "matches_start" || v.SourceObservation == "matches_final"
		v.Note = "An application was admitted without a confirmed final receipt. Inspect the source observation; explicit recovery never replays the patch."
		return v, matching, nil
	}
	if matching[0].result.Applied && v.SourceObservation == "matches_final" {
		v.State = "applied"
		v.Note = "The source matches the recorded applied result and starting HEAD."
	} else if !matching[0].result.Applied && v.SourceObservation == "matches_start" {
		v.State = "not_applied"
		v.Note = "The source matches the starting snapshot; no application is confirmed."
	} else {
		v.State = "source_changed"
		v.Note = "Current source identity differs from the recorded application outcome. Inspect concurrent or later changes."
	}
	return v, matching, nil
}

// RecoverApply resolves an interrupted application by comparing exact source
// content and HEAD with its recorded start/final identities. It only updates
// existing journal records; it never replays the patch or undoes source work.
func RecoverApply(ctx context.Context, directory, candidateID string) (ApplyInspection, error) {
	r, err := LoadReport(directory)
	if err != nil {
		return ApplyInspection{}, err
	}
	ctx, closeReport, err := ReportContext(ctx, r)
	if err != nil {
		return ApplyInspection{}, err
	}
	defer closeReport()
	c, err := selectResult(r, candidateID)
	if err != nil {
		return ApplyInspection{}, err
	}
	lease, err := lockSourceApplication(ctx, r.Repository)
	if err != nil {
		return ApplyInspection{}, err
	}
	defer lease.Close()
	ctx = context.WithValue(ctx, applicationLeaseContextKey{}, lease)
	v, receipts, err := inspectApplication(ctx, r, c)
	if err != nil || v.State != "uncertain" {
		if err == nil && v.State == "unknown" {
			err = errors.New(v.Note)
		}
		return v, err
	}
	if !v.Recoverable {
		return v, errors.New("application remains uncertain: source does not match the exact starting or final snapshot; inspect it manually")
	}
	if v.SourceObservation == "matches_final" {
		if err = syncAppliedSource(ctx, r, *c.FinalSnapshot); err != nil {
			return v, fmt.Errorf("persist recovered source: %w", err)
		}
	}
	// Re-observe after persistence, before resolving evidence. A concurrent
	// writer is never inferred to have stopped merely because a lock is held.
	confirmed, _, err := inspectApplication(ctx, r, c)
	if err != nil || confirmed.SourceObservation != v.SourceObservation {
		return v, errors.New("source changed during application recovery; journal remains uncertain")
	}
	for _, receipt := range receipts {
		if !receipt.result.Uncertain {
			continue
		}
		result := receipt.result
		result.Uncertain = false
		result.Applied = v.SourceObservation == "matches_final"
		result.Phase = "recovered"
		result.Note = "Explicit recovery confirmed exact source " + v.SourceObservation + "; no patch was replayed."
		if err := saveJSON(receipt.path, result); err != nil {
			return v, fmt.Errorf("finalize recovered receipt: %w", err)
		}
	}
	return InspectApply(ctx, directory, candidateID)
}

func readApplicationReceipts(r Report) ([]applicationReceipt, error) {
	dir, err := openWorkflowDirectory(r.Output)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	names := []string{}
	entries := 0
	for {
		files, readErr := dir.Readdirnames(256)
		entries += len(files)
		if entries > 4096 || readErr != nil && readErr != io.EOF {
			return nil, errors.New("application history enumeration is incomplete")
		}
		for _, name := range files {
			if strings.HasPrefix(name, "applied-") && strings.HasSuffix(name, ".json") {
				names = append(names, name)
				if len(names) > 128 {
					return nil, errors.New("application history exceeds 128 receipts")
				}
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	receipts := make([]applicationReceipt, 0, len(names))
	for _, name := range names {
		path := filepath.Join(r.Output, name)
		var result ApplyResult
		data, err := ReadRegularFile(path, 64<<10)
		if err == nil {
			err = DecodeJSON(data, &result)
		}
		if err != nil || result.Run != r.ID || result.Repository != r.Repository || !digestID(result.PatchSHA256) {
			return nil, fmt.Errorf("damaged application receipt %s", name)
		}
		if result.Phase != "" && result.Phase != "intent" && result.Phase != "finished" && result.Phase != "recovered" || result.Phase == "intent" && (!result.Uncertain || result.Applied) {
			return nil, fmt.Errorf("invalid application phase in %s", name)
		}
		receipts = append(receipts, applicationReceipt{path: path, result: result})
	}
	return receipts, nil
}

type applicationLeaseContextKey struct{}

func lockSourceApplication(ctx context.Context, repository string) (*os.File, error) {
	common, err := gitText(ctx, repository, nil, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(repository, common)
	}
	return lockApplication(filepath.Join(common, "agentmux-apply.lock"))
}

func syncAppliedSource(ctx context.Context, r Report, final Snapshot) error {
	paths, err := snapshotGitOutput(ctx, SnapshotObjectRepository(final), final.Store != "", nil, "diff", "--name-only", "--no-renames", "-z", "--no-ext-diff", "--no-textconv", SourceSnapshot(r).Tree, final.Tree, "--")
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(r.Repository)
	if err != nil {
		return err
	}
	defer root.Close()
	directories := map[string]bool{r.Repository: true}
	for _, relative := range strings.Split(string(paths), "\x00") {
		if relative == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(r.Repository, relative)
		if !pathWithin(r.Repository, path) {
			return errors.New("changed source path lies outside the repository")
		}
		info, statErr := privateCurrentPathInfo(root, relative)
		if statErr != nil {
			return statErr
		}
		if info != nil && info.Mode().IsRegular() {
			f, err := privateRootOpenRegular(root, relative, info)
			if err != nil {
				return err
			}
			syncErr := f.Sync()
			closeErr := f.Close()
			if err := errors.Join(syncErr, closeErr); err != nil {
				return err
			}
		} else if info != nil && info.IsDir() {
			// A removed tracked file may now be a directory containing new
			// selected files. Persist its directory entry as well.
			directories[path] = true
		} else if info != nil && info.Mode()&os.ModeSymlink == 0 {
			return errors.New("applied source path is not a regular file or symlink")
		}
		for parent := filepath.Dir(relative); parent != "."; parent = filepath.Dir(parent) {
			info, err := privateCurrentPathInfo(root, parent)
			if err != nil {
				return err
			}
			if info != nil && info.IsDir() {
				directories[filepath.Join(r.Repository, parent)] = true
			}
		}
	}
	// Sync children before parents so newly created nested directories become
	// durable before publishing their parent directory entries.
	ordered := make([]string, 0, len(directories))
	for directory := range directories {
		ordered = append(ordered, directory)
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, directory := range ordered {
		if err := syncDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}
