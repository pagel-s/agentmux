package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"agentmux/internal/egress"
	"agentmux/internal/managedconfig"
)

type TargetGrant struct {
	To      string   `json:"to"`
	Actions []string `json:"actions"`
}
type Capabilities struct {
	BoardsRead     []string      `json:"boards_read,omitempty"`
	BoardsWrite    []string      `json:"boards_write,omitempty"`
	MemoryRead     []string      `json:"memory_read,omitempty"`
	MemoryWrite    []string      `json:"memory_write,omitempty"`
	Targets        []TargetGrant `json:"targets,omitempty"`
	SpawnTemplates []string      `json:"spawn_templates,omitempty"`
	CreateTeams    bool          `json:"create_teams,omitempty"`
	JoinTeams      bool          `json:"join_teams,omitempty"`
}
type AgentInfo struct {
	ID           string              `json:"id"`
	Title        string              `json:"title"`
	TitleVersion int                 `json:"title_version"`
	RoleVersion  int                 `json:"role_version"`
	External     bool                `json:"external,omitempty"`
	ExternalRef  string              `json:"external_ref,omitempty"`
	OwnerManaged bool                `json:"owner_managed,omitempty"`
	Parent       string              `json:"parent,omitempty"`
	Template     string              `json:"template,omitempty"`
	State        string              `json:"state"`
	RetiredAt    string              `json:"retired_at,omitempty"`
	Depth        int                 `json:"depth"`
	Config       managedconfig.Agent `json:"config"`
	Capabilities Capabilities        `json:"capabilities"`
	CreatedAt    string              `json:"created_at,omitempty"`
	Deadline     string              `json:"deadline,omitempty"`
}

func (s *Store) readAgent(ctx context.Context, q queryer, id string) (AgentInfo, error) {
	var info AgentInfo
	err := get(ctx, q, "agent", "agent_"+id, &info)
	if err == nil {
		return sessionPresentation(ctx, q, info)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return info, err
	}
	if a, ok := s.cfg.Agents[id]; ok {
		return sessionPresentation(ctx, q, AgentInfo{ID: id, State: "active", Config: copyAgent(a)})
	}
	return info, sql.ErrNoRows
}
func (s *Store) knownAgent(ctx context.Context, q queryer, id string) bool {
	a, err := s.agent(ctx, q, id)
	return err == nil && a.State == "active"
}

// Agent loads the full owner-side process definition. RPC registry output
// redacts environment values and never contains credentials or lease tokens.
func (s *Store) Agent(id string) (managedconfig.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.agent(context.Background(), s.db, id)
	if err != nil || a.State != "active" {
		return managedconfig.Agent{}, errors.New("agent is not active")
	}
	return a.Config, nil
}

