package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

type Request struct {
	Operation           string          `json:"operation"`
	ResearchRunnerProof string          `json:"research_runner_proof,omitempty"`
	RequestID           string          `json:"request_id,omitempty"`
	Args                json.RawMessage `json:"args"`
}
type Fault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (f *Fault) Error() string          { return f.Code + ": " + f.Message }
func (f *Fault) ErrorCode() string      { return f.Code }
func fault(code, message string) *Fault { return &Fault{Code: code, Message: message} }
func forbidden() *Fault                 { return fault("forbidden", "operation not permitted") }
func invalid(err error) *Fault          { return fault("invalid", err.Error()) }
func internal(err error) *Fault {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fault("cancelled", "request cancelled")
	}
	return fault("internal", "coordination storage operation failed")
}

func isWrite(op string) bool {
	switch op {
	case "native.configure", "native.approve", "native.register", "native.claim", "native.event":
		return true
	case "agent.role":
		return true
	case "capacity.update", "research.publish", "research.control", "control.update", "worker.next", "autonomy.control", "autonomy.ack", "memory.delete", "memory.prune", "memory.index":
		return true
	case "board.post", "message.send", "ping.send", "instruct", "task.claim", "task.complete", "task.stopped", "task.cancel", "task.verify", "task.resolve", "memory.put", "memory.patch", "agent.retire", "agent.add", "agent.label", "agent.spawn", "agent.stop", "team.create", "team.invite", "team.join", "team.leave", "team.remove", "team.dissolve", "team.update", "team.transfer", "team.accept-owner", "team.move", "conversation.begin", "conversation.reset":
		return true
	}
	return false
}

