package sandbox

import (
	"fmt"
	"os"
	"path/filepath"

	"agentmux/internal/harness"
)

// HomeFile exposes one narrowly selected private credential copy. The caller
// creates and removes its per-turn copy; no source home directory is mounted.
type HomeFile = harness.HomeFile

var homeTargets = map[string]bool{
	"/home/agent/.claude/.credentials.json": true,
	"/home/agent/.claude.json":              true,
	"/home/agent/.codex/auth.json":          true,
	"/home/agent/.pi/agent/auth.json":       true,
	"/home/agent/.pi/agent/models.json":     true,
}

func homeMounts(files []HomeFile, workspace, state, credential, socket string) ([]string, error) {
	args := []string{}
	seen := map[string]bool{}
	for _, file := range files {
		if !homeTargets[file.Target] || seen[file.Target] {
			return nil, fmt.Errorf("isolated home file requires a unique supported credential target")
		}
		source, err := realPath(file.Source, "isolated home credential copy", false)
		if err != nil {
			return nil, fmt.Errorf("isolated home credential copy is unavailable")
		}
		absolute, err := filepath.Abs(file.Source)
		if err != nil || absolute != source {
			return nil, fmt.Errorf("isolated home credential copy must not use symlinks")
		}
		info, err := os.Lstat(source)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, fmt.Errorf("isolated home credential copy must be a private regular file")
		}
		if containsPath(workspace, source) || containsPath(state, source) || source == credential || source == socket {
			return nil, fmt.Errorf("isolated home credential copy must be outside workspace and coordination state")
		}
		args = append(args, "--ro-bind", source, file.Target)
		seen[file.Target] = true
	}
	return args, nil
}
