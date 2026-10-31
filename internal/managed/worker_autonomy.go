package managed

import (
	"context"
	"encoding/json"
	"errors"
)

func workerAutonomyReady(ctx context.Context, caller workerCaller) (bool, error) {
	raw, problem := caller.Call(ctx, Request{Operation: "autonomy.status", Args: json.RawMessage(`{}`)})
	if problem != nil {
		return false, workerProblem("autonomy.status", problem)
	}
	var out struct {
		Status *struct {
			Ready bool `json:"ready"`
		} `json:"status"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Status == nil {
		return false, errors.New("invalid autonomy status response")
	}
	return out.Status.Ready, nil
}

func workerWakeRead(ctx context.Context, caller workerCaller, id string) ([]Message, error) {
	args, _ := json.Marshal(map[string]string{"id": id})
	raw, problem := caller.Call(ctx, Request{Operation: "wake.read", Args: args})
	if problem != nil {
		return nil, workerProblem("wake.read", problem)
	}
	var out struct {
		TaskID   string     `json:"task_id"`
		Messages *[]Message `json:"messages"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.TaskID != id || out.Messages == nil {
		return nil, errors.New("invalid observation response")
	}
	return *out.Messages, nil
}