// Call binds the actor to a credential before decoding operation arguments.
// Every mutating call requires an idempotency key, scoped to its actor.
func (s *Store) Call(ctx context.Context, token string, r Request) (json.RawMessage, *Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fault("unavailable", "store is closed")
	}
	s.stagedPaths = nil
	committed := false
	defer func() {
		if !committed {
			for i := len(s.stagedPaths) - 1; i >= 0; i-- {
				_ = os.Remove(s.stagedPaths[i])
			}
		}
		s.stagedPaths = nil
	}()
	a, err := s.authenticate(ctx, token)
	if err != nil {
		return nil, fault("unauthorized", "invalid credential")
	}
	if len(r.Args) == 0 {
		r.Args = json.RawMessage(`{}`)
	}
	if len(r.Args) > 2*s.cfg.Limits.MaxMessageBytes+32768 {
		return nil, fault("invalid", "request arguments exceed limit")
	}
	if problem := s.authenticateResearchRunner(ctx, s.db, &a, r.ResearchRunnerProof); problem != nil {
		return nil, problem
	}
	var q queryer = s.db
	var tx *sql.Tx
	digest := ""
	if isWrite(r.Operation) {
		if !validKey(r.RequestID) {
			return nil, fault("invalid", "mutating operations require a request_id of 1–128 characters")
		}
		var normalized any
		d := json.NewDecoder(strings.NewReader(string(r.Args)))
		d.UseNumber()
		if err = d.Decode(&normalized); err != nil {
			return nil, invalid(err)
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			return nil, fault("invalid", "expected one JSON value")
		}
		canonical, _ := json.Marshal(normalized)
		digest = hash(r.Operation + "\x00" + string(canonical))
		tx, err = s.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, internal(err)
		}
		defer tx.Rollback()
		q = tx
		// CLI credential rotation can use a separate SQLite connection. Bind
		// authorization and receipt lookup to this transaction's snapshot. If a
		// concurrent rotation commits after this read, SQLite rejects upgrading
		// the stale snapshot to a writer rather than committing a revoked actor.
		a, err = authenticateWith(ctx, tx, token)
		if err != nil {
			return nil, fault("unauthorized", "invalid credential")
		}
		if problem := s.authenticateResearchRunner(ctx, tx, &a, r.ResearchRunnerProof); problem != nil {
			return nil, problem
		}
		var prior, priorDigest string
		requestActor := a.ID + "/" + a.Instance
		err = tx.QueryRowContext(ctx, "SELECT digest,response FROM requests WHERE actor=? AND request_id=?", requestActor, r.RequestID).Scan(&priorDigest, &prior)
		if err == nil {
			if priorDigest != digest {
				return nil, fault("conflict", "request_id was already used for different arguments")
			}
			if r.Operation == "native.claim" {
				// A receipt can confirm reservation after a lost response, but
				// must never resubmit native input or resurrect a revoked source.
				var claimed struct {
					Delivery *struct {
						ID string `json:"id"`
					} `json:"delivery"`
				}
				if err := json.Unmarshal([]byte(prior), &claimed); err != nil {
					return nil, internal(err)
				}
				id := ""
				if claimed.Delivery != nil {
					id = claimed.Delivery.ID
				}
				data, _ := json.Marshal(map[string]any{"delivery": nil, "reserved_id": id, "reason": "reservation already exists; native input is never replayed from a receipt"})
				return data, nil
			}
			memoryReplay, problem := s.memoryReplay(ctx, tx, a, r, json.RawMessage(prior))
			if problem != nil {
				return nil, problem
			}
			replay, err := s.autonomyReplay(ctx, tx, a, r.Operation, memoryReplay)
			if err != nil {
				return nil, internal(err)
			}
			if err := s.recordExposures(ctx, tx, a, r.Operation, replay); err != nil {
				return nil, internal(err)
			}
			if err := tx.Commit(); err != nil {
				return nil, internal(err)
			}
			return replay, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, internal(err)
		}
		deadline, e := s.admissionDeadline(ctx, q)
		if e != nil {
			return nil, internal(e)
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			switch r.Operation {
			case "autonomy.ack":
			case "autonomy.control":
				var in struct {
					Mode string `json:"mode"`
				}
				_ = json.Unmarshal(r.Args, &in)
				if in.Mode != "paused" && in.Mode != "stopped" {
					return nil, fault("limit", "session runtime budget exhausted")
				}
			case "research.control":
				var in struct {
					Action string `json:"action"`
				}
				_ = json.Unmarshal(r.Args, &in)
				if in.Action != "pause" && in.Action != "stop" {
					return nil, fault("limit", "session runtime budget exhausted")
				}
			case "research.publish", "task.complete", "task.stopped", "task.cancel", "task.verify", "agent.stop", "agent.retire", "agent.label", "team.leave", "team.remove", "team.dissolve", "conversation.reset", "memory.delete", "memory.prune", "native.event":
			case "native.configure":
				var in struct {
					Mode string `json:"mode"`
				}
				_ = json.Unmarshal(r.Args, &in)
				if in.Mode != "manual" {
					return nil, fault("limit", "session runtime budget exhausted")
				}
			case "task.resolve":
				var in struct {
					Resolution string `json:"resolution"`
				}
				_ = json.Unmarshal(r.Args, &in)
				if in.Resolution != "fail" {
					return nil, fault("limit", "session runtime budget exhausted")
				}
			default:
				return nil, fault("limit", "session runtime budget exhausted")
			}
		}
	}
	result, e := s.dispatch(ctx, q, a, r)
	if e != nil {
		return nil, e
	}
	if tx != nil && a.owner() && !a.researchRunner && r.Operation != "research.control" {
		if err := recordResearchOwnerMutation(ctx, q, a, r); err != nil {
			return nil, internal(err)
		}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, internal(err)
	}
	if err := s.recordExposures(ctx, q, a, r.Operation, data); err != nil {
		return nil, internal(err)
	}
	if tx != nil {
		var records, requests int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM records").Scan(&records); err != nil {
			return nil, internal(err)
		}
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM requests").Scan(&requests); err != nil {
			return nil, internal(err)
		}
		reserved, err := lifecycleReserve(ctx, tx)
		if err != nil {
			return nil, internal(err)
		}
		// Active tasks reserve enough slots for claim/completion/verification
		// or cancellation/stop. Ordinary writes cannot consume this capacity.
		if records+reserved > s.cfg.Limits.MaxRecords || requests+1+reserved > s.cfg.Limits.MaxRequests {
			return nil, fault("limit", "managed storage budget reached (including active-task reserves); archive this run before continuing")
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO requests(actor,request_id,digest,response) VALUES(?,?,?,?)", a.ID+"/"+a.Instance, r.RequestID, digest, string(data)); err != nil {
			return nil, internal(err)
		}
		if err = tx.Commit(); err != nil {
			return nil, internal(err)
		}
		committed = true
	}
	return data, nil
}

