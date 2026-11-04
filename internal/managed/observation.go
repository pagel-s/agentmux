package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Bound diagnostic query work. Slow SQL cancellation/decoding runs on an
// independent read-only connection, outside the worker lease-monitor lock.
const operationalObservationReadBudget = 250 * time.Millisecond

// ObservationSnapshot is a fresh owner-only read projection. Counts cover the
// entire retained task history. Attention is bounded and contains no task,
// result, usage, credential, or native event bodies. A quiet adapter is not
// evidence that execution failed or that its side effects stopped.
type ObservationSnapshot struct {
	ObservedAt          string              `json:"observed_at"`
	RuntimeDeadline     string              `json:"runtime_deadline"`
	Tasks               int                 `json:"tasks"`
	TaskStates          map[string]int      `json:"task_states"`
	Deliveries          map[string]int      `json:"deliveries"`
	EffectiveDeliveries map[string]int      `json:"effective_deliveries"`
	RecoveryEligible    int                 `json:"recovery_eligible"`
	AttentionCount      int                 `json:"attention_count"`
	Attention           []ObservationTask   `json:"attention"`
	AttentionMore       bool                `json:"attention_more"`
	ActiveCount         int                 `json:"active_count"`
	Active              []ObservationTask   `json:"active"`
	ActiveMore          bool                `json:"active_more"`
	LastEventAt         string              `json:"last_event_at,omitempty"`
	Capacity            ObservationCapacity `json:"capacity"`
}

// Counts and configured ceilings describe storage admission resources, not a
// promise that every unused slot can be allocated. Recovery and shutdown keep
// reserved capacity and other admission policies still apply.
type ObservationCapacity struct {
	Records     int `json:"records"`
	MaxRecords  int `json:"max_records"`
	Requests    int `json:"requests"`
	MaxRequests int `json:"max_requests"`
	Messages    int `json:"messages"`
	MaxMessages int `json:"max_messages"`
	Tasks       int `json:"tasks"`
	MaxTasks    int `json:"max_tasks"`
}

type ObservationTask struct {
	ID                string         `json:"id"`
	To                string         `json:"to"`
	Version           int            `json:"version"`
	Kind              string         `json:"kind"`
	State             string         `json:"state"`
	Delivery          string         `json:"delivery"`
	EffectiveDelivery string         `json:"effective_delivery"`
	CreatedAt         string         `json:"created_at"`
	UpdatedAt         string         `json:"updated_at"`
	Reason            string         `json:"reason"`
	Recovery          *TaskRecovery  `json:"recovery,omitempty"`
	NativeReceipt     *NativeReceipt `json:"native_receipt,omitempty"`
	Trace             *TraceSummary  `json:"trace,omitempty"`
	Progress          *TaskProgress  `json:"progress,omitempty"`
}

// Decode only selected records' whitelisted metadata. Task/result/usage bodies,
// native session references and unknown fields never enter this projection.
type observationRecord struct {
	ID, To, State, Delivery string
	Version                 int
	CreatedAt               string `json:"created_at"`
	UpdatedAt               string `json:"updated_at"`
	Expires                 int64
	LeaseToken              string `json:"lease_token"`
	Instance                string
	Native                  *struct {
		ID, Adapter, Phase, Evidence string
		UpdatedAt                    string `json:"updated_at"`
	}
	Trace *struct {
		ID, MAC  string
		Records  int
		Bytes    int64
		Complete bool
	}
}

func (r observationRecord) task() taskRecord {
	t := taskRecord{Task: Task{ID: r.ID, To: r.To, State: r.State, Delivery: r.Delivery, Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}, Expires: r.Expires, LeaseToken: r.LeaseToken, Instance: r.Instance}
	if r.Native != nil {
		t.Native = &NativeReceipt{ID: r.Native.ID, Adapter: r.Native.Adapter, Phase: r.Native.Phase, Evidence: r.Native.Evidence, UpdatedAt: r.Native.UpdatedAt}
	}
	if r.Trace != nil {
		t.Trace = &TraceSummary{ID: r.Trace.ID, MAC: r.Trace.MAC, Records: r.Trace.Records, Bytes: r.Trace.Bytes, Complete: r.Trace.Complete, Coverage: traceCoverage}
	}
	return t
}

