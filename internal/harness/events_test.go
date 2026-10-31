package harness

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEventCallbackOwnsCopyAndRetainsValidatedOrder(t *testing.T) {
	req := helperRequest("codex", "codex")
	var events []json.RawMessage
	req.OnEvent = func(event json.RawMessage) error {
		events = append(events, append(json.RawMessage(nil), event...))
		for i := range event {
			event[i] = 'x'
		}
		return nil
	}
	result, err := Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || len(result.Events) != 3 {
		t.Fatalf("events=%d retained=%d", len(events), len(result.Events))
	}
	for i := range events {
		if string(events[i]) != string(result.Events[i]) {
			t.Fatal("callback mutation changed retained event")
		}
	}
}

func TestAfterProcessCommitsTranscriptBeforeVerification(t *testing.T) {
	req := helperRequest("claude", "conversation-hook")
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	req.Conversation = &Conversation{Mode: "create", ID: conversationTestID, Dir: dir}
	req.Wrap = func(spec Spec) (Spec, error) { return spec, nil }
	marker := filepath.Join(t.TempDir(), "process-finished")
	req.Env["AGENTMUX_PROCESS_FINISHED"] = marker
	calls := 0
	req.AfterProcess = func() error {
		calls++
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("checkpoint ran before child completion")
		}
		return os.WriteFile(filepath.Join(dir, conversationTestID+".jsonl"), []byte("{}\n"), 0600)
	}
	if _, err := Build(req); err != nil || calls != 0 {
		t.Fatal("Build called checkpoint or failed")
	}
	result, err := Run(context.Background(), req)
	if err != nil || result.Text != "checkpoint-answer" || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestAfterProcessFailureFailsClosedAndPreservesExecutionError(t *testing.T) {
	failed := errors.New("checkpoint failed")
	for _, scenario := range []string{"codex", "codex-error"} {
		req := helperRequest("codex", scenario)
		calls := 0
		req.AfterProcess = func() error { calls++; return failed }
		result, err := Run(context.Background(), req)
		if !errors.Is(err, failed) || calls != 1 || len(result.Events) != 3 {
			t.Fatalf("scenario=%s err=%v calls=%d", scenario, err, calls)
		}
		if scenario == "codex-error" && !strings.Contains(err.Error(), "turn.failed") {
			t.Fatal("original process failure was discarded")
		}
	}
}

func TestEventCallbackFailureStopsTurnAndPreservesPartialResult(t *testing.T) {
	req := helperRequest("codex", "codex")
	failed := errors.New("trace storage failed")
	calls := 0
	req.OnEvent = func(event json.RawMessage) error { calls++; return failed }
	result, err := Run(context.Background(), req)
	if !errors.Is(err, failed) || calls != 1 || len(result.Events) != 1 {
		t.Fatalf("err=%v calls=%d retained=%d", err, calls, len(result.Events))
	}
	if result.SessionID != "codex-1" {
		t.Fatal("event was not consumed before recording")
	}
}

func TestEventObserverReceivesDecisiveRejectingFrame(t *testing.T) {
	req := protocolRequest("claude", `{"type":"assistant","message":{"content":[{"type":"text","text":"earlier text"}]}}`, `{"type":"result","subtype":"success","result":null}`)
	var observed []json.RawMessage
	req.OnEvent = func(raw json.RawMessage) error {
		observed = append(observed, append(json.RawMessage(nil), raw...))
		return nil
	}
	result, err := Run(context.Background(), req)
	if !errors.Is(err, ErrProtocol) || len(observed) != 2 || len(result.Events) != 2 || string(observed[1]) != string(result.Events[1]) {
		t.Fatalf("rejecting result disappeared from trace: observed=%d retained=%d err=%v", len(observed), len(result.Events), err)
	}
	traceErr := errors.New("trace write failed")
	req.OnEvent = func(raw json.RawMessage) error {
		if strings.Contains(string(raw), `"result":null`) {
			return traceErr
		}
		return nil
	}
	if _, err := Run(context.Background(), req); !errors.Is(err, ErrProtocol) || !errors.Is(err, traceErr) {
		t.Fatalf("trace failure hid protocol rejection or vice versa: %v", err)
	}
	req = helperRequest("claude", "invalid")
	calls := 0
	req.OnEvent = func(json.RawMessage) error { calls++; return nil }
	if _, err := Run(context.Background(), req); err == nil || calls != 0 {
		t.Fatal("malformed non-JSON was reported as a structured frame")
	}
}
