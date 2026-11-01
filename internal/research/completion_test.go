package research

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"agentmux/internal/managed"
)

func TestTerminalBoundaryCarriesAdmissionWithoutAnotherPhase(t *testing.T) {
	for _, mode := range []string{"running", "pause", "unused-step", "used-step", "stop"} {
		t.Run(mode, func(t *testing.T) {
			live, control, status := liveFixture(t)
			if mode == "used-step" {
				control("step", 1)
			}
			if err := live.barrier(context.Background(), 1, "act", live.progress.Population); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "pause", "stop":
				control(mode, 1)
			case "unused-step":
				control("step", 1)
			}
			before := status()
			ended, next, err := live.complete(context.Background())
			want := map[string]string{"running": "running", "pause": "paused", "unused-step": "step", "used-step": "paused", "stop": ""}[mode]
			if next != want || mode == "stop" && !errors.Is(err, ErrResearchStopped) || mode != "stop" && err != nil {
				t.Fatalf("terminal admission: next=%q error=%v", next, err)
			}
			wantState := "completed"
			if mode == "stop" {
				wantState = "stopped"
			}
			if ended.Progress.State != wantState || ended.Progress.AckVersion != ended.Control.Version || ended.Progress.Deadline != before.Progress.Deadline || ended.Progress.ModelTasksReserved != before.Progress.ModelTasksReserved || len(ended.Progress.TaskIDs) != 0 {
				t.Fatalf("terminal boundary dispatched work or lost acknowledgement: %+v", ended)
			}
		})
	}
}

func TestTerminalBoundaryRechecksDefiniteControlConflict(t *testing.T) {
	for _, action := range []string{"stop", "pause", "step"} {
		t.Run(action, func(t *testing.T) {
			live, control, _ := liveFixture(t)
			call := live.call
			injected := false
			live.call = func(ctx context.Context, operation string, args any) (json.RawMessage, error) {
				if operation == "research.publish" && !injected {
					injected = true
					control(action, 1)
				}
				return call(ctx, operation, args)
			}
			ended, next, err := live.complete(context.Background())
			if action == "stop" {
				if !errors.Is(err, ErrResearchStopped) || ended.Progress.State != "stopped" {
					t.Fatalf("lost racing stop: %+v %v", ended, err)
				}
			} else if err != nil || next != map[string]string{"pause": "paused", "step": "step"}[action] {
				t.Fatalf("lost racing admission control: next=%s %v", next, err)
			}
			if ended.Progress.AckVersion != 2 || ended.Progress.Revision != 2 {
				t.Fatalf("rejected completion advanced revision or omitted acknowledgement: %+v", ended)
			}
		})
	}
}

func TestTerminalBoundaryDoesNotReplayUncertainPublication(t *testing.T) {
	live, _, _ := liveFixture(t)
	call := live.call
	publications := 0
	live.call = func(ctx context.Context, operation string, args any) (json.RawMessage, error) {
		if operation == "research.publish" {
			publications++
			return nil, &managed.Fault{Code: "unavailable", Message: "lost completion response"}
		}
		return call(ctx, operation, args)
	}
	if _, _, err := live.complete(context.Background()); err == nil || publications != 1 {
		t.Fatalf("uncertain completion retried: publications=%d err=%v", publications, err)
	}
}
