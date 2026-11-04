package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"agentmux/internal/managedconfig"
	"agentmux/internal/nativebridge"
)

// NativeReceipt is evidence about the transport, separate from task acceptance
// and verification. In particular a socket write is never a model completion.
type NativeReceipt struct {
	Evidence  string `json:"evidence,omitempty"`
	ID        string `json:"id"`
	Adapter   string `json:"adapter"`
	Session   string `json:"session"`
	Phase     string `json:"phase"`
	NativeRef string `json:"native_ref,omitempty"`
	Reason    string `json:"reason,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

type NativePolicy struct {
	managedconfig.NativeDelivery
	Version int `json:"version"`
}

type nativeRegistration struct {
	nativebridge.Registration
	Instance string `json:"instance"`
}

type nativePresence struct {
	State, Reason     string
	Instance, Session string
	At                time.Time
}

type nativeRecord struct {
	nativebridge.Delivery
	Session   string `json:"session"`
	Instance  string `json:"instance"`
	Candidate string `json:"candidate"`
	Phase     string `json:"phase"`
	NativeRef string `json:"native_ref,omitempty"`
	Reason    string `json:"reason,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

type NativeStatus struct {
	nativebridge.Status
	Agent        string                    `json:"agent"`
	Policy       NativePolicy              `json:"policy"`
	Adapter      string                    `json:"adapter,omitempty"`
	Session      string                    `json:"session,omitempty"`
	Capabilities nativebridge.Capabilities `json:"capabilities"`
	Connected    bool                      `json:"connected"`
	// Readiness is an ephemeral adapter observation, separate from retained
	// delivery receipts and never proof of model progress or completion.
	ReadinessObservedAt string                  `json:"readiness_observed_at,omitempty"`
	Turns               int                     `json:"turns"`
	Pending             []nativebridge.Delivery `json:"pending"`
	PendingCount        int                     `json:"pending_count"`
	PendingKnown        bool                    `json:"pending_known"`
	PendingMore         bool                    `json:"pending_more"`
	Active              *NativeReceipt          `json:"active,omitempty"`
	Coverage            string                  `json:"coverage,omitempty"`
}

func nativeTerminal(phase string) bool {
	return phase == "completed" || phase == "failed" || phase == "cancelled"
}

func nativePolicy(ctx context.Context, q queryer, id string, cfg *managedconfig.NativeDelivery) (NativePolicy, error) {
	p := NativePolicy{NativeDelivery: managedconfig.NativeDelivery{Mode: "manual", MaxTurns: 20, MaxHops: 4, Results: true}, Version: 1}
	if cfg != nil {
		p.NativeDelivery = *cfg
		if p.Mode == "" {
			p.Mode = "manual"
		}
		if p.MaxTurns == 0 {
			p.MaxTurns = 20
		}
		if p.MaxHops == 0 {
			p.MaxHops = 4
		}
	}
	var raw json.RawMessage
	err := get(ctx, q, "native_policy", "native_policy:"+id, &raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	if e := checkStateRecoveryDigest(ctx, q, "native_policy", id, raw); e != nil {
		return p, e
	}
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err = json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	if p.Version < 1 {
		return p, errors.New("native policy revision is invalid")
	}
	return p, validateNativePolicy(p.NativeDelivery)
}

func validateNativePolicy(p managedconfig.NativeDelivery) error {
	if p.Mode != "manual" && p.Mode != "review" && p.Mode != "auto" {
		return errors.New("mode must be manual, review or auto")
	}
	if p.MaxTurns < 1 || p.MaxTurns > 10000 || p.MaxHops < 1 || p.MaxHops > 64 || p.CooldownSeconds < 0 || p.CooldownSeconds > 86400 {
		return errors.New("max_turns must be 1–10000, max_hops 1–64 and cooldown_seconds 0–86400")
	}
	return nil
}

func nativeRegistrationFor(ctx context.Context, q queryer, a actor) (nativeRegistration, error) {
	var reg nativeRegistration
	err := get(ctx, q, "native_registration", "native_registration:"+a.ID, &reg)
	if err == nil && reg.Instance != a.Instance {
		return reg, errors.New("native registration belongs to an earlier worker")
	}
	return reg, err
}

func (s *Store) native(ctx context.Context, q queryer, a actor, r Request) (any, *Fault) {
	ctx = boundaryReadContext(ctx)
	// All recipient resolution is credential bound. Owners can inspect/control,
	// but cannot impersonate a native terminal or acknowledge a model turn.
	var in struct {
		Agent        string                    `json:"agent"`
		Session      string                    `json:"session"`
		Adapter      string                    `json:"adapter"`
		Capabilities nativebridge.Capabilities `json:"capabilities"`
		State        string                    `json:"state"`
		Reason       string                    `json:"reason"`
		Candidate    string                    `json:"candidate"`
		ID           string                    `json:"id"`
		Phase        string                    `json:"phase"`
		NativeRef    string                    `json:"native_ref"`
		Result       string                    `json:"result"`
		Version      int                       `json:"version"`
		Mode         string                    `json:"mode"`
		MaxTurns     *int                      `json:"max_turns"`
		MaxHops      *int                      `json:"max_hops"`
		Cooldown     *int                      `json:"cooldown_seconds"`
		Notes        *bool                     `json:"notes"`
		Pings        *bool                     `json:"pings"`
		Boards       *bool                     `json:"boards"`
		Memory       *bool                     `json:"memory_updates"`
		Results      *bool                     `json:"results"`
		After        string                    `json:"after"`
		Limit        int                       `json:"limit"`
		Summary      bool                      `json:"summary"`
	}
	if err := strictJSON(r.Args, &in); err != nil {
		return nil, invalid(err)
	}
	if in.Agent == "" {
		in.Agent = a.ID
	}
	if in.Summary && (!a.owner() || in.Agent != ownerID || r.Operation != "native.status") {
		return nil, fault("invalid", "native summary is available only for the owner status inventory")
	}
	if a.owner() && in.Agent == ownerID && r.Operation == "native.status" {
		if in.Summary {
			return s.nativeObservationInventory(ctx, q, in.After, in.Limit)
		}
		agents, err := s.nativeAgents(ctx, q)
		if err != nil {
			return nil, internal(err)
		}
		statuses := []NativeStatus{}
		if in.Limit <= 0 || in.Limit > 200 {
			in.Limit = 200
		}
		cursor := in.After
		hasMore := false
		for _, info := range agents {
			if info.State != "retired" && info.Config.Interaction == "interactive" && info.ID > in.After {
				if len(statuses) >= in.Limit {
					hasMore = true
					break
				}
				st, err := s.nativeStatus(ctx, q, info.ID)
				if err != nil {
					return nil, internal(err)
				}
				st.Pending = []nativebridge.Delivery{}
				statuses = append(statuses, st)
				cursor = info.ID
			}
		}
		return map[string]any{"statuses": statuses, "cursor": cursor, "has_more": hasMore}, nil
	}
	if !a.owner() && in.Agent != a.ID {
		return nil, forbidden()
	}
	info, err := s.agent(ctx, q, in.Agent)
	if err != nil || info.State != "active" {
		return nil, fault("not_found", "active agent not found")
	}
	if info.Config.Interaction != "interactive" {
		return nil, fault("unsupported", "native delivery is only available to interactive agents")
	}
	p, err := nativePolicy(ctx, q, in.Agent, info.Config.NativeDelivery)
	if err != nil {
		return nil, internal(err)
	}
	if r.Operation == "native.status" {
		if !a.owner() && in.Session != "" {
			reg, e := nativeRegistrationFor(ctx, q, a)
			if e != nil || reg.Session != in.Session {
				return nil, fault("conflict", "native session changed")
			}
			if presence, ok := s.nativePresence[a.ID]; ok {
				presence.At = time.Now()
				s.nativePresence[a.ID] = presence
			}
		}
		// A registered adapter polls policy and its retained transport slot.
		// Queued bodies are neither returned nor needed for stop/cancel; do not
		// let an unrelated pending source hide a definite Manual transition.
		st, err := s.nativeStatusWithPending(ctx, q, in.Agent, a.owner() || in.Session == "")
		if err != nil {
			return nil, internal(err)
		}
		if !a.owner() && in.Session != "" && st.Session != in.Session {
			return nil, fault("conflict", "native session changed")
		}
		if !a.owner() && in.Session != "" {
			st.Pending = []nativebridge.Delivery{}
		}
		return map[string]any{"status": st}, nil
	}
	if r.Operation == "native.configure" {
		if !a.owner() {
			return nil, forbidden()
		}
		if in.Version != p.Version {
			return nil, fault("conflict", "native delivery policy changed; refresh before editing")
		}
		if in.Mode != "" {
			p.Mode = in.Mode
		}
		if in.MaxTurns != nil {
			p.MaxTurns = *in.MaxTurns
		}
		if in.MaxHops != nil {
			p.MaxHops = *in.MaxHops
		}
		if in.Cooldown != nil {
			p.CooldownSeconds = *in.Cooldown
		}
		if in.Notes != nil {
			p.Notes = *in.Notes
		}
		if in.Pings != nil {
			p.Pings = *in.Pings
		}
		if in.Boards != nil {
			p.Boards = *in.Boards
		}
		if in.Memory != nil {
			p.MemoryUpdates = *in.Memory
		}
		if in.Results != nil {
			p.Results = *in.Results
		}
		if err := validateNativePolicy(p.NativeDelivery); err != nil {
			return nil, invalid(err)
		}
		p.Version++
		if err := save(ctx, q, "native_policy", "native_policy:"+in.Agent, ownerID, in.Agent, "owner", p); err != nil {
			return nil, internal(err)
		}
		if err := saveStateRecoveryDigest(ctx, q, "native_policy", in.Agent, p); err != nil {
			return nil, internal(err)
		}
		if err := event(ctx, q, a, r.Operation, in.Agent, map[string]any{"mode": p.Mode, "version": p.Version}); err != nil {
			return nil, internal(err)
		}
		return map[string]any{"policy": p}, nil
	}
	if r.Operation == "native.approve" {
		if !a.owner() {
			return nil, forbidden()
		}
		if p.Mode != "review" || in.Version != p.Version {
			return nil, fault("conflict", "approval requires the current review policy version")
		}
		candidates, err := s.nativeCandidateInfos(ctx, q, in.Agent, p)
		if err != nil {
			return nil, internal(err)
		}
		found := false
		for _, candidate := range candidates {
			if candidate.Delivery.ID == in.Candidate {
				if _, err = candidate.hydrateForExecution(); err != nil {
					return nil, internal(err)
				}
				found = true
				break
			}
		}
		if !found {
			return nil, fault("conflict", "delivery is no longer eligible")
		}
		if err := save(ctx, q, "native_approval", "native_approval:"+hash(in.Agent+":"+in.Candidate), ownerID, in.Agent, in.Candidate, p.Version); err != nil {
			return nil, internal(err)
		}
		return map[string]any{"approved": in.Candidate}, nil
	}
	if a.owner() {
		return nil, forbidden()
	}
	if r.Operation == "native.register" {
		if info.Config.NativeOnly || !validKey(in.Session) || in.Adapter != info.Config.Harness || (in.Adapter != "claude" && in.Adapter != "codex" && in.Adapter != "pi") {
			return nil, fault("unsupported", "native session cannot register this adapter")
		}
		reg := nativeRegistration{Registration: nativebridge.Registration{Adapter: in.Adapter, Session: in.Session, Capabilities: in.Capabilities}, Instance: a.Instance}
		prior, e := nativeRegistrationFor(ctx, q, a)
		if e == nil && prior.Session != in.Session {
			active, e := list[nativeRecord](ctx, q, "native_delivery", "AND target=?", a.ID)
			if e != nil {
				return nil, internal(e)
			}
			for _, d := range active {
				if !nativeTerminal(d.Phase) {
					d.Phase = "uncertain"
					d.Reason = "native conversation changed before completion"
					d.UpdatedAt = now()
					t, err := loadTask(ctx, q, d.TaskID)
					if err != nil {
						return nil, internal(err)
					}
					if t.Native == nil || t.Native.ID != d.ID {
						continue
					}
					if t.Delivery == "dispatching" {
						t.Delivery = "uncertain"
					} else if t.Delivery == "cancel_requested" {
						t.Delivery = "cancel_uncertain"
					}
					if t.Native != nil {
						t.Native.Phase = d.Phase
						t.Native.Reason = d.Reason
						t.Native.UpdatedAt = d.UpdatedAt
					}
					t.Version++
					t.UpdatedAt = d.UpdatedAt
					if err = saveTask(ctx, q, t); err != nil {
						return nil, internal(err)
					}
					if e = s.saveNativeRecord(ctx, q, d); e != nil {
						return nil, internal(e)
					}
				}
			}
		}
		if err := save(ctx, q, "native_registration", "native_registration:"+a.ID, a.ID, a.ID, "", reg); err != nil {
			return nil, internal(err)
		}
		return map[string]any{"registered": true, "mode": p.Mode, "max_result_bytes": s.cfg.Limits.MaxMessageBytes}, nil
	}
	reg, err := nativeRegistrationFor(ctx, q, a)
	if err != nil || reg.Session != in.Session {
		return nil, fault("conflict", "register this native conversation before delivery")
	}
	if r.Operation == "native.event" {
		return s.nativeEvent(ctx, q, a, reg, in.ID, in.Phase, in.NativeRef, in.Result, in.Reason)
	}
	if r.Operation == "native.peek" {
		if in.State != "idle" && in.State != "ready" && in.State != "busy" && in.State != "draft" && in.State != "approval" && in.State != "offline" {
			return nil, fault("invalid", "state must be idle, ready, busy, draft, approval or offline")
		}
		if len(in.Reason) > 512 {
			return nil, fault("invalid", "readiness reason exceeds 512 bytes")
		}
		if s.nativePresence == nil {
			s.nativePresence = map[string]nativePresence{}
		}
		s.nativePresence[a.ID] = nativePresence{State: in.State, Reason: in.Reason, Instance: a.Instance, Session: reg.Session, At: time.Now()}
	}
	if r.Operation != "native.peek" && r.Operation != "native.claim" {
		return nil, fault("invalid", "unknown native operation")
	}
	st, err := s.nativeStatusWithPending(ctx, q, a.ID, false)
	if err != nil {
		return nil, internal(err)
	}
	if st.State != "ready" {
		return map[string]any{"candidate": "", "delivery": nil, "reason": st.Reason}, nil
	}
	var selected *nativebridge.Delivery
	candidates, err := s.nativeCandidateInfos(ctx, q, a.ID, p)
	if err != nil {
		return nil, internal(err)
	}
	for _, candidate := range candidates {
		d := candidate.Delivery
		if p.Mode == "review" {
			var approved int
			if get(ctx, q, "native_approval", "native_approval:"+hash(a.ID+":"+d.ID), &approved) != nil || approved != p.Version {
				continue
			}
		}
		if r.Operation != "native.peek" {
			d, err = candidate.hydrateForExecution()
			if err != nil {
				return nil, internal(err)
			}
		} else if candidate.validate != nil {
			if err = candidate.validate(); err != nil {
				return nil, internal(err)
			}
		}
		copy := d
		selected = &copy
		break
	}
	if selected == nil {
		return map[string]any{"candidate": "", "delivery": nil, "reason": "waiting for an eligible, approved delivery"}, nil
	}
	if r.Operation == "native.peek" {
		return map[string]any{"candidate": selected.ID}, nil
	}
	if in.Candidate != selected.ID {
		return nil, fault("conflict", "delivery queue changed; refresh before claiming")
	}
	if selected.Kind == "instruction" && !reg.Capabilities.Instructions || selected.Kind != "instruction" && !reg.Capabilities.Observations {
		return nil, fault("unsupported", "adapter cannot deliver this kind of turn")
	}
	var t taskRecord
	if selected.TaskID != "" {
		t, err = loadTask(ctx, q, selected.TaskID)
		if err != nil {
			return nil, internal(err)
		}
	} else {
		t = taskRecord{Task: Task{ID: randomID("task"), From: selected.From, To: a.ID, Text: fmt.Sprintf("Native %s observation from %s. Source content is delivered separately through the current scoped adapter.", selected.Kind, selected.From), State: "proposed", Delivery: "pending", Version: 1, CreatedAt: now(), UpdatedAt: now(), Origin: &TaskOrigin{Kind: "observation"}}}
		origin, e := s.nativeOrigin(ctx, q, *selected)
		if e != nil {
			return nil, internal(e)
		}
		t.Origin = origin
		var n int
		n, err = count(ctx, q, "task")
		if err != nil {
			return nil, internal(err)
		}
		if n >= s.cfg.Limits.MaxTasks {
			return nil, fault("limit", "stored task limit reached")
		}
	}
	var concurrent int
	if err = q.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind='task' AND json_extract(data,'$.delivery') IN ('dispatching','cancel_requested','uncertain','cancel_uncertain')").Scan(&concurrent); err != nil {
		return nil, internal(err)
	}
	if concurrent >= s.cfg.Limits.MaxConcurrentTasks {
		return nil, fault("busy", "concurrent task budget occupied")
	}
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
	selected.TaskID = t.ID
	// Preserve the immutable source identity when ID becomes a delivery receipt.
	if selected.SourceID == "" {
		selected.SourceID = selected.ID
	}
	d := nativeRecord{Delivery: *selected, Session: reg.Session, Instance: a.Instance, Candidate: selected.ID, Phase: "claimed", UpdatedAt: now()}
	d.ID = randomID("delivery")
	t.Native = &NativeReceipt{ID: d.ID, Adapter: reg.Adapter, Session: reg.Session, Phase: d.Phase, UpdatedAt: d.UpdatedAt, Evidence: reg.Capabilities.CompletionEvidence}
	if err = saveTask(ctx, q, t); err != nil {
		return nil, internal(err)
	}
	if err = s.saveNativeRecord(ctx, q, d); err != nil {
		return nil, internal(err)
	}
	var turns int
	if err = q.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind='native_delivery' AND target=?", a.ID).Scan(&turns); err != nil {
		return nil, internal(err)
	}
	if err = save(ctx, q, "native_usage", "native_usage:"+a.ID, ownerID, a.ID, "owner", turns); err != nil {
		return nil, internal(err)
	}
	if err = saveStateRecoveryDigest(ctx, q, "native_usage", a.ID, turns); err != nil {
		return nil, internal(err)
	}
	if err = event(ctx, q, a, "native.claim", d.ID, map[string]any{"task_id": t.ID, "kind": d.Kind}); err != nil {
		return nil, internal(err)
	}
	return map[string]any{"delivery": d.Delivery}, nil
}

