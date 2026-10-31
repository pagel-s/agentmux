package sandbox

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"agentmux/internal/harness"
)

func sessionMounts(directories []harness.SessionDir, workspace, state, credential, socket string) ([]string, error) {
	if len(directories) == 0 {
		return nil, nil
	}
	if len(directories) != 1 {
		return nil, fmt.Errorf("one private native conversation store is permitted per turn")
	}
	directory := directories[0]
	switch directory.Target {
	case "/home/agent/.claude/projects", "/home/agent/.codex/sessions", "/home/agent/.pi/agent/sessions":
	default:
		return nil, fmt.Errorf("unsupported private conversation mount target")
	}
	source, err := realPath(directory.Source, "private conversation directory", true)
	absolute, absErr := filepath.Abs(directory.Source)
	if err != nil || absErr != nil || source != absolute || broadHostPath(source) || exposesWholeHome(source) {
		return nil, fmt.Errorf("conversation directory must be a canonical dedicated directory without symlinks")
	}
	info, err := os.Stat(source)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("conversation directory must be private (mode 0700)")
	}
	if containsPath(source, state) || containsPath(source, credential) || containsPath(source, socket) || containsPath(workspace, source) || containsPath(source, workspace) {
		return nil, fmt.Errorf("conversation store must not expose workspace, coordination state, or credentials")
	}
	if containsPath(state, source) {
		root := filepath.Join(state, "sessions")
		relative, err := filepath.Rel(root, source)
		parts := strings.Split(relative, string(filepath.Separator))
		if err != nil || !containsPath(root, source) || len(parts) != 2 || parts[0] == "" || parts[1] == "" || !strings.HasPrefix(parts[1], "g-") {
			return nil, fmt.Errorf("state-backed conversation store requires sessions/agent/generation")
		}
	}
	entries := 0
	err = filepath.WalkDir(source, func(_ string, entry fs.DirEntry, err error) error {
		entries++
		if err != nil || entries > 10000 {
			return fmt.Errorf("conversation storage inspection exceeded its bounds")
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return fmt.Errorf("conversation storage must contain only directories and regular files")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return []string{"--bind", source, directory.Target}, nil
}
