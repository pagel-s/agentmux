// Package managedsetup builds explicit managed configurations without changing
// provider settings, discovering credentials, or launching a model.
package managedsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"agentmux/internal/managedconfig"
	"agentmux/internal/modelmeta"
	"gopkg.in/yaml.v3"
)

type Spec struct {
	Session       string  `json:"session"`
	WorkspaceRoot string  `json:"workspace_root"`
	Sandbox       string  `json:"sandbox"`
	BrokerSandbox string  `json:"broker_sandbox"`
	SharedBoard   bool    `json:"shared_board"`
	InitGit       bool    `json:"init_git"`
	Agents        []Agent `json:"agents"`
}

// Auth is an explicit choice: none, native-local (trusted host login), file
// (one private native credential file), or api-env (named environment only).
type Agent struct {
	Interaction   string               `json:"interaction,omitempty"`
	NativeOnly    bool                 `json:"native_only,omitempty"`
	Workdir       string               `json:"workdir,omitempty"`
	ID            string               `json:"id"`
	Role          string               `json:"role"`
	Harness       string               `json:"harness"`
	Model         string               `json:"model"`
	Binary        string               `json:"binary"`
	Auth          string               `json:"auth"`
	AuthFile      string               `json:"auth_file"`
	AuthProvider  string               `json:"auth_provider"`
	APIKeyEnv     string               `json:"api_key_env"`
	BaseURL       string               `json:"base_url"`
	Protocol      string               `json:"protocol"`
	Conversation  string               `json:"conversation"`
	CodingTools   bool                 `json:"coding_tools"`
	CodexSandbox  string               `json:"codex_sandbox"`
	ModelMetadata modelmeta.Metadata   `json:"model_metadata,omitempty"`
	Network       string               `json:"network,omitempty"`
	Egress        managedconfig.Egress `json:"egress,omitempty"`
}

type Plan struct {
	Config        managedconfig.Config `json:"-"`
	BrokerSandbox string               `json:"broker_sandbox"`
	Workspaces    []string             `json:"workspaces"`
	Warnings      []string             `json:"warnings"`
}

func Decode(r io.Reader) (Spec, error) {
	dec := json.NewDecoder(io.LimitReader(r, 1024*1024+1))
	dec.DisallowUnknownFields()
	var spec Spec
	if err := dec.Decode(&spec); err != nil {
		return spec, errors.New("setup spec must be one valid JSON object with recognized fields")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return spec, errors.New("setup spec must contain exactly one JSON object")
	}
	return spec, nil
}

