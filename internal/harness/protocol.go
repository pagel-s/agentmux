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
	answerSeen  bool
	turnStarted bool
	lastPiStop  string
}

func (s *streamState) consume(raw []byte) error {
	var event object
	if err := json.Unmarshal(raw, &event); err != nil || event == nil {
		return fmt.Errorf("invalid %s JSON event", s.name)
	}
	s.result.Events = append(s.result.Events, append(json.RawMessage(nil), raw...))
	// Subagent output is evidence, not the main Claude turn's answer or ID.
	if s.name == "claude" && (stringField(event, "type") == "assistant" || stringField(event, "type") == "result") && (stringField(event, "parent_tool_use_id") != "" || stringField(event, "agent_id") != "") {
		return nil
	}
	if err := s.validateEvent(event); err != nil {
		return err
	}
	switch s.name {
	case "claude":
		s.claude(event)
	case "codex":
		s.codex(event)
	case "antigravity":
		s.antigravity(event)
	case "pi":
		if stringField(event, "type") == "message_end" && stringField(objectField(event, "message"), "role") == "assistant" {
			if err := s.addPiUsage(objectField(event, "message")["usage"]); err != nil {
				return err
			}
		}
		s.pi(event)
	}
	return nil
}

func (s *streamState) claude(e object) {
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
				s.answerSeen = true
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
	case "system":
		if stringField(e, "subtype") == "permission_denied" {
			s.denied = true
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
			s.answerSeen = true
		}
	case "turn.started":
		s.turnStarted = true
	case "turn.completed":
		s.complete = true
		// Exec currently drops app-server's will_retry flag from error
		// notifications. A later explicit successful turn plus zero process
		// exit resolves that ambiguity; raw preceding errors stay in Events.
		s.err = nil
		s.result.Usage = cloneJSON(e["usage"])
	case "turn.failed":
		s.complete = true
		s.err = terminalError(s.name, "turn.failed", errorText(e["error"]))
	case "error":
		// An error without a later successful terminal event remains failure.
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
		s.answerSeen = true
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
		if disposition := stringField(objectField(e, "data"), "disposition"); disposition != "started" {
			// Handled/queued acknowledge receipt, not execution of our one
			// submitted turn. Independent extension work is not its result.
			s.complete = true
			s.err = fmt.Errorf("%w: pi prompt was %s rather than started", ErrIncomplete, disposition)
		}
	case "message_end":
		message := objectField(e, "message")
		if stringField(message, "role") != "assistant" {
			return
		}
		s.result.Text = messageText(message)
		s.answerSeen = true
		s.lastPiStop = stringField(message, "stopReason")
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
		if !s.answerSeen || s.lastPiStop == "toolUse" {
			s.err = fmt.Errorf("%w: pi settled without a final assistant completion", ErrIncomplete)
		}
		if boolField(e, "aborted") {
			s.err = terminalError(s.name, "aborted", "")
		}
	}
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
