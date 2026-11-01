package managed

import (
	"context"
	"database/sql"
	"errors"
)

// Legacy controls cannot distinguish generated pair allows from deliberate
// direct owner grants. Only an explicit owner policy may classify those allows;
// denies and unrelated controls are never inferred or discarded.
func reviewLegacyWorkspaceGroup(ctx context.Context, q queryer, c *ControlState, ids []string, policy string) *Fault {
	var group WorkspaceGroup
	err := get(ctx, q, "workspace_group", workspaceGroupKey(ids), &group)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return internal(err)
	}
	if group.ContributionsVersion >= 1 {
		return nil
	}
	if policy == "" {
		return fault("conflict", "legacy collaboration permissions need explicit owner review; no changes applied. Choose legacy_policy preserve to retain historical direct overrides, or group to remove untagged pair allows while preserving all denies. CLI: agentmux collaborate AGENT AGENT --legacy-policy preserve|group; inspect control.get first")
	}
	if policy == "group" {
		edges := []EdgeControl{}
		for _, edge := range c.Edges {
			if edge.Source == "" && edge.Allow && edge.From != edge.To && contains(group.Agents, edge.From) && contains(group.Agents, edge.To) {
				continue
			}
			edges = append(edges, edge)
		}
		c.Edges = edges
	}
	return nil
}