// Exact indexed expressions keep retained task bodies out of aggregation and
// ranking. Only a bounded selected page joins back to records.data. This matters
// because broker reads share the lease-monitor lock with running workers.
// Match effectiveTaskDelivery, including a missing/zero active expiry.
const observedTasksSQL = `WITH task_metadata AS MATERIALIZED (SELECT id AS record_id,seq,
 COALESCE(json_extract(data,'$.state'),'') AS state,
 COALESCE(json_extract(data,'$.delivery'),'') AS delivery,
 COALESCE(json_extract(data,'$.expires'),0) AS expires
 FROM records INDEXED BY records_task_observation_metadata WHERE kind='task'),
 observed_tasks AS (SELECT record_id,seq,state,delivery,
 CASE WHEN delivery='dispatching' AND expires<? THEN 'uncertain'
 WHEN delivery='cancel_requested' AND expires<? THEN 'cancel_uncertain'
 ELSE delivery END AS effective_delivery FROM task_metadata) `

func observationAttentionKind(state, delivery, effective string) string {
	if len(taskResolutionActions(state, effective)) != 0 {
		return "recovery"
	}
	if state == "failed" || delivery == "failed" {
		return "failed"
	}
	if state == "submitted" {
		return "review"
	}
	if state == "proposed" || effective == "pending" {
		return "pending"
	}
	return ""
}

func taskObservationCounts(ctx context.Context, q queryer, at time.Time) (ObservationSnapshot, error) {
	result := ObservationSnapshot{ObservedAt: at.UTC().Format(time.RFC3339Nano), TaskStates: map[string]int{}, Deliveries: map[string]int{}, EffectiveDeliveries: map[string]int{}, Attention: []ObservationTask{}, Active: []ObservationTask{}}
	rows, err := q.QueryContext(ctx, observedTasksSQL+`SELECT state,delivery,effective_delivery,COUNT(*) FROM observed_tasks GROUP BY state,delivery,effective_delivery`, at.Unix(), at.Unix())
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var state, delivery, effective string
		var n int
		if err = rows.Scan(&state, &delivery, &effective, &n); err != nil {
			return result, err
		}
		result.Tasks += n
		result.TaskStates[state] += n
		result.Deliveries[delivery] += n
		result.EffectiveDeliveries[effective] += n
		if len(taskResolutionActions(state, effective)) != 0 {
			result.RecoveryEligible += n
		}
		if observationAttentionKind(state, delivery, effective) != "" {
			result.AttentionCount += n
		}
		if state == "running" && effective == "dispatching" {
			result.ActiveCount += n
		}
	}
	return result, rows.Err()
}

