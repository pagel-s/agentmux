package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

var consoleTaskReads atomic.Uint64

type consoleTaskPage struct {
	ExactID          string
	Recipient, State string
	Query            string
	After            string
	Cursor           string
	More             bool
	Epoch            uint64
	ReadSequence     uint64
}

func loadConsoleTask(ctx context.Context, client *managed.Client, id string) (managed.Task, error) {
	if !validConsoleTaskID(id) {
		return managed.Task{}, errors.New("use an exact task_ ID to open its workbench view")
	}
	raw, err := workCall(ctx, client, "task.get", map[string]string{"id": id})
	if err != nil {
		return managed.Task{}, err
	}
	var result struct {
		Task managed.Task `json:"task"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return managed.Task{}, err
	}
	if !validConsoleTaskRecord(result.Task, id) {
		return managed.Task{}, errors.New("broker returned a different, missing or invalid exact task")
	}
	return result.Task, nil
}

func validConsoleTaskID(id string) bool {
	return strings.HasPrefix(id, "task_") && len(id) <= 256 && managedconfig.ValidID(id)
}

func validConsoleTaskRecord(task managed.Task, id string) bool {
	if task.ID != id || task.Version < 1 || !managedconfig.ValidID(task.To) {
		return false
	}
	switch task.State {
	case "proposed", "running", "submitted", "verified", "failed", "cancelled":
	default:
		return false
	}
	switch task.Delivery {
	case "pending", "dispatching", "acknowledged", "failed", "stopped", "cancel_requested", "cancel_uncertain", "uncertain":
		return true
	}
	return false
}

func (m *console) leaveExactTask() {
	if m.taskExactID == "" {
		return
	}
	m.taskExactID = ""
	m.taskVersion = 0
	m.taskReadSequence = 0
	m.taskEpoch++
	m.taskAfter, m.taskCursor = "", ""
	m.taskBack = nil
	m.taskMore = false
	m.task = 0
	m.data.activity.Tasks = nil
	m.data.taskPage = nil
}
