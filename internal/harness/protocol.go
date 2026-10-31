package harness

import (
	"encoding/json"
	"fmt"
	"strings"
)

type object map[string]json.RawMessage

type streamState struct {
	name        string
	result      Result
	complete    bool
	accepted    bool
	denied      bool
	err         error
	lastPiError error
	piUsage     map[string]float64
	piCost      map[string]float64
}

func (s *streamState) consume(raw []byte) error {
	var event object
	if err := json.Unmarshal(raw, &event); err != nil || event == nil {
		return fmt.Errorf("invalid %s JSON event", s.name)
	}
	s.result.Events = append(s.result.Events, append(json.RawMessage(nil), raw...))
	switch s.name {
	case "claude":
		s.claude(event)
	case "codex":
		s.codex(event)
	case "antigravity":
		s.antigravity(event)
	case "pi":
		s.pi(event)
	}
	return nil
}

func (s *streamState) claude(e object) {
	if id := stringField(e, "session_id"); id != "" {
		s.result.SessionID = id
	}
	switch stringField(e, "type") {
	case "assistant":
		// Subagent messages, when enabled, must not replace the main answer.
		if stringField(e, "parent_tool_use_id") == "" {
			s.result.Text = messageText(objectField(e, "message"))
		}
	case "result":
		s.complete = true
		if value, ok := e["result"]; ok {
			var text string
			if json.Unmarshal(value, &text) == nil {
				s.result.Text = text
			}
		}
		s.result.Usage = cloneJSON(e["usage"])
		var denials []json.RawMessage
		if json.Unmarshal(e["permission_denials"], &denials) == nil && len(denials) > 0 {
			s.denied = true
		}
		if boolField(e, "is_error") || stringField(e, "subtype") != "success" {
			message := stringField(e, "result")
			if message == "" {
				message = errorText(e["errors"])
			}
			s.err = terminalError(s.name, stringField(e, "subtype"), message)
		}
	}
}

func (s *streamState) codex(e object) {
	switch stringField(e, "type") {
	case "thread.started":
		s.result.SessionID = stringField(e, "thread_id")
	case "item.completed":
		item := objectField(e, "item")
		if stringField(item, "type") == "agent_message" {
			// Exec's JSONL stream contains interim messages as separate items.
			// Its final answer is the last completed agent message, matching
			// --output-last-message; the full stream stays in result.Events.
			// https://learn.chatgpt.com/docs/non-interactive-mode
			s.result.Text = stringField(item, "text")
		}
	case "turn.completed":
		s.complete = true
		s.err = nil // A transient stream error may have recovered.
		s.result.Usage = cloneJSON(e["usage"])
	case "turn.failed":
		s.complete = true
		s.err = terminalError(s.name, "turn.failed", errorText(e["error"]))
	case "error":
		// A successful terminal event can clear a recoverable stream error.
		s.err = terminalError(s.name, "error", stringField(e, "message"))
	}
}

func (s *streamState) antigravity(e object) {
	if id := stringField(e, "conversation_id"); id != "" {
		s.result.SessionID = id
	}
	switch stringField(e, "event") {
	case "step_update":
		step := objectField(e, "step_update")
		if stringField(step, "step_type") == "agent_response" {
			s.result.Text += stringField(step, "text_delta")
		}
		tool := objectField(step, "tool_info")
		if permissionDenial(errorText(tool["error"])) {
			s.denied = true
		}
	case "result":
		s.complete = true
		result := objectField(e, "result")
		if id := stringField(result, "conversation_id"); id != "" {
			s.result.SessionID = id
		}
		s.result.Text = stringField(result, "response")
		s.result.Usage = cloneJSON(result["usage"])
		if stringField(result, "status") != "SUCCESS" {
			s.err = terminalError(s.name, stringField(result, "status"), errorText(result["error"]))
		}
	}
}

func (s *streamState) pi(e object) {
	switch stringField(e, "type") {
	case "response":
		if stringField(e, "command") == "get_state" && stringField(e, "id") == "agentmux-state" && boolField(e, "success") {
			s.result.SessionID = stringField(objectField(e, "data"), "sessionId")
			return
		}
		if stringField(e, "command") != "prompt" || stringField(e, "id") != "agentmux-prompt" {
			return
		}
		if !boolField(e, "success") {
			s.complete = true
			s.err = terminalError(s.name, "prompt rejected", errorText(e["error"]))
			return
		}
		s.accepted = true
		if stringField(objectField(e, "data"), "disposition") == "handled" {
			// The protocol explicitly says no run follows a handled command.
			s.complete = true
		}
	case "message_end":
		message := objectField(e, "message")
		if stringField(message, "role") != "assistant" {
			return
		}
		s.result.Text = messageText(message)
		s.addPiUsage(message["usage"])
		switch reason := stringField(message, "stopReason"); reason {
		case "error", "aborted", "length":
			s.lastPiError = terminalError(s.name, reason, stringField(message, "errorMessage"))
		default:
			s.lastPiError = nil
		}
	case "tool_execution_end":
		if boolField(e, "isError") && permissionDenial(errorText(e["result"])) {
			s.denied = true
		}
	case "agent_settled":
		s.complete = true
		s.err = s.lastPiError
		if boolField(e, "aborted") {
			s.err = terminalError(s.name, "aborted", "")
		}
	}
}

func (s *streamState) addPiUsage(raw json.RawMessage) {
	usage := object{}
	if json.Unmarshal(raw, &usage) != nil {
		return
	}
	if s.piUsage == nil {
		s.piUsage, s.piCost = map[string]float64{}, map[string]float64{}
	}
	for _, key := range []string{"input", "output", "cacheRead", "cacheWrite", "totalTokens"} {
		var value float64
		if json.Unmarshal(usage[key], &value) == nil {
			s.piUsage[key] += value
		}
	}
	for key, rawValue := range objectField(usage, "cost") {
		var value float64
		if json.Unmarshal(rawValue, &value) == nil {
			s.piCost[key] += value
		}
	}
	result := map[string]any{}
	for key, value := range s.piUsage {
		result[key] = value
	}
	if len(s.piCost) > 0 {
		result["cost"] = s.piCost
	}
	s.result.Usage, _ = json.Marshal(result)
}

func cloneJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || string(value) == "null" {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func stringField(e object, name string) string {
	var result string
	_ = json.Unmarshal(e[name], &result)
	return result
}

func boolField(e object, name string) bool {
	var result bool
	_ = json.Unmarshal(e[name], &result)
	return result
}

func objectField(e object, name string) object {
	var result object
	_ = json.Unmarshal(e[name], &result)
	return result
}

func messageText(message object) string {
	var content []object
	if json.Unmarshal(message["content"], &content) != nil {
		return ""
	}
	var text strings.Builder
	for _, block := range content {
		if stringField(block, "type") == "text" {
			text.WriteString(stringField(block, "text"))
		}
	}
	return text.String()
}

func errorText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var message object
	if json.Unmarshal(raw, &message) == nil {
		return strings.TrimSpace(stringField(message, "type") + " " + stringField(message, "message") + " " + messageText(message))
	}
	var messages []string
	if json.Unmarshal(raw, &messages) == nil {
		return strings.Join(messages, "; ")
	}
	return ""
}

func terminalError(name, status, message string) error {
	if status == "" {
		status = "unknown terminal status"
	}
	if message != "" {
		return fmt.Errorf("%s %s: %s", name, status, message)
	}
	return fmt.Errorf("%s %s", name, status)
}
