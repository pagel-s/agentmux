package managed

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"time"
)

// ConversationState is a reference to private native history, not authority.
// Only a confirmed task completion advances it. An interrupted turn leaves it
// busy until the owner fences the task and explicitly resets the generation.
type ConversationState struct {
	Agent      string `json:"agent"`
	Generation int    `json:"generation"`
	State      string `json:"state"`
	SessionID  string `json:"session_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
}

var sessionReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func loadConversation(ctx context.Context, q queryer, id string) (ConversationState, error) {
	c := ConversationState{Agent: id, Generation: 1, State: "ready"}
	err := get(ctx, q, "conversation", "conversation_"+id, &c)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	return c, err
}

func saveConversation(ctx context.Context, q queryer, c ConversationState) error {
	return save(ctx, q, "conversation", "conversation_"+c.Agent, c.Agent, c.Agent, "", c)
}

func (s *Store) conversation(ctx context.Context, q queryer, a actor, r Request) (any, *Fault) {
	var in struct {
		Agent string `json:"agent"`
		ID    string `json:"id"`
		Lease string `json:"lease"`
	}
	if err := strictJSON(r.Args, &in); err != nil {
		return nil, invalid(err)
	}
	id := in.Agent
	if id == "" {
		id = a.ID
	}
	if !a.owner() && id != a.ID {
		return nil, forbidden()
	}
	info, err := s.agent(ctx, q, id)
	if err != nil || info.Config.Conversation != "persistent" {
		return nil, fault("unsupported", "agent does not use persistent conversations")
	}
	c, err := loadConversation(ctx, q, id)
	if err != nil {
		return nil, internal(err)
	}
	switch r.Operation {
	case "conversation.status":
		return map[string]any{"conversation": c}, nil
	case "conversation.reset":
		if !a.owner() {
			return nil, forbidden()
		}
		active, err := list[taskRecord](ctx, q, "task", "AND target=? AND json_extract(data,'$.delivery') IN ('dispatching','cancel_requested','uncertain','cancel_uncertain') LIMIT 1", id)
		if err != nil {
			return nil, internal(err)
		}
		if len(active) != 0 {
			return nil, fault("conflict", "stop and resolve active or uncertain delivery before resetting history")
		}
		c = ConversationState{Agent: id, Generation: c.Generation + 1, State: "ready"}
	case "conversation.begin":
		if a.owner() || id != a.ID {
			return nil, forbidden()
		}
		t, err := loadTask(ctx, q, in.ID)
		if err != nil || t.To != a.ID || t.Instance != a.Instance || in.Lease == "" || t.LeaseToken != in.Lease || t.Expires < time.Now().Unix() || t.State != "running" || t.Delivery != "dispatching" {
			return nil, fault("conflict", "conversation requires a current task lease")
		}
		if c.State != "ready" {
			return nil, fault("conflict", "conversation requires explicit owner reset after an interrupted turn")
		}
		c.State, c.TaskID = "busy", t.ID
	default:
		return nil, fault("unsupported", "unsupported conversation operation")
	}
	if err := saveConversation(ctx, q, c); err != nil {
		return nil, internal(err)
	}
	if err := event(ctx, q, a, r.Operation, id, map[string]any{"generation": c.Generation, "state": c.State}); err != nil {
		return nil, internal(err)
	}
	return map[string]any{"conversation": c}, nil
}

func (s *Store) finishConversation(ctx context.Context, q queryer, a actor, t taskRecord, sessionID string) *Fault {
	info, err := s.agent(ctx, q, t.To)
	if err != nil {
		return internal(err)
	}
	if info.Config.Conversation != "persistent" {
		if sessionID != "" {
			return fault("invalid", "fresh turns cannot commit native conversation references")
		}
		return nil
	}
	c, err := loadConversation(ctx, q, a.ID)
	if err != nil {
		return internal(err)
	}
	if c.State != "busy" || c.TaskID != t.ID {
		return fault("conflict", "task has no matching conversation turn")
	}
	if t.State == "submitted" {
		if !sessionReference.MatchString(sessionID) || (c.SessionID != "" && c.SessionID != sessionID) {
			return fault("invalid", "native conversation reference is missing or changed")
		}
		c.SessionID, c.State, c.TaskID = sessionID, "ready", ""
	} else {
		c.State = "blocked"
	}
	if err := saveConversation(ctx, q, c); err != nil {
		return internal(err)
	}
	return nil
}

func (s *Store) blockConversation(ctx context.Context, q queryer, a actor, taskID string) *Fault {
	c, err := loadConversation(ctx, q, a.ID)
	if err != nil {
		return internal(err)
	}
	if c.State != "busy" || c.TaskID != taskID {
		return nil
	}
	c.State = "blocked"
	if err := saveConversation(ctx, q, c); err != nil {
		return internal(err)
	}
	return nil
}
