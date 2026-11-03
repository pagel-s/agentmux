package managed

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"time"
)

// TaskProgress contains only broker-timestamped observations of one exact
// execution attempt. A worker check-in is not model progress; a native event
// is not evidence of task correctness. Observations are ephemeral and unknown
// after broker restart. No raw native event or client-supplied timestamp enters
// this projection.
type TaskProgress struct {
	WorkerObservedAt string `json:"worker_observed_at"`
	EventObservedAt  string `json:"event_observed_at,omitempty"`
	EventCount       int    `json:"event_count"`
	EventKind        string `json:"event_kind,omitempty"`
}

type taskProgressObservation struct {
	TaskID, Instance, Lease string
	Generation              int
	Progress                TaskProgress
}

func validProgressEventKind(kind string) bool {
	switch kind {
	case "native_event", "message", "tool", "result", "usage", "error", "other":
		return true
	}
	return false
}

// currentTaskProgress prunes observations for revoked/rotated credentials so
// the map cannot grow with retired identities. It holds one metadata record
// per current worker credential and never adds persistent records/receipts.
func (s *Store) currentTaskProgress(ctx context.Context, q queryer) (map[string]taskProgressObservation, error) {
	current := map[string]taskProgressObservation{}
	if len(s.taskProgress) == 0 {
		return current, nil
	}
	rows, err := q.QueryContext(ctx, "SELECT actor,instance,generation FROM credentials")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, instance string
		var generation int
		if err = rows.Scan(&id, &instance, &generation); err != nil {
			return nil, err
		}
		if observation, ok := s.taskProgress[id]; ok && observation.Instance == instance && observation.Generation == generation {
			current[id] = observation
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	s.taskProgress = current
	return current, nil
}

// task.observe is a lease-bound ephemeral check-in, not a durable task mutation.
// Deliberately keep it out of isWrite: frequent observations cannot exhaust
// idempotency/storage budgets or silently extend an execution lease.
func (s *Store) observeTask(ctx context.Context, q queryer, a actor, raw json.RawMessage) (any, *Fault) {
	if a.owner() {
		return nil, forbidden()
	}
	var in struct {
		ID         string `json:"id"`
		Lease      string `json:"lease"`
		EventCount int    `json:"event_count"`
		EventKind  string `json:"event_kind"`
	}
	if err := strictJSON(raw, &in); err != nil {
		return nil, invalid(err)
	}
	if !validKey(in.ID) || in.Lease == "" || len(in.Lease) > 256 || in.EventCount < 0 || in.EventCount > 1000000 || in.EventCount == 0 && in.EventKind != "" || in.EventCount > 0 && !validProgressEventKind(in.EventKind) {
		return nil, fault("invalid", "task observation requires an exact lease, bounded event count and supported event kind")
	}
	t, err := loadTask(ctx, q, in.ID)
	at := time.Now()
	if err != nil || t.To != a.ID || t.Instance != a.Instance || subtle.ConstantTimeCompare([]byte(t.LeaseToken), []byte(in.Lease)) != 1 || t.State != "running" || t.Delivery != "dispatching" || t.Expires < at.Unix() {
		return nil, fault("conflict", "task observation no longer matches an active execution lease")
	}
	current, err := s.currentTaskProgress(ctx, q)
	if err != nil {
		return nil, internal(err)
	}
	observation := current[a.ID]
	if observation.TaskID != t.ID || observation.Instance != a.Instance || observation.Generation != a.Generation || subtle.ConstantTimeCompare([]byte(observation.Lease), []byte(in.Lease)) != 1 {
		observation = taskProgressObservation{TaskID: t.ID, Instance: a.Instance, Generation: a.Generation, Lease: in.Lease}
	}
	if in.EventCount < observation.Progress.EventCount || in.EventCount == observation.Progress.EventCount && in.EventKind != observation.Progress.EventKind {
		return nil, fault("conflict", "task observation event count or kind moved backwards")
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	observation.Progress.WorkerObservedAt = stamp
	if in.EventCount > observation.Progress.EventCount {
		observation.Progress.EventCount = in.EventCount
		observation.Progress.EventKind = in.EventKind
		observation.Progress.EventObservedAt = stamp
	}
	current[a.ID] = observation
	s.taskProgress = current
	return map[string]any{"observed": true, "progress": observation.Progress}, nil
}

func projectedTaskProgress(t taskRecord, current map[string]taskProgressObservation) *TaskProgress {
	observation, ok := current[t.To]
	if !ok || t.State != "running" || t.Delivery != "dispatching" || observation.TaskID != t.ID || observation.Instance != t.Instance || subtle.ConstantTimeCompare([]byte(observation.Lease), []byte(t.LeaseToken)) != 1 {
		return nil
	}
	progress := observation.Progress
	return &progress
}
