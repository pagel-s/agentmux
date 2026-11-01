package managed

import (
	"agentmux/internal/managedconfig"
	"context"
	"encoding/json"
	"sort"
	"time"
)

type Capacity struct {
	ResourceVersion         int                        `json:"resource_version"`
	AdmissionDeadline       string                     `json:"admission_deadline,omitempty"`
	Agent                   string                     `json:"agent"`
	SpawnRemaining          int                        `json:"spawn_remaining"`
	SpawnDepthRemaining     int                        `json:"spawn_depth_remaining"`
	TeamsRemaining          int                        `json:"teams_remaining"`
	TeamMemberLimit         int                        `json:"team_member_limit"`
	TeamSlots               map[string]int             `json:"team_slots"`
	RuntimeDeadline         string                     `json:"runtime_deadline,omitempty"`
	RuntimeRemainingSeconds *int64                     `json:"runtime_remaining_seconds,omitempty"`
	SpawnTemplates          []string                   `json:"spawn_templates"`
	CanSpawn                bool                       `json:"can_spawn"`
	CanCreateTeams          bool                       `json:"can_create_teams"`
	CanJoinTeams            bool                       `json:"can_join_teams"`
	BlockedReasons          []string                   `json:"blocked_reasons"`
	AdmissionBlocked        bool                       `json:"admission_blocked"`
	AdmissionReason         string                     `json:"admission_reason,omitempty"`
	AgentBudget             *managedconfig.AgentBudget `json:"agent_budget,omitempty"`
	SessionBudget           *managedconfig.Budget      `json:"session_budget,omitempty"`
}

func nonnegative(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
func (s *Store) capacity(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	var in struct {
		Agent string `json:"agent"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	if in.Agent == "" {
		in.Agent = a.ID
	}
	if !a.owner() && in.Agent != a.ID {
		return nil, forbidden()
	}
	return s.capacityFor(ctx, q, in.Agent)
}
func (s *Store) capacityFor(ctx context.Context, q queryer, id string) (Capacity, *Fault) {
	out := Capacity{Agent: id, SpawnTemplates: []string{}, TeamSlots: map[string]int{}, BlockedReasons: []string{}}
	depth := 0
	if id != ownerID {
		info, err := s.agent(ctx, q, id)
		if err != nil {
			return out, fault("not_found", "agent not found")
		}
		depth = info.Depth
		if info.State != "active" {
			out.BlockedReasons = append(out.BlockedReasons, "agent "+info.State)
		}
	}
	var spawned int
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind='agent' AND json_extract(data,'$.parent') IS NOT NULL AND COALESCE(json_extract(data,'$.owner_managed'),0)=0").Scan(&spawned); err != nil {
		return out, internal(err)
	}
	teams, err := list[Team](ctx, q, "team", "ORDER BY seq")
	if err != nil {
		return out, internal(err)
	}
	out.SpawnRemaining = nonnegative(s.cfg.Limits.MaxSpawns - spawned)
	out.SpawnDepthRemaining = nonnegative(s.cfg.Limits.MaxSpawnDepth - depth)
	out.TeamsRemaining = nonnegative(s.cfg.Limits.MaxTeams - len(teams))
	out.TeamMemberLimit = s.cfg.Limits.MaxTeamMembers
	for _, team := range teams {
		if team.State == "active" && (id == ownerID || team.Owner == id || contains(team.Members, id) || contains(team.Invited, id)) {
			out.TeamSlots[team.ID] = nonnegative(s.cfg.Limits.MaxTeamMembers - len(team.Members) - len(team.Invited))
		}
	}
	if s.cfg.Spawn.Enabled {
		if id == ownerID {
			for name := range s.cfg.Spawn.Templates {
				out.SpawnTemplates = append(out.SpawnTemplates, name)
			}
		} else {
			out.SpawnTemplates = append(out.SpawnTemplates, s.spawnTemplates(ctx, q, id)...)
		}
	}
	out.CanCreateTeams = s.cfg.Teams.Enabled && (id == ownerID || s.canCreateTeams(ctx, q, id)) && out.TeamsRemaining > 0
	sort.Strings(out.SpawnTemplates)
	out.CanJoinTeams = s.cfg.Teams.Enabled && (id == ownerID || s.canJoinTeams(ctx, q, id))
	if len(out.SpawnTemplates) == 0 {
		out.BlockedReasons = append(out.BlockedReasons, "no spawn template delegated")
	}
	if out.SpawnRemaining == 0 {
		out.BlockedReasons = append(out.BlockedReasons, "lifetime spawn limit reached")
	}
	if out.SpawnDepthRemaining == 0 {
		out.BlockedReasons = append(out.BlockedReasons, "spawn depth exhausted")
	}
	resource, err := readResourceControls(ctx, q)
	if err != nil {
		return out, internal(err)
	}
	out.ResourceVersion = resource.Version
	if id == ownerID {
		budget := s.cfg.Budget
		if resource.SessionBudget != nil {
			budget = *resource.SessionBudget
		}
		out.SessionBudget = &budget
	} else {
		info, e := s.agent(ctx, q, id)
		if e != nil {
			return out, internal(e)
		}
		budget := info.Config.Budget
		if v, ok := resource.AgentBudgets[id]; ok {
			budget = v
		}
		out.AgentBudget = &budget
	}
	out.AdmissionDeadline = resource.AdmissionDeadline
	deadline, err := s.admissionDeadline(ctx, q)
	if err != nil {
		return out, internal(err)
	}
	if !deadline.IsZero() {
		out.RuntimeDeadline = deadline.UTC().Format(time.RFC3339Nano)
		seconds := int64(time.Until(deadline) / time.Second)
		if seconds < 0 {
			seconds = 0
		}
		out.RuntimeRemainingSeconds = &seconds
		if !time.Now().Before(deadline) {
			out.BlockedReasons = append(out.BlockedReasons, "session runtime budget exhausted")
			out.CanCreateTeams = false
			out.CanJoinTeams = false
			out.AdmissionBlocked = true
			out.AdmissionReason = "session runtime budget exhausted"
		}
	}
	if id != ownerID {
		if _, problem := s.turnBudgetAllowance(ctx, q, id); problem != nil {
			if problem.Code != "limit" {
				return out, problem
			}
			if !out.AdmissionBlocked {
				out.AdmissionBlocked = true
				out.AdmissionReason = problem.Message
			}
		}
	}
	out.CanSpawn = len(out.BlockedReasons) == 0
	return out, nil
}
