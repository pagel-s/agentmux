package managed

import (
	"context"
	"time"
)

// TaskRecovery is an owner read projection of task.resolve's current
// eligibility. It does not assert that a process stopped or change the recorded
// task state, delivery, version or execution lease.
type TaskRecovery struct {
	Delivery string   `json:"delivery"`
	Actions  []string `json:"actions"`
	Reason   string   `json:"reason"`
}

func effectiveTaskDelivery(t taskRecord, at int64) string {
	if t.Expires < at {
		switch t.Delivery {
		case "dispatching":
			return "uncertain"
		case "cancel_requested":
			return "cancel_uncertain"
		}
	}
	return t.Delivery
}

func taskResolutionActions(state, delivery string) []string {
	if delivery == "cancel_uncertain" {
		return []string{"fail"}
	}
	if delivery == "uncertain" && state == "running" {
		return []string{"fail", "retry"}
	}
	return nil
}

func taskView(t taskRecord, a actor, at time.Time) Task {
	view := t.Task
	view.Recovery = nil
	view.Progress = nil
	if !a.owner() {
		return view
	}
	delivery := effectiveTaskDelivery(t, at.Unix())
	actions := taskResolutionActions(t.State, delivery)
	if len(actions) == 0 {
		return view
	}
	reason := "Delivery outcome is unconfirmed; stop or fence the prior process before retrying."
	if delivery == "cancel_uncertain" {
		reason = "Cancellation outcome is unconfirmed; check the prior process stopped before resolving."
	}
	if delivery != t.Delivery {
		if delivery == "cancel_uncertain" {
			reason = "Cancellation lease expired; process termination is unconfirmed. Check the prior process stopped before resolving."
		} else {
			reason = "Execution lease expired; the prior attempt's outcome is unconfirmed. Stop or fence its process before retrying."
		}
	}
	view.Recovery = &TaskRecovery{Delivery: delivery, Actions: actions, Reason: reason}
	return view
}

// Capture one read time and admission view for an entire page. In particular,
// task.resolve permits only fail after the session runtime budget expires.
func (s *Store) taskViews(ctx context.Context, q queryer, a actor) (func(taskRecord) Task, *Fault) {
	return s.taskViewsAt(ctx, q, a, time.Now())
}

func (s *Store) taskViewsAt(ctx context.Context, q queryer, a actor, at time.Time) (func(taskRecord) Task, *Fault) {
	retryAllowed := true
	var progress map[string]taskProgressObservation
	if a.owner() {
		deadline, err := s.admissionDeadline(ctx, q)
		if err != nil {
			return nil, internal(err)
		}
		retryAllowed = deadline.IsZero() || !at.After(deadline)
		progress, err = s.currentTaskProgress(ctx, q)
		if err != nil {
			return nil, internal(err)
		}
	}
	return func(t taskRecord) Task {
		view := taskView(t, a, at)
		if a.owner() {
			view.Progress = projectedTaskProgress(t, progress)
		}
		if view.Recovery != nil && !retryAllowed && contains(view.Recovery.Actions, "retry") {
			view.Recovery.Actions = []string{"fail"}
			view.Recovery.Reason += " Session runtime budget is exhausted; retry is unavailable."
		}
		return view
	}, nil
}