func (s *Store) saveNativeRecord(ctx context.Context, q queryer, d nativeRecord) error {
	return save(ctx, q, "native_delivery", d.ID, d.From, d.To, d.Candidate, d)
}

func (s *Store) nativeStatus(ctx context.Context, q queryer, id string) (NativeStatus, error) {
	return s.nativeStatusWithPending(ctx, q, id, true)
}

func (s *Store) nativeStatusWithPending(ctx context.Context, q queryer, id string, includePending bool) (NativeStatus, error) {
	st := NativeStatus{Agent: id, Pending: []nativebridge.Delivery{}, PendingKnown: includePending}
	if !includePending {
		st.Coverage = "Policy and retained transport evidence; pending admission candidates were not evaluated."
	}
	st.MaxResultBytes = s.cfg.Limits.MaxMessageBytes
	info, err := s.agent(ctx, q, id)
	if err != nil {
		return st, err
	}
	if info.State != "active" {
		st.State = "stopped"
		st.Reason = "agent identity was revoked"
		return st, nil
	}
	p, err := nativePolicy(ctx, q, id, info.Config.NativeDelivery)
	if err != nil {
		return st, err
	}
	st.Policy = p
	st.Mode = p.Mode
	st.State = "unavailable"
	st.Reason = "relaunch this native session with the current Agentmux controller to enable delivery"
	if info.External {
		st.State = "manual"
		st.Reason = "external terminal is observed manually; automatic native delivery is unavailable for this connection; read its inbox manually"
		return st, nil
	}
	if info.Config.NativeOnly {
		st.Reason = "pure native session; coordination integration is disabled"
		return st, nil
	}
	if info.Config.Harness == "antigravity" || info.Config.Harness == "command" {
		st.Reason = "this harness has no verified native delivery adapter; use manual inbox access"
		return st, nil
	}
	var reg nativeRegistration
	if err = get(ctx, q, "native_registration", "native_registration:"+id, &reg); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return st, err
	}
	if err == nil {
		var current string
		if err = q.QueryRowContext(ctx, "SELECT instance FROM credentials WHERE actor=?", id).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return st, err
		}
		if current == reg.Instance {
			st.Adapter = reg.Adapter
			st.Session = reg.Session
			st.Capabilities = reg.Capabilities
		}
	}
	if includePending {
		infos, err := s.nativeCandidateInfos(ctx, q, id, p)
		if err == nil {
			st.PendingCount = len(infos)
			var clipped bool
			st.Pending, clipped, err = nativeCandidateBodies(infos, nativeStatusBodyBudget)
			st.PendingMore = clipped || len(infos) >= nativeCandidateLimit
		}
		if err != nil {
			if ctx.Err() != nil {
				return st, err
			}
			// A quarantined pending source cannot hide a committed Manual
			// policy or cancellation of an unrelated, already-running turn.
			// Publish no bodies/counts, then inspect retained transport using
			// the original full typed decoder below.
			st.Pending, st.PendingCount, st.PendingMore = []nativebridge.Delivery{}, 0, false
			st.PendingKnown = false
			st.Coverage = "Pending queue validation is unavailable; no queued bodies or counts are reported. Policy and retained transport evidence are separate."
		}
	}

	all, err := list[nativeRecord](ctx, q, "native_delivery", "AND target=? ORDER BY seq DESC", id)
	if err != nil {
		return st, err
	}
	st.Turns = len(all)
	var usageRaw json.RawMessage
	usageErr := get(ctx, q, "native_usage", "native_usage:"+id, &usageRaw)
	if usageErr != nil && !errors.Is(usageErr, sql.ErrNoRows) {
		return st, usageErr
	}
	if err = checkStateRecoveryDigest(ctx, q, "native_usage", id, usageRaw); err != nil {
		return st, err
	}
	if usageErr == nil {
		var charged int
		if err = json.Unmarshal(usageRaw, &charged); err != nil {
			return st, err
		}
		if charged != st.Turns {
			return st, errors.New("native delivery usage is missing or damaged; restore valid owner state")
		}
	}
	ready, ok := s.nativePresence[id]
	if ok && st.Session != "" && ready.Instance == reg.Instance && ready.Session == st.Session && !ready.At.IsZero() {
		st.ReadinessObservedAt = ready.At.UTC().Format(time.RFC3339Nano)
		age := time.Since(ready.At)
		st.Connected = age >= 0 && age < 10*time.Second
	}
	for _, d := range all {
		if !nativeTerminal(d.Phase) {
			st.ActiveID = d.ID
			st.Active = &NativeReceipt{ID: d.ID, Adapter: st.Adapter, Session: d.Session, Phase: d.Phase, NativeRef: d.NativeRef, Reason: d.Reason, UpdatedAt: d.UpdatedAt, Evidence: st.Capabilities.CompletionEvidence}
			t, e := loadTask(ctx, q, d.TaskID)
			if e == nil && (t.Native == nil || t.Native.ID != d.ID) {
				st.ActiveID = ""
				st.Active = nil
				continue
			}
			if e == nil && t.Delivery != "dispatching" && t.Delivery != "cancel_requested" && t.Delivery != "uncertain" && t.Delivery != "cancel_uncertain" {
				st.ActiveID = ""
				st.Active = nil
				continue
			}
			source := d.Delivery
			source.ID = d.Candidate
			st.Cancel = p.Mode == "manual" || e == nil && (t.Instance != reg.Instance || t.Delivery == "cancel_requested" || t.Delivery == "cancel_uncertain" || t.Expires > 0 && t.Expires < time.Now().Unix()) || !s.nativeDeliveryAllowed(ctx, q, id, source, p)
			st.State = "active"
			st.Reason = "native transport: " + d.Phase
			if d.Phase == "running" && d.Reason != "" {
				st.Reason = d.Reason
			}
			if d.Phase == "uncertain" || e == nil && (t.Delivery == "uncertain" || t.Delivery == "cancel_uncertain" || t.Expires > 0 && t.Expires < time.Now().Unix()) {
				st.State = "uncertain"
				st.Reason = "delivery outcome unknown; resolve the task before another turn"
			}
			return st, nil
		}
	}
	if st.Session == "" {
		return st, nil
	}
	if !st.Connected {
		st.State = "offline"
		st.Reason = "native adapter is not reporting readiness"
		return st, nil
	}
	if p.Mode == "manual" {
		st.State = "manual"
		st.Reason = "automatic delivery is off; enable Review or Auto to receive native turns"
		return st, nil
	}
	if ready.State != "idle" && !(ready.State == "ready" && reg.Capabilities.NativeQueue) {
		st.State = ready.State
		st.Reason = ready.Reason
		return st, nil
	}
	if st.Turns >= p.MaxTurns {
		st.State = "limited"
		st.Reason = "native turn limit reached; increase max_turns to continue"
		return st, nil
	}
	if len(all) > 0 {
		at, e := time.Parse(time.RFC3339Nano, all[0].UpdatedAt)
		if e == nil && time.Since(at) < time.Duration(p.CooldownSeconds)*time.Second {
			st.State = "cooldown"
			st.Reason = "waiting for the configured cooldown"
			return st, nil
		}
	}
	// Unresolved managed or native attempts retain their slot across restarts.
	active, err := list[taskRecord](ctx, q, "task", "AND target=? AND json_extract(data,'$.delivery') IN ('dispatching','cancel_requested','uncertain','cancel_uncertain') LIMIT 1", id)
	if err != nil {
		return st, err
	}
	if len(active) > 0 {
		st.State = "uncertain"
		st.Reason = "an earlier task still owns the execution slot"
		return st, nil
	}
	st.State = "ready"
	st.Reason = "waiting for an eligible delivery"
	return st, nil
}

