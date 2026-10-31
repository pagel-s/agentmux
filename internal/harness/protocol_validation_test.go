package harness

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	piState      = `{"type":"response","command":"get_state","id":"agentmux-state","success":true,"data":{"sessionId":"fixture-session"}}`
	piAccepted   = `{"type":"response","command":"prompt","id":"agentmux-prompt","success":true,"data":{"disposition":"started"}}`
	piAnswer     = `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"answer"}],"stopReason":"stop"}}`
	piSettled    = `{"type":"agent_settled","aborted":false}`
	claudeAnswer = `{"type":"result","subtype":"success","session_id":"fixture-session","result":"answer"}`
	codexAnswer  = `{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}`
	codexDone    = `{"type":"turn.completed","usage":{"input_tokens":1}}`
	agyAnswer    = `{"event":"result","result":{"status":"SUCCESS","response":"answer","conversation_id":"fixture-session"}}`
)

func protocolRequest(name string, events ...string) Request {
	req := helperRequest(name, "protocol-fixture")
	req.Env["AGENTMUX_PROTOCOL_NAME"] = name
	req.Env["AGENTMUX_PROTOCOL_LINES"] = strings.Join(events, "\n") + "\n"
	return req
}

func TestMalformedResultsCannotReuseEarlierTextOrClaimEmptySuccess(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		events []string
		want   error
	}{
		{"claude", []string{`{"type":"assistant","message":{"content":[{"type":"text","text":"earlier commentary"}]}}`, `{"type":"result","subtype":"success"}`}, ErrProtocol},
		{"claude", []string{`{"type":"result","subtype":"success","result":null}`}, ErrProtocol},
		{"claude", []string{`{"type":"result","subtype":"success","result":{},"is_error":false}`}, ErrProtocol},
		{"claude", []string{`{"type":"result","subtype":"success","result":"answer","is_error":"false"}`}, ErrProtocol},
		{"claude", []string{`{"type":"result","subtype":"success","result":"answer","permission_denials":{}}`}, ErrProtocol},
		{"claude", []string{`{"type":"assistant","message":{"content":[{"type":"text","text":null}]}}`, claudeAnswer}, ErrProtocol},
		{"codex", []string{codexDone}, ErrIncomplete},
		{"codex", []string{codexAnswer, `{"type":"item.completed","item":{"type":"agent_message","text":null}}`, codexDone}, ErrProtocol},
		{"antigravity", []string{`{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"earlier draft"}}`, `{"event":"result","result":{"status":"SUCCESS"}}`}, ErrProtocol},
		{"pi", []string{piState, piAccepted, piSettled}, ErrIncomplete},
		{"pi", []string{piState, piAccepted, `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"pending"}}`, piSettled}, ErrProtocol},
		{"pi", []string{piState, piAccepted, `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"toolUse"}}`, piSettled}, ErrIncomplete},
		{"pi", []string{piState, piAccepted, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":99}],"stopReason":"stop"}}`, piSettled}, ErrProtocol},
	} {
		t.Run(fixture.name+"/"+strings.Join(fixture.events, ""), func(t *testing.T) {
			_, err := Run(context.Background(), protocolRequest(fixture.name, fixture.events...))
			if !errors.Is(err, fixture.want) {
				t.Fatalf("error=%v want=%v", err, fixture.want)
			}
		})
	}
}

func TestExplicitEmptyAnswersRemainValidAndExact(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		events []string
	}{
		{"claude", []string{`{"type":"result","subtype":"success","result":""}`}},
		{"codex", []string{`{"type":"item.completed","item":{"type":"agent_message","text":""}}`, codexDone}},
		{"pi", []string{piState, piAccepted, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":""}],"stopReason":"stop"}}`, piSettled}},
		{"antigravity", []string{`{"event":"result","result":{"status":"SUCCESS","response":""}}`}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			result, err := Run(context.Background(), protocolRequest(fixture.name, fixture.events...))
			if err != nil || result.Text != "" {
				t.Fatalf("explicit empty answer rejected/changed: %v %q", err, result.Text)
			}
		})
	}
}

func TestTerminalResultCannotBeReplacedOrResurrected(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		events []string
	}{
		{"claude", []string{claudeAnswer, `{"type":"result","subtype":"success","result":"replacement"}`}},
		{"claude", []string{claudeAnswer, `{"type":"assistant","message":{"content":[{"type":"text","text":"late commentary"}]}}`}},
		{"codex", []string{codexAnswer, `{"type":"turn.failed","error":{"message":"failed"}}`, codexDone}},
		{"codex", []string{codexAnswer, codexDone, `{"type":"item.completed","item":{"type":"agent_message","text":"replacement"}}`}},
		{"pi", []string{piState, piAccepted, piAnswer, piSettled, piAnswer}},
		{"pi", []string{piState, piAccepted, piAnswer, piSettled, piSettled}},
		{"antigravity", []string{agyAnswer, `{"event":"result","result":{"status":"SUCCESS","response":"replacement"}}`}},
	} {
		t.Run(fixture.name+"/"+strings.Join(fixture.events, ""), func(t *testing.T) {
			result, err := Run(context.Background(), protocolRequest(fixture.name, fixture.events...))
			if !errors.Is(err, ErrProtocol) || result.Text != "answer" {
				t.Fatalf("terminal outcome changed: text=%q err=%v", result.Text, err)
			}
			if len(result.Events) != len(fixture.events) {
				t.Fatal("rejected protocol evidence was discarded")
			}
		})
	}
}

