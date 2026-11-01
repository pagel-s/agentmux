package managed

import "context"

func (s *Store) boundaryEdgeReason(ctx context.Context, q queryer, from, action, to string) string {
	c, err := controls(ctx, q)
	if err != nil {
		return "permission state unavailable"
	}
	for _, edge := range c.Edges {
		if edge.From == from && edge.To == to && edge.Action == action && edge.Source == "isolation" {
			return "isolation overlay; reconnect selected sessions to restore prior intent"
		}
	}
	for _, edge := range c.Edges {
		if edge.From == from && edge.To == to && edge.Action == action {
			if edge.Allow {
				return "explicit owner allow"
			}
			return "explicit owner deny"
		}
	}
	if independent(ctx, q, from) || independent(ctx, q, to) {
		return "session is independent"
	}
	groups, err := workspaceGroups(ctx, q)
	if err != nil {
		return "group state unavailable"
	}
	for _, group := range groups {
		if group.ContributionsVersion >= 1 && group.State == "active" && contains(group.Members, from) && contains(group.Members, to) && (action != "instruct" || group.Commands) {
			return "collaboration group " + group.ID
		}
	}
	if s.can(ctx, q, from, action, to) {
		return "configuration or parent delegation"
	}
	return "not granted by configuration, delegation or an active collaboration group"
}

// boundaryScope reports persisted intent separately from current authority. A
// historical allow never implies membership or lineage requirements were met.
func (s *Store) boundaryScope(ctx context.Context, q queryer, id, name string, memory bool, source string) BoundaryScope {
	v := BoundaryScope{Name: name, Source: source, Read: s.namespace(ctx, q, id, name, memory, false), Write: s.namespace(ctx, q, id, name, memory, true)}
	c, err := controls(ctx, q)
	if err != nil {
		v.Reason = "permission state unavailable"
		return v
	}
	kind := "board"
	if memory {
		kind = "memory"
	}
	for _, stored := range c.Scopes {
		if stored.Agent != id || stored.Namespace != name || stored.Kind != kind {
			continue
		}
		if stored.Source == "isolation" {
			v.Suspended = true
			continue
		}
		read, write := stored.Read, stored.Write
		v.StoredRead = &read
		v.StoredWrite = &write
	}
	if group, err := workspaceGroupNamespace(ctx, q, name, memory); err != nil {
		v.Reason = "group state unavailable"
		return v
	} else if group != nil {
		if group.State != "active" {
			v.Reason = "collaboration group inactive"
			return v
		}
		if !contains(group.Members, id) {
			v.Reason = "session is not a current collaboration member"
			return v
		}
	}
	if v.Suspended {
		v.Reason = "stored permissions suspended by isolation; reconnect restores only isolation overlays"
		return v
	}
	if independent(ctx, q, id) && !v.Read && !v.Write {
		v.Reason = "session is independent"
		return v
	}
	if source == "team" {
		teams, err := boundaryTeams(ctx, q)
		if err != nil {
			v.Reason = "team state unavailable"
			return v
		}
		for _, team := range teams {
			if memory && team.Memory == name || !memory && team.Board == name {
				access := s.teamMemberAccess(ctx, q, team, id)
				if !access.Allowed {
					v.Reason = access.Reason
					if access.BlockedBy != "" {
						v.Reason += " (" + access.BlockedBy + ")"
					}
					return v
				}
			}
		}
	}
	if v.StoredRead != nil || v.StoredWrite != nil {
		v.Reason = "explicit owner override (stored intent; effective read/write shown separately)"
		return v
	}
	if v.Read || v.Write {
		v.Reason = "allowed by " + source
		return v
	}
	v.Reason = "not granted by current configuration or delegation"
	return v
}