func (s *Store) dispatch(ctx context.Context, q queryer, a actor, r Request) (any, *Fault) {
	if r.Operation == "control.get" || r.Operation == "control.update" || r.Operation == "boundaries.get" {
		return s.control(ctx, q, a, r)
	}
	switch r.Operation {
	case "native.configure", "native.approve", "native.register", "native.peek", "native.claim", "native.event", "native.status":
		return s.native(ctx, q, a, r)
	case "research.status", "research.publish", "research.control", "research.heartbeat":
		return s.researchControl(ctx, q, a, r)
	case "capacity.update":
		return s.updateCapacity(ctx, q, a, r.Args)
	case "capacity.get":
		return s.capacity(ctx, q, a, r.Args)
	case "budget.status":
		return s.budgetStatus(ctx, q, a, r.Args)
	case "worker.next", "autonomy.status", "autonomy.control", "autonomy.ack", "wake.read":
		return s.autonomy(ctx, q, a, r)
	case "message.get":
		var in struct {
			ID string `json:"id"`
		}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		var m Message
		if err := get(ctx, q, "message", in.ID, &m); err != nil || !s.canSeeMessage(ctx, q, a, m) {
			return nil, fault("not_found", "visible message not found")
		}
		return map[string]any{"message": m}, nil
	case "conversation.status", "conversation.begin", "conversation.reset":
		return s.conversation(ctx, q, a, r)
	case "identity":
		var in struct{}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		result := map[string]any{"agent_id": a.ID, "instance_id": a.Instance, "generation": a.Generation, "owner": a.owner(), "session": s.cfg.Session}
		control, controlErr := controls(ctx, q)
		if controlErr != nil {
			return nil, internal(controlErr)
		}
		result["boundaries_version"] = control.Version
		if !a.owner() {
			info, err := s.agent(ctx, q, a.ID)
			if err != nil {
				return nil, internal(err)
			}
			result["role"], result["role_version"], result["native_only"] = info.Config.Role, info.RoleVersion, info.Config.NativeOnly
			result["boards_read"] = s.readableBoards(ctx, q, a.ID)
			result["spawn_templates"] = s.spawnTemplates(ctx, q, a.ID)
			result["can_create_teams"] = s.canCreateTeams(ctx, q, a.ID)
			result["private_memory_namespace"] = "private"
		}
		deadline, err := s.deadline(ctx, q)
		if err != nil {
			return nil, internal(err)
		}
		result["runtime_deadline"] = deadline
		capacity, problem := s.capacityFor(ctx, q, a.ID)
		if problem != nil {
			return nil, problem
		}
		result["capacity"] = capacity
		return result, nil
	case "board.post", "message.send", "ping.send", "instruct":
		return s.send(ctx, q, a, r)
	case "message.list", "board.list":
		return s.messages(ctx, q, a, r.Args, r.Operation == "board.list")
	case "task.summary":
		return s.taskSummary(ctx, q, a, r.Args)
	case "task.get", "task.list", "task.claim", "task.complete", "task.stopped", "task.cancel", "task.verify", "task.resolve":
		return s.tasks(ctx, q, a, r)
	case "memory.get", "memory.list", "memory.put", "memory.patch", "memory.head", "memory.search", "memory.delete", "memory.prune", "memory.index":
		return s.memory(ctx, q, a, r)
	case "agent.list", "agent.retire", "agent.add", "agent.label", "agent.role", "agent.spawn", "agent.stop":
		return s.agents(ctx, q, a, r)
	case "team.create", "team.list", "team.invite", "team.join", "team.leave", "team.remove", "team.dissolve", "team.update", "team.transfer", "team.accept-owner", "team.move":
		return s.teams(ctx, q, a, r)
	case "export":
		if !a.owner() {
			return nil, forbidden()
		}
		var in struct {
			After string `json:"after"`
			Limit int    `json:"limit"`
		}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		page, err := s.export(ctx, q, in.After, in.Limit)
		if err != nil {
			return nil, invalid(err)
		}
		return page, nil
	case "activity":
		if !a.owner() {
			return nil, forbidden()
		}
		var in struct {
			Limit int `json:"limit"`
		}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		if in.Limit <= 0 || in.Limit > 50 {
			in.Limit = 20
		}
		messages, err := list[Message](ctx, q, "message", "ORDER BY seq DESC LIMIT ?", in.Limit)
		if err != nil {
			return nil, internal(err)
		}
		tasks, err := list[taskRecord](ctx, q, "task", "ORDER BY seq DESC LIMIT ?", in.Limit)
		if err != nil {
			return nil, internal(err)
		}
		events, err := list[Event](ctx, q, "event", "ORDER BY seq DESC LIMIT ?", in.Limit)
		if err != nil {
			return nil, internal(err)
		}
		// Bound the combined content page independently of per-item limits.
		size := 0
		mp := []Message{}
		for _, m := range messages {
			raw, _ := json.Marshal(m)
			if size+len(raw) > maxWireBytes/3 {
				break
			}
			size += len(raw)
			mp = append(mp, m)
		}
		size = 0
		tp := []Task{}
		viewTask, p := s.taskViews(ctx, q, a)
		if p != nil {
			return nil, p
		}
		for _, record := range tasks {
			t := viewTask(record)
			raw, _ := json.Marshal(t)
			if size+len(raw) > maxWireBytes/3 {
				break
			}
			size += len(raw)
			tp = append(tp, t)
		}
		deadline, err := s.deadline(ctx, q)
		if err != nil {
			return nil, internal(err)
		}
		return map[string]any{"messages": mp, "tasks": tp, "events": events, "runtime_deadline": deadline}, nil
	case "status":
		var in struct{}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		if !a.owner() {
			return nil, forbidden()
		}
		messages, err := count(ctx, q, "message")
		if err != nil {
			return nil, internal(err)
		}
		tasks, err := list[taskRecord](ctx, q, "task", "ORDER BY seq")
		if err != nil {
			return nil, internal(err)
		}
		states := map[string]int{}
		delivery := map[string]int{}
		for _, t := range tasks {
			states[t.State]++
			delivery[t.Delivery]++
		}
		cfg := s.Config()
		for id, agent := range cfg.Agents {
			for name := range agent.Env {
				agent.Env[name] = "[redacted]"
			}
			cfg.Agents[id] = agent
		}
		for id, template := range cfg.Spawn.Templates {
			for key := range template.Env {
				template.Env[key] = "[redacted]"
			}
			cfg.Spawn.Templates[id] = template
		}
		deadline, err := s.deadline(ctx, q)
		if err != nil {
			return nil, internal(err)
		}
		agents, err := list[AgentInfo](ctx, q, "agent", "ORDER BY seq")
		if err != nil {
			return nil, internal(err)
		}
		active := len(cfg.Agents)
		for _, info := range agents {
			if _, static := cfg.Agents[info.ID]; static {
				if info.State != "active" {
					active--
				}
			} else if info.State == "active" {
				active++
			}
		}
		return map[string]any{"session": s.cfg.Session, "agents": len(s.cfg.Agents), "active_agents": active, "runtime_deadline": deadline, "runtime_expired": !deadline.IsZero() && time.Now().After(deadline), "messages": messages, "tasks": len(tasks), "task_states": states, "deliveries": delivery, "config": cfg}, nil
	case "events", "events.list", "event.list":
		return s.listEvents(ctx, q, a, r.Args)
	case "exposure.list":
		return s.listExposures(ctx, q, a, r.Args)
	default:
		return nil, fault("unsupported", "unsupported managed operation")
	}
}

