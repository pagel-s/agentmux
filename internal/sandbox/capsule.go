package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type CapsuleInput struct{ Source, Target string }
type CapsuleOptions struct {
	Manifest, Socket, Executable         string
	Inputs                               []CapsuleInput
	WorkspaceBytes, HomeBytes, TempBytes int64
}

func capsuleMounts(c *CapsuleOptions, workspace, state string) ([]string, string, error) {
	if c == nil {
		return nil, "", nil
	}
	if len(c.Inputs) < 1 || len(c.Inputs) > 2 {
		return nil, "", fmt.Errorf("capsule requires workspace and optional native history seed")
	}
	if c.Executable == "" || filepath.Dir(workspace) != filepath.Join(state, "workspaces") {
		return nil, "", fmt.Errorf("capsule requires trusted shim and dedicated state-owned workspace")
	}
	for _, n := range []int64{c.WorkspaceBytes, c.HomeBytes, c.TempBytes} {
		if n < 1<<20 || n > 1<<40 {
			return nil, "", fmt.Errorf("invalid capsule tmpfs size")
		}
	}
	agent := filepath.Base(workspace)
	root := filepath.Join(state, "capsules", agent)
	for _, p := range []string{c.Manifest, c.Socket} {
		canonical, err := realPath(p, "capsule resource", false)
		if err != nil || canonical != p || !containsPath(root, p) {
			return nil, "", fmt.Errorf("capsule resource must be canonical private state")
		}
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm()&0077 != 0 {
			return nil, "", fmt.Errorf("capsule resource must be private")
		}
	}
	manifestInfo, _ := os.Stat(c.Manifest)
	socketInfo, _ := os.Stat(c.Socket)
	if !manifestInfo.Mode().IsRegular() || socketInfo.Mode()&os.ModeSocket == 0 {
		return nil, "", fmt.Errorf("capsule requires manifest file and Unix collector socket")
	}
	args := []string{"--dir", "/run/agentmux/input", "--ro-bind", c.Manifest, "/run/agentmux/capsule.json", "--ro-bind", c.Socket, "/run/agentmux/checkpoint.sock"}
	for i, input := range c.Inputs {
		source, err := realPath(input.Source, "capsule input", true)
		if err != nil || source != input.Source || input.Target != "/run/agentmux/input/"+strconv.Itoa(i) {
			return nil, "", fmt.Errorf("invalid capsule input")
		}
		if i == 0 && source != workspace {
			return nil, "", fmt.Errorf("capsule first input must be workspace")
		}
		if i == 1 {
			history := filepath.Join(state, "sessions", agent)
			rel, e := filepath.Rel(history, source)
			if e != nil || rel == "." || strings.Contains(rel, string(filepath.Separator)) || !strings.HasPrefix(rel, "g-") {
				return nil, "", fmt.Errorf("capsule history seed must be the same agent's dedicated state history")
			}
		}
		args = append(args, "--ro-bind", source, input.Target)
	}
	return args, c.Executable, nil
}
