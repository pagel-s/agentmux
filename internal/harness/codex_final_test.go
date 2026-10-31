package harness

import (
	"bytes"
	"encoding/json"
	"testing"
)

// The native exec JSONL contract uses agent_message items without app-server's
// camelCase agentMessage/optional phase schema. Preserve those event records,
// while returning only the last completed message as the task's final answer.
func TestCodexReturnsLastCompletedMessageWithoutProgressOrToolOutput(t *testing.T) {
	const answer = `{"project":null,"contribute":0}`
	events := []string{
		`{"type":"thread.started","thread_id":"thread-test"}`,
		`{"type":"item.started","item":{"id":"m1","type":"agent_message","text":"incomplete draft"}}`,
		`{"type":"item.completed","item":{"id":"m1","type":"agent_message","text":"I will inspect the board first."}}`,
		`{"type":"item.completed","item":{"id":"cmd1","type":"command_execution","aggregated_output":"untrusted tool output"}}`,
		`{"type":"item.updated","item":{"id":"m2","type":"agent_message","text":"uncommitted answer"}}`,
		`{"type":"item.completed","item":{"id":"m2","type":"agent_message","text":"{\"project\":null,\"contribute\":0}"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":11,"output_tokens":7}}`,
	}
	state := streamState{name: "codex"}
	for _, raw := range events {
		if err := state.consume([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if state.result.Text != answer || !json.Valid([]byte(state.result.Text)) || !state.complete || state.err != nil || state.result.SessionID != "thread-test" {
		t.Fatalf("final answer included progress or lost terminal state: %+v", state.result)
	}
	if len(state.result.Events) != len(events) {
		t.Fatal("canonical answer selection discarded raw events")
	}
	for i, raw := range events {
		if !bytes.Equal(state.result.Events[i], []byte(raw)) {
			t.Fatalf("native event %d was rewritten", i)
		}
	}
	var usage map[string]int
	if json.Unmarshal(state.result.Usage, &usage) != nil || usage["input_tokens"] != 11 || usage["output_tokens"] != 7 {
		t.Fatal("answer selection changed native usage")
	}
}

func TestCodexFinalMessageIsExactIncludingEmptyAndNonJSONAnswers(t *testing.T) {
	for _, answer := range []string{"", "prose before {\"project\":null,\"contribute\":0}", "A multiline answer.\nSecond paragraph."} {
		t.Run(answer, func(t *testing.T) {
			state := streamState{name: "codex"}
			if err := state.consume([]byte(`{"type":"item.completed","item":{"type":"agent_message","text":"earlier commentary"}}`)); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": answer}})
			if err := state.consume(raw); err != nil {
				t.Fatal(err)
			}
			if state.result.Text != answer {
				t.Fatal("adapter reused commentary, trimmed, or salvaged a JSON suffix")
			}
			if err := state.consume([]byte(`{"type":"turn.failed","error":{"message":"native failure"}}`)); err != nil {
				t.Fatal(err)
			}
			if state.err == nil || !state.complete {
				t.Fatal("a final message concealed native turn failure")
			}
		})
	}
}