func (s *Store) nativeAgents(ctx context.Context, q queryer) ([]AgentInfo, error) {
	ids := map[string]bool{}
	for id := range s.cfg.Agents {
		ids[id] = true
	}
	dynamic, err := list[AgentInfo](ctx, q, "agent", "")
	if err != nil {
		return nil, err
	}
	for _, info := range dynamic {
		ids[info.ID] = true
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	out := []AgentInfo{}
	for _, id := range ordered {
		info, e := s.agent(ctx, q, id)
		if e != nil {
			return nil, e
		}
		out = append(out, info)
	}
	return out, nil
}

func (s *Store) nativeDeliveryAllowed(ctx context.Context, q queryer, id string, d nativebridge.Delivery, p NativePolicy) bool {
	if d.SourceID != "" {
		d.ID = d.SourceID
	}
	if d.Kind == "instruction" {
		return d.From == ownerID || s.can(ctx, q, d.From, "instruct", id)
	}
	if d.Kind == "result" {
		if !p.Results {
			return false
		}
		t, err := loadTask(ctx, q, strings.TrimPrefix(d.ID, "result:"))
		return err == nil && t.From == id && (id == ownerID || s.can(ctx, q, id, "instruct", t.To))
	}
	if d.Kind == "boundaries" {
		current, err := s.nativeBoundary(ctx, q, id)
		return err == nil && d.Scope == current.Scope
	}
	if d.Kind == "memory" {
		if !p.MemoryUpdates || d.Scope == "" || strings.HasPrefix(d.Scope, "private:") || !s.namespace(ctx, q, id, d.Scope, true, false) {
			return false
		}
		memories, err := list[Memory](ctx, q, "memory", "AND scope=?", d.Scope)
		if err != nil {
			return false
		}
		for _, m := range memories {
			if d.ID == fmt.Sprintf("memory:%s:%d", hash(m.Namespace+":"+m.Key), m.Version) && memoryState(m, time.Now()) == "live" {
				return true
			}
		}
		return false
	} // Candidates re-evaluate exact namespace and version.
	var m Message
	if get(ctx, q, "message", strings.TrimPrefix(d.ID, "message:"), &m) != nil {
		return false
	}
	if m.Board != "" {
		return p.Boards && s.canSeeMessage(ctx, q, actor{ID: id}, m)
	}
	action := "message.send"
	allowed := p.Notes
	if m.Kind == "ping" {
		action = "ping.send"
		allowed = p.Pings
	}
	return allowed && contains(m.To, id) && (m.From == ownerID || s.can(ctx, q, m.From, action, id))
}

func (s *Store) nativeCandidates(ctx context.Context, q queryer, id string, p NativePolicy) ([]nativebridge.Delivery, error) {
	infos, err := s.nativeCandidateInfos(ctx, q, id, p)
	if err != nil {
		return nil, err
	}
	items, _, err := nativeCandidateBodies(infos, 0)
	return items, err
}

func (s *Store) nativeBoundary(ctx context.Context, q queryer, id string) (nativebridge.Delivery, error) {
	info, err := s.agent(ctx, q, id)
	if err != nil {
		return nativebridge.Delivery{}, err
	}
	raw, _ := json.Marshal(map[string]any{"agent": id})
	value, problem := s.control(ctx, q, actor{ID: id}, Request{Operation: "boundaries.get", Args: raw})
	if problem != nil {
		return nativebridge.Delivery{}, problem
	}
	boundaries := value.(Boundaries)
	stable := boundaries
	stable.Version = 0
	stable.Targets = []BoundaryTarget{}
	for _, target := range boundaries.Targets {
		if len(target.Actions) > 0 {
			stable.Targets = append(stable.Targets, target)
		}
	}
	var registration nativeRegistration
	if err := get(ctx, q, "native_registration", "native_registration:"+id, &registration); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nativebridge.Delivery{}, err
	}
	fingerprint, _ := json.Marshal(struct {
		Boundaries    Boundaries
		Role          string
		RoleVersion   int
		NativeSession string
	}{stable, info.Config.Role, info.RoleVersion, registration.Instance + ":" + registration.Session})
	encoded, _ := json.Marshal(stable)
	var visible map[string]any
	_ = json.Unmarshal(encoded, &visible)
	delete(visible, "version")
	data, _ := json.Marshal(visible)
	text := fmt.Sprintf("Current role (version %d): %s\nEffective boundaries changed. Broker enforcement is authoritative. Previously read information cannot be forgotten by revoking access.\n%s", info.RoleVersion, info.Config.Role, string(data))
	if len(text) > s.cfg.Limits.MaxMessageBytes {
		text = fmt.Sprintf("Role or effective boundaries changed (role version %d). Read current identity and boundaries.get before coordinating. Broker enforcement is authoritative.", info.RoleVersion)
	}
	previous, err := list[nativeRecord](ctx, q, "native_delivery", "AND target=? AND json_extract(data,'$.kind')='boundaries' ORDER BY seq DESC LIMIT 1", id)
	if err != nil {
		return nativebridge.Delivery{}, err
	}
	anchor := "initial"
	if len(previous) > 0 {
		anchor = previous[0].ID
	}
	digest := hash(string(fingerprint))
	return nativebridge.Delivery{ID: "boundaries:" + digest[:32] + ":" + anchor, Scope: digest, Kind: "boundaries", From: ownerID, To: id, Text: text}, nil
}

