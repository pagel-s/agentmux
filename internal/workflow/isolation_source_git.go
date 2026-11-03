package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"agentmux/internal/sandbox"
)

// The original source may be a linked worktree whose .git file points outside
// the project. Discover these two exact roots before installing a new runtime;
// an inherited runtime still constrains these fixed read-only Git operations.
// Native/check wrappers never consume this controller-only capability.
func isolatedSourceGitPaths(ctx context.Context, source string, metadataOnly bool) ([]string, error) {
	if _, err := os.Stat(source); metadataOnly && errors.Is(err, os.ErrNotExist) {
		return nil, nil // retained private evidence can outlive its original source
	}
	paths := []string{}
	for _, flag := range []string{"--absolute-git-dir", "--git-common-dir"} {
		path, err := gitText(ctx, source, nil, "rev-parse", "--path-format=absolute", flag)
		if err != nil {
			return nil, fmt.Errorf("discover isolated source Git metadata: %w", err)
		}
		if sandbox.RuntimeOverlaps(path) {
			return nil, errors.New("original source Git metadata must not overlap system runtime mounts")
		}
		canonical, err := filepath.EvalSymlinks(path)
		info, statErr := os.Stat(canonical)
		if err != nil || statErr != nil || !filepath.IsAbs(path) || canonical != path || !info.IsDir() {
			return nil, errors.New("isolated source Git metadata must use canonical directory paths")
		}
		if len(paths) == 0 || paths[0] != canonical {
			paths = append(paths, canonical)
		}
	}
	return paths, nil
}

func (r *isolationRuntime) validateNativeGitDependencies(paths []string) error {
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue // the namespace wrapper separately validates availability
		}
		for _, metadata := range r.protectedGitPaths {
			if pathWithin(canonical, metadata) || pathWithin(metadata, canonical) || pathWithin(absolute, metadata) || pathWithin(metadata, absolute) {
				return errors.New("native/check dependency would expose original source Git metadata")
			}
		}
		for _, source := range r.nativeSourcePaths() {
			if pathWithin(canonical, source) || pathWithin(source, canonical) || pathWithin(absolute, source) || pathWithin(source, absolute) {
				return errors.New("native/check dependency would expose original source/input paths")
			}
		}
	}
	return nil
}

func (r *isolationRuntime) nativeSourcePaths() []string {
	selected := map[string]bool{}
	if r.source != "" {
		selected[r.source] = true
	}
	for path := range r.inputs {
		if path != "" {
			selected[path] = true
		}
	}
	paths := make([]string, 0, len(selected))
	for path := range selected {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
