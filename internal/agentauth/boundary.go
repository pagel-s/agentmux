package agentauth

import (
	"errors"
	"path/filepath"
	"strings"

	"agentmux/internal/managedconfig"
	"agentmux/internal/sandbox"
)

// CheckBoundary prevents an original login from also being exposed through a
// writable workspace or an ordinary dependency mount, including a peer's.
// Worker-specific credential snapshots are the only intended auth mounts.
func CheckBoundary(cfg managedconfig.Config) error {
	var sources, mounts []string
	inspect := func(a managedconfig.Agent) {
		if a.Auth.File != "" {
			sources = append(sources, a.Auth.File)
		}
		if a.Workdir != "" {
			mounts = append(mounts, a.Workdir)
		}
		mounts = append(mounts, a.ReadOnlyPaths...)
	}
	for _, a := range cfg.Agents {
		inspect(a)
	}
	if cfg.Spawn.Enabled {
		for _, a := range cfg.Spawn.Templates {
			inspect(a)
		}
	}
	for _, source := range sources {
		canonicalSource, err := filepath.EvalSymlinks(source)
		if err != nil {
			continue
		} // Prepare reports missing/unsafe sources without path contents.
		if sandbox.RuntimeExposes(canonicalSource) {
			return errors.New("native auth originals must be outside default shared runtime mounts")
		}
		for _, mount := range mounts {
			canonicalMount, err := filepath.EvalSymlinks(mount)
			if err != nil {
				canonicalMount, _ = filepath.Abs(mount)
			}
			relative, err := filepath.Rel(canonicalMount, canonicalSource)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return errors.New("native auth originals must be outside every agent workspace and declared read-only dependency mount")
			}
		}
	}
	return nil
}
