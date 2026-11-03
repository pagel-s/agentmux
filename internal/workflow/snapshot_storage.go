package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Snapshot bounds protect hashing and Git-object admission. They are not a
// disk quota: trusted Git filters and concurrent writers retain host authority.
const (
	MaxSnapshotBytes int64 = 512 << 20
	MaxSnapshotFiles       = 100000
)

type SnapshotSelection struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// SnapshotUsage observes the raw bytes selected for the next snapshot without
// writing an index, Git objects, refs or run output. The result describes this
// observation, not a lock against later writers. Ignored untracked data is not
// selected, while ignored data already tracked by HEAD or the index is selected.
// Private worktrees also retain their controller-pinned starting paths.
func SnapshotUsage(ctx context.Context, repository string) (SnapshotSelection, error) {
	private, err := privateStorageForRepository(ctx, repository)
	if err != nil {
		return SnapshotSelection{}, err
	}
	if private != nil {
		ctx = context.WithValue(ctx, snapshotStorageContextKey{}, private)
	}
	repo, err := repositoryRoot(ctx, repository)
	if err != nil {
		return SnapshotSelection{}, err
	}
	if private != nil {
		paths, err := privateSelectedPaths(ctx, repo)
		if err != nil {
			return SnapshotSelection{}, err
		}
		root, err := os.OpenRoot(repo)
		if err != nil {
			return SnapshotSelection{}, err
		}
		defer root.Close()
		return measurePrivateSelectedPaths(ctx, root, paths)
	}
	return snapshotUsageRoot(ctx, repo)
}

func snapshotUsageRoot(ctx context.Context, repo string) (SnapshotSelection, error) {
	// A staged deletion does not necessarily mean the working file is gone.
	// HEAD supplies those paths; the real index supplies newly tracked paths
	// that may already match .gitignore. Both are selected by contentState.
	head, err := gitOutput(ctx, repo, nil, "ls-tree", "-r", "--name-only", "-z", "HEAD", "--")
	if err != nil {
		return SnapshotSelection{}, err
	}
	current, err := gitOutput(ctx, repo, nil, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return SnapshotSelection{}, err
	}
	return measureSelectedPaths(ctx, repo, [][]byte{head, current}, MaxSnapshotFiles, MaxSnapshotBytes)
}

func measureSelectedPaths(ctx context.Context, repo string, lists [][]byte, maxFiles int, maxBytes int64) (SnapshotSelection, error) {
	usage := SnapshotSelection{}
	seen := map[string]bool{}
	for _, list := range lists {
		for _, path := range bytes.Split(list, []byte{0}) {
			if len(path) == 0 || seen[string(path)] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return usage, err
			}
			seen[string(path)] = true
			if len(seen) > 2*maxFiles {
				return usage, errors.New("selected snapshot path enumeration exceeds its metadata limit")
			}
			// Git emits repository-relative paths. Reject a corrupt enumeration
			// rather than let an absolute or parent path leave this project.
			name := filepath.FromSlash(strings.TrimSuffix(string(path), "/"))
			if filepath.IsAbs(name) || filepath.Clean(name) != name || name == ".." || bytes.HasPrefix(path, []byte("../")) {
				return usage, errors.New("invalid selected snapshot path")
			}
			info, err := os.Lstat(filepath.Join(repo, name))
			if errors.Is(err, os.ErrNotExist) {
				continue // a deletion contributes no current working content
			}
			if err != nil {
				return usage, err
			}
			if info.IsDir() {
				return usage, errors.New("workflow snapshots do not support submodules or embedded Git repositories; select a repository without gitlinks")
			}
			if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				return usage, errors.New("selected project content is not a file or symlink")
			}
			usage.Files++
			if usage.Files > maxFiles {
				return usage, fmt.Errorf("selected snapshot contains more than %d files", maxFiles)
			}
			size := info.Size()
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(filepath.Join(repo, name))
				if err != nil {
					return usage, err
				}
				size = int64(len(target))
			}
			if size > maxBytes-usage.Bytes {
				return usage, fmt.Errorf("selected snapshot contents exceed %d bytes (512 MiB maximum)", maxBytes)
			}
			usage.Bytes += size
		}
	}
	return usage, nil
}