func (s *Store) observation(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	if !a.owner() {
		return nil, forbidden()
	}
	var in struct {
		Limit json.RawMessage `json:"limit"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	limit := 25
	if len(in.Limit) != 0 {
		if err := json.Unmarshal(in.Limit, &limit); err != nil {
			return nil, invalid(err)
		}
		if string(in.Limit) == "null" {
			return nil, fault("invalid", "observation limit must be 1..200")
		}
	}
	if limit < 1 || limit > 200 {
		return nil, fault("invalid", "observation limit must be 1..200")
	}
	// A large configured history may exceed this observation budget. Return an
	// unavailable sample instead of starving workers' 500ms lease monitors.
	ctx, cancel := context.WithTimeout(ctx, operationalObservationReadBudget)
	defer cancel()
	at := time.Now()
	result, err := taskObservationCounts(ctx, q, at)
	if err != nil {
		return nil, internal(err)
	}
	result.Capacity = ObservationCapacity{MaxRecords: s.cfg.Limits.MaxRecords, MaxRequests: s.cfg.Limits.MaxRequests, MaxMessages: s.cfg.Limits.MaxMessages, Tasks: result.Tasks, MaxTasks: s.cfg.Limits.MaxTasks}
	if err = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM records").Scan(&result.Capacity.Records); err != nil {
		return nil, internal(err)
	}
	if err = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM requests").Scan(&result.Capacity.Requests); err != nil {
		return nil, internal(err)
	}
	if result.Capacity.Messages, err = count(ctx, q, "message"); err != nil {
		return nil, internal(err)
	}
	deadline, err := s.admissionDeadline(ctx, q)
	if err != nil {
		return nil, internal(err)
	}
	if !deadline.IsZero() {
		result.RuntimeDeadline = deadline.UTC().Format(time.RFC3339Nano)
	}
	if err = q.QueryRowContext(ctx, `SELECT COALESCE(json_extract(data,'$.at'),'') FROM records WHERE kind='event' ORDER BY seq DESC LIMIT 1`).Scan(&result.LastEventAt); err != nil && err != sql.ErrNoRows {
		return nil, internal(err)
	}
	var problem *Fault
	result.Attention, problem = s.observationTaskPage(ctx, q, a, at, limit, false)
	if problem != nil {
		return nil, problem
	}
	result.Active, problem = s.observationTaskPage(ctx, q, a, at, limit, true)
	if problem != nil {
		return nil, problem
	}
	result.AttentionMore = len(result.Attention) < result.AttentionCount
	result.ActiveMore = len(result.Active) < result.ActiveCount
	return result, nil
}

func observationTaskPageQuery(active bool) string {
	condition := `effective_delivery='cancel_uncertain'
 OR (effective_delivery='uncertain' AND state='running')
 OR state IN ('failed','submitted','proposed') OR delivery='failed' OR effective_delivery='pending'`
	query, source, order := observedTasksSQL, "observed_tasks", "seq DESC"
	if active {
		condition = "state='running' AND effective_delivery='dispatching'"
	} else {
		// Retained failures cannot be acknowledged away. Give each kind a turn
		// before taking another item from the same kind, so an old failure tail
		// cannot permanently hide new reviews or queued work. Recovery leads
		// each round; newest items lead within their own kind.
		query += `, attention_candidates AS (SELECT *,
 CASE WHEN effective_delivery='cancel_uncertain' OR (effective_delivery='uncertain' AND state='running') THEN 0
 WHEN state='failed' OR delivery='failed' THEN 1 WHEN state='submitted' THEN 2 ELSE 3 END AS attention_priority
 FROM observed_tasks WHERE ` + condition + `),
 ranked_attention AS (SELECT *,ROW_NUMBER() OVER (PARTITION BY attention_priority ORDER BY seq DESC) AS attention_position FROM attention_candidates) `
		source, condition, order = "ranked_attention", "1", "attention_position,attention_priority,seq DESC"
	}
	// Pin the chosen bounded rows before reading JSON bodies. Without this
	// materialization SQLite may carry large data values through window sorting.
	query += `, chosen_tasks AS MATERIALIZED (SELECT * FROM ` + source + ` WHERE ` + condition + ` ORDER BY ` + order + ` LIMIT ?) `
	if active {
		order = "0 AS kind_position,0 AS kind_priority"
	} else {
		order = "chosen.attention_position AS kind_position,chosen.attention_priority AS kind_priority"
	}
	// Keep indexed expressions at this SELECT's top level. Nesting them inside
	// json_object prevents SQLite from substituting covering expression-index
	// values, even when every field is indexed. Materialize only small metadata.
	query += `, projected_metadata AS MATERIALIZED (SELECT
 json_extract(data,'$.id') AS task_id,json_extract(data,'$.to') AS recipient,
 chosen.state AS state,chosen.delivery AS delivery,json_extract(data,'$.version') AS version,
 json_extract(data,'$.created_at') AS created_at,json_extract(data,'$.updated_at') AS updated_at,
 COALESCE(json_extract(data,'$.expires'),0) AS expires,
 json_extract(data,'$.lease_token') AS lease_token,json_extract(data,'$.instance') AS instance,
 json_extract(data,'$.native.id') AS native_id,json_extract(data,'$.native.adapter') AS native_adapter,
 json_extract(data,'$.native.phase') AS native_phase,json_extract(data,'$.native.updated_at') AS native_updated_at,
 json_extract(data,'$.native.evidence') AS native_evidence,
 json_extract(data,'$.trace.id') AS trace_id,json_extract(data,'$.trace.records') AS trace_records,
 json_extract(data,'$.trace.bytes') AS trace_bytes,json_extract(data,'$.trace.mac') AS trace_mac,
 json_extract(data,'$.trace.complete') AS trace_complete,chosen.seq AS row_seq,` + order + `
 FROM chosen_tasks AS chosen JOIN records AS body INDEXED BY records_task_observation_metadata ON body.id=chosen.record_id AND body.kind='task') `
	return query + `SELECT json_object('id',task_id,'to',recipient,'state',state,'delivery',delivery,
 'version',version,'created_at',created_at,'updated_at',updated_at,'expires',expires,'lease_token',lease_token,'instance',instance,
 'native',CASE WHEN native_id IS NOT NULL THEN json_object('id',native_id,'adapter',native_adapter,
  'phase',native_phase,'updated_at',native_updated_at,'evidence',native_evidence) ELSE NULL END,
 'trace',CASE WHEN trace_id IS NOT NULL THEN json_object('id',trace_id,'records',trace_records,'bytes',trace_bytes,
  'mac',trace_mac,'complete',json(CASE WHEN trace_complete=1 THEN 'true' ELSE 'false' END)) ELSE NULL END)
 FROM projected_metadata ORDER BY kind_position,kind_priority,row_seq DESC`
}

func (s *Store) observationTaskPage(ctx context.Context, q queryer, a actor, at time.Time, limit int, active bool) ([]ObservationTask, *Fault) {
	viewTask, problem := s.taskViewsAt(ctx, q, a, at)
	if problem != nil {
		return nil, problem
	}
	rows, err := q.QueryContext(ctx, observationTaskPageQuery(active), at.Unix(), at.Unix(), limit)
	if err != nil {
		return nil, internal(err)
	}
	defer rows.Close()
	items := []ObservationTask{}
	bytes := 0
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, internal(err)
		}
		var metadata observationRecord
		if err = json.Unmarshal(raw, &metadata); err != nil {
			return nil, internal(err)
		}
		record := metadata.task()
		t := viewTask(record)
		effective := effectiveTaskDelivery(record, at.Unix())
		item := ObservationTask{ID: t.ID, To: t.To, Version: t.Version, Kind: observationAttentionKind(t.State, t.Delivery, effective), State: t.State, Delivery: t.Delivery, EffectiveDelivery: effective, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, Recovery: t.Recovery, NativeReceipt: t.Native, Trace: t.Trace, Progress: t.Progress}
		if active {
			item.Kind, item.Reason = "active", "Execution lease is live; worker check-ins and native event observations are separate evidence."
		}
		switch item.Kind {
		case "recovery":
			item.Reason = t.Recovery.Reason
		case "failed":
			item.Reason = "Recorded failure; inspect the exact task result and native evidence."
		case "review":
			item.Reason = "Result submitted; owner verification is not yet recorded."
		case "pending":
			item.Reason = "Awaiting delivery or admission; inspect the receiver's current policy and readiness."
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, internal(err)
		}
		if bytes+len(encoded) > maxWireBytes/4 {
			break
		}
		bytes += len(encoded)
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, internal(err)
	}
	return items, nil
}
