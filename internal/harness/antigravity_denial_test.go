package harness

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Recorded CLI1.4.12 reports denied actions on an otherwise successful final
// result, without a tool error or diagnostic stderr. Exercise the public Run
// boundary so this cannot silently become a successful completed turn.
func TestAntigravityTerminalDeniedActionsFailClosed(t *testing.T) {
	for _, test := range []struct {
		name, line string
		want       error
	}{
		{"denied", `{"event":"result","result":{"status":"SUCCESS","response":"","denied_actions":[{"action":"command","display_name":"RunCommand"}]}}`, ErrToolDenied},
		{"denied-with-answer", `{"event":"result","result":{"status":"SUCCESS","response":"partial work","denied_actions":[{"action":"write_file"}]}}`, ErrToolDenied},
		{"allowed", `{"event":"result","result":{"status":"SUCCESS","response":"finished","denied_actions":[]}}`, nil},
		{"malformed", `{"event":"result","result":{"status":"SUCCESS","response":"finished","denied_actions":"none"}}`, ErrProtocol},
		{"unknown-denial", `{"event":"result","result":{"status":"SUCCESS","response":"finished","denied_actions":[{}]}}`, ErrProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := helperRequest("antigravity", "protocol-fixture")
			req.Env["AGENTMUX_PROTOCOL_LINES"] = test.line + "\n"
			req.Env["AGENTMUX_PROTOCOL_NAME"] = "antigravity"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := Run(ctx, req)
			if !errors.Is(err, test.want) {
				t.Fatalf("outcome %v, want %v", err, test.want)
			}
			if len(result.Events) != 1 {
				t.Fatal("decisive final event was not retained")
			}
		})
	}
}
