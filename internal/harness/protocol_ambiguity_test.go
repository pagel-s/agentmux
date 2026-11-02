package harness

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNativeProtocolRejectsAmbiguousJSONBeforeItCanEraseFailure(t *testing.T) {
	for _, fixture := range []struct{ name, frame string }{
		{"antigravity", `{"event":"result","result":{"status":"FAILURE","status":"SUCCESS","response":"answer"}}`},
		{"antigravity", `{"event":"result","result":{"status":"SUCCESS","response":"answer","denied_actions":[{"action":"command"}],"denied_actions":[]}}`},
		{"antigravity", `{"event":"result","result":{"status":"SUCCESS","response":"answer","denied_actions":[{"action":"command"}],"denied_\u0061ctions":[]}}`},
		{"codex", `{"type":"error","type":"turn.completed","message":"failed"}`},
	} {
		t.Run(fixture.name+fixture.frame, func(t *testing.T) {
			events := []string{fixture.frame}
			if fixture.name == "codex" {
				events = append([]string{codexAnswer}, events...)
			}
			result, err := Run(context.Background(), protocolRequest(fixture.name, events...))
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("ambiguous failure frame accepted: result=%+v err=%v", result, err)
			}
			if len(result.Events) != len(events) || string(result.Events[len(result.Events)-1]) != fixture.frame {
				t.Fatal("decisive ambiguous frame was not retained")
			}
		})
	}
}

func TestNativeProtocolRejectsInvalidUTF8AndExcessiveNesting(t *testing.T) {
	for _, frame := range []string{
		"{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\",\"response\":\"bad" + string([]byte{0xff}) + "\"}}",
		`{"event":"result","result":{"status":"SUCCESS","response":"answer","extra":` + strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65) + "}}",
	} {
		state := streamState{name: "antigravity"}
		if err := state.consume([]byte(frame)); !errors.Is(err, ErrProtocol) {
			t.Fatalf("invalid native frame accepted: %v", err)
		}
	}
}