func (s *Store) send(ctx context.Context, q queryer, a actor, r Request) (any, *Fault) {
	var in struct {
		To      []string `json:"to"`
		Board   string   `json:"board"`
		Text    string   `json:"text"`
		ReplyTo string   `json:"reply_to"`
	}
	if err := strictJSON(r.Args, &in); err != nil {
		return nil, invalid(err)
	}
	if strings.TrimSpace(in.Text) == "" || len(in.Text) > s.cfg.Limits.MaxMessageBytes {
		return nil, fault("invalid", "text is empty or exceeds max_message_bytes")
	}
	m := Message{ID: randomID("msg"), Kind: "note", From: a.ID, Text: in.Text, CreatedAt: now()}
	if r.Operation == "board.post" {
		if in.Board == "" || len(in.To) != 0 {
			return nil, fault("invalid", "board.post requires board and no direct recipients")
		}
		if !s.namespaceExists(ctx, q, in.Board, false) {
			return nil, forbidden()
		}
		if !a.owner() && !s.namespace(ctx, q, a.ID, in.Board, false, true) {
			return nil, forbidden()
		}
		m.Board = in.Board
	} else {
		if in.Board != "" || len(in.To) == 0 {
			return nil, fault("invalid", "direct messages require recipients and no board")
		}
		seen := map[string]bool{}
		for _, to := range in.To {
			if !s.knownAgent(ctx, q, to) {
				return nil, forbidden()
			}
			if !a.owner() && !s.can(ctx, q, a.ID, r.Operation, to) {
				return nil, forbidden()
			}
			if !seen[to] {
				m.To = append(m.To, to)
				seen[to] = true
			}
		}
		sort.Strings(m.To)
	}
	if r.Operation == "ping.send" {
		m.Kind = "ping"
	}
	if r.Operation == "instruct" {
		if len(m.To) != 1 {
			return nil, fault("invalid", "instruct requires exactly one recipient")
		}
		m.Kind = "instruction"
		m.TaskID = randomID("task")
	}
	if in.ReplyTo != "" {
		thread, problem := s.replyThread(ctx, q, a, in.ReplyTo, m)
		if problem != nil {
			return nil, problem
		}
		m.ReplyTo, m.ThreadID = in.ReplyTo, thread
	} else {
		m.ThreadID = m.ID
	}
	n, err := count(ctx, q, "message")
	if err != nil {
		return nil, internal(err)
	}
	if n >= s.cfg.Limits.MaxMessages {
		return nil, fault("limit", "stored message limit reached")
	}
	var t *Task
	m.Provenance, err = s.messageProvenance(ctx, q, a, m.ID)
	if err != nil {
		return nil, internal(err)
	}
	if m.TaskID != "" {
		if target, e := s.agent(ctx, q, m.To[0]); e == nil && target.Config.Autonomy.Enabled && m.Provenance.Hop > target.Config.Autonomy.MaxHops {
			return nil, fault("limit", "causal hop budget exhausted")
		}
		n, err = count(ctx, q, "task")
		if err != nil {
			return nil, internal(err)
		}
		if n >= s.cfg.Limits.MaxTasks {
			return nil, fault("limit", "stored task limit reached")
		}
		record := taskRecord{Task: Task{ID: m.TaskID, MessageID: m.ID, From: a.ID, To: m.To[0], Text: m.Text, State: "proposed", Delivery: "pending", Version: 1, CreatedAt: m.CreatedAt, UpdatedAt: m.CreatedAt}}
		record.ReplyTo, record.ThreadID = m.ReplyTo, m.ThreadID
		record.Origin = &TaskOrigin{Kind: "instruction", Provenance: *m.Provenance}
		if err = saveTask(ctx, q, record); err != nil {
			return nil, internal(err)
		}
		t = &record.Task
	}
	if err = save(ctx, q, "message", m.ID, m.From, "", m.Board, m); err != nil {
		return nil, internal(err)
	}
	if err = s.enqueueWakes(ctx, q, m); err != nil {
		return nil, internal(err)
	}
	if err = event(ctx, q, a, r.Operation, m.ID, map[string]any{"task_id": m.TaskID, "board": m.Board, "recipients": m.To}); err != nil {
		return nil, internal(err)
	}
	return map[string]any{"message": m, "task": t}, nil
}

