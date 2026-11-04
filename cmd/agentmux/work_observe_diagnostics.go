package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"agentmux/internal/managed"
)

// These timings measure local observation collection, including transport and
// decoding. Components run concurrently, so their durations must not be added.
// They cannot establish model latency, task correctness or worker health.
type workObservationDiagnostics struct {
	CollectionDurationMS float64                   `json:"collection_duration_ms"`
	Components           workObservationComponents `json:"components"`
}

type workObservationComponents struct {
	Broker  workObservationComponent `json:"broker"`
	Runtime workObservationComponent `json:"runtime"`
	Native  workObservationComponent `json:"native"`
	Storage workObservationComponent `json:"storage"`
}

type workObservationComponent struct {
	State      string   `json:"state"`
	ErrorCode  *string  `json:"error_code"`
	ReasonCode *string  `json:"reason_code"`
	DurationMS *float64 `json:"duration_ms"`
}

func newWorkObservationDiagnostics() workObservationDiagnostics {
	notAttempted := workObservationComponent{State: "not_attempted"}
	return workObservationDiagnostics{Components: workObservationComponents{
		Broker: notAttempted, Runtime: notAttempted, Native: notAttempted, Storage: notAttempted,
	}}
}

func beginWorkObservationComponent(component *workObservationComponent) func() {
	started := time.Now()
	component.State = "observed"
	return func() {
		duration := float64(time.Since(started)) / float64(time.Millisecond)
		component.DurationMS = &duration
	}
}

func (component *workObservationComponent) fail(code string) {
	component.State = "unavailable"
	component.ErrorCode = &code
	component.ReasonCode = nil
}

func (component *workObservationComponent) failWithError(code string, err error) {
	component.fail(code)
	var problem *managed.Fault
	if errors.As(err, &problem) {
		if reason := problem.OperationalReadReasonCode(); reason != "" {
			// A concurrent local deadline may supersede the server's generic
			// cancellation classification. Do not emit a contradictory pair.
			if code == "unavailable" && (reason == "operational_read_busy" || reason == "shutdown") ||
				code == "canceled" && (reason == "query_budget_exhausted" || reason == "shutdown") {
				component.ReasonCode = &reason
			}
		}
	}
}

// The managed transport deliberately redacts underlying errors. Retain this
// read's own deadline so a local timeout can be distinguished from another
// transport failure without parsing potentially sensitive error prose.
func workObservationCall(ctx context.Context, client *managed.Client, op string, args any) (json.RawMessage, error, string) {
	query, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := workCall(query, client, op, args)
	if err != nil {
		return nil, err, workObservationErrorCode(query, err)
	}
	return raw, nil, ""
}

// Only fixed codes enter diagnostics. In particular, never copy a server's
// unknown code or derive machine-readable classifications from its message.
func workObservationErrorCode(ctx context.Context, err error) string {
	var problem *managed.Fault
	if errors.As(err, &problem) {
		switch problem.Code {
		case "unauthorized":
			return "unauthenticated"
		case "forbidden":
			return "forbidden"
		case "unsupported":
			return "unsupported"
		case "unavailable":
			return "unavailable"
		case "transport", "cancelled":
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return "timeout"
			}
			if errors.Is(ctx.Err(), context.Canceled) || problem.Code == "cancelled" {
				return "canceled"
			}
			return "transport"
		default:
			return "unknown"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return "unknown"
}
