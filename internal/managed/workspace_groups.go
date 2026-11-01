package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
)

// WorkspaceGroup is an owner-authorized collaboration scope, separate from
// agent-created teams and their invitation/consent protocol. Agents identifies
// the stable original member set; Members identifies current participants.
type WorkspaceGroup struct {
	ID                   string   `json:"id"`
	Agents               []string `json:"agents"`
	Members              []string `json:"members"`
	Board                string   `json:"board"`
	Memory               string   `json:"memory"`
	State                string   `json:"state"`
	Version              int      `json:"version"`
	CreatedAt            string   `json:"created_at"`
	Commands             bool     `json:"commands"`
	ContributionsVersion int      `json:"contributions_version,omitempty"`
}

// Groups contribute grants independently. Explicit owner edge overrides are
// evaluated first; removing one group never changes a different group's grants.
func workspaceGroupCan(ctx context.Context, q queryer, from, action, to string) bool {
	groups, err := workspaceGroups(ctx, q)
	if err != nil {
		return false
	}
	for _, group := range groups {
		if group.ContributionsVersion >= 1 && group.State == "active" && contains(group.Members, from) && contains(group.Members, to) && (action != "instruct" || group.Commands) {
			return true
		}
	}
	return false
}

func workspaceGroups(ctx context.Context, q queryer) ([]WorkspaceGroup, error) {
	if state := boundaryReadState(ctx); state != nil {
		if !state.groupsLoaded {
			state.groups, state.groupsErr = list[WorkspaceGroup](ctx, q, "workspace_group", "ORDER BY seq")
			state.groupsLoaded = true
		}
		return state.groups, state.groupsErr
	}
	return list[WorkspaceGroup](ctx, q, "workspace_group", "ORDER BY seq")
}

func workspaceGroupKey(ids []string) string {
	ordered := append([]string(nil), ids...)
	sort.Strings(ordered)
	encoded, _ := json.Marshal(ordered)
	return "workspace_group_" + hash(string(encoded))
}

func (s *Store) setWorkspaceGroup(ctx context.Context, q queryer, a actor, ids []string, enabled bool, commands *bool) (WorkspaceGroup, *Fault) {
	key := workspaceGroupKey(ids)
	var group WorkspaceGroup
	err := get(ctx, q, "workspace_group", key, &group)
	if errors.Is(err, sql.ErrNoRows) {
		if !enabled {
			return group, fault("not_found", "workspace collaboration group not found")
		}
		n, err := count(ctx, q, "workspace_group")
		if err != nil {
			return group, internal(err)
		}
		if n >= 128 {
			return group, fault("limit", "workspace collaboration group limit reached (128 member sets)")
		}
		ordered := append([]string(nil), ids...)
		sort.Strings(ordered)
		group = WorkspaceGroup{ID: randomID("group"), Agents: ordered, CreatedAt: now(), ContributionsVersion: 1}
		group.Board, group.Memory = "board_"+group.ID, "memory_"+group.ID
		if s.namespaceExists(ctx, q, group.Board, false) || s.namespaceExists(ctx, q, group.Memory, true) {
			return group, fault("conflict", "workspace namespace collision; retry with a new request")
		}
	} else if err != nil {
		return group, internal(err)
	}
	beforeMembers := append([]string(nil), group.Members...)
	group.Members = []string{}
	group.ContributionsVersion = 1
	if commands != nil {
		group.Commands = *commands
	}
	group.State = "inactive"
	if enabled {
		group.Members = append(group.Members, group.Agents...)
		group.State = "active"
	}
	group.Version++
	if err := save(ctx, q, "workspace_group", key, ownerID, "", "owner", group); err != nil {
		return group, internal(err)
	}
	if err := event(ctx, q, a, "workspace_group.changed", group.ID, map[string]any{"version": group.Version, "state": group.State, "members": group.Members, "before_members": beforeMembers, "commands": group.Commands, "contributions_version": group.ContributionsVersion}); err != nil {
		return group, internal(err)
	}
	return group, nil
}

func removeWorkspaceGroupMembers(ctx context.Context, q queryer, a actor, chosen map[string]bool) *Fault {
	groups, err := workspaceGroups(ctx, q)
	if err != nil {
		return internal(err)
	}
	for _, group := range groups {
		members := []string{}
		for _, id := range group.Members {
			if !chosen[id] {
				members = append(members, id)
			}
		}
		if len(members) == len(group.Members) {
			continue
		}
		beforeMembers := append([]string(nil), group.Members...)
		group.Members = members
		if len(members) == 0 {
			group.State = "inactive"
		}
		group.Version++
		if err := save(ctx, q, "workspace_group", workspaceGroupKey(group.Agents), ownerID, "", "owner", group); err != nil {
			return internal(err)
		}
		if err := event(ctx, q, a, "workspace_group.changed", group.ID, map[string]any{"version": group.Version, "state": group.State, "members": members, "before_members": beforeMembers}); err != nil {
			return internal(err)
		}
	}
	return nil
}

func workspaceGroupNamespace(ctx context.Context, q queryer, namespace string, memory bool) (*WorkspaceGroup, error) {
	if boundaryReadState(ctx) != nil {
		groups, err := workspaceGroups(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, group := range groups {
			if memory && group.Memory == namespace || !memory && group.Board == namespace {
				return &group, nil
			}
		}
		return nil, nil
	}
	field := "board"
	if memory {
		field = "memory"
	}
	groups, err := list[WorkspaceGroup](ctx, q, "workspace_group", "AND json_extract(data,'$."+field+"')=?", namespace)
	if err != nil || len(groups) == 0 {
		return nil, err
	}
	return &groups[0], nil
}
