package managedcheck

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentmux/internal/managedconfig"
)

func executablePath(binary, workdir string, env []string) (string, error) {
	if binary == "" || strings.ContainsRune(binary, 0) {
		return "", fmt.Errorf("invalid executable")
	}
	path := binary
	if !strings.ContainsRune(binary, filepath.Separator) {
		search := os.Getenv("PATH")
		for _, entry := range env {
			if strings.HasPrefix(entry, "PATH=") {
				search = strings.TrimPrefix(entry, "PATH=")
			}
		}
		path = ""
		for _, dir := range filepath.SplitList(search) {
			if !filepath.IsAbs(dir) {
				continue
			}
			candidate := filepath.Join(dir, binary)
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
				path = candidate
				break
			}
		}
		if path == "" {
			return "", fmt.Errorf("executable not found")
		}
	} else if !filepath.IsAbs(path) {
		if workdir == "" {
			return "", fmt.Errorf("relative executable requires an absolute working directory")
		}
		path = filepath.Join(workdir, path)
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return "", fmt.Errorf("executable is not an executable regular file")
	}
	return path, nil
}

func (c *checker) workspace(id, path, mode string) bool {
	if path == "" {
		c.add(id, "workspace", StatusWarning, "a private workspace will be assigned during initialization")
		return false
	}
	if !filepath.IsAbs(path) {
		c.add(id, "workspace", StatusError, "workdir must be resolved to an absolute path")
		return false
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		if c.initialized && !strings.HasPrefix(id, "template:") {
			c.add(id, "workspace", StatusError, "configured workspace is missing from initialized state")
		} else {
			c.add(id, "workspace", StatusWarning, "workspace does not exist yet; initialization will create it")
		}
		return false
	}
	if err != nil || !info.IsDir() {
		c.add(id, "workspace", StatusError, "configured workdir is not an accessible directory")
		return false
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		c.add(id, "workspace", StatusError, "workdir cannot be resolved")
		return false
	}
	if mode == "bubblewrap" && (broadPath(canonical) || wholeHome(canonical) || (c.opts.StateDir != "" && within(canonical, c.opts.StateDir))) {
		c.add(id, "workspace", StatusError, "workspace would expose a broad host directory or coordination state")
		return false
	}
	c.add(id, "workspace", StatusOK, "workspace directory is available")
	return true
}

func (c *checker) dependencies(id string, paths []string, workspace, mode string) bool {
	valid := true
	for index, path := range paths {
		name := fmt.Sprintf("dependency.%03d", index+1)
		if !filepath.IsAbs(path) {
			c.add(id, name, StatusError, "read_only_paths must be resolved to absolute paths")
			valid = false
			continue
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			c.add(id, name, StatusError, "declared read-only dependency is unavailable")
			valid = false
			continue
		}
		if mode == "bubblewrap" && (broadPath(canonical) || broadPath(path) || wholeHome(canonical) || wholeHome(path) || (c.opts.StateDir != "" && (within(canonical, c.opts.StateDir) || (within(c.opts.StateDir, canonical) && !within(workspace, canonical))))) {
			c.add(id, name, StatusError, "read-only dependency would expose broad host directories or private coordination state")
			valid = false
			continue
		}
		entries := 0
		err = filepath.WalkDir(canonical, func(_ string, entry os.DirEntry, err error) error {
			entries++
			if err != nil || c.ctx.Err() != nil || entries > 100000 {
				return fmt.Errorf("dependency inspection incomplete")
			}
			if entry.IsDir() || entry.Type().IsRegular() || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			return fmt.Errorf("dependency contains an unsafe inode type")
		})
		if err != nil {
			c.add(id, name, StatusError, "read-only dependency is inaccessible, too large to preflight, or contains a socket/device/pipe")
			valid = false
		} else {
			c.add(id, name, StatusOK, "read-only dependency exists and contains only files, directories, or symlinks")
		}
	}
	return valid
}

func within(parent, child string) bool {
	if parent == "" || child == "" {
		return false
	}
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func wholeHome(path string) bool {
	home, _ := os.UserHomeDir()
	if canonical, err := filepath.EvalSymlinks(home); err == nil {
		home = canonical
	}
	return home != "" && within(path, home)
}

func broadPath(path string) bool {
	switch filepath.Clean(path) {
	case "/", "/home", "/root", "/tmp", "/etc", "/usr", "/var", "/run", "/proc", "/dev", "/sys", "/bin", "/lib", "/lib64", "/sbin":
		return true
	}
	return false
}

func envSlice(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

// Node-based CLIs need more than their executable script. Check the known
// dependency closure before state exists, rather than letting a template pass
// merely because its host CLI can print help.
func (c *checker) scriptClosure(id string, agent managedconfig.Agent, binary, workspace string, env []string) {
	script := ""
	file, err := os.Open(binary)
	if err == nil {
		line, _ := bufio.NewReader(io.LimitReader(file, 256)).ReadString('\n')
		_ = file.Close()
		if strings.HasPrefix(line, "#!") && strings.Contains(line, "node") {
			script = binary
			candidate := searchExecutable("node", env)
			if candidate == "" {
				c.add(id, "dependency.closure", StatusError, "Node-based CLI requires an available node runtime")
				return
			}
			if !systemRuntime(candidate) && !mountTargetCovered(candidate, agent.ReadOnlyPaths) {
				c.add(id, "dependency.closure", StatusError, "Node-based CLI needs its PATH-visible node executable in read_only_paths")
				return
			}
		}
	}
	if script == "" && strings.HasPrefix(filepath.Base(binary), "node") {
		for _, arg := range specPrefix(agent) {
			if strings.HasSuffix(arg, ".js") || strings.HasSuffix(arg, ".mjs") || strings.HasSuffix(arg, ".cjs") {
				script = arg
				if !filepath.IsAbs(script) {
					script = filepath.Join(workspace, script)
				}
				if canonical, err := filepath.EvalSymlinks(script); err == nil {
					script = canonical
				}
				break
			}
		}
	}
	if script == "" {
		c.add(id, "dependency.closure", StatusOK, "no additional Node script dependency mounts were identified")
		return
	}
	packageDir := filepath.Dir(script)
	for depth := 0; depth < 8 && packageDir != filepath.Dir(packageDir); depth++ {
		if regularFile(filepath.Join(packageDir, "package.json")) {
			if !systemRuntime(packageDir) && !within(workspace, packageDir) && !mountTargetCovered(packageDir, agent.ReadOnlyPaths) {
				c.add(id, "dependency.closure", StatusError, "Node-based CLI requires its complete package directory in read_only_paths")
				return
			}
			c.add(id, "dependency.closure", StatusOK, "Node runtime and CLI package paths are available through declared mounts")
			return
		}
		packageDir = filepath.Dir(packageDir)
	}
	c.add(id, "dependency.closure", StatusWarning, "script package root could not be identified; exact sandbox help check remains necessary")
}

func mountTargetCovered(path string, paths []string) bool {
	for _, target := range paths {
		if filepath.IsAbs(target) && within(filepath.Clean(target), path) {
			return true
		}
	}
	return false
}

func systemRuntime(path string) bool {
	for _, root := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64"} {
		if within(root, path) {
			return true
		}
	}
	return false
}

func searchExecutable(name string, env []string) string {
	search := os.Getenv("PATH")
	for _, entry := range env {
		if strings.HasPrefix(entry, "PATH=") {
			search = strings.TrimPrefix(entry, "PATH=")
		}
	}
	for _, dir := range filepath.SplitList(search) {
		if !filepath.IsAbs(dir) {
			continue
		}
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
			return candidate
		}
	}
	return ""
}
