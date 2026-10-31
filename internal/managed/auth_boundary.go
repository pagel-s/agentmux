package managed

import (
	"context"
	"errors"
	"path/filepath"

	"agentmux/internal/agentauth"
	"agentmux/internal/managedconfig"
)

// authBoundaryConfig includes persisted additions and delegated workers, whose
// mounts are absent from the immutable launch configuration.
func (s *Store) authBoundaryConfig(ctx context.Context, q queryer) (managedconfig.Config, error) {
	cfg := s.Config().Normalized()
	agents, err := list[AgentInfo](ctx, q, "agent", "ORDER BY seq")
	if err != nil {
		return cfg, err
	}
	for _, info := range agents {
		if info.State == "active" {
			cfg.Agents[info.ID] = copyAgent(info.Config)
		}
	}
	return cfg, nil
}

// Admission must not silently skip authentication sources hidden from the
// broker's own namespace. The owner can instead use a broker with host access.
func checkWorkspaceAuthBoundary(cfg managedconfig.Config) error {
	hasAuth := false
	for _, a := range cfg.Agents {
		hasAuth = hasAuth || a.Auth.File != ""
	}
	if cfg.Spawn.Enabled {
		for _, a := range cfg.Spawn.Templates {
			hasAuth = hasAuth || a.Auth.File != ""
		}
	}
	if !hasAuth {
		return nil
	}
	inspect := func(a managedconfig.Agent) error {
		for _, path := range append([]string{a.Auth.File, a.Workdir}, a.ReadOnlyPaths...) {
			if path == "" {
				continue
			}
			if _, err := filepath.EvalSymlinks(path); err != nil {
				return errors.New("cannot establish native authentication boundaries; authentication and mount paths must be visible to the owner broker")
			}
		}
		return nil
	}
	for _, a := range cfg.Agents {
		if err := inspect(a); err != nil {
			return err
		}
	}
	if cfg.Spawn.Enabled {
		for _, a := range cfg.Spawn.Templates {
			if err := inspect(a); err != nil {
				return err
			}
		}
	}
	return agentauth.CheckBoundary(cfg)
}

func workerAuthBoundaryConfig(ctx context.Context, opts WorkerOptions, cfg managedconfig.Config, agent managedconfig.Agent, dir string) (managedconfig.Config, error) {
	if opts.DefinitionFile != "" {
		// Remote definitions contain only paths provisioned on that remote host.
		cfg = cfg.Normalized()
		cfg.Agents[opts.AgentID] = agent
		return cfg, nil
	}
	s, err := Open(dir)
	if err != nil {
		return cfg, err
	}
	defer s.Close()
	return s.authBoundaryConfig(ctx, s.db)
}