// A result or observation is another causal link, never a fresh root that can
// reset the team's hop bound. The broker derives this from stored sources.
func (s *Store) nativeOrigin(ctx context.Context, q queryer, d nativebridge.Delivery) (*TaskOrigin, error) {
	if d.SourceID != "" {
		d.ID = d.SourceID
	}
	origin := &TaskOrigin{Kind: "observation"}
	switch d.Kind {
	case "instruction":
		t, err := loadTask(ctx, q, d.TaskID)
		if err != nil {
			return nil, err
		}
		if t.Origin != nil {
			copy := *t.Origin
			return &copy, nil
		}
	case "result":
		t, err := loadTask(ctx, q, strings.TrimPrefix(d.ID, "result:"))
		if err != nil {
			return nil, err
		}
		if t.Origin != nil {
			origin.Provenance = t.Origin.Provenance
		}
		origin.ParentTaskID = t.ID
		origin.Hop++
	case "note", "ping", "board":
		var m Message
		if err := get(ctx, q, "message", strings.TrimPrefix(d.ID, "message:"), &m); err != nil {
			return nil, err
		}
		origin.MessageIDs = []string{m.ID}
		if m.Provenance != nil {
			origin.Provenance = *m.Provenance
		}
	case "memory":
		memories, err := list[Memory](ctx, q, "memory", "AND scope=?", d.Scope)
		if err != nil {
			return nil, err
		}
		for _, m := range memories {
			if d.ID == fmt.Sprintf("memory:%s:%d", hash(m.Namespace+":"+m.Key), m.Version) && m.Provenance != nil {
				origin.Provenance = *m.Provenance
				break
			}
		}
	}
	return origin, nil
}