func (s *Store) can(ctx context.Context, q queryer, from, action, to string) bool {
	if action != "message.send" && action != "ping.send" && action != "instruct" {
		return false
	}
	f, err := s.agent(ctx, q, from)
	if err != nil || f.State != "active" {
		return false
	}
	target, err := s.agent(ctx, q, to)
	if err != nil || target.State != "active" {
		return false
	}
	if allow, exists := edgeControl(ctx, q, from, action, to); exists {
		return allow
	}
	if independent(ctx, q, from) || independent(ctx, q, to) {
		return false
	}
	if from != to && workspaceGroupCan(ctx, q, from, action, to) {
		return true
	}
	// Creating a worker grants only a control edge to that worker. A child
	// may report back, but cannot command its parent through this edge.
	if target.Parent == from {
		return true
	}
	if f.Parent == to && (action == "message.send" || action == "ping.send") {
		return true
	}
	if f.Parent == "" {
		return s.cfg.Can(from, action, to)
	}
	for _, grant := range f.Capabilities.Targets {
		if grant.To == to && contains(grant.Actions, action) {
			return f.Parent == ownerID || s.can(ctx, q, f.Parent, action, to)
		}
	}
	return false
}
func (s *Store) namespace(ctx context.Context, q queryer, id, name string, memory, write bool) bool {
	a, err := s.agent(ctx, q, id)
	if err != nil || a.State != "active" {
		return false
	}
	group, err := workspaceGroupNamespace(ctx, q, name, memory)
	if err != nil {
		return false
	}
	if group != nil && (group.State != "active" || !contains(group.Members, id)) {
		return false
	}
	teamScope, err := teamNamespace(ctx, q, name, memory)
	if err != nil {
		return false
	}
	if teamScope {
		// A named delegation or old owner scope override is not participant
		// consent. Durable namespace recognition also fences dissolved teams.
		return !independent(ctx, q, id) && s.teamAccess(ctx, q, id, name, memory)
	}
	if allow, exists := scopeControl(ctx, q, id, name, memory, write); exists {
		return allow
	}
	if independent(ctx, q, id) {
		return false
	}
	if group != nil {
		return true
	}
	if s.teamAccess(ctx, q, id, name, memory) {
		return true
	}
	if a.Parent == "" {
		if memory {
			if write {
				return s.cfg.CanWriteMemory(id, name)
			}
			return s.cfg.CanReadMemory(id, name)
		}
		if write {
			return s.cfg.CanWriteBoard(id, name)
		}
		return s.cfg.CanReadBoard(id, name)
	}
	values := a.Capabilities.BoardsRead
	if write {
		values = a.Capabilities.BoardsWrite
	}
	if memory {
		values = a.Capabilities.MemoryRead
		if write {
			values = a.Capabilities.MemoryWrite
		}
	}
	return contains(values, name) && (a.Parent == ownerID || s.namespace(ctx, q, a.Parent, name, memory, write))
}

func teamNamespace(ctx context.Context, q queryer, name string, memory bool) (bool, error) {
	if boundaryReadState(ctx) != nil {
		teams, err := boundaryTeams(ctx, q)
		if err != nil {
			return false, err
		}
		for _, team := range teams {
			if memory && team.Memory == name || !memory && team.Board == name {
				return true, nil
			}
		}
		return false, nil
	}
	field := "$.board"
	if memory {
		field = "$.memory"
	}
	var matches int
	err := q.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind='team' AND json_extract(data,?)=?", field, name).Scan(&matches)
	return matches > 0, err
}

func (s *Store) namespaceExists(ctx context.Context, q queryer, name string, memory bool) bool {
	if group, err := workspaceGroupNamespace(ctx, q, name, memory); err != nil {
		return false
	} else if group != nil {
		return true
	}
	if memory {
		if _, ok := s.cfg.Memory[name]; ok {
			return true
		}
	} else {
		if _, ok := s.cfg.Boards[name]; ok {
			return true
		}
	}
	teams, err := list[Team](ctx, q, "team", "AND json_extract(data,'$.state')='active'")
	if err != nil {
		return false
	}
	for _, team := range teams {
		if (!memory && team.Board == name) || (memory && team.Memory == name) {
			return true
		}
	}
	return false
}
func (s *Store) readableBoards(ctx context.Context, q queryer, id string) []string {
	boards := []string{}
	for name := range s.cfg.Boards {
		if s.namespace(ctx, q, id, name, false, false) {
			boards = append(boards, name)
		}
	}
	a, err := s.agent(ctx, q, id)
	if err == nil && a.Parent != "" {
		for _, name := range a.Capabilities.BoardsRead {
			if !contains(boards, name) && s.namespace(ctx, q, id, name, false, false) {
				boards = append(boards, name)
			}
		}
	}
	teams, err := list[Team](ctx, q, "team", "AND json_extract(data,'$.state')='active'")
	if err == nil {
		for _, team := range teams {
			if contains(team.Members, id) && s.namespace(ctx, q, id, team.Board, false, false) {
				boards = append(boards, team.Board)
			}
		}
	}
	groups, err := workspaceGroups(ctx, q)
	if err == nil {
		for _, group := range groups {
			if contains(group.Members, id) && s.namespace(ctx, q, id, group.Board, false, false) {
				boards = append(boards, group.Board)
			}
		}
	}
	sort.Strings(boards)
	return boards
}
func (s *Store) spawnTemplates(ctx context.Context, q queryer, id string) []string {
	if !s.cfg.Spawn.Enabled || independent(ctx, q, id) {
		return nil
	}
	a, err := s.agent(ctx, q, id)
	if err != nil || a.State != "active" {
		return nil
	}
	if a.Parent != "" {
		return a.Capabilities.SpawnTemplates
	}
	return s.cfg.Spawn.Allow[id]
}
func (s *Store) canCreateTeams(ctx context.Context, q queryer, id string) bool {
	if independent(ctx, q, id) {
		return false
	}
	if !s.cfg.Teams.Enabled {
		return false
	}
	a, err := s.agent(ctx, q, id)
	if err != nil || a.State != "active" {
		return false
	}
	if a.Parent != "" {
		return a.Capabilities.CreateTeams
	}
	return contains(s.cfg.Teams.Creators, id) || contains(s.cfg.Teams.Creators, "*")
}

