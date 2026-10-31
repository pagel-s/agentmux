package managedsetup

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Discover resolves executable and public code dependencies. It never mounts
// a provider's home/config directory or reads credential files.
func Discover(name, binary string) ([]string, []string, error) {
	if binary == "" {
		switch name {
		case "claude", "codex", "pi":
			binary = name
		case "antigravity":
			binary = "agy"
		default:
			return nil, nil, errors.New("setup supports claude, codex, pi, antigravity")
		}
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return nil, nil, errors.New("selected harness executable is not installed")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, nil, errors.New("harness executable cannot be resolved")
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return nil, nil, errors.New("harness executable is not an executable regular file")
	}
	deps := map[string]bool{}
	add := func(path string) {
		if !systemPath(path) {
			deps[path] = true
		}
	}
	add(real)
	command := []string{real}
	if name == "codex" {
		root := filepath.Dir(filepath.Dir(real))
		manifest := filepath.Join(root, "codex-package.json")
		var metadata struct {
			LayoutVersion int    `json:"layoutVersion"`
			Entrypoint    string `json:"entrypoint"`
			ResourcesDir  string `json:"resourcesDir"`
			PathDir       string `json:"pathDir"`
		}
		if err := readPublicJSON(manifest, &metadata); err == nil && metadata.LayoutVersion == 1 && metadata.Entrypoint == "bin/codex" && metadata.ResourcesDir == "codex-resources" && metadata.PathDir == "codex-path" {
			for _, relative := range []string{"codex-package.json", "bin/codex-code-mode-host", "codex-resources/bwrap", "codex-path/rg"} {
				dependency := filepath.Join(root, relative)
				if info, err := os.Stat(dependency); err != nil || !info.Mode().IsRegular() {
					return nil, nil, errors.New("known Codex standalone code companion is missing")
				}
				add(dependency)
			}
		}
	}
	if name == "pi" || name == "codex" || name == "claude" {
		f, err := os.Open(real)
		if err != nil {
			return nil, nil, err
		}
		head := make([]byte, 256)
		n, _ := f.Read(head)
		f.Close()
		if (strings.HasPrefix(string(head[:n]), "#!") && strings.Contains(strings.SplitN(string(head[:n]), "\n", 2)[0], "node")) || strings.HasSuffix(real, ".js") {
			node, err := exec.LookPath("node")
			if err != nil {
				return nil, nil, errors.New("JavaScript harness entry requires an installed Node runtime")
			}
			node, err = filepath.EvalSymlinks(node)
			if err != nil {
				return nil, nil, err
			}
			add(node)
			command = []string{node, real}
			found := false
			parent := filepath.Dir(real)
			for range 8 {
				var metadata struct {
					Name                 string            `json:"name"`
					OptionalDependencies map[string]string `json:"optionalDependencies"`
				}
				if readPublicJSON(filepath.Join(parent, "package.json"), &metadata) == nil {
					recognized := (name == "pi" && strings.HasSuffix(metadata.Name, "/pi-coding-agent")) || (name == "codex" && metadata.Name == "@openai/codex") || (name == "claude" && metadata.Name == "@anthropic-ai/claude-code")
					if !recognized {
						return nil, nil, errors.New("harness package metadata is not recognized")
					}
					add(parent)
					global := filepath.Dir(parent)
					if strings.HasPrefix(metadata.Name, "@") {
						global = filepath.Dir(global)
					}
					for dependency := range metadata.OptionalDependencies {
						if strings.Contains(dependency, "..") || strings.HasPrefix(dependency, "/") {
							continue
						}
						dep := filepath.Join(global, filepath.FromSlash(dependency))
						if info, err := os.Stat(dep); err == nil && info.IsDir() {
							add(dep)
						}
					}
					found = true
					break
				}
				next := filepath.Dir(parent)
				if next == parent {
					break
				}
				parent = next
			}
			if !found {
				return nil, nil, errors.New("harness package code directory could not be located; use a supported installed package")
			}
		}
	}
	paths := make([]string, 0, len(deps))
	for path := range deps {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return command, paths, nil
}
