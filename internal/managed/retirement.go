package managed

import (
	"context"
	"encoding/json"
	"path/filepath"
)

// Retiring preserves the stable identity and every history reference. Keeping
// the original record is also the permanent ID tombstone used by agent.add.
func (s *Store) retireAgent(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	if !a.owner() {
		return nil, forbidden()
	}
	var in struct {
		Agent string `json:"agent"`
		ID    string `json:"id"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	if in.Agent == "" {
		in.Agent = in.ID
	}
	if in.Agent == "" || in.ID != "" && in.ID != in.Agent {
		return nil, fault("invalid", "select one agent")
	}
	info, err := s.agent(ctx, q, in.Agent)
	if err != nil {
		return nil, fault("not_found", "agent not found")
	}
	if info.State == "retired" {
		return map[string]any{"id": info.ID, "state": info.State, "retired_at": info.RetiredAt}, nil
	}
	if info.State != "revoked" {
		return nil, fault("conflict", "stop and revoke the agent before retiring it")
	}
	// Revocation is not process completion. A worker/native terminal still owns
	// this lock until it exits, so do not claim it has stopped while it is live.
	unlock, err := acquireLock(filepath.Join(s.dir, "worker-"+info.ID+".lock"))
	if err != nil {
		return nil, fault("conflict", "agent process is still running; wait for it to stop before retiring")
	}
	defer unlock()
	if _, err := q.ExecContext(ctx, "DELETE FROM credentials WHERE actor=?", info.ID); err != nil {
		return nil, internal(err)
	}
	info.State, info.RetiredAt = "retired", now()
	if err := save(ctx, q, "agent", "agent_"+info.ID, info.Parent, info.ID, "", info); err != nil {
		return nil, internal(err)
	}
	if p := removeWorkspaceGroupMembers(ctx, q, a, map[string]bool{info.ID: true}); p != nil {
		return nil, p
	}
	if err := advanceBoundaryRevision(ctx, q); err != nil {
		return nil, internal(err)
	}
	if err := event(ctx, q, a, "agent.retired", info.ID, map[string]any{"retired_at": info.RetiredAt}); err != nil {
		return nil, internal(err)
	}
	return map[string]any{"id": info.ID, "state": info.State, "retired_at": info.RetiredAt}, nil
}

func (s *Store) unarchivedDefinitionCount(ctx context.Context, q queryer) (int, error) {
	ids := map[string]bool{}
	for id := range s.cfg.Agents {
		ids[id] = true
	}
	entries, err := list[AgentInfo](ctx, q, "agent", "")
	if err != nil {
		return 0, err
	}
	for _, info := range entries {
		ids[info.ID] = info.State != "retired"
	}
	n := 0
	for _, included := range ids {
		if included {
			n++
		}
	}
	return n, nil
}