func (s *Store) agents(ctx context.Context, q queryer, a actor, r Request) (any, *Fault) {
	switch r.Operation {
	case "agent.list":
		if !a.owner() {
			return nil, forbidden()
		}
		var in struct {
			After           string `json:"after"`
			Limit           int    `json:"limit"`
			IncludeArchived bool   `json:"include_archived"`
		}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		if in.Limit <= 0 || in.Limit > 200 {
			in.Limit = 200
		}
		ids := []string{}
		for id := range s.cfg.Agents {
			ids = append(ids, id)
		}
		dynamic, err := list[AgentInfo](ctx, q, "agent", "ORDER BY seq")
		if err != nil {
			return nil, internal(err)
		}
		for _, v := range dynamic {
			if _, ok := s.cfg.Agents[v.ID]; !ok {
				ids = append(ids, v.ID)
			}
		}
		sort.Strings(ids)
		out := []AgentInfo{}
		cursor := in.After
		size := 0
		deadline, _ := s.deadline(ctx, q)
		for _, id := range ids {
			if id <= in.After {
				continue
			}
			info, err := s.agent(ctx, q, id)
			if err != nil {
				return nil, internal(err)
			}
			if info.State == "retired" && !in.IncludeArchived {
				continue
			}
			for key := range info.Config.Env {
				info.Config.Env[key] = "[redacted]"
			}
			if !deadline.IsZero() {
				info.Deadline = deadline.UTC().Format(time.RFC3339Nano)
			}
			raw, _ := json.Marshal(info)
			if size+len(raw) > maxWireBytes/2 {
				break
			}
			size += len(raw)
			out = append(out, info)
			cursor = id
			if len(out) >= in.Limit {
				break
			}
		}
		return map[string]any{"agents": out, "cursor": cursor}, nil
	case "agent.retire":
		return s.retireAgent(ctx, q, a, r.Args)
	case "agent.add":
		return s.addWorkspaceAgent(ctx, q, a, r.Args)
	case "agent.label":
		return s.labelSession(ctx, q, a, r.Args)
	case "agent.role":
		return s.updateSessionRole(ctx, q, a, r.Args)
	case "agent.spawn":
		return s.spawn(ctx, q, a, r.Args)
	case "agent.stop":
		return s.stopAgent(ctx, q, a, r.Args)
	}
	return nil, fault("unsupported", "unsupported agent operation")
}