// Build only inspects public executable/package metadata. No credential file
// is opened, directory created, or subprocess launched.
func Build(spec Spec) (Plan, error) {
	p := Plan{BrokerSandbox: spec.BrokerSandbox, Warnings: []string{}}
	if p.BrokerSandbox == "" {
		p.BrokerSandbox = "bubblewrap"
	}
	if p.BrokerSandbox != "local" && p.BrokerSandbox != "bubblewrap" {
		return p, errors.New("broker_sandbox must be local or bubblewrap")
	}
	if spec.Sandbox == "" {
		spec.Sandbox = "bubblewrap"
	}
	if !managedconfig.ValidID(spec.Session) {
		return p, errors.New("setup requires a valid session name")
	}
	if len(spec.Agents) == 0 || len(spec.Agents) > 32 {
		return p, errors.New("setup requires between one and 32 agents")
	}
	root, err := filepath.Abs(spec.WorkspaceRoot)
	if err != nil || spec.WorkspaceRoot == "" || root == "/" {
		return p, errors.New("setup requires a dedicated workspace_root")
	}
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: spec.Session, Agents: map[string]managedconfig.Agent{}, Connections: map[string]managedconfig.Connection{}, Policy: managedconfig.Policy{Default: "deny"}}
	ids := []string{}
	for _, a := range spec.Agents {
		if !managedconfig.ValidID(a.ID) {
			return p, errors.New("setup requires valid distinct agent IDs")
		}
		if _, exists := cfg.Agents[a.ID]; exists {
			return p, errors.New("setup requires valid distinct agent IDs")
		}
		if a.Harness == "agy" {
			a.Harness = "antigravity"
		}
		if a.Harness == "claude-code" {
			a.Harness = "claude"
		}
		if a.Harness == "antigravity-bridge" {
			if a.BaseURL == "" || a.Auth != "api-env" || a.APIKeyEnv == "" {
				return p, errors.New("antigravity-bridge requires an owner-started bridge base_url and explicit api-env authentication")
			}
			a.Harness = "pi"
			p.Warnings = append(p.Warnings, "agent "+a.ID+": native Antigravity inference uses a separately owned trusted-local bridge; Pi owns isolated tools and private persistent history")
		}
		if !a.ModelMetadata.Empty() && (a.Harness != "pi" || a.BaseURL == "") {
			return p, errors.New("model_metadata requires an explicit Pi endpoint")
		}
		if a.Harness == "codex" && !spec.InitGit {
			p.Warnings = append(p.Warnings, "agent "+a.ID+": Codex requires an approved Git workspace; use --init-git for newly created directories or initialize/review it manually")
		}
		discover := Discover
		if spec.Sandbox == "local" && a.Interaction == "interactive" {
			discover = discoverNativeLauncher
		}
		command, deps, err := discover(a.Harness, a.Binary)
		if err != nil {
			return p, fmt.Errorf("agent %s: %w", a.ID, err)
		}
		agent := managedconfig.Agent{Harness: a.Harness, Role: a.Role, Model: a.Model, Sandbox: spec.Sandbox, Network: "host", Command: command, ReadOnlyPaths: deps, Workdir: filepath.Join(root, a.ID), Conversation: a.Conversation, CodexSandbox: a.CodexSandbox}
		agent.Interaction = a.Interaction
		agent.NativeOnly = a.NativeOnly
		if a.Workdir != "" {
			agent.Workdir, err = filepath.Abs(a.Workdir)
			if err != nil {
				return p, err
			}
		}
		if a.Network != "" {
			agent.Network = a.Network
		}
		agent.Egress = a.Egress
		if a.CodingTools && a.Harness == "claude" {
			agent.Command = append(agent.Command, "--allowedTools", "Read,Write,Edit,Bash")
		}
		if a.Harness == "pi" {
			if a.Interaction != "interactive" {
				agent.Command = append(agent.Command, "--offline", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-context-files", "--no-approve")
			}
			if a.BaseURL == "" && a.AuthProvider != "" {
				agent.Command = append(agent.Command, "--provider", a.AuthProvider)
			}
		}
		if a.Auth == "" {
			return p, fmt.Errorf("agent %s: auth choice must be explicit (none, native-local, file, api-env)", a.ID)
		}
		if a.Auth != "file" && (a.AuthFile != "" || a.AuthProvider != "") {
			return p, errors.New("auth_file and auth_provider require auth=file")
		}
		if a.Auth != "api-env" && a.APIKeyEnv != "" {
			return p, errors.New("api_key_env requires auth=api-env")
		}
		switch a.Auth {
		case "none":
			if spec.Sandbox == "local" && a.BaseURL == "" {
				return p, fmt.Errorf("agent %s: local native harnesses may load host login; explicitly choose native-local", a.ID)
			}
		case "native-local":
			if spec.Sandbox != "local" || a.BaseURL != "" {
				return p, errors.New("native-local requires trusted local mode and no custom endpoint")
			}
			p.Warnings = append(p.Warnings, "agent "+a.ID+": trusted local mode exposes the selected harness to its host login and workspace authority")
		case "file":
			if spec.Sandbox != "bubblewrap" || a.AuthFile == "" {
				return p, errors.New("file auth requires bubblewrap and an explicit private file")
			}
			path, err := filepath.Abs(a.AuthFile)
			if err != nil {
				return p, errors.New("auth file path is invalid")
			}
			agent.Auth = managedconfig.NativeAuth{File: path, Provider: a.AuthProvider}
		case "api-env":
			if a.APIKeyEnv == "" {
				return p, errors.New("api-env requires a named environment variable")
			}
		default:
			return p, errors.New("unsupported setup auth choice")
		}
		if a.BaseURL != "" || a.APIKeyEnv != "" {
			protocol := a.Protocol
			if protocol == "" {
				protocol = "openai-compatible"
				if a.Harness == "claude" {
					protocol = "anthropic"
				}
				if a.Harness == "codex" {
					protocol = "openai-responses"
				}
			}
			key := "endpoint-" + a.ID
			cfg.Connections[key] = managedconfig.Connection{Protocol: protocol, BaseURL: a.BaseURL, APIKeyEnv: a.APIKeyEnv, ModelMetadata: a.ModelMetadata}
			agent.Connection = key
		}
		if a.Harness == "antigravity" && spec.Sandbox == "bubblewrap" {
			return p, errors.New("Antigravity isolated native authentication is not verified; choose explicit trusted local mode or a supported isolated harness")
		}
		cfg.Agents[a.ID] = agent
		ids = append(ids, a.ID)
		p.Workspaces = append(p.Workspaces, agent.Workdir)
	}
	sort.Strings(ids)
	sort.Strings(p.Workspaces)
	if spec.SharedBoard {
		cfg.Boards = map[string]managedconfig.Access{"team": {Readers: ids, Writers: ids}}
		cfg.Memory = map[string]managedconfig.Access{"project": {Readers: ids, Writers: ids}}
	}
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return p, err
	}
	p.Config = cfg
	return p, nil
}

// Write creates a new 0600 YAML file exclusively. Existing files and provider
// settings are never overwritten. Parent must be an owner-selected directory.
func Write(path string, cfg managedconfig.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("setup output already exists or cannot be created")
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

// InitWorkspaces creates only absent dedicated agent directories. Git init is
// optional and applies only to a directory created by this call. Existing
// directories/repositories are preserved and reported for manual inspection.
func InitWorkspaces(ctx context.Context, p Plan, initGit bool) ([]string, error) {
	var notes []string
	for _, path := range p.Workspaces {
		if info, err := os.Lstat(path); err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return notes, errors.New("workspace must be a real directory")
			}
			notes = append(notes, "existing workspace preserved: "+path)
			continue
		} else if !os.IsNotExist(err) {
			return notes, err
		}
		if err := privateParents(filepath.Dir(path)); err != nil {
			return notes, err
		}
		if err := os.Mkdir(path, 0700); err != nil {
			return notes, err
		}
		if initGit {
			cmd := exec.CommandContext(ctx, "git", "-C", path, "-c", "core.hooksPath=/dev/null", "init", "--quiet")
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + path, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TEMPLATE_DIR="}
			if err := cmd.Run(); err != nil {
				return notes, errors.New("new workspace created but Git init failed; initialize it manually")
			}
		}
	}
	return notes, nil
}

func privateParents(path string) error {
	if path == "/" || path == "." {
		return nil
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("workspace ancestors must be real directories")
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || canonical != filepath.Clean(path) {
			return errors.New("workspace ancestors must not contain symlinks")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := privateParents(filepath.Dir(path)); err != nil {
		return err
	}
	return os.Mkdir(path, 0700)
}

func readPublicJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(io.LimitReader(f, 256*1024)).Decode(value)
}

func systemPath(path string) bool {
	for _, prefix := range []string{"/usr/", "/bin/", "/lib/", "/lib64/", "/sbin/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
