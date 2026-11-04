package managed

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

var errWorkerProgressLimit = errors.New("native progress event count limit reached")

// Progress contains metadata only. Raw provider output and credential values
// never enter this channel, and heartbeats do not assert useful task progress.
type workerTurnProgress struct {
	mu       sync.Mutex
	count    int
	kind     string
	disabled bool
}

func (p *workerTurnProgress) event(raw json.RawMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.count >= 1000000 {
		return errWorkerProgressLimit
	}
	p.count++
	p.kind = workerEventKind(raw)
	return nil
}

func workerEventKind(raw json.RawMessage) string {
	var frame struct {
		Type  string `json:"type"`
		Event string `json:"event"`
		Item  struct {
			Type string `json:"type"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &frame) != nil {
		return "other"
	}
	kind := frame.Type
	if kind == "" {
		kind = frame.Event
	}
	switch kind {
	case "assistant", "message", "message_start", "message_update", "message_end", "message_delta", "message_stop", "content_block_start", "content_block_delta", "content_block_stop", "text_delta":
		return "message"
	case "tool_call", "tool_result", "tool_execution_start", "tool_execution_update", "tool_execution_end":
		return "tool"
	case "item.started", "item.updated", "item.completed":
		switch frame.Item.Type {
		case "agent_message", "reasoning":
			return "message"
		case "command_execution", "file_change", "mcp_tool_call", "web_search", "function_call", "function_call_output":
			return "tool"
		}
		return "native_event"
	case "result", "turn.completed", "done", "agent_end":
		return "result"
	case "usage":
		return "usage"
	case "error", "turn.failed":
		return "error"
	case "system", "thread.started", "turn.started", "init", "agent_start", "turn_start", "turn_end", "stream_event", "response":
		return "native_event"
	default:
		return "other"
	}
}

func (p *workerTurnProgress) flush(ctx context.Context, caller workerCaller, taskID, lease string) error {
	p.mu.Lock()
	count, kind, disabled := p.count, p.kind, p.disabled
	p.mu.Unlock()
	if disabled {
		return nil
	}
	args, _ := json.Marshal(map[string]any{"id": taskID, "lease": lease, "event_count": count, "event_kind": kind})
	raw, problem := caller.Call(ctx, Request{Operation: "task.observe", Args: args})
	if problem != nil {
		if problem.Code == "unsupported" {
			// Older controllers still enforce task.get's exact lease monitor.
			// They cannot advertise this optional live observation channel.
			p.mu.Lock()
			p.disabled = true
			p.mu.Unlock()
			return nil
		}
		return workerProblem("task.observe", problem)
	}
	var result struct {
		Observed bool `json:"observed"`
	}
	if json.Unmarshal(raw, &result) != nil || !result.Observed {
		return errors.New("invalid native progress acknowledgement; worker stopped")
	}
	return nil
}
