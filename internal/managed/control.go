package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"agentmux/internal/managedconfig"
)

// Live controls overlay the saved launch configuration. Keeping launch inputs
// immutable preserves restart identity, fingerprints and process boundaries.
type EdgeControl struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Action string `json:"action"`
	Allow  bool   `json:"allow"`
	Source string `json:"source,omitempty"`
}
type ScopeControl struct {
	Agent     string `json:"agent"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Read      bool   `json:"read"`
	Write     bool   `json:"write"`
	Source    string `json:"source,omitempty"`
}
type ControlState struct {
	Version     int              `json:"version"`
	Independent []string         `json:"independent,omitempty"`
	Edges       []EdgeControl    `json:"edges"`
	Scopes      []ScopeControl   `json:"scopes"`
	Group       *WorkspaceGroup  `json:"group,omitempty"`
	Groups      []WorkspaceGroup `json:"groups,omitempty"`
}
type BoundaryScope struct {
	Name        string `json:"name"`
	Read        bool   `json:"read"`
	Write       bool   `json:"write"`
	Source      string `json:"source,omitempty"`
	TeamID      string `json:"team_id,omitempty"`
	Label       string `json:"label,omitempty"`
	StoredRead  *bool  `json:"stored_read,omitempty"`
	StoredWrite *bool  `json:"stored_write,omitempty"`
	Suspended   bool   `json:"suspended,omitempty"`
	Reason      string `json:"reason,omitempty"`
}
type Boundaries struct {
	Version       int              `json:"version"`
	Agent         string           `json:"agent"`
	Targets       []BoundaryTarget `json:"targets"`
	Boards        []BoundaryScope  `json:"boards"`
	Memory        []BoundaryScope  `json:"memory"`
	PrivateMemory string           `json:"private_memory"`
}
type BoundaryTarget struct {
	To            string            `json:"to"`
	Actions       []string          `json:"actions"`
	Sources       map[string]string `json:"sources,omitempty"`
	DeniedReasons map[string]string `json:"denied_reasons,omitempty"`
}

func readControls(ctx context.Context, q queryer) (ControlState, error) {
	c := ControlState{Version: 1, Edges: []EdgeControl{}, Scopes: []ScopeControl{}}
	var persisted *ControlState
	var raw json.RawMessage
	err := get(ctx, q, "control", "live_access", &raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	if e := checkStateRecoveryDigest(ctx, q, "control", "live_access", raw); e != nil {
		return c, e
	}
	var version string
	markerErr := q.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='live_access_version'").Scan(&version)
	if markerErr != nil && !errors.Is(markerErr, sql.ErrNoRows) {
		return c, markerErr
	}
	if errors.Is(err, sql.ErrNoRows) {
		if markerErr == nil {
			return c, errors.New("persisted live permissions are missing; restore valid owner state")
		}
		return c, nil
	}
	if err = json.Unmarshal(raw, &persisted); err != nil {
		return c, err
	}
	if persisted == nil || persisted.Version < 1 || persisted.Edges == nil || persisted.Scopes == nil || markerErr == nil && version != strconv.Itoa(persisted.Version) {
		return c, errors.New("persisted live permissions are invalid; restore valid owner state")
	}
	return *persisted, nil
}

func edgeControl(ctx context.Context, q queryer, from, action, to string) (bool, bool) {
	c, err := controls(ctx, q)
	if err != nil {
		return false, true
	}
	for _, e := range c.Edges {
		if e.From == from && e.To == to && e.Action == action && e.Source == "isolation" {
			return false, true
		}
	}
	for _, e := range c.Edges {
		if e.From == from && e.To == to && e.Action == action {
			return e.Allow, true
		}
	}
	return false, false
}
func scopeControl(ctx context.Context, q queryer, agent, namespace string, memory, write bool) (bool, bool) {
	c, err := controls(ctx, q)
	if err != nil {
		return false, true
	}
	kind := "board"
	if memory {
		kind = "memory"
	}
	for _, v := range c.Scopes {
		if v.Agent == agent && v.Namespace == namespace && v.Kind == kind && v.Source == "isolation" {
			return false, true
		}
	}
	for _, v := range c.Scopes {
		if v.Agent == agent && v.Namespace == namespace && v.Kind == kind {
			if write {
				return v.Write, true
			}
			return v.Read, true
		}
	}
	return false, false
}

// One revision covers every effective permission source, including consented
// membership. Clients must not reuse a boundary snapshot across a team change.
func saveControls(ctx context.Context, q queryer, c ControlState) error {
	if err := save(ctx, q, "control", "live_access", ownerID, "", "owner", c); err != nil {
		return err
	}
	if err := saveStateRecoveryDigest(ctx, q, "control", "live_access", c); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, "INSERT INTO metadata(key,value) VALUES('live_access_version',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", strconv.Itoa(c.Version))
	return err
}

func advanceBoundaryRevision(ctx context.Context, q queryer) error {
	c, err := controls(ctx, q)
	if err != nil {
		return err
	}
	c.Version++
	return saveControls(ctx, q, c)
}

func (s *Store) control(ctx context.Context, q queryer, a actor, r Request) (any, *Fault) {
	if r.Operation == "boundaries.get" {
		ctx = boundaryReadContext(ctx)
		var in struct {
			Agent string `json:"agent"`
		}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		if in.Agent == "" {
			in.Agent = a.ID
		}
		if !a.owner() && in.Agent != a.ID {
			return nil, forbidden()
		}
		if !s.knownAgent(ctx, q, in.Agent) {
			return nil, fault("not_found", "active agent not found")
		}
		c, err := controls(ctx, q)
		if err != nil {
			return nil, internal(err)
		}
		b := Boundaries{Version: c.Version, Agent: in.Agent, Targets: []BoundaryTarget{}, Boards: []BoundaryScope{}, Memory: []BoundaryScope{}, PrivateMemory: "private"}
		ids, err := s.controlAgentIDs(ctx, q)
		if err != nil {
			return nil, internal(err)
		}
		for _, id := range ids {
			t := BoundaryTarget{To: id, Actions: []string{}, Sources: map[string]string{}, DeniedReasons: map[string]string{}}
			for _, action := range []string{"message.send", "ping.send", "instruct"} {
				if s.can(ctx, q, in.Agent, action, id) {
					t.Actions = append(t.Actions, action)
					t.Sources[action] = s.boundaryEdgeReason(ctx, q, in.Agent, action, id)
				} else {
					t.DeniedReasons[action] = s.boundaryEdgeReason(ctx, q, in.Agent, action, id)
				}
			}
			if len(t.Actions) > 0 || a.owner() {
				b.Targets = append(b.Targets, t)
			}
		}
		for _, entry := range []struct {
			memory     bool
			namespaces map[string]managedconfig.Access
		}{{false, s.cfg.Boards}, {true, s.cfg.Memory}} {
			names := []string{}
			for name := range entry.namespaces {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				v := s.boundaryScope(ctx, q, in.Agent, name, entry.memory, "configuration")
				if !a.owner() && !v.Read && !v.Write {
					continue
				}
				if entry.memory {
					b.Memory = append(b.Memory, v)
				} else {
					b.Boards = append(b.Boards, v)
				}
			}
		}
		groups, err := workspaceGroups(ctx, q)
		if err != nil {
			return nil, internal(err)
		}
		for _, group := range groups {
			if !contains(group.Agents, in.Agent) {
				continue
			}
			for _, entry := range []struct {
				name   string
				memory bool
			}{{group.Board, false}, {group.Memory, true}} {
				scope := s.boundaryScope(ctx, q, in.Agent, entry.name, entry.memory, "group")
				scope.TeamID, scope.Label = group.ID, "Workspace collaboration"
				if entry.memory {
					b.Memory = append(b.Memory, scope)
				} else {
					b.Boards = append(b.Boards, scope)
				}
			}
		}
		teams, err := activeTeams(ctx, q)
		if err != nil {
			return nil, internal(err)
		}
		for _, team := range teams {
			for _, entry := range []struct {
				name   string
				memory bool
			}{{team.Board, false}, {team.Memory, true}} {
				scope := s.boundaryScope(ctx, q, in.Agent, entry.name, entry.memory, "team")
				scope.TeamID, scope.Label = team.ID, team.Name
				if !a.owner() && !scope.Read && !scope.Write {
					continue
				}
				if entry.memory {
					b.Memory = append(b.Memory, scope)
				} else {
					b.Boards = append(b.Boards, scope)
				}
			}
		}
		return b, nil
	}
	if !a.owner() {
		return nil, forbidden()
	}
	c, err := controls(ctx, q)
	if err != nil {
		return nil, internal(err)
	}
	if r.Operation == "control.get" {
		var in struct{}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		c.Groups, err = workspaceGroups(ctx, q)
		if err != nil {
			return nil, internal(err)
		}
		return c, nil
	}
	var in struct {
		Version      int      `json:"version"`
		Kind         string   `json:"kind"`
		From         string   `json:"from"`
		To           string   `json:"to"`
		Action       string   `json:"action"`
		Allow        bool     `json:"allow"`
		Agent        string   `json:"agent"`
		Namespace    string   `json:"namespace"`
		Read         bool     `json:"read"`
		Write        bool     `json:"write"`
		Agents       []string `json:"agents"`
		Enabled      bool     `json:"enabled"`
		Commands     bool     `json:"commands"`
		Board        string   `json:"board"`
		Memory       string   `json:"memory"`
		Reset        bool     `json:"reset"`
		LegacyPolicy string   `json:"legacy_policy"`
	}
	if err := strictJSON(r.Args, &in); err != nil {
		return nil, invalid(err)
	}
	if in.Version != c.Version {
		return nil, fault("conflict", "permissions changed; refresh before editing")
	}
	if in.LegacyPolicy != "" && in.LegacyPolicy != "preserve" && in.LegacyPolicy != "group" {
		return nil, fault("invalid", "legacy_policy must be preserve or group")
	}
	if in.LegacyPolicy != "" && (in.Kind != "collaboration" || in.Board != "" || in.Memory != "") {
		return nil, fault("invalid", "legacy_policy applies only to owner collaboration groups")
	}
	static := func(id string) bool { return s.ownerControllable(ctx, q, id) }
	resettable := func(id string) bool {
		if !in.Reset {
			return static(id)
		}
		_, err := s.agent(ctx, q, id)
		return err == nil
	}
	var responseGroup *WorkspaceGroup
	if in.Kind == "collaboration" || in.Kind == "independent" {
		if len(in.Agents) == 0 || len(in.Agents) > 32 {
			return nil, fault("invalid", "select 1–32 workspace agents")
		}
		chosen := map[string]bool{}
		for _, id := range in.Agents {
			if !static(id) || chosen[id] {
				return nil, fault("invalid", "select distinct active workspace agents")
			}
			chosen[id] = true
		}
		if in.Kind == "collaboration" && len(chosen) < 2 {
			return nil, fault("invalid", "collaboration requires at least two agents")
		}
		if in.Board != "" {
			if _, ok := s.cfg.Boards[in.Board]; !ok {
				return nil, fault("invalid", "unknown shared board")
			}
		}
		if in.Memory != "" {
			if _, ok := s.cfg.Memory[in.Memory]; !ok {
				return nil, fault("invalid", "unknown shared memory")
			}
		}
		if in.Kind == "collaboration" && in.Board == "" && in.Memory == "" {
			if problem := reviewLegacyWorkspaceGroup(ctx, q, &c, in.Agents, in.LegacyPolicy); problem != nil {
				return nil, problem
			}
			group, problem := s.setWorkspaceGroup(ctx, q, a, in.Agents, in.Enabled, in.Commands)
			if problem != nil {
				return nil, problem
			}
			responseGroup = &group
		}
		// Generated isolation overlays have their own source. They suspend rather
		// than overwrite explicit owner grants, and reconnection removes only
		// these overlays. Group contributions live on the group itself.
		edgeReadCtx := boundaryReadContext(ctx)
		putEdge := func(from, to, action string, allow bool) {
			for i, v := range c.Edges {
				if v.From == from && v.To == to && v.Action == action && v.Source == "isolation" {
					c.Edges[i].Allow = allow
					return
				}
			}
			if !allow && !s.can(edgeReadCtx, q, from, action, to) {
				return
			}
			c.Edges = append(c.Edges, EdgeControl{From: from, To: to, Action: action, Allow: allow, Source: "isolation"})
		}
		putScope := func(id, kind, name string, allow bool) {
			source := ""
			if in.Kind == "independent" {
				source = "isolation"
			}
			for i, v := range c.Scopes {
				if v.Agent == id && v.Kind == kind && v.Namespace == name && v.Source == source {
					c.Scopes[i].Read = allow
					c.Scopes[i].Write = allow
					return
				}
			}
			c.Scopes = append(c.Scopes, ScopeControl{Agent: id, Kind: kind, Namespace: name, Read: allow, Write: allow, Source: source})
		}
		ids, err := s.controlAgentIDs(ctx, q)
		if err != nil {
			return nil, internal(err)
		}
		if in.Kind == "independent" {
			for _, from := range ids {
				for _, to := range ids {
					if from != to && (chosen[from] || chosen[to]) {
						for _, action := range []string{"message.send", "ping.send", "instruct"} {
							putEdge(from, to, action, false)
						}
					}
				}
			}
		} else if in.Enabled {
			edges := []EdgeControl{}
			for _, edge := range c.Edges {
				if edge.Source == "isolation" && chosen[edge.From] && chosen[edge.To] {
					continue
				}
				edges = append(edges, edge)
			}
			c.Edges = edges
			scopes := []ScopeControl{}
			for _, scope := range c.Scopes {
				if scope.Source == "isolation" && chosen[scope.Agent] {
					continue
				}
				scopes = append(scopes, scope)
			}
			c.Scopes = scopes
		}
		if in.Kind == "collaboration" && responseGroup == nil {
			// Named static scopes are an explicit global owner edit, unlike a
			// named collaboration group's independently removable contribution.
			for from := range chosen {
				for to := range chosen {
					if from == to {
						continue
					}
					for _, action := range []string{"message.send", "ping.send", "instruct"} {
						allow := in.Enabled && (action != "instruct" || in.Commands)
						next := []EdgeControl{}
						for _, edge := range c.Edges {
							if edge.From == from && edge.To == to && edge.Action == action {
								continue
							}
							next = append(next, edge)
						}
						c.Edges = append(next, EdgeControl{From: from, To: to, Action: action, Allow: allow})
					}
				}
			}
		}
		for id := range chosen {
			if in.Kind == "independent" {
				if !contains(c.Independent, id) {
					c.Independent = append(c.Independent, id)
				}
				for name := range s.cfg.Boards {
					putScope(id, "board", name, false)
				}
				for name := range s.cfg.Memory {
					putScope(id, "memory", name, false)
				}
			} else {
				if in.Enabled {
					c.Independent = without(c.Independent, id)
				}
				if in.Board != "" {
					putScope(id, "board", in.Board, in.Enabled)
				}
				if in.Memory != "" {
					putScope(id, "memory", in.Memory, in.Enabled)
				}
			}
		}
		if in.Kind == "independent" {
			if problem := removeWorkspaceGroupMembers(ctx, q, a, chosen); problem != nil {
				return nil, problem
			}
			teams, err := list[Team](ctx, q, "team", "AND json_extract(data,'$.state')='active'")
			if err != nil {
				return nil, internal(err)
			}
			for _, team := range teams {
				if chosen[team.Owner] {
					raw, _ := json.Marshal(map[string]any{"id": team.ID, "version": team.Version})
					if _, problem := s.teams(ctx, q, a, Request{Operation: "team.dissolve", Args: raw}); problem != nil {
						return nil, problem
					}
					continue
				}
				version := team.Version
				for id := range chosen {
					if contains(team.Members, id) || contains(team.Invited, id) {
						raw, _ := json.Marshal(map[string]any{"id": team.ID, "agent": id, "version": version})
						if _, problem := s.teams(ctx, q, a, Request{Operation: "team.remove", Args: raw}); problem != nil {
							return nil, problem
						}
						version++
					}
				}
			}
		}
	} else {
		switch in.Kind {
		case "edge":
			if !resettable(in.From) || !resettable(in.To) || (in.Action != "message.send" && in.Action != "ping.send" && in.Action != "instruct") || in.Agent != "" || in.Namespace != "" || in.Read || in.Write {
				return nil, fault("invalid", "edge control requires configured active from/to agents and an exact messaging action")
			}
			next := EdgeControl{From: in.From, To: in.To, Action: in.Action, Allow: in.Allow}
			edges := []EdgeControl{}
			for _, v := range c.Edges {
				if v.From == next.From && v.To == next.To && v.Action == next.Action {
					continue
				}
				edges = append(edges, v)
			}
			if !in.Reset {
				edges = append(edges, next)
			}
			c.Edges = edges
		case "board", "memory":
			_, ok := s.cfg.Boards[in.Namespace]
			if in.Kind == "memory" {
				_, ok = s.cfg.Memory[in.Namespace]
			}
			if !ok {
				group, err := workspaceGroupNamespace(ctx, q, in.Namespace, in.Kind == "memory")
				if err != nil {
					return nil, internal(err)
				}
				ok = group != nil && (in.Reset || group.State == "active" && contains(group.Members, in.Agent))
			}
			if in.Reset && !ok {
				for _, scope := range c.Scopes {
					if scope.Agent == in.Agent && scope.Kind == in.Kind && scope.Namespace == in.Namespace {
						ok = true
						break
					}
				}
			}
			if !ok || !resettable(in.Agent) || in.From != "" || in.To != "" || in.Action != "" || in.Allow {
				return nil, fault("invalid", "scope control requires a configured shared namespace and active agent; team membership uses consent operations")
			}
			next := ScopeControl{Agent: in.Agent, Kind: in.Kind, Namespace: in.Namespace, Read: in.Read, Write: in.Write}
			scopes := []ScopeControl{}
			for _, v := range c.Scopes {
				if v.Agent == next.Agent && v.Kind == next.Kind && v.Namespace == next.Namespace {
					continue
				}
				scopes = append(scopes, v)
			}
			if !in.Reset {
				scopes = append(scopes, next)
			}
			c.Scopes = scopes
		default:
			return nil, fault("invalid", "control kind must be edge, board or memory")
		}
	}
	if len(c.Edges)+len(c.Scopes) > 4096 {
		return nil, fault("limit", "live permission override limit reached")
	}
	// Independence can remove several consented memberships in this same
	// transaction. Preserve their revisions instead of overwriting with an
	// earlier value captured before those nested mutations.
	current, err := controls(ctx, q)
	if err != nil {
		return nil, internal(err)
	}
	if current.Version > c.Version {
		c.Version = current.Version
	}
	c.Version++
	if err := saveControls(ctx, q, c); err != nil {
		return nil, internal(err)
	}
	if err := event(ctx, q, a, "boundaries.changed", "live_access", map[string]any{"version": c.Version, "kind": in.Kind, "agent": in.Agent, "from": in.From, "to": in.To, "action": in.Action, "namespace": in.Namespace, "legacy_policy": in.LegacyPolicy}); err != nil {
		return nil, internal(err)
	}
	c.Group = responseGroup
	return c, nil
}

func (s *Store) controlAgentIDs(ctx context.Context, q queryer) ([]string, error) {
	ids := map[string]bool{}
	for id := range s.cfg.Agents {
		ids[id] = true
	}
	dynamic, err := list[AgentInfo](ctx, q, "agent", "ORDER BY seq")
	if err != nil {
		return nil, err
	}
	for _, v := range dynamic {
		ids[v.ID] = true
	}
	out := []string{}
	for id := range ids {
		if s.knownAgent(ctx, q, id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

// An independent workspace may receive explicit owner grants, but cannot regain
// ambient team/delegation capabilities or implicit future child relationships.
func independent(ctx context.Context, q queryer, id string) bool {
	c, err := controls(ctx, q)
	return err != nil || contains(c.Independent, id)
}
