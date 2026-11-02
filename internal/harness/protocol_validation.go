package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

func (s *streamState) protocolError(message string) error {
	return fmt.Errorf("%w: %s %s", ErrProtocol, s.name, message)
}

// Validate only fields used to select an answer, identity or completion. New
// informational events/fields remain forward-compatible and stay in Events.
func (s *streamState) validateEvent(e object) error {
	kind := stringField(e, "type")
	if s.name == "antigravity" {
		kind = stringField(e, "event")
	}
	if kind == "" {
		return s.protocolError("event discriminator must be a nonempty string")
	}
	switch s.name {
	case "claude":
		if s.complete && (kind == "result" || kind == "assistant") {
			return s.protocolError("answer or terminal event followed the terminal result")
		}
		if raw, present := e["parent_tool_use_id"]; present && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if _, ok := requiredString(e, "parent_tool_use_id"); !ok {
				return s.protocolError("parent tool identity must be a string or null")
			}
		}
		if err := s.bindOptionalID(e, "session_id"); err != nil {
			return err
		}
		if kind == "assistant" {
			return validateMessageText(objectField(e, "message"), s.protocolError)
		}
		if kind == "result" {
			if _, ok := requiredString(e, "subtype"); !ok {
				return s.protocolError("result subtype must be a string")
			}
			if raw, ok := e["is_error"]; ok && !isBool(raw) {
				return s.protocolError("result is_error must be a boolean")
			}
			if stringField(e, "subtype") == "success" && !boolField(e, "is_error") {
				if _, ok := requiredString(e, "result"); !ok {
					return s.protocolError("successful result must contain its own result string")
				}
			}
			if raw, ok := e["permission_denials"]; ok && !isArray(raw) {
				return s.protocolError("permission_denials must be an array")
			}
		}
	case "codex":
		if s.complete && (kind == "thread.started" || kind == "turn.started" || kind == "turn.completed" || kind == "turn.failed" || kind == "error" || kind == "item.completed" && stringField(objectField(e, "item"), "type") == "agent_message") {
			return s.protocolError("answer or turn event followed the terminal result")
		}
		if kind == "thread.started" {
			id, ok := requiredString(e, "thread_id")
			if !ok || id == "" {
				return s.protocolError("thread.started must identify its thread")
			}
			if err := s.bindID(id); err != nil {
				return err
			}
		}
		if kind == "turn.started" && s.turnStarted {
			return s.protocolError("multiple turns were started for one request")
		}
		if kind == "item.completed" {
			item := objectField(e, "item")
			if item == nil || stringField(item, "type") == "" {
				return s.protocolError("completed item must identify its type")
			}
			if stringField(item, "type") == "agent_message" {
				if _, ok := requiredString(item, "text"); !ok {
					return s.protocolError("completed agent message must contain a text string")
				}
			}
		}
	case "pi":
		if kind == "response" {
			id, command := stringField(e, "id"), stringField(e, "command")
			if id == "agentmux-state" && command != "get_state" || id == "agentmux-prompt" && command != "prompt" {
				return s.protocolError("RPC response ID does not match its command")
			}
			if id == "agentmux-state" || id == "agentmux-prompt" {
				if !isBool(e["success"]) {
					return s.protocolError("RPC response success must be a boolean")
				}
			}
			if id == "agentmux-state" {
				if !boolField(e, "success") {
					return s.protocolError("session identity request failed")
				}
				data := objectField(e, "data")
				id, ok := requiredString(data, "sessionId")
				if !ok || id == "" {
					return s.protocolError("session identity response omitted its session ID")
				}
				return s.bindID(id)
			}
			if id == "agentmux-prompt" {
				if s.complete || s.accepted {
					return s.protocolError("duplicate or late prompt response")
				}
				if boolField(e, "success") {
					disposition := stringField(objectField(e, "data"), "disposition")
					if disposition != "started" && disposition != "handled" && disposition != "queued" {
						return s.protocolError("prompt response omitted a known disposition")
					}
				}
			}
		}
		if kind == "message_end" && (objectField(e, "message") == nil || stringField(objectField(e, "message"), "role") == "") {
			return s.protocolError("completed message must identify its role")
		}
		if kind == "message_end" && stringField(objectField(e, "message"), "role") == "assistant" {
			if s.complete || !s.accepted {
				return s.protocolError("assistant completion is outside the accepted prompt")
			}
			message := objectField(e, "message")
			if err := validateMessageText(message, s.protocolError); err != nil {
				return err
			}
			switch stringField(message, "stopReason") {
			case "stop", "toolUse", "length", "error", "aborted":
			default:
				return s.protocolError("assistant completion has no terminal stop reason")
			}
		}
		if kind == "agent_settled" {
			if s.complete || !s.accepted {
				return s.protocolError("settled event is outside the accepted prompt")
			}
			if !isBool(e["aborted"]) {
				return s.protocolError("settled aborted must be a boolean")
			}
		}
		if s.complete && (kind == "agent_start" || kind == "turn_start") {
			return s.protocolError("new work followed the terminal result")
		}
	case "antigravity":
		if s.complete && (kind == "result" || kind == "step_update" && stringField(objectField(e, "step_update"), "step_type") == "agent_response") {
			return s.protocolError("answer or terminal event followed the terminal result")
		}
		if err := s.bindOptionalID(e, "conversation_id"); err != nil {
			return err
		}
		if kind == "result" {
			result := objectField(e, "result")
			if raw, present := result["denied_actions"]; present {
				var actions []object
				if !isArray(raw) || json.Unmarshal(raw, &actions) != nil {
					return s.protocolError("denied_actions must be an array of action objects")
				}
				for _, action := range actions {
					if name, ok := requiredString(action, "action"); !ok || name == "" {
						return s.protocolError("denied action must identify its action")
					}
				}
			}
			if result == nil || stringField(result, "status") == "" {
				return s.protocolError("result must contain its status")
			}
			if err := s.bindOptionalID(result, "conversation_id"); err != nil {
				return err
			}
			if stringField(result, "status") == "SUCCESS" {
				if _, ok := requiredString(result, "response"); !ok {
					return s.protocolError("successful result must contain its own response string")
				}
			}
		}
	}
	return nil
}