func (s *Store) messages(ctx context.Context, q queryer, a actor, raw json.RawMessage, boardOnly bool) (any, *Fault) {
	var in struct {
		Board string `json:"board"`
		After string `json:"after"`
		Limit int    `json:"limit"`
		Scope string `json:"scope"`
		Agent string `json:"agent"`
		From  string `json:"from"`
		To    string `json:"to"`
		Query string `json:"query"`
		Order string `json:"order"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	if boardOnly && in.Scope == "" {
		in.Scope = "board"
	}
	if in.Scope != "" && in.Scope != "all" && in.Scope != "inbox" && in.Scope != "board" {
		return nil, fault("invalid", "scope must be all, inbox or board")
	}
	if in.Order != "" && in.Order != "asc" && in.Order != "desc" {
		return nil, fault("invalid", "order must be asc or desc")
	}
	if len(in.Query) > 4096 {
		return nil, fault("invalid", "query exceeds history bounds")
	}
	if in.Agent != "" && !a.owner() && in.Agent != a.ID {
		return nil, forbidden()
	}
	if in.Board != "" && !a.owner() && !s.namespace(ctx, q, a.ID, in.Board, false, false) {
		return nil, forbidden()
	}
	if in.Scope == "inbox" && in.Board != "" {
		return nil, fault("invalid", "inbox cannot select a board")
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 100
	}
	condition := ""
	values := []any{}
	if in.After != "" {
		var m Message
		if err := get(ctx, q, "message", in.After, &m); err != nil || !s.canSeeMessage(ctx, q, a, m) {
			return nil, fault("invalid", "invalid visible message cursor")
		}
		var after int64
		if err := q.QueryRowContext(ctx, "SELECT seq FROM records WHERE kind='message' AND id=?", in.After).Scan(&after); err != nil {
			return nil, internal(err)
		}
		cmp := ">"
		if in.Order == "desc" {
			cmp = "<"
		}
		condition += " AND seq" + cmp + "?"
		values = append(values, after)
	}
	// Every visibility and history filter precedes LIMIT. An unrelated busy
	// board or recipient cannot hide a matching older row behind a recent page.
	if !a.owner() {
		condition += " AND (actor=? OR EXISTS (SELECT 1 FROM json_each(records.data,'$.to') WHERE value=?)"
		values = append(values, a.ID, a.ID)
		for _, board := range s.readableBoards(ctx, q, a.ID) {
			condition += " OR (scope=? AND seq>?)"
			values = append(values, board, s.boardFloor(ctx, q, a.ID, board))
		}
		condition += ")"
	}
	if in.Board != "" {
		condition += " AND scope=?"
		values = append(values, in.Board)
		if !a.owner() {
			condition += " AND seq>?"
			values = append(values, s.boardFloor(ctx, q, a.ID, in.Board))
		}
	}
	if in.Scope == "board" {
		condition += " AND scope<>''"
	}
	if in.Scope == "inbox" {
		condition += " AND scope=''"
		recipient := in.Agent
		if recipient == "" && !a.owner() {
			recipient = a.ID
		}
		if recipient != "" {
			condition += " AND EXISTS (SELECT 1 FROM json_each(records.data,'$.to') WHERE value=?)"
			values = append(values, recipient)
		}
	} else if in.Agent != "" {
		condition += " AND (actor=? OR EXISTS (SELECT 1 FROM json_each(records.data,'$.to') WHERE value=?))"
		values = append(values, in.Agent, in.Agent)
	}
	if in.From != "" {
		condition += " AND actor=?"
		values = append(values, in.From)
	}
	if in.To != "" {
		condition += " AND EXISTS (SELECT 1 FROM json_each(records.data,'$.to') WHERE value=?)"
		values = append(values, in.To)
	}
	if in.Query != "" {
		condition += " AND (instr(lower(id),lower(?))>0 OR instr(lower(actor),lower(?))>0 OR instr(lower(scope),lower(?))>0 OR instr(lower(coalesce(json_extract(data,'$.text'),'')),lower(?))>0 OR EXISTS (SELECT 1 FROM json_each(records.data,'$.to') WHERE instr(lower(value),lower(?))>0))"
		values = append(values, in.Query, in.Query, in.Query, in.Query, in.Query)
	}
	order := "ASC"
	if in.Order == "desc" {
		order = "DESC"
	}
	condition += " ORDER BY seq " + order + " LIMIT ?"
	values = append(values, in.Limit+1)
	all, err := list[Message](ctx, q, "message", condition, values...)
	if err != nil {
		return nil, internal(err)
	}
	visible := []Message{}
	cursor := in.After
	size := 0
	for _, m := range all {
		raw, _ := json.Marshal(m)
		if len(visible) >= in.Limit || size+len(raw) > maxWireBytes/2 {
			break
		}
		size += len(raw)
		visible = append(visible, m)
		cursor = m.ID
	}
	control, err := controls(ctx, q)
	if err != nil {
		return nil, internal(err)
	}
	return map[string]any{"messages": visible, "cursor": cursor, "has_more": len(all) > len(visible), "boundaries_version": control.Version}, nil
}

func (s *Store) tasks(ctx context.Context, q queryer, a actor, r Request) (any, *Fault) {
	viewTask, p := s.taskViews(ctx, q, a)
	if p != nil {
		return nil, p
	}
	if r.Operation == "task.list" {
		var in struct {
			State string `json:"state"`
			Limit int    `json:"limit"`
			After string `json:"after"`
			Query string `json:"query"`
			To    string `json:"to"`
			Order string `json:"order"`
		}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		if in.Limit <= 0 || in.Limit > 200 {
			in.Limit = 100
		}
		if len(in.Query) > 512 || (in.Order != "" && in.Order != "asc" && in.Order != "desc") {
			return nil, fault("invalid", "task query is limited to 512 bytes; order must be asc or desc")
		}
		var after int64
		if in.After != "" {
			t, err := loadTask(ctx, q, in.After)
			if err != nil || !canSeeTask(a, t.Task) {
				return nil, fault("invalid", "invalid visible task cursor")
			}
			if err = q.QueryRowContext(ctx, "SELECT seq FROM records WHERE id=?", in.After).Scan(&after); err != nil {
				return nil, internal(err)
			}
		}
		condition := "AND seq>?"
		values := []any{after}
		if in.Order == "desc" {
			condition = ""
			values = nil
			if in.After != "" {
				condition = "AND seq<?"
				values = []any{after}
			}
		}
		if !a.owner() {
			condition += " AND (actor=? OR target=?)"
			values = append(values, a.ID, a.ID)
		}
		if in.State != "" {
			condition += " AND json_extract(data,'$.state')=?"
			values = append(values, in.State)
		}
		if in.To != "" {
			condition += " AND target=?"
			values = append(values, in.To)
		}
		if in.Query != "" {
			condition += " AND (instr(lower(id),lower(?))>0 OR instr(lower(coalesce(json_extract(data,'$.text'),'')),lower(?))>0 OR instr(lower(coalesce(json_extract(data,'$.result'),'')),lower(?))>0)"
			values = append(values, in.Query, in.Query, in.Query)
		}
		if in.Order == "desc" {
			condition += " ORDER BY seq DESC LIMIT ?"
		} else {
			condition += " ORDER BY seq LIMIT ?"
		}
		values = append(values, in.Limit+1)
		all, err := list[taskRecord](ctx, q, "task", condition, values...)
		if err != nil {
			return nil, internal(err)
		}
		visible := []Task{}
		cursor := in.After
		size := 0
		for _, t := range all {
			if len(visible) >= in.Limit {
				break
			}
			view := viewTask(t)
			raw, _ := json.Marshal(view)
			if size+len(raw) > maxWireBytes/2 {
				break
			}
			size += len(raw)
			visible = append(visible, view)
			cursor = t.ID
		}
		if len(visible) == 0 && len(all) > 0 {
			return nil, fault("limit", "task exceeds list response size; use task.get for the exact task")
		}
		return map[string]any{"tasks": visible, "cursor": cursor, "has_more": len(all) > len(visible)}, nil
	}
	if r.Operation == "task.claim" {
		if a.owner() {
			return nil, forbidden()
		}
		if problem := s.autonomyBeforeClaim(ctx, q, a); problem != nil {
			return nil, problem
		}
		info, e := s.agent(ctx, q, a.ID)
		if e != nil {
			return nil, internal(e)
		}
		if info.Config.Conversation == "persistent" {
			conversation, err := loadConversation(ctx, q, a.ID)
			if err != nil {
				return nil, internal(err)
			}
			if conversation.State != "ready" {
				return nil, fault("conflict", "conversation requires explicit owner reset before claiming another turn")
			}
		}
		var in struct{}
		if err := strictJSON(r.Args, &in); err != nil {
			return nil, invalid(err)
		}
		active, err := list[taskRecord](ctx, q, "task", "AND target=? AND json_extract(data,'$.delivery') IN ('dispatching','cancel_requested','uncertain','cancel_uncertain') LIMIT 1", a.ID)
		if err != nil {
			return nil, internal(err)
		}
		if len(active) > 0 {
			return nil, fault("conflict", "agent already has active or uncertain work; stop and resolve it first")
		}
		var concurrent int
		if err = q.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind='task' AND json_extract(data,'$.delivery') IN ('dispatching','cancel_requested','uncertain','cancel_uncertain')").Scan(&concurrent); err != nil {
			return nil, internal(err)
		}
		if concurrent >= s.cfg.Limits.MaxConcurrentTasks {
			return nil, fault("busy", "concurrent task budget occupied; unresolved deliveries retain their slots")
		}
		all, err := list[taskRecord](ctx, q, "task", "AND target=? AND json_extract(data,'$.delivery')='pending' AND json_extract(data,'$.state')='proposed' ORDER BY seq LIMIT 1", a.ID)
		if err != nil {
			return nil, internal(err)
		}
		if len(all) == 0 {
			return map[string]any{"task": nil}, nil
		}
		t := all[0]
		if problem := s.reserveTurnBudget(ctx, q, &t); problem != nil {
			return nil, problem
		}
		t.State = "running"
		t.Delivery = "dispatching"
		t.Version++
		t.UpdatedAt = now()
		t.LeaseToken = randomID("lease")
		t.Instance = a.Instance
		t.Expires = time.Now().Add(time.Duration(s.cfg.Limits.MaxRuntimeSeconds+15) * time.Second).Unix()
		deadline, e := s.admissionDeadline(ctx, q)
		if e != nil {
			return nil, internal(e)
		}
		if !deadline.IsZero() && deadline.Add(15*time.Second).Unix() < t.Expires {
			t.Expires = deadline.Add(15 * time.Second).Unix()
		}
		if err = saveTask(ctx, q, t); err != nil {
			return nil, internal(err)
		}
		if err = s.autonomyClaimed(ctx, q, t); err != nil {
			return nil, internal(err)
		}
		if err = event(ctx, q, a, "task.claimed", t.ID, map[string]any{"from": t.From, "to": t.To}); err != nil {
			return nil, internal(err)
		}
		return map[string]any{"task": t.Task, "lease": t.LeaseToken, "lease_expires": t.Expires, "role": info.Config.Role, "role_version": info.RoleVersion}, nil
	}
	var in struct {
		ID         string          `json:"id"`
		Lease      string          `json:"lease"`
		Version    int             `json:"version"`
		Result     string          `json:"result"`
		Usage      json.RawMessage `json:"usage"`
		Status     string          `json:"status"`
		Resolution string          `json:"resolution"`
		SessionID  string          `json:"session_id"`
		Skipped    bool            `json:"skipped"`
		Trace      *TraceSummary   `json:"trace"`
	}
	if err := strictJSON(r.Args, &in); err != nil {
		return nil, invalid(err)
	}
	t, err := loadTask(ctx, q, in.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fault("not_found", "task not found")
		}
		return nil, internal(err)
	}
	if !canSeeTask(a, t.Task) {
		return nil, fault("not_found", "task not found")
	}
	if r.Operation == "task.get" {
		// A worker must observe its exact execution attempt, not just the
		// logical task ID: recovery and explicit retry can replace its lease
		// while leaving the same task running. This validity check exposes no
		// lease material and is unavailable to the issuer or owner.
		if in.Lease != "" {
			if a.ID != t.To || a.owner() {
				return nil, forbidden()
			}
			if t.Instance != a.Instance || t.LeaseToken == "" || in.Lease != t.LeaseToken ||
				(t.Delivery != "dispatching" && t.Delivery != "cancel_requested") ||
				(t.Delivery == "dispatching" && t.Expires < time.Now().Unix()) {
				return nil, fault("conflict", "task lease is no longer valid")
			}
		}
		return map[string]any{"task": viewTask(t)}, nil
	}
	if in.Version != t.Version {
		return nil, fault("conflict", "task version changed; read it again")
	}
	preserveLease := false
	switch r.Operation {
	case "task.complete":
		if a.ID != t.To || t.Instance != a.Instance || t.LeaseToken == "" || in.Lease != t.LeaseToken || t.Expires < time.Now().Unix() || t.Delivery != "dispatching" || t.State != "running" {
			return nil, fault("conflict", "task lease is no longer valid")
		}
		if len(in.Result) > s.cfg.Limits.MaxMessageBytes {
			return nil, fault("invalid", "result exceeds max_message_bytes")
		}
		if len(in.Usage) > 16384 {
			return nil, fault("invalid", "usage exceeds observation limit")
		}
		if len(in.Usage) > 0 && !json.Valid(in.Usage) {
			return nil, fault("invalid", "invalid usage observation")
		}
		t.Usage = in.Usage
		if in.Status != "submitted" && in.Status != "failed" {
			return nil, fault("invalid", "status must be submitted or failed")
		}
		if in.Trace != nil {
			if !strings.HasPrefix(in.Trace.ID, t.ID+"-") {
				return nil, fault("invalid", "trace must belong to this task")
			}
			verified, err := VerifyTrace(s.dir, t.To, in.Trace.ID, in.Trace.MAC)
			if err != nil || verified.Records != in.Trace.Records || verified.Bytes != in.Trace.Bytes || !in.Trace.Complete {
				return nil, fault("invalid", "native trace could not be authenticated")
			}
			t.Trace = &verified
		}
		info, err := s.agent(ctx, q, t.To)
		if err != nil {
			return nil, internal(err)
		}
		if info.Config.Trace.Enabled && !in.Skipped && t.Trace == nil {
			return nil, fault("invalid", "configured native trace is required before completion")
		}
		t.State = in.Status
		t.Result = in.Result
		t.Delivery = "acknowledged"
		if t.State == "failed" {
			t.Delivery = "failed"
		}
		if in.Skipped {
			if t.Origin == nil || t.Origin.Kind != "observation" || in.Status != "submitted" || in.SessionID != "" {
				return nil, fault("invalid", "only an unexecuted observation turn may be skipped")
			}
			c, err := loadConversation(ctx, q, a.ID)
			if err != nil {
				return nil, internal(err)
			}
			if c.State != "ready" {
				return nil, fault("conflict", "begun native turns cannot be skipped")
			}
		} else if problem := s.finishConversation(ctx, q, a, t, in.SessionID); problem != nil {
			return nil, problem
		}
	case "task.cancel":
		if !a.owner() && a.ID != t.From {
			return nil, forbidden()
		}
		if t.State == "verified" || t.State == "failed" || t.State == "cancelled" {
			return nil, fault("conflict", "task is already terminal")
		}
		t.State = "cancelled"
		if t.Delivery == "dispatching" {
			t.Delivery = "cancel_requested"
			preserveLease = true
		} else if t.Delivery == "uncertain" {
			t.Delivery = "cancel_uncertain"
		} else {
			t.Delivery = "stopped"
		}
	case "task.stopped":
		if a.ID != t.To || t.Instance != a.Instance || t.LeaseToken == "" || in.Lease != t.LeaseToken || t.State != "cancelled" || t.Delivery != "cancel_requested" {
			return nil, fault("conflict", "no matching cancellation to acknowledge")
		}
		t.Delivery = "stopped"
		if problem := s.blockConversation(ctx, q, a, t.ID); problem != nil {
			return nil, problem
		}
	case "task.verify":
		if !a.owner() {
			return nil, forbidden()
		}
		if t.State != "submitted" {
			return nil, fault("conflict", "only submitted tasks can be verified")
		}
		t.State = "verified"
	case "task.resolve":
		if !a.owner() {
			return nil, forbidden()
		}
		// Expired leases can be fenced without restarting the broker. The owner
		// must stop the old process before requesting a retry.
		t.Delivery = effectiveTaskDelivery(t, time.Now().Unix())
		if t.Delivery == "cancel_uncertain" && in.Resolution == "fail" {
			t.Delivery = "stopped"
			break
		}
		if t.Delivery != "uncertain" || t.State != "running" {
			return nil, fault("conflict", "only uncertain running deliveries need resolution")
		}
		if in.Resolution == "retry" {
			t.State = "proposed"
			t.Delivery = "pending"
		} else if in.Resolution == "fail" {
			t.State = "failed"
			t.Delivery = "failed"
		} else {
			return nil, fault("invalid", "resolution must be retry or fail")
		}
	default:
		return nil, fault("unsupported", "unsupported task operation")
	}
	if !preserveLease {
		t.LeaseToken = ""
		t.Instance = ""
		t.Expires = 0
	}
	t.Version++
	t.UpdatedAt = now()
	if err = saveTask(ctx, q, t); err != nil {
		return nil, internal(err)
	}
	if err = s.autonomyCompleted(ctx, q, t); err != nil {
		return nil, internal(err)
	}
	if err = event(ctx, q, a, r.Operation, t.ID, map[string]any{"state": t.State, "delivery": t.Delivery, "version": t.Version, "from": t.From, "to": t.To}); err != nil {
		return nil, internal(err)
	}
	return map[string]any{"task": viewTask(t)}, nil
}

func (s *Store) memory(ctx context.Context, q queryer, a actor, r Request) (any, *Fault) {
	return s.memoryOperation(ctx, q, a, r)
}

func formatFault(f *Fault) error {
	if f == nil {
		return nil
	}
	return fmt.Errorf("%s: %s", f.Code, f.Message)
}
