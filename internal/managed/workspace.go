package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"agentmux/internal/managedconfig"
)

// addWorkspaceAgent is an owner operation, separate from delegated spawning.
// Definitions persist in the registry so supervisor restarts retain them.
func (s *Store) addWorkspaceAgent(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	if !a.owner() {
		return nil, forbidden()
	}
	var in struct {
		ID          string              `json:"id"`
		Config      managedconfig.Agent `json:"config"`
		ExternalRef string              `json:"external_ref"`
		External    bool                `json:"external"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	if !managedconfig.ValidID(in.ID) {
		return nil, fault("invalid", "invalid agent ID")
	}
	if _, err := s.agent(ctx, q, in.ID); err == nil {
		return nil, fault("conflict", "agent ID already exists; choose a new name")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, internal(err)
	}
	if !in.External && in.ExternalRef != "" {
		return nil, fault("invalid", "external_ref is only valid for external sessions")
	}
	if len(in.ExternalRef) > 512 {
		return nil, fault("invalid", "external_ref exceeds the observation ID limit")
	}
	if in.ExternalRef != "" {
		registered, err := list[AgentInfo](ctx, q, "agent", "ORDER BY seq")
		if err != nil {
			return nil, internal(err)
		}
		for _, info := range registered {
			if info.External && info.State == "active" && info.ExternalRef == in.ExternalRef {
				return nil, fault("conflict", "observed terminal is already connected as "+info.ID)
			}
		}
	}
	n, err := count(ctx, q, "agent")
	if err != nil {
		return nil, internal(err)
	}
	if n+len(s.cfg.Agents) >= 200 {
		return nil, fault("limit", "workspace registry limit reached (200 definitions)")
	}
	if in.Config.Interaction != "interactive" {
		return nil, fault("invalid", "workspace additions must be native interactive agents; use configured spawn templates for managed workers")
	}
	if in.Config.Workdir == "" || !filepath.IsAbs(in.Config.Workdir) {
		return nil, fault("invalid", "workspace requires an absolute project directory")
	}
	project, err := filepath.EvalSymlinks(in.Config.Workdir)
	if err != nil {
		return nil, fault("invalid", "project must be visible to the owner broker")
	}
	projectInfo, err := os.Stat(project)
	if err != nil || !projectInfo.IsDir() {
		return nil, fault("invalid", "project must be an existing directory")
	}
	in.Config.Workdir = project
	// Never deliberately mount the private broker/controller tree as a project.
	for _, protected := range []string{s.dir, s.dir + ".bin"} {
		rel, e := filepath.Rel(filepath.Clean(in.Config.Workdir), protected)
		if e != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return nil, fault("invalid", "project must not contain broker or controller state")
		}
	}
	cfg := s.Config()
	cfg.Agents[in.ID] = in.Config
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return nil, invalid(err)
	}
	definition := cfg.Agents[in.ID]
	boundary, err := s.authBoundaryConfig(ctx, q)
	if err != nil {
		return nil, internal(err)
	}
	boundary.Agents[in.ID] = definition
	if err := checkWorkspaceAuthBoundary(boundary); err != nil {
		return nil, invalid(err)
	}
	credential := Credential{AgentID: in.ID, InstanceID: randomID("instance"), Generation: 1, Socket: SocketPath(s.dir), Token: randomID("token")}
	path := CredentialPath(s.dir, in.ID)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, internal(err)
	}
	s.stagedPaths = append(s.stagedPaths, path)
	data, _ := json.Marshal(credential)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return nil, internal(err)
	}
	if closeErr != nil {
		return nil, internal(closeErr)
	}
	if _, err = q.ExecContext(ctx, "INSERT INTO credentials(actor,instance,generation,token_hash) VALUES(?,?,?,?)", in.ID, credential.InstanceID, credential.Generation, hash(credential.Token)); err != nil {
		return nil, internal(err)
	}
	info := AgentInfo{ID: in.ID, Parent: ownerID, OwnerManaged: true, External: in.External, ExternalRef: in.ExternalRef, State: "active", Config: definition, CreatedAt: now()}
	if err = save(ctx, q, "agent", "agent_"+in.ID, a.ID, in.ID, "", info); err != nil {
		return nil, internal(err)
	}
	if err = event(ctx, q, a, "agent.added", in.ID, map[string]any{"harness": definition.Harness, "sandbox": definition.Sandbox}); err != nil {
		return nil, internal(err)
	}
	for k := range info.Config.Env {
		info.Config.Env[k] = "[redacted]"
	}
	return map[string]any{"agent": info}, nil
}

func (s *Store) ownerControllable(ctx context.Context, q queryer, id string) bool {
	info, err := s.agent(ctx, q, id)
	if err != nil || info.State != "active" {
		return false
	}
	_, configured := s.cfg.Agents[id]
	return configured || info.OwnerManaged
}
