package workflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxCatalogEntries = 1024

type RunSummary struct {
	ID         string `json:"id"`
	Goal       string `json:"goal"`
	State      string `json:"state"`
	Directory  string `json:"directory"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`
}

type catalogRecord struct {
	Version      int    `json:"version"`
	ID           string `json:"id"`
	Repository   string `json:"repository"`
	Directory    string `json:"directory"`
	StartedAt    string `json:"started_at"`
	RegisteredAt string `json:"registered_at"`
	Goal         string `json:"goal,omitempty"`
	State        string `json:"state,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
	Error        string `json:"error,omitempty"`
}

// CatalogRoot resolves the configured history destination without creating it.
// Existing symlinks at the selected root are refused; ancestor aliases such as
// macOS /tmp are resolved once and do not become managed catalog entries.
func CatalogRoot(root string) (string, error) {
	if root == "" {
		state := os.Getenv("XDG_STATE_HOME")
		if state == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			state = filepath.Join(home, ".local", "state")
		}
		if !filepath.IsAbs(state) {
			return "", errors.New("XDG_STATE_HOME must be absolute")
		}
		root = filepath.Join(state, "agentmux", "workflows")
	}
	return catalogFuturePath(root)
}

func catalogFuturePath(path string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return "", errors.New("catalog path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := abs
	missing := []string{}
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if current == abs && info.Mode()&os.ModeSymlink != 0 {
				return "", errors.New("catalog root cannot be a symlink")
			}
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			info, err = os.Stat(resolved)
			if err != nil || !info.IsDir() {
				return "", errors.New("catalog parent must be a directory")
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("catalog has no existing parent")
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func catalogPaths(ctx context.Context, repository, root string) (repo, base, bucket, index, runs string, err error) {
	repo, err = repositoryRoot(ctx, repository)
	if err != nil {
		return
	}
	base, err = CatalogRoot(root)
	if err != nil {
		return
	}
	if pathWithin(repo, base) {
		err = errors.New("work history must be outside the source repository")
		return
	}
	sum := sha256.Sum256([]byte(repo))
	bucket = filepath.Join(base, hex.EncodeToString(sum[:12]))
	index = filepath.Join(bucket, "index")
	runs = filepath.Join(bucket, "runs")
	return
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// SuggestedOutput creates no files or directories. The final run directory is
// intentionally absent so the existing exclusive workflow writer can own it.
func SuggestedOutput(ctx context.Context, repository, root string) (string, error) {
	_, base, bucket, index, runs, err := catalogPaths(ctx, repository, root)
	if err != nil {
		return "", err
	}
	for _, path := range []string{base, bucket, index, runs} {
		if _, err := os.Lstat(path); err == nil {
			if err = privateCatalogDirectory(path); err != nil {
				return "", err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	var entropy [6]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return filepath.Join(runs, time.Now().UTC().Format("20060102T150405Z")+"-"+hex.EncodeToString(entropy[:])), nil
}

// PrepareCatalog is called only after the owner chooses to execute. Existing
// managed directories must already be private; it never chmods unrelated state.
func PrepareCatalog(ctx context.Context, repository, root string) error {
	_, base, bucket, index, runs, err := catalogPaths(ctx, repository, root)
	if err != nil {
		return err
	}
	if err = mkdirAllDurable(base, 0700); err != nil {
		return err
	}
	for _, path := range []string{base, bucket, index, runs} {
		if err = os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err = privateCatalogDirectory(path); err != nil {
			return err
		}
		if err = syncDirectory(path); err != nil {
			return err
		}
		if err = syncParent(path); err != nil {
			return err
		}
	}
	return nil
}

func privateCatalogDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("catalog directory must be private and cannot be a symlink: %s", path)
	}
	return nil
}

func checkCatalogPaths(base, bucket, index string) error {
	for _, p := range []string{base, bucket, index} {
		if err := privateCatalogDirectory(p); err != nil {
			return err
		}
	}
	return nil
}

func canonicalRunDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("run directory must be a directory, not a symlink")
	}
	return filepath.EvalSymlinks(abs)
}

// RegisterRun records a pointer, never copies prompts, histories or credentials.
// Independent index files avoid a lost-update race between concurrent runs.
func RegisterRun(ctx context.Context, repository, root, runDirectory string) error {
	repo, base, bucket, index, _, err := catalogPaths(ctx, repository, root)
	if err != nil {
		return err
	}
	if err = checkCatalogPaths(base, bucket, index); err != nil {
		return err
	}
	dir, err := canonicalRunDirectory(runDirectory)
	if err != nil {
		return err
	}
	r, err := LoadReport(dir)
	if err != nil {
		return err
	}
	if r.Repository != repo {
		return errors.New("run belongs to a different repository")
	}
	unlock, err := catalogRegistrationLock(ctx, index, r.ID)
	if err != nil {
		return err
	}
	defer unlock()
	record := catalogRecord{Version: 1, ID: r.ID, Repository: repo, Directory: dir, StartedAt: r.StartedAt, RegisteredAt: timestamp(), Goal: shortGoal(r.Goal, 512), State: r.State, FinishedAt: r.FinishedAt, Error: bounded(r.Error, 4096)}
	path := filepath.Join(index, r.ID+".json")
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(index, ".catalog-*")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Link publishes a complete file exclusively; existing metadata is never
	// overwritten. It is an idempotent success only for the same exact run.
	if err = os.Link(temporary, path); err == nil {
		return syncDirectory(index)
	}
	if !errors.Is(err, os.ErrExist) {
		return err
	}
	old, readErr := readCatalogRecord(path, repo, r.ID)
	if readErr != nil {
		return readErr
	}
	if old.Directory != dir || old.StartedAt != r.StartedAt {
		return errors.New("catalog identity already refers to another run")
	}
	if old.State != "" && old.State != "running" && record.State == "running" {
		return nil
	}
	// The existing file is a validated record of this same run. Atomic rename
	// updates its recorded outcome without rewriting a shared global index.
	return saveJSON(path, record)
}

func readCatalogRecord(path, repo, id string) (catalogRecord, error) {
	var record catalogRecord
	data, err := ReadRegularFile(path, 64<<10)
	if err != nil {
		return record, err
	}
	if err = DecodeJSON(data, &record); err != nil {
		return record, err
	}
	if record.Version != 1 || record.ID != id || !workflowID.MatchString(record.ID) || record.Repository != repo || !filepath.IsAbs(record.Directory) {
		return record, errors.New("invalid or cross-repository catalog record")
	}
	if _, err = time.Parse(time.RFC3339Nano, record.StartedAt); err != nil {
		return record, errors.New("invalid catalog start time")
	}
	if record.State != "" && record.State != "running" && record.State != "completed" && record.State != "failed" && record.State != "interrupted" {
		return record, errors.New("unknown recorded work state")
	}
	return record, nil
}

func ListRuns(ctx context.Context, repository, root string) ([]RunSummary, error) {
	repo, base, bucket, index, _, err := catalogPaths(ctx, repository, root)
	if err != nil {
		return nil, err
	}
	if _, err = os.Lstat(index); errors.Is(err, os.ErrNotExist) {
		return []RunSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err = checkCatalogPaths(base, bucket, index); err != nil {
		return nil, err
	}
	dir, err := openWorkflowDirectory(index)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries := []os.FileInfo{}
	observed := 0
	for {
		batch, readErr := dir.Readdir(128)
		observed += len(batch)
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		if observed > maxCatalogEntries*4 {
			return nil, errors.New("work history has too many temporary or coordination entries; inspect the private index")
		}
		for _, entry := range batch {
			name := entry.Name()
			if strings.HasPrefix(name, ".catalog-") || strings.HasPrefix(name, ".register-") && strings.HasSuffix(name, ".lock") {
				if !entry.Mode().IsRegular() || entry.Mode().Perm()&0077 != 0 {
					return nil, errors.New("work history coordination entries must be private regular files")
				}
				continue
			}
			entries = append(entries, entry)
			if len(entries) > maxCatalogEntries {
				return nil, errors.New("work history exceeds 1024 records; archive old catalog entries explicitly")
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	result := []RunSummary{}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".catalog-") || strings.HasPrefix(name, ".register-") && strings.HasSuffix(name, ".lock") {
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			return nil, errors.New("unexpected file in work history index")
		}
		id := strings.TrimSuffix(name, ".json")
		record, err := readCatalogRecord(filepath.Join(index, name), repo, id)
		if err != nil {
			return nil, fmt.Errorf("read work history %s: %w", id, err)
		}
		row := RunSummary{ID: id, Directory: record.Directory, StartedAt: record.StartedAt, State: record.State, Goal: record.Goal, FinishedAt: record.FinishedAt, Error: record.Error}
		if row.State == "" {
			row.State = "unknown"
		}
		actual, err := canonicalRunDirectory(record.Directory)
		if err == nil {
			var info os.FileInfo
			info, err = os.Lstat(filepath.Join(actual, "report.json"))
			if err == nil && !info.Mode().IsRegular() {
				err = errors.New("recorded report is not a regular file")
			}
		}
		if err != nil {
			row.State = "unavailable"
			row.Error = "Recorded run is unavailable: " + err.Error()
		}
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool {
		// RFC3339 permits different UTC offsets and omitted fractional seconds;
		// lexical order can select an older run as latest. Records were parsed
		// above, so compare their actual instants and use ID only for ties.
		left, _ := time.Parse(time.RFC3339Nano, result[i].StartedAt)
		right, _ := time.Parse(time.RFC3339Nano, result[j].StartedAt)
		if left.Equal(right) {
			return result[i].ID > result[j].ID
		}
		return left.After(right)
	})
	return result, nil
}

func catalogRegistrationLock(ctx context.Context, index, id string) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	lock := filepath.Join(index, ".register-catalog.lock")
	for {
		f, err := lockApplication(lock)
		if err == nil {
			cancel()
			return func() { _ = f.Close() }, nil
		}
		if !applicationLockBusy(err) {
			cancel()
			return nil, err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			cancel()
			return nil, errors.New("work history registration is busy; another registration is still active")
		case <-timer.C:
		}
	}
}

func ResolveRun(ctx context.Context, repository, root, selector string) (string, error) {
	repo, err := repositoryRoot(ctx, repository)
	if err != nil {
		return "", err
	}
	if selector == "" || selector == "latest" {
		runs, err := ListRuns(ctx, repo, root)
		if err != nil {
			return "", err
		}
		if len(runs) == 0 {
			return "", errors.New("no recorded work for this repository; start with amx work \"your task\"")
		}
		if runs[0].State == "unavailable" {
			return "", errors.New(runs[0].Error + "; use amx work list to inspect older work")
		}
		return validateCatalogSelection(repo, runs[0])
	}
	if workflowID.MatchString(selector) {
		runs, err := ListRuns(ctx, repo, root)
		if err != nil {
			return "", err
		}
		for _, run := range runs {
			if run.ID == selector {
				if run.State == "unavailable" {
					return "", errors.New(run.Error)
				}
				return validateCatalogSelection(repo, run)
			}
		}
	}
	dir, err := canonicalRunDirectory(selector)
	if err != nil {
		return "", err
	}
	r, err := LoadReport(dir)
	if err != nil {
		return "", err
	}
	if r.Repository != repo {
		return "", errors.New("selected run belongs to a different repository")
	}
	return dir, nil
}

func validateCatalogSelection(repo string, row RunSummary) (string, error) {
	dir, err := canonicalRunDirectory(row.Directory)
	if err != nil {
		return "", err
	}
	r, err := LoadReport(dir)
	if err != nil {
		return "", err
	}
	if r.Repository != repo || r.ID != row.ID || r.StartedAt != row.StartedAt {
		return "", errors.New("run no longer matches its catalog identity")
	}
	return dir, nil
}