func TestCodexRecoveryRequiresSuccessfulTerminalEventAndProcessExit(t *testing.T) {
	errorEvent := `{"type":"error","message":"stream disconnected"}`
	result, err := Run(context.Background(), protocolRequest("codex", errorEvent, codexAnswer, codexDone))
	if err != nil || result.Text != "answer" || len(result.Events) != 3 || string(result.Events[0]) != errorEvent {
		t.Fatalf("explicit recovery failed or lost preceding evidence: %v", err)
	}
	for _, events := range [][]string{{errorEvent}, {errorEvent, codexAnswer}, {errorEvent, codexAnswer, `{"type":"turn.failed","error":{"message":"native turn failed"}}`}} {
		if _, err := Run(context.Background(), protocolRequest("codex", events...)); err == nil {
			t.Fatal("unresolved native error became success")
		}
	}
	req := protocolRequest("codex", errorEvent, codexAnswer, codexDone)
	req.Env["AGENTMUX_PROTOCOL_FAIL_EXIT"] = "1"
	if _, err := Run(context.Background(), req); err == nil {
		t.Fatal("terminal event concealed nonzero process exit")
	}
	result, err = Run(context.Background(), protocolRequest("codex", `{"type":"item.completed","item":{"type":"error","message":"recoverable warning"}}`, codexAnswer, codexDone))
	if err != nil || result.Text != "answer" {
		t.Fatalf("warning became terminal: %v", err)
	}
}

func TestSessionIDsArePinnedAndClaudeSubagentsCannotReplaceProvenance(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		events []string
	}{
		{"claude", []string{`{"type":"system","session_id":"first"}`, claudeAnswer}},
		{"codex", []string{`{"type":"thread.started","thread_id":"first"}`, `{"type":"thread.started","thread_id":"second"}`}},
		{"pi", []string{piState, strings.Replace(piState, "fixture-session", "second", 1)}},
		{"antigravity", []string{`{"event":"init","conversation_id":"first"}`, agyAnswer}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			result, err := Run(context.Background(), protocolRequest(fixture.name, fixture.events...))
			if !errors.Is(err, ErrProtocol) || result.SessionID == "second" {
				t.Fatalf("identity swap accepted: %+v %v", result, err)
			}
		})
	}
	result, err := Run(context.Background(), protocolRequest("claude",
		`{"type":"system","session_id":"fixture-session"}`,
		`{"type":"assistant","session_id":"child","agent_id":"subagent","parent_tool_use_id":null,"message":{"content":[{"type":"text","text":"child answer"}]}}`,
		`{"type":"result","session_id":"child","agent_id":"subagent","parent_tool_use_id":"tool-1","subtype":"success","result":"child answer"}`,
		claudeAnswer, `{"type":"system","session_id":"fixture-session","subtype":"informational","content":"teardown"}`))
	if err != nil || result.SessionID != "fixture-session" || result.Text != "answer" || len(result.Events) != 5 {
		t.Fatalf("subagent contaminated main result: %+v %v", result, err)
	}
}