func (s *Store) spawn(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	var in struct {
		Template     string       `json:"template"`
		Capabilities Capabilities `json:"capabilities"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	if !s.cfg.Spawn.Enabled {
		return nil, forbidden()
	}
	template, ok := s.cfg.Spawn.Templates[in.Template]
	if !ok {
		return nil, forbidden()
	}
	parent := AgentInfo{}
	if !a.owner() {
		if independent(ctx, q, a.ID) {
			return nil, forbidden()
		}
		if !contains(s.spawnTemplates(ctx, q, a.ID), in.Template) {
			return nil, forbidden()
		}
		var err error
		parent, err = s.agent(ctx, q, a.ID)
		if err != nil {
			return nil, forbidden()
		}
	}
	if parent.Depth >= s.cfg.Limits.MaxSpawnDepth {
		return nil, fault("limit", "spawn depth exhausted")
	}
	var spawned int
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind='agent' AND json_extract(data,'$.parent') IS NOT NULL AND COALESCE(json_extract(data,'$.owner_managed'),0)=0").Scan(&spawned); err != nil {
		return nil, internal(err)
	}
	if spawned >= s.cfg.Limits.MaxSpawns {
		return nil, fault("limit", "lifetime spawn limit reached")
	}
	if !a.owner() {
		if parent.Config.Sandbox == "bubblewrap" {
			switch parent.Config.Network {
			case "none":
				if template.Network != "none" {
					return nil, forbidden()
				}
			case "allowlist":
				if template.Network != "none" && (template.Network != "allowlist" || !egress.IsSubset(template.Egress.Allow, parent.Config.Egress.Allow)) {
					return nil, forbidden()
				}
			case "host":
			default:
				return nil, forbidden()
			}
		}
		for _, entry := range []struct {
			values        []string
			memory, write bool
		}{{in.Capabilities.BoardsRead, false, false}, {in.Capabilities.BoardsWrite, false, true}, {in.Capabilities.MemoryRead, true, false}, {in.Capabilities.MemoryWrite, true, true}} {
			for _, name := range entry.values {
				if !s.namespace(ctx, q, a.ID, name, entry.memory, entry.write) {
					return nil, forbidden()
				}
			}
		}
		for _, grant := range in.Capabilities.Targets {
			for _, action := range grant.Actions {
				if !s.can(ctx, q, a.ID, action, grant.To) {
					return nil, forbidden()
				}
			}
		}
		for _, name := range in.Capabilities.SpawnTemplates {
			if !contains(s.spawnTemplates(ctx, q, a.ID), name) {
				return nil, forbidden()
			}
		}
		if in.Capabilities.CreateTeams && !s.canCreateTeams(ctx, q, a.ID) {
			return nil, forbidden()
		}
		if in.Capabilities.JoinTeams && !s.canJoinTeams(ctx, q, a.ID) {
			return nil, forbidden()
		}
	}
	// Owner grants must also name existing namespaces/targets and concrete
	// supported actions, so malformed capability records never reach storage.
	for _, entry := range []struct {
		values []string
		memory bool
	}{{in.Capabilities.BoardsRead, false}, {in.Capabilities.BoardsWrite, false}, {in.Capabilities.MemoryRead, true}, {in.Capabilities.MemoryWrite, true}} {
		for _, name := range entry.values {
			if !s.namespaceExists(ctx, q, name, entry.memory) {
				return nil, forbidden()
			}
		}
	}
	for _, grant := range in.Capabilities.Targets {
		if !s.knownAgent(ctx, q, grant.To) {
			return nil, forbidden()
		}
		for _, action := range grant.Actions {
			if action != "message.send" && action != "ping.send" && action != "instruct" {
				return nil, forbidden()
			}
		}
	}
	for _, name := range in.Capabilities.SpawnTemplates {
		if _, ok := s.cfg.Spawn.Templates[name]; !ok {
			return nil, forbidden()
		}
	}
	id := randomID("child")
	template = copyAgent(template)
	template.Workdir = filepath.Join(s.dir, "workspaces", id)
	if template.Network == "" {
		template.Network = "none"
	}
	if err := os.Mkdir(template.Workdir, 0700); err != nil {
		return nil, internal(err)
	}
	s.stagedPaths = append(s.stagedPaths, template.Workdir)
	c := Credential{AgentID: id, InstanceID: randomID("instance"), Generation: 1, Socket: SocketPath(s.dir), Token: randomID("token")}
	path := CredentialPath(s.dir, id)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, internal(err)
	}
	s.stagedPaths = append(s.stagedPaths, path)
	data, _ := json.Marshal(c)
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return nil, internal(err)
	}
	if closeErr != nil {
		return nil, internal(closeErr)
	}
	if _, err = q.ExecContext(ctx, "INSERT INTO credentials(actor,instance,generation,token_hash) VALUES(?,?,?,?)", id, c.InstanceID, c.Generation, hash(c.Token)); err != nil {
		return nil, internal(err)
	}
	deadline, _ := s.deadline(ctx, q)
	info := AgentInfo{ID: id, Parent: a.ID, Template: in.Template, State: "active", Depth: parent.Depth + 1, Config: template, Capabilities: in.Capabilities, CreatedAt: now()}
	if !deadline.IsZero() {
		info.Deadline = deadline.UTC().Format(time.RFC3339Nano)
	}
	if err = save(ctx, q, "agent", "agent_"+id, a.ID, id, "", info); err != nil {
		return nil, internal(err)
	}
	if template.Autonomy.Enabled {
		state, e := loadAutonomy(ctx, q, id)
		if e != nil {
			return nil, internal(e)
		}
		if e = saveAutonomy(ctx, q, state); e != nil {
			return nil, internal(e)
		}
	}
	if err = event(ctx, q, a, "agent.spawned", id, map[string]any{"parent": a.ID, "template": in.Template, "depth": info.Depth}); err != nil {
		return nil, internal(err)
	}
	for key := range info.Config.Env {
		info.Config.Env[key] = "[redacted]"
	}
	return map[string]any{"agent": info}, nil
}

func copyAgent(a managedconfig.Agent) managedconfig.Agent {
	if a.NativeDelivery != nil {
		copy := *a.NativeDelivery
		a.NativeDelivery = &copy
	}
	env := make(map[string]string, len(a.Env))
	for k, v := range a.Env {
		env[k] = v
	}
	a.Env = env
	a.Command = append([]string(nil), a.Command...)
	a.ReadOnlyPaths = append([]string(nil), a.ReadOnlyPaths...)
	a.Autonomy.Boards = append([]string(nil), a.Autonomy.Boards...)
	a.Egress.Allow = append(a.Egress.Allow[:0:0], a.Egress.Allow...)
	for i := range a.Egress.Allow {
		a.Egress.Allow[i].IPs = append([]string(nil), a.Egress.Allow[i].IPs...)
	}
	return a
}

func (s *Store) stopAgent(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	var in struct {
		ID             string   `json:"id"`
		ExpectedAgents []string `json:"expected_agents"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	info, err := s.agent(ctx, q, in.ID)
	if err != nil {
		return nil, fault("not_found", "agent not found")
	}
	if !a.owner() {
		ancestor := info.Parent
		allowed := false
		for depth := 0; depth <= s.cfg.Limits.MaxSpawnDepth; depth++ {
			if ancestor == a.ID {
				allowed = true
				break
			}
			p, err := s.agent(ctx, q, ancestor)
			if err != nil {
				break
			}
			ancestor = p.Parent
		}
		if !allowed {
			return nil, forbidden()
		}
	}
	if info.State == "retired" {
		return nil, fault("conflict", "retired agent cannot be changed")
	}
	if info.State == "revoked" {
		return map[string]any{"id": info.ID, "state": "revoked", "process_stopped": false}, nil
	}
	all, err := list[AgentInfo](ctx, q, "agent", "ORDER BY seq")
	if err != nil {
		return nil, internal(err)
	}
	revoke := []AgentInfo{info}
	ids := map[string]bool{info.ID: true}
	for depth := 0; depth < s.cfg.Limits.MaxSpawnDepth; depth++ {
		for _, child := range all {
			if child.State == "active" && ids[child.Parent] && !ids[child.ID] {
				ids[child.ID] = true
				revoke = append(revoke, child)
			}
		}
	}
	if in.ExpectedAgents != nil {
		if len(in.ExpectedAgents) != len(ids) {
			return nil, fault("conflict", "agent subtree changed; refresh before revoking")
		}
		seen := map[string]bool{}
		for _, id := range in.ExpectedAgents {
			if !ids[id] || seen[id] {
				return nil, fault("conflict", "agent subtree changed; refresh before revoking")
			}
			seen[id] = true
		}
	}
	for _, target := range revoke {
		if err := s.revokeAgent(ctx, q, a, target); err != nil {
			return nil, internal(err)
		}
	}
	return map[string]any{"id": info.ID, "state": "revoked", "revoked_agents": len(revoke), "process_stopped": false}, nil
}

func (s *Store) revokeAgent(ctx context.Context, q queryer, a actor, info AgentInfo) error {
	// Revocation does not assert process termination. The supervisor observes
	// this state and interrupts the pane; a running worker also fails closed
	// when its next authenticated task observation is rejected.
	info.State = "revoked"
	if _, err := q.ExecContext(ctx, "DELETE FROM credentials WHERE actor=?", info.ID); err != nil {
		return err
	}
	if err := save(ctx, q, "agent", "agent_"+info.ID, info.Parent, info.ID, "", info); err != nil {
		return err
	}
	tasks, err := list[taskRecord](ctx, q, "task", "AND target=?", info.ID)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		switch t.Delivery {
		case "pending":
			t.State = "failed"
			t.Delivery = "failed"
		case "dispatching":
			t.Delivery = "uncertain"
		case "cancel_requested":
			t.Delivery = "cancel_uncertain"
		default:
			continue
		}
		t.Version++
		t.UpdatedAt = now()
		t.LeaseToken = ""
		t.Instance = ""
		t.Expires = 0
		if err = saveTask(ctx, q, t); err != nil {
			return err
		}
	}
	return event(ctx, q, a, "agent.revoked", info.ID, nil)
}