func (s *Store) nativeEvent(ctx context.Context, q queryer, a actor, reg nativeRegistration, id, phase, ref, result, reason string) (any, *Fault) {
	var d nativeRecord
	if err := get(ctx, q, "native_delivery", id, &d); err != nil || d.To != a.ID || d.Instance != a.Instance || d.Session != reg.Session {
		return nil, fault("conflict", "delivery does not belong to this native conversation")
	}
	if len(ref) > 256 || len(reason) > 2048 || len(result) > s.cfg.Limits.MaxMessageBytes {
		return nil, fault("invalid", "native receipt exceeds configured limits")
	}
	// Claude can acknowledge an inbox item before the helper's post-write
	// receipt reaches the broker. Late transport evidence must not move a model
	// turn backwards or replace its completed result.
	if (phase == "submitted" || phase == "submitting") && (d.Phase == "running" || nativeTerminal(d.Phase) || phase == "submitting" && d.Phase == "submitted") {
		return map[string]any{"receipt": d.Phase}, nil
	}
	if nativeTerminal(d.Phase) {
		if phase == d.Phase {
			return map[string]any{"receipt": d.Phase}, nil
		}
		return nil, fault("conflict", "native delivery is already terminal")
	}
	if phase == d.Phase && (phase == "submitting" || phase == "submitted" || phase == "running" || phase == "uncertain") {
		if ref != d.NativeRef {
			return nil, fault("conflict", "native receipt evidence changed")
		}
		if reason == d.Reason {
			return map[string]any{"receipt": phase}, nil
		}
		// A correlated running turn can gain diagnostic information, such as
		// human steering preventing a peer pause from interrupting the turn.
		// This does not prove completion or release its execution slot.
		if phase != "running" {
			return nil, fault("conflict", "native receipt evidence changed")
		}
	}
	valid := false
	switch phase {
	case "submitting":
		valid = d.Phase == "claimed"
	case "submitted":
		valid = d.Phase == "claimed" || d.Phase == "submitting"
	case "running":
		valid = d.Phase == "claimed" || d.Phase == "submitting" || d.Phase == "submitted" || d.Phase == "running"
	case "completed":
		valid = d.Phase == "running"
	case "failed", "cancelled", "uncertain":
		valid = true
	}
	if !valid {
		return nil, fault("conflict", "native lifecycle transition requires correlated evidence")
	}
	t, err := loadTask(ctx, q, d.TaskID)
	if err != nil {
		return nil, internal(err)
	}
	if t.Instance != a.Instance || t.Native == nil || t.Native.ID != d.ID {
		return nil, fault("conflict", "native task attempt changed")
	}
	if t.Delivery != "dispatching" && t.Delivery != "cancel_requested" && t.Delivery != "uncertain" && t.Delivery != "cancel_uncertain" {
		return nil, fault("conflict", "native task was resolved by the owner")
	}
	if reg.Adapter == "claude" && phase == "running" && d.Phase != "running" {
		info, e := s.agent(ctx, q, a.ID)
		if e != nil {
			return nil, internal(e)
		}
		p, e := nativePolicy(ctx, q, a.ID, info.Config.NativeDelivery)
		if e != nil {
			return nil, internal(e)
		}
		source := d.Delivery
		source.ID = d.Candidate
		if p.Mode == "manual" || t.State == "cancelled" || t.Expires > 0 && t.Expires < time.Now().Unix() || !s.nativeDeliveryAllowed(ctx, q, a.ID, source, p) {
			return nil, fault("forbidden", "delivery permission was withdrawn; do not execute this request")
		}
	}
	if phase == "completed" && (t.State == "cancelled" || t.Delivery == "cancel_requested") {
		phase = "cancelled"
	}
	d.Phase = phase
	d.NativeRef = ref
	d.Reason = reason
	d.UpdatedAt = now()
	t.Native.Phase = phase
	t.Native.NativeRef = ref
	t.Native.Reason = reason
	t.Native.UpdatedAt = d.UpdatedAt
	t.UpdatedAt = d.UpdatedAt
	t.Version++
	switch phase {
	case "completed":
		t.State = "submitted"
		t.Delivery = "acknowledged"
		t.Result = result
	case "failed":
		t.State = "failed"
		t.Delivery = "failed"
		t.Result = result
	case "cancelled":
		t.State = "cancelled"
		t.Delivery = "stopped"
	case "uncertain":
		if t.State == "cancelled" {
			t.Delivery = "cancel_uncertain"
		} else {
			t.Delivery = "uncertain"
		}
	}
	if nativeTerminal(phase) {
		t.LeaseToken = ""
		t.Expires = 0
	}
	if err = saveTask(ctx, q, t); err != nil {
		return nil, internal(err)
	}
	if err = s.saveNativeRecord(ctx, q, d); err != nil {
		return nil, internal(err)
	}
	if err = event(ctx, q, a, "native."+phase, id, map[string]any{"task_id": t.ID, "native_ref": ref, "reason": reason}); err != nil {
		return nil, internal(err)
	}
	return map[string]any{"receipt": phase, "task_id": t.ID}, nil
}