func TestPiAcknowledgementsAreNotModelExecutionAndRPCResponsesAreCorrelated(t *testing.T) {
	for _, disposition := range []string{"handled", "queued"} {
		req := protocolRequest("pi", piState, strings.Replace(piAccepted, "started", disposition, 1))
		result, err := Run(context.Background(), req)
		if !errors.Is(err, ErrIncomplete) || result.Text != "" {
			t.Fatalf("%s claimed completed work: %v", disposition, err)
		}
	}
	for _, bad := range []string{
		strings.Replace(piState, `"success":true`, `"success":false`, 1),
		strings.Replace(piAccepted, `"command":"prompt"`, `"command":"get_state"`, 1),
		strings.Replace(piAccepted, `"success":true`, `"success":"true"`, 1),
		strings.Replace(piAccepted, `{"disposition":"started"}`, `{}`, 1),
	} {
		if _, err := Run(context.Background(), protocolRequest("pi", bad)); !errors.Is(err, ErrProtocol) {
			t.Fatalf("RPC mismatch/malformed response passed: %v", err)
		}
	}
}

func TestClaudeStreamDenialCannotBeErasedByEmptyTerminalDenials(t *testing.T) {
	result, err := Run(context.Background(), protocolRequest("claude", `{"type":"system","subtype":"permission_denied","tool_name":"Bash"}`, claudeAnswer))
	if !errors.Is(err, ErrToolDenied) || result.Text != "answer" {
		t.Fatalf("observed denial was lost: %v", err)
	}
}

func TestPiUsagePreservesUnknownsAndRejectsInvalidOrOverflowingObservations(t *testing.T) {
	state := streamState{name: "pi"}
	for _, unknown := range []string{"", `null`, `{}`, `{"input":null,"cost":null}`} {
		if err := state.addPiUsage(json.RawMessage(unknown)); err != nil || len(state.result.Usage) != 0 {
			t.Fatalf("unknown became zero: %s %v", state.result.Usage, err)
		}
	}
	if err := state.addPiUsage(json.RawMessage(`{"input":0,"output":3,"cost":{"output":0}}`)); err != nil {
		t.Fatal(err)
	}
	observed := string(state.result.Usage)
	if strings.Contains(observed, "totalTokens") || strings.Contains(observed, "cacheRead") {
		t.Fatal("missing counters were fabricated")
	}
	for _, bad := range []string{`{"input":-1}`, `{"output":1.5}`, `{"totalTokens":9007199254740992}`, `{"output":"3"}`, `{"cost":{"total":-0.1}}`, `{"cost":[]}`} {
		if err := state.addPiUsage(json.RawMessage(bad)); !errors.Is(err, ErrProtocol) || string(state.result.Usage) != observed {
			t.Fatalf("invalid usage corrupted prior observations: %s err=%v", bad, err)
		}
	}
	for _, fixture := range []struct{ first, second string }{
		{`{"input":9007199254740991}`, `{"input":1}`},
		{`{"cost":{"total":1e308}}`, `{"cost":{"total":1e308}}`},
	} {
		state := streamState{name: "pi"}
		if err := state.addPiUsage(json.RawMessage(fixture.first)); err != nil {
			t.Fatal(err)
		}
		before := string(state.result.Usage)
		if err := state.addPiUsage(json.RawMessage(fixture.second)); !errors.Is(err, ErrProtocol) || string(state.result.Usage) != before {
			t.Fatalf("overflow erased usage: %s %v", state.result.Usage, err)
		}
	}
}

func TestPrematureNativeSuccessDoesNotHideUndeliveredPrompt(t *testing.T) {
	req := helperRequest("codex", "early-success")
	req.Prompt = strings.Repeat("undelivered task input ", 35000)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := Run(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "deliver complete codex prompt") || result.Text != "unearned answer" {
		t.Fatalf("undelivered prompt accepted as completed work: %v", err)
	}
}
