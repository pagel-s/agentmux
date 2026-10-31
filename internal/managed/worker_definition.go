package managed

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"agentmux/internal/managedconfig"
)

// WorkerDefinition contains one worker's process definition, never the owner
// token, peer credentials, SQLite database or peers' native login sources.
// It is an owner-controlled deployment artifact, not an agent capability.
type WorkerDefinition struct {
	Version      int                  `json:"version"`
	AgentID      string               `json:"agent_id"`
	Config       managedconfig.Config `json:"config"`
	Deadline     time.Time            `json:"deadline"`
	SpawnEnabled bool                 `json:"spawn_enabled"`
	TeamsEnabled bool                 `json:"teams_enabled"`
}

// RemoteLayout changes only host-specific paths and command argv. Policy,
// model, role and broker admission remain those selected by the owner.
type RemoteLayout struct {
	Workdir       string   `json:"workdir"`
	Command       []string `json:"command,omitempty"`
	ReadOnlyPaths []string `json:"read_only_paths,omitempty"`
	AuthFile      string   `json:"auth_file,omitempty"`
}

func (s *Store) WorkerDefinition(id string, layout RemoteLayout) (WorkerDefinition, error) {
	a, err := s.Agent(id)
	if err != nil {
		return WorkerDefinition{}, err
	}
	if a.Trace.Enabled {
		return WorkerDefinition{}, errors.New("remote trace anchoring requires a central collector; disable tracing explicitly before remote deployment")
	}
	if a.Sandbox != "bubblewrap" {
		return WorkerDefinition{}, errors.New("remote workers require bubblewrap")
	}
	if !filepath.IsAbs(layout.Workdir) {
		return WorkerDefinition{}, errors.New("remote layout requires an absolute workspace")
	}
	a.Workdir = layout.Workdir
	if len(layout.Command) > 0 {
		if a.Harness != "command" {
			return WorkerDefinition{}, errors.New("command override requires command harness")
		}
		a.Command = append([]string(nil), layout.Command...)
	}
	a.ReadOnlyPaths = append([]string(nil), layout.ReadOnlyPaths...)
	if a.Auth.File != "" && layout.AuthFile == "" {
		return WorkerDefinition{}, errors.New("native authentication must be provisioned separately on the remote host")
	}
	a.Auth.File = layout.AuthFile
	cfg := s.Config().Normalized()
	out := managedconfig.Config{Version: 2, Mode: "managed", Session: cfg.Session, Agents: map[string]managedconfig.Agent{id: a}, Connections: map[string]managedconfig.Connection{}, Boards: map[string]managedconfig.Access{}, Limits: cfg.Limits, Policy: managedconfig.Policy{Default: "deny"}, Budget: cfg.Budget, MemoryPolicy: cfg.MemoryPolicy}
	if a.Connection != "" {
		out.Connections[a.Connection] = cfg.Connections[a.Connection]
	}
	boards, memory, err := s.remoteNamespaces(id)
	if err != nil {
		return WorkerDefinition{}, err
	}
	out.Boards, out.Memory = boards, memory
	out = out.Normalized()
	if err := out.Validate(); err != nil {
		return WorkerDefinition{}, err
	}
	deadline, err := s.RuntimeDeadline()
	if err != nil {
		return WorkerDefinition{}, err
	}
	if deadline.IsZero() || !deadline.After(time.Now()) {
		return WorkerDefinition{}, errors.New("remote deployment requires a started, unexpired broker runtime")
	}
	return WorkerDefinition{Version: 1, AgentID: id, Config: out, Deadline: deadline, SpawnEnabled: cfg.Spawn.Enabled, TeamsEnabled: cfg.Teams.Enabled}, nil
}

func (s *Store) remoteNamespaces(id string) (map[string]managedconfig.Access, map[string]managedconfig.Access, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := context.Background()
	info, err := s.agent(ctx, s.db, id)
	if err != nil || info.State != "active" {
		return nil, nil, errors.New("agent is not active")
	}
	boardNames, memoryNames := map[string]bool{}, map[string]bool{}
	for name := range s.cfg.Boards {
		boardNames[name] = true
	}
	for name := range s.cfg.Memory {
		memoryNames[name] = true
	}
	teams, err := list[Team](ctx, s.db, "team", "AND json_extract(data,'$.state')='active'")
	if err != nil {
		return nil, nil, err
	}
	for _, team := range teams {
		boardNames[team.Board] = true
		memoryNames[team.Memory] = true
	}
	selectAccess := func(names map[string]bool, memory bool) map[string]managedconfig.Access {
		out := map[string]managedconfig.Access{}
		for name := range names {
			r, w := s.namespace(ctx, s.db, id, name, memory, false), s.namespace(ctx, s.db, id, name, memory, true)
			if r || w {
				a := managedconfig.Access{}
				if r {
					a.Readers = []string{id}
				}
				if w {
					a.Writers = []string{id}
				}
				out[name] = a
			}
		}
		return out
	}
	return selectAccess(boardNames, false), selectAccess(memoryNames, true), nil
}

func ReadWorkerDefinition(path string) (WorkerDefinition, error) {
	var d WorkerDefinition
	info, err := os.Lstat(path)
	if err != nil {
		return d, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return d, errors.New("worker definition must be a private regular file no larger than 1 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return d, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, err
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return d, errors.New("worker definition must contain one object")
	}
	if d.Version != 1 || !managedconfig.ValidID(d.AgentID) || len(d.Config.Agents) != 1 || d.Deadline.IsZero() {
		return d, errors.New("invalid worker definition")
	}
	a, ok := d.Config.Agents[d.AgentID]
	if !ok || a.Sandbox != "bubblewrap" || a.Trace.Enabled || d.Config.Spawn.Enabled || d.Config.Teams.Enabled {
		return d, errors.New("unsupported remote worker definition")
	}
	if err := d.Config.Validate(); err != nil {
		return d, err
	}
	return d, nil
}

func loadWorkerDefinition(opts WorkerOptions) (managedconfig.Config, managedconfig.Agent, string, time.Time, error) {
	if opts.DefinitionFile != "" {
		d, err := ReadWorkerDefinition(opts.DefinitionFile)
		if err != nil {
			return managedconfig.Config{}, managedconfig.Agent{}, "", time.Time{}, err
		}
		if d.AgentID != opts.AgentID {
			return managedconfig.Config{}, managedconfig.Agent{}, "", time.Time{}, errors.New("worker definition identity mismatch")
		}
		dir, err := filepath.Abs(opts.StateDir)
		if err != nil {
			return managedconfig.Config{}, managedconfig.Agent{}, "", time.Time{}, err
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return managedconfig.Config{}, managedconfig.Agent{}, "", time.Time{}, errors.New("remote state must be an existing private directory")
		}
		cfg := d.Config.Normalized()
		a := cfg.Agents[opts.AgentID]
		cfg.Spawn.Enabled = d.SpawnEnabled
		cfg.Teams.Enabled = d.TeamsEnabled
		return cfg, a, dir, d.Deadline, nil
	}
	s, err := Open(opts.StateDir)
	if err != nil {
		return managedconfig.Config{}, managedconfig.Agent{}, "", time.Time{}, err
	}
	defer s.Close()
	cfg, dir := s.Config().Normalized(), s.dir
	a, err := s.Agent(opts.AgentID)
	if err != nil {
		return cfg, a, dir, time.Time{}, err
	}
	deadline, err := s.RuntimeDeadline()
	return cfg, a, dir, deadline, err
}
