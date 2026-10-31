package managedcheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"agentmux/internal/managedconfig"
)

// codexRuntime checks the known standalone package, not arbitrary Codex
// installations. Help/version do not exercise the code execution companion.
// Only public package metadata is read; nothing is mounted automatically.
func (c *checker) codexRuntime(id string, agent managedconfig.Agent, binary, workspace string, env []string) {
	unknown := func() {
		c.add(id, "harness.runtime", StatusWarning, "Codex runtime package layout is unverified; help alone does not establish tool execution readiness")
	}
	resolved := ""
	for _, candidate := range append([]string{binary}, specPrefix(agent)...) {
		if filepath.Base(candidate) != "codex" {
			continue
		}
		if executable, err := executablePath(candidate, workspace, env); err == nil {
			resolved = executable
			break
		}
	}
	if resolved == "" || filepath.Base(filepath.Dir(resolved)) != "bin" {
		unknown()
		return
	}
	root := filepath.Dir(filepath.Dir(resolved))
	manifest := filepath.Join(root, "codex-package.json")
	info, err := os.Lstat(manifest)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		unknown()
		return
	}
	data, err := os.ReadFile(manifest)
	var layout struct {
		LayoutVersion int    `json:"layoutVersion"`
		Version       string `json:"version"`
		Variant       string `json:"variant"`
		Entrypoint    string `json:"entrypoint"`
		ResourcesDir  string `json:"resourcesDir"`
		PathDir       string `json:"pathDir"`
	}
	if err != nil || json.Unmarshal(data, &layout) != nil || layout.LayoutVersion != 1 || !strings.HasPrefix(layout.Version, "0.165.") || layout.Variant != "codex" || layout.Entrypoint != "bin/codex" || layout.ResourcesDir != "codex-resources" || layout.PathDir != "codex-path" {
		unknown()
		return
	}
	for _, name := range []string{"codex-package.json", "bin/codex-code-mode-host", "codex-resources/bwrap", "codex-path/rg"} {
		path := filepath.Join(root, name)
		canonical, err := filepath.EvalSymlinks(path)
		info, statErr := os.Stat(path)
		if err != nil || statErr != nil || !info.Mode().IsRegular() || (name != "codex-package.json" && info.Mode()&0111 == 0) {
			c.add(id, "harness.runtime", StatusError, "known standalone Codex runtime is missing an executable companion or public manifest: "+name)
			return
		}
		visible := func(target string) bool {
			return systemRuntime(target) || within(workspace, target) || mountTargetCovered(target, agent.ReadOnlyPaths)
		}
		if agent.Sandbox == "bubblewrap" && (!visible(path) || !visible(canonical)) {
			c.add(id, "harness.runtime", StatusError, "known standalone Codex companion is not visible through explicit read_only_paths: "+name+"; mount its dedicated package tree without exposing a host home")
			return
		}
	}
	c.add(id, "harness.runtime", StatusOK, "known standalone Codex executable companions and public manifest are present and visible; no model or tool command was executed")
}
