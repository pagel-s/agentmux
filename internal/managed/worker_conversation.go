package managed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"agentmux/internal/harness"
)

func workerConversation(ctx context.Context, caller workerCaller, req harness.Request, dir, id string, task *Task, lease string) (harness.Request, error) {
	op := "conversation.status"
	args := map[string]any{}
	if task != nil {
		op = "conversation.begin"
		args["id"], args["lease"] = task.ID, lease
	}
	data, _ := json.Marshal(args)
	r := Request{Operation: op, Args: data}
	if task != nil {
		r.RequestID = randomID("conversation_begin")
	}
	raw, problem := caller.Call(ctx, r)
	if problem != nil {
		return req, workerProblem(op, problem)
	}
	var response struct {
		Conversation ConversationState `json:"conversation"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return req, errors.New("invalid conversation response")
	}
	c := response.Conversation
	expected := "ready"
	if task != nil {
		expected = "busy"
	}
	if c.Agent != id || c.Generation < 1 || c.State != expected || (task != nil && c.TaskID != task.ID) {
		return req, errors.New("conversation is interrupted or invalid; stop and resolve the task, then reset its history")
	}
	privateDir, err := privateConversationDir(dir, id, c.Generation)
	if err != nil {
		return req, err
	}
	mode := "create"
	if c.SessionID != "" {
		mode = "resume"
	}
	req.Conversation = &harness.Conversation{Mode: mode, ID: c.SessionID, Dir: privateDir}
	return req, nil
}

func privateConversationDir(dir, id string, generation int) (string, error) {
	// Parents are never exposed to a worker. It can write its native transcript
	// tree, but cannot replace a sibling agent or a later generation's root.
	current := dir
	for _, part := range []string{"sessions", id, fmt.Sprintf("g-%d", generation)} {
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return "", errors.New("native conversation directory must be private and contain no symlink parents")
		}
	}
	return current, nil
}
