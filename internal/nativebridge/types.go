// Package nativebridge connects native harness sessions to scoped Agentmux
// deliveries. Provider adapters never transmit terminal drafts or type keystrokes.
package nativebridge

import (
	"context"
	"encoding/json"
	"fmt"
)

type Capabilities struct {
	Instructions       bool   `json:"instructions"`
	Observations       bool   `json:"observations"`
	Completion         bool   `json:"completion"`
	CompletionEvidence string `json:"completion_evidence,omitempty"`
	Cancellation       bool   `json:"cancellation"`
	PreservesDraft     bool   `json:"preserves_draft"`
	NativeQueue        bool   `json:"native_queue"`
}

type Registration struct {
	Adapter      string       `json:"adapter"`
	Session      string       `json:"session"`
	Capabilities Capabilities `json:"capabilities"`
}

type Readiness struct {
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

type Delivery struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id,omitempty"`
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	Scope    string `json:"scope,omitempty"`
	SourceID string `json:"source_id,omitempty"`
	Text     string `json:"text"`
	ThreadID string `json:"thread_id,omitempty"`
	ReplyTo  string `json:"reply_to,omitempty"`
}

type Event struct {
	ID        string `json:"id"`
	Phase     string `json:"phase"`
	NativeRef string `json:"native_ref,omitempty"`
	Result    string `json:"result,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type Status struct {
	MaxResultBytes int    `json:"max_result_bytes,omitempty"`
	Mode           string `json:"mode"`
	State          string `json:"state"`
	Reason         string `json:"reason,omitempty"`
	Cancel         bool   `json:"cancel"`
	ActiveID       string `json:"active_id,omitempty"`
}

// Connection is implemented by Client for real broker calls and by fixtures
// for model-free provider contract tests. Poll leases at most one authorized
// delivery. A provider must never replay a submitted-but-unconfirmed delivery.
type Connection interface {
	Register(context.Context, Registration) error
	Poll(context.Context, Readiness) (*Delivery, error)
	Report(context.Context, Event) error
	Status(context.Context) (Status, error)
}

type CallFunc func(context.Context, string, any) (json.RawMessage, error)

type Client struct {
	Call    CallFunc
	Session string
}

func (c *Client) Register(ctx context.Context, r Registration) error {
	_, err := c.Call(ctx, "native.register", r)
	if err == nil {
		c.Session = r.Session
	}
	return err
}

func (c *Client) Poll(ctx context.Context, ready Readiness) (*Delivery, error) {
	raw, err := c.Call(ctx, "native.peek", map[string]any{"session": c.Session, "state": ready.State, "reason": ready.Reason})
	if err != nil {
		return nil, err
	}
	var result struct {
		Candidate string `json:"candidate"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.Candidate == "" {
		return nil, nil
	}
	raw, err = c.Call(ctx, "native.claim", map[string]any{"session": c.Session, "candidate": result.Candidate})
	if err != nil {
		return nil, err
	}
	var claimed struct {
		Delivery *Delivery `json:"delivery"`
	}
	if err := json.Unmarshal(raw, &claimed); err != nil {
		return nil, err
	}
	return claimed.Delivery, nil
}

func (c *Client) Report(ctx context.Context, event Event) error {
	args := map[string]any{"session": c.Session, "id": event.ID, "phase": event.Phase, "native_ref": event.NativeRef, "result": event.Result, "reason": event.Reason}
	_, err := c.Call(ctx, "native.event", args)
	return err
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	raw, err := c.Call(ctx, "native.status", map[string]any{"session": c.Session})
	var result struct {
		Status Status `json:"status"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &result)
	}
	return result.Status, err
}

func Prompt(d Delivery) string {
	kind := "Agent-authored instruction"
	origin := "This message is from another agent, not the human owner."
	if d.From == "$owner" {
		kind = "Owner-authored instruction"
		origin = "The workspace owner sent this through Agentmux; it remains separate from direct terminal input."
	}
	if d.Kind != "instruction" {
		kind = "Observation, not an instruction grant"
		if d.Kind == "boundaries" {
			origin = "The coordination service supplied this current owner role and boundary notice."
		}
	}
	return fmt.Sprintf("[Agentmux %s from %q; delivery %s; task %s]\n%s Current broker permissions remain authoritative.\n\n%s", kind, d.From, d.ID, d.TaskID, origin, d.Text)
}
