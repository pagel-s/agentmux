package managed

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"agentmux/internal/nativebridge"
)

const nativeObservationCoverage = "Readiness and retained transport metadata only; pending admission, cancellation and task correctness are not evaluated."

type nativeObservedAgent struct {
	ID, State, Interaction, Harness string
	NativeOnly, External            bool
}

const nativeObservedAgentsSQL = `SELECT json_extract(data,'$.id'),
 COALESCE(json_extract(data,'$.state'),''),COALESCE(json_extract(data,'$.config.interaction'),''),
 COALESCE(json_extract(data,'$.config.harness'),''),COALESCE(json_extract(data,'$.config.native_only'),0),
 COALESCE(json_extract(data,'$.external'),0)
 FROM records INDEXED BY records_agent_observation_metadata WHERE kind='agent' AND json_extract(data,'$.id')>?`

func (s *Store) nativeObservationInventory(ctx context.Context, q queryer, after string, limit int) (any, *Fault) {
	ctx, cancel := operationalQueryContext(ctx)
	defer cancel()
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	agents := map[string]nativeObservedAgent{}
	for id, config := range s.cfg.Agents {
		if err := ctx.Err(); err != nil {
			return nil, operationalQueryFault(ctx, err)
		}
		if id > after {
			agents[id] = nativeObservedAgent{ID: id, State: "active", Interaction: config.Interaction, Harness: config.Harness, NativeOnly: config.NativeOnly}
		}
	}
	// Agent registry presentation may contain roles, environment and history.
	// Read only indexed identity/routing metadata, including retired overrides.
	rows, err := q.QueryContext(ctx, nativeObservedAgentsSQL, after)
	if err != nil {
		return nil, operationalQueryFault(ctx, err)
	}
	for rows.Next() {
		var agent nativeObservedAgent
		if err = rows.Scan(&agent.ID, &agent.State, &agent.Interaction, &agent.Harness, &agent.NativeOnly, &agent.External); err != nil {
			rows.Close()
			return nil, operationalQueryFault(ctx, err)
		}
		agents[agent.ID] = agent
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, operationalQueryFault(ctx, err)
	}
	rows.Close()
	ids := make([]string, 0, len(agents))
	for id, agent := range agents {
		if agent.State != "retired" && agent.Interaction == "interactive" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	statuses := []NativeStatus{}
	cursor := after
	for _, id := range ids {
		status, err := s.nativeObservationStatus(ctx, q, agents[id])
		if err != nil {
			return nil, operationalQueryFault(ctx, err)
		}
		statuses = append(statuses, status)
		cursor = id
	}
	return map[string]any{"statuses": statuses, "cursor": cursor, "has_more": more}, nil
}

const nativeObservedActiveSQL = `SELECT d.id,json_extract(d.data,'$.phase'),
 json_extract(d.data,'$.session'),json_extract(d.data,'$.instance'),json_extract(d.data,'$.updated_at'),
 COALESCE(json_extract(t.data,'$.state'),''),COALESCE(json_extract(t.data,'$.delivery'),''),
 COALESCE(json_extract(t.data,'$.expires'),0),json_extract(t.data,'$.instance'),
 json_extract(t.data,'$.native.adapter'),json_extract(t.data,'$.native.evidence')
 FROM records AS d INDEXED BY records_native_observation_metadata
 JOIN records AS t INDEXED BY records_task_observation_metadata
 ON t.id=json_extract(d.data,'$.task_id') AND t.kind='task'
 WHERE d.kind='native_delivery' AND d.target=?
 AND json_extract(d.data,'$.phase') NOT IN ('completed','failed','cancelled')
 AND json_extract(t.data,'$.native.id')=d.id
 AND json_extract(t.data,'$.to')=d.target
 AND COALESCE(json_extract(t.data,'$.delivery'),'') IN ('dispatching','cancel_requested','uncertain','cancel_uncertain')
 ORDER BY d.seq DESC LIMIT 1`

func (s *Store) nativeObservationStatus(ctx context.Context, q queryer, agent nativeObservedAgent) (NativeStatus, error) {
	status := NativeStatus{Agent: agent.ID, Pending: []nativebridge.Delivery{}, PendingKnown: false, Coverage: nativeObservationCoverage}
	status.State, status.Reason = "unavailable", "No current native registration; pending admission is not evaluated."
	if agent.State != "active" {
		status.State, status.Reason = "stopped", "Coordination identity is not active."
		return status, nil
	}
	if agent.External || agent.NativeOnly || agent.Harness == "command" || agent.Harness == "antigravity" {
		status.Reason = "This session has no managed native adapter; pending admission is not evaluated."
		return status, nil
	}
	var registration nativeRegistration
	if err := get(ctx, q, "native_registration", "native_registration:"+agent.ID, &registration); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return status, err
	}
	var current string
	if err := q.QueryRowContext(ctx, "SELECT instance FROM credentials WHERE actor=?", agent.ID).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return status, err
	}
	registered := current != "" && registration.Instance == current && registration.Session != ""
	if registered {
		status.Adapter, status.Capabilities = registration.Adapter, registration.Capabilities
		if presence, ok := s.nativePresence[agent.ID]; ok && presence.Instance == current && presence.Session == registration.Session && !presence.At.IsZero() {
			status.ReadinessObservedAt = presence.At.UTC().Format(time.RFC3339Nano)
			age := time.Since(presence.At)
			status.Connected = age >= 0 && age < 10*time.Second
		}
		status.State, status.Reason = "not_reporting", "Native adapter is not reporting; pending admission is not evaluated."
		if status.Connected {
			status.State, status.Reason = "reporting", "Native readiness observed; pending admission is not evaluated."
		}
	}
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM records WHERE kind='native_delivery' AND target=?", agent.ID).Scan(&status.Turns); err != nil {
		return status, err
	}
	var receipt NativeReceipt
	var session, instance string
	var adapter, evidence sql.NullString
	var task taskRecord
	if err := q.QueryRowContext(ctx, nativeObservedActiveSQL, agent.ID).Scan(&receipt.ID, &receipt.Phase, &session, &instance, &receipt.UpdatedAt, &task.State, &task.Delivery, &task.Expires, &task.Instance, &adapter, &evidence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return status, nil
		}
		return status, err
	}
	receipt.Adapter, receipt.Evidence = adapter.String, evidence.String
	status.Active, status.ActiveID = &receipt, receipt.ID
	status.State, status.Reason = "retained_delivery", "Retained native transport receipt; current execution and pending admission are not established."
	effective := effectiveTaskDelivery(task, time.Now().Unix())
	if !registered || instance != current || task.Instance != current || session != registration.Session || effective == "uncertain" || effective == "cancel_uncertain" {
		status.State, status.Reason = "uncertain", "Retained delivery outcome is unconfirmed; inspect the exact task before recovery."
	}
	return status, nil
}