func (s *streamState) bindOptionalID(e object, field string) error {
	if _, present := e[field]; !present {
		return nil
	}
	id, ok := requiredString(e, field)
	if !ok || id == "" {
		return s.protocolError("session identity must be a nonempty string")
	}
	return s.bindID(id)
}

func (s *streamState) bindID(id string) error {
	if s.result.SessionID != "" && s.result.SessionID != id {
		return s.protocolError("session identity changed within one request")
	}
	s.result.SessionID = id
	return nil
}

func requiredString(e object, key string) (string, bool) {
	// encoding/json permits null into a string; it is not an emitted empty answer.
	raw := bytes.TrimSpace(e[key])
	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func isBool(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("true")) || bytes.Equal(bytes.TrimSpace(raw), []byte("false"))
}

func isArray(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	return len(value) > 0 && value[0] == '['
}

func validateMessageText(message object, fail func(string) error) error {
	if !isArray(message["content"]) {
		return fail("assistant content must be an array")
	}
	var blocks []object
	if json.Unmarshal(message["content"], &blocks) != nil {
		return fail("assistant content blocks must be objects")
	}
	for _, block := range blocks {
		if block == nil || stringField(block, "type") == "" {
			return fail("assistant content block must identify its type")
		}
		if stringField(block, "type") == "text" {
			if _, ok := requiredString(block, "text"); !ok {
				return fail("assistant text block must contain a text string")
			}
		}
	}
	return nil
}

func (s *streamState) addPiUsage(raw json.RawMessage) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil // Unknown usage is not an observed zero.
	}
	usage := object{}
	if json.Unmarshal(raw, &usage) != nil || usage == nil {
		return s.protocolError("usage must be an object when reported")
	}
	counts, cost := map[string]float64{}, map[string]float64{}
	read := func(source object, target, previous map[string]float64, keys []string, integer bool) error {
		for _, key := range keys {
			raw, exists := source[key]
			if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				continue
			}
			var value float64
			if json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || integer && (math.Trunc(value) != value || value > 1<<53-1) {
				return s.protocolError("usage contains an invalid numeric observation")
			}
			total := previous[key] + value
			if math.IsInf(total, 0) || integer && total > 1<<53-1 {
				return s.protocolError("usage accumulation exceeds numeric bounds")
			}
			target[key] = total
		}
		return nil
	}
	if err := read(usage, counts, s.piUsage, []string{"input", "output", "cacheRead", "cacheWrite", "totalTokens"}, true); err != nil {
		return err
	}
	if rawCost, exists := usage["cost"]; exists && !bytes.Equal(bytes.TrimSpace(rawCost), []byte("null")) {
		costObject := objectField(usage, "cost")
		if costObject == nil {
			return s.protocolError("usage cost must be an object when reported")
		}
		if err := read(costObject, cost, s.piCost, []string{"input", "output", "cacheRead", "cacheWrite", "total"}, false); err != nil {
			return err
		}
	}
	if s.piUsage == nil {
		s.piUsage, s.piCost = map[string]float64{}, map[string]float64{}
	}
	for key, value := range counts {
		s.piUsage[key] = value
	}
	for key, value := range cost {
		s.piCost[key] = value
	}
	result := map[string]any{}
	for key, value := range s.piUsage {
		result[key] = value
	}
	if len(s.piCost) > 0 {
		result["cost"] = s.piCost
	}
	if len(result) != 0 {
		s.result.Usage, _ = json.Marshal(result)
	}
	return nil
}
