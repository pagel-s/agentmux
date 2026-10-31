package managed

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

type Team struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Owner     string   `json:"owner"`
	Members   []string `json:"members"`
	Invited   []string `json:"invited"`
	State     string   `json:"state"`
	Version   int      `json:"version"`
	Board     string   `json:"board"`
	Memory    string   `json:"memory"`
	CreatedAt string   `json:"created_at"`
}
type teamRecord struct {
	Team
	JoinedAfter map[string]int64 `json:"joined_after"`
}

func (s *Store) canJoinTeams(ctx context.Context, q queryer, id string) bool {
	if !s.cfg.Teams.Enabled || independent(ctx, q, id) {
		return false
	}
	a, err := s.agent(ctx, q, id)
	if err != nil || a.State != "active" {
		return false
	}
	if a.Parent == "" {
		return true
	}
	return a.Capabilities.JoinTeams && (a.Parent == ownerID || s.canJoinTeams(ctx, q, a.Parent))
}

func (s *Store) teamAccess(ctx context.Context, q queryer, id, name string, memory bool) bool {
	teams, err := list[Team](ctx, q, "team", "AND json_extract(data,'$.state')='active'")
	if err != nil {
		return false
	}
	for _, t := range teams {
		if contains(t.Members, id) && ((memory && t.Memory == name) || (!memory && t.Board == name)) {
			a, err := s.agent(ctx, q, id)
			if err != nil || a.State != "active" {
				return false
			}
			if a.Parent == "" {
				return true
			}
			if t.Owner == id {
				return s.canCreateTeams(ctx, q, id)
			}
			return a.Capabilities.JoinTeams && (a.Parent == ownerID || s.teamAccess(ctx, q, a.Parent, name, memory))
		}
	}
	return false
}
func (s *Store) teams(ctx context.Context, q queryer, a actor, r Request) (any, *Fault) {
	var in struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Agent   string `json:"agent"`
		Version int    `json:"version"`
		After   string `json:"after"`
		Limit   int    `json:"limit"`
	}
	if err := strictJSON(r.Args, &in); err != nil {
		return nil, invalid(err)
	}
	if r.Operation == "team.list" {
		if in.Limit <= 0 || in.Limit > 200 {
			in.Limit = 100
		}
		var after int64
		if in.After != "" {
			var prior Team
			if err := get(ctx, q, "team", in.After, &prior); err != nil || (!a.owner() && prior.Owner != a.ID && !contains(prior.Members, a.ID) && !contains(prior.Invited, a.ID)) {
				return nil, fault("invalid", "invalid visible team cursor")
			}
			if err := q.QueryRowContext(ctx, "SELECT seq FROM records WHERE id=?", in.After).Scan(&after); err != nil {
				return nil, internal(err)
			}
		}
		condition := "AND seq>?"
		values := []any{after}
		if !a.owner() {
			condition += " AND (actor=? OR EXISTS(SELECT 1 FROM json_each(records.data,'$.members') WHERE value=?) OR EXISTS(SELECT 1 FROM json_each(records.data,'$.invited') WHERE value=?))"
			values = append(values, a.ID, a.ID, a.ID)
		}
		condition += " ORDER BY seq LIMIT ?"
		values = append(values, in.Limit)
		all, err := list[Team](ctx, q, "team", condition, values...)
		if err != nil {
			return nil, internal(err)
		}
		out := []Team{}
		cursor := in.After
		size := 0
		for _, t := range all {
			if a.owner() || t.Owner == a.ID || contains(t.Members, a.ID) || contains(t.Invited, a.ID) {
				raw, _ := json.Marshal(t)
				if size+len(raw) > maxWireBytes/2 {
					break
				}
				size += len(raw)
				cursor = t.ID
				out = append(out, t)
			}
		}
		return map[string]any{"teams": out, "cursor": cursor}, nil
	}
	if !s.cfg.Teams.Enabled {
		return nil, forbidden()
	}
	if r.Operation == "team.create" {
		if !a.owner() && !s.canCreateTeams(ctx, q, a.ID) {
			return nil, forbidden()
		}
		if strings.TrimSpace(in.Name) == "" || len(in.Name) > 128 {
			return nil, fault("invalid", "team name must contain 1–128 bytes")
		}
		n, err := count(ctx, q, "team")
		if err != nil {
			return nil, internal(err)
		}
		if n >= s.cfg.Limits.MaxTeams {
			return nil, fault("limit", "lifetime team limit reached")
		}
		t := teamRecord{Team: Team{ID: randomID("team"), Name: in.Name, Owner: a.ID, Members: []string{}, Invited: []string{}, State: "active", Version: 1, CreatedAt: now()}, JoinedAfter: map[string]int64{}}
		t.Board = "board_" + t.ID
		t.Memory = "memory_" + t.ID
		if !a.owner() {
			t.Members = append(t.Members, a.ID)
			var after int64
			if err = q.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM records").Scan(&after); err != nil {
				return nil, internal(err)
			}
			t.JoinedAfter[a.ID] = after
		}
		if err = save(ctx, q, "team", t.ID, a.ID, "", "", t); err != nil {
			return nil, internal(err)
		}
		if err = event(ctx, q, a, "team.created", t.ID, nil); err != nil {
			return nil, internal(err)
		}
		return map[string]any{"team": t.Team}, nil
	}
	var t teamRecord
	if err := get(ctx, q, "team", in.ID, &t); err != nil {
		return nil, fault("not_found", "team not found")
	}
	manager := a.owner() || t.Owner == a.ID
	if !manager && !contains(t.Members, a.ID) && !contains(t.Invited, a.ID) {
		return nil, fault("not_found", "team not found")
	}
	if t.State != "active" {
		return nil, fault("conflict", "team is dissolved")
	}
	if in.Version != t.Version {
		return nil, fault("conflict", "team version changed; read it again")
	}
	switch r.Operation {
	case "team.invite":
		if !manager {
			return nil, forbidden()
		}
		if !s.knownAgent(ctx, q, in.Agent) {
			return nil, forbidden()
		}
		if !a.owner() && !s.can(ctx, q, a.ID, "message.send", in.Agent) {
			return nil, forbidden()
		}
		if contains(t.Members, in.Agent) || contains(t.Invited, in.Agent) {
			return nil, fault("conflict", "agent is already a member or invited")
		}
		if len(t.Members)+len(t.Invited) >= s.cfg.Limits.MaxTeamMembers {
			return nil, fault("limit", "team membership limit reached")
		}
		t.Invited = append(t.Invited, in.Agent)
		sort.Strings(t.Invited)
	case "team.join":
		if a.owner() || !contains(t.Invited, a.ID) {
			return nil, forbidden()
		}
		if !s.canJoinTeams(ctx, q, a.ID) {
			return nil, forbidden()
		}
		info, err := s.agent(ctx, q, a.ID)
		if err != nil {
			return nil, forbidden()
		}
		if info.Parent != "" && info.Parent != ownerID && !s.teamAccess(ctx, q, info.Parent, t.Board, false) {
			return nil, forbidden()
		}
		var after int64
		if err = q.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM records").Scan(&after); err != nil {
			return nil, internal(err)
		}
		if t.JoinedAfter == nil {
			t.JoinedAfter = map[string]int64{}
		}
		t.JoinedAfter[a.ID] = after
		t.Invited = without(t.Invited, a.ID)
		t.Members = append(t.Members, a.ID)
		sort.Strings(t.Members)
	case "team.remove":
		if !manager {
			return nil, forbidden()
		}
		if in.Agent == t.Owner {
			return nil, fault("invalid", "team owner must dissolve the team instead")
		}
		if !contains(t.Members, in.Agent) && !contains(t.Invited, in.Agent) {
			return nil, fault("not_found", "team participant not found")
		}
		t.Members = without(t.Members, in.Agent)
		t.Invited = without(t.Invited, in.Agent)
	case "team.leave":
		if a.owner() || t.Owner == a.ID {
			return nil, fault("invalid", "team owner must dissolve the team instead")
		}
		if !contains(t.Members, a.ID) && !contains(t.Invited, a.ID) {
			return nil, forbidden()
		}
		t.Members = without(t.Members, a.ID)
		t.Invited = without(t.Invited, a.ID)
	case "team.dissolve":
		if !manager {
			return nil, forbidden()
		}
		t.State = "dissolved"
		t.Invited = []string{}
	default:
		return nil, fault("unsupported", "unsupported team operation")
	}
	t.Version++
	if err := save(ctx, q, "team", t.ID, t.Owner, "", "", t); err != nil {
		return nil, internal(err)
	}
	if err := event(ctx, q, a, r.Operation, t.ID, map[string]any{"version": t.Version}); err != nil {
		return nil, internal(err)
	}
	return map[string]any{"team": t.Team}, nil
}

// boardFloor is internal owner-sequence metadata, never part of agent JSON.
// Delegated team access cannot expose history older than the parent's view.
func (s *Store) boardFloor(ctx context.Context, q queryer, id, board string) int64 {
	if s.cfg.Teams.History == "all" {
		return 0
	}
	all, err := list[teamRecord](ctx, q, "team", "AND json_extract(data,'$.board')=?", board)
	if err != nil {
		return 1<<63 - 1
	}
	if len(all) == 0 {
		return 0
	}
	t := all[0]
	a, err := s.agent(ctx, q, id)
	if err != nil {
		return 1<<63 - 1
	}
	floor := t.JoinedAfter[id]
	if a.Parent != "" && a.Parent != ownerID && t.Owner != id {
		parent := s.boardFloor(ctx, q, a.Parent, board)
		if parent > floor {
			floor = parent
		}
	}
	return floor
}
func without(items []string, id string) []string {
	out := []string{}
	for _, item := range items {
		if item != id {
			out = append(out, item)
		}
	}
	return out
}