func (s *Store) deadline(ctx context.Context, q queryer) (time.Time, error) {
	var raw string
	err := q.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='deadline'").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, raw)
}
func (s *Store) RuntimeDeadline() (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deadline(context.Background(), s.db)
}
func (s *Store) startRuntime(ctx context.Context) error {
	// Human-led native workspaces end with owner cancellation/down. Preserve
	// any explicit existing deadline rather than silently renewing/removing it.
	if s.cfg.InteractiveWorkspace {
		return nil
	}
	d := time.Now().Add(time.Duration(s.cfg.Limits.MaxRuntimeSeconds) * time.Second).UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, "INSERT INTO metadata(key,value) VALUES('deadline',?) ON CONFLICT(key) DO NOTHING", d)
	return err
}

type ExportRecord struct {
	Kind string          `json:"kind"`
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data"`
}
type ExportPage struct {
	Records []ExportRecord `json:"records"`
	Cursor  string         `json:"cursor"`
}

// Export is an owner-filesystem API: coordination records only, with task
// leases and process environment values removed. Private memory is sensitive.
func (s *Store) Export(ctx context.Context, after string, limit int) (ExportPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.export(ctx, s.db, after, limit)
}
func (s *Store) export(ctx context.Context, q queryer, after string, limit int) (ExportPage, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	var seq int64
	if after != "" {
		if err := q.QueryRowContext(ctx, "SELECT seq FROM records WHERE id=?", after).Scan(&seq); err != nil {
			return ExportPage{}, fmt.Errorf("invalid export cursor")
		}
	}
	rows, err := q.QueryContext(ctx, "SELECT kind,id,data FROM records WHERE seq>? ORDER BY seq LIMIT ?", seq, limit)
	if err != nil {
		return ExportPage{}, err
	}
	defer rows.Close()
	page := ExportPage{Records: []ExportRecord{}, Cursor: after}
	size := 0
	for rows.Next() {
		var v ExportRecord
		var raw string
		if err = rows.Scan(&v.Kind, &v.ID, &raw); err != nil {
			return page, err
		}
		v.Data = json.RawMessage(raw)
		switch v.Kind {
		case "task":
			var t Task
			if err = json.Unmarshal(v.Data, &t); err != nil {
				return page, err
			}
			v.Data, _ = json.Marshal(t)
		case "agent":
			var a AgentInfo
			if err = json.Unmarshal(v.Data, &a); err != nil {
				return page, err
			}
			for k := range a.Config.Env {
				a.Config.Env[k] = "[redacted]"
			}
			v.Data, _ = json.Marshal(a)
		}
		if size+len(v.Data) > maxWireBytes/2 {
			break
		}
		size += len(v.Data)
		page.Records = append(page.Records, v)
		page.Cursor = v.ID
	}
	return page, rows.Err()
}
