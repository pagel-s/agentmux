package sandbox

import (
	"errors"
	"os"
	"path/filepath"
)

// RuntimeOverlaps reports whether a protected tree would overlap any default
// runtime bind, as either its ancestor or descendant. Both host sources and
// namespace destinations matter: system aliases may bind a canonical host path
// under a different destination. Unresolvable paths fail closed.
func RuntimeOverlaps(path string) bool {
	selected, err := runtimeOverlapPaths(path)
	if err != nil {
		return true
	}
	args := runtimeMounts()
	for i := 0; i+2 < len(args); i += 3 {
		for _, mount := range args[i+1 : i+3] {
			paths, err := runtimeOverlapPaths(mount)
			if err != nil {
				return true
			}
			for _, left := range selected {
				for _, right := range paths {
					if containsPath(left, right) || containsPath(right, left) {
						return true
					}
				}
			}
		}
	}
	return false
}

func runtimeOverlapPaths(path string) ([]string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil || path == "" {
		return nil, errors.New("runtime overlap requires a valid absolute path")
	}
	ancestor, suffix := absolute, ""
	for {
		canonical, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			return []string{absolute, filepath.Join(canonical, suffix)}, nil
		}
		if !errors.Is(err, os.ErrNotExist) || ancestor == filepath.Dir(ancestor) {
			return nil, err
		}
		suffix = filepath.Join(filepath.Base(ancestor), suffix)
		ancestor = filepath.Dir(ancestor)
	}
}
