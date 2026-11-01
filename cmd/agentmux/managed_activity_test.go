package main

import (
	"encoding/json"
	"strings"
	"testing"

	"agentmux/internal/managed"
)

func TestManagedActivityEscapesUntrustedTerminalContent(t *testing.T) {
	attack := "\x1b]52;c;clipboard\a\x1b[2J\r\x00\u202e reversed\nnext"
	snapshot := managedActivitySnapshot{
		Messages:        []managed.Message{{From: attack, Text: attack, Board: attack, Kind: attack, CreatedAt: attack, To: []string{attack}}},
		Tasks:           []managed.Task{{ID: attack, To: attack, State: "failed", Delivery: attack, Text: attack, Result: attack, Usage: json.RawMessage(`{"output_tokens":2,"note":"\u001b[2J"}`)}},
		Events:          []managed.Event{{At: attack, Type: attack, Actor: attack, EntityID: attack}},
		RuntimeDeadline: attack,
	}
	output := formatManagedActivity(snapshot)
	for _, control := range []string{"\x1b", "\a", "\r", "\x00", "\u202e"} {
		if strings.Contains(output, control) {
			t.Fatalf("untrusted terminal control %q was emitted", control)
		}
	}
	for _, visible := range []string{`\x1b`, `\u202e`, "failed", "result=", "observed_usage=", "output_tokens"} {
		if !strings.Contains(output, visible) {
			t.Fatalf("activity omitted safe visible evidence %q", visible)
		}
	}
}

func TestManagedActivityBoundsLargeContentAndItemCount(t *testing.T) {
	snapshot := managedActivitySnapshot{}
	for i := 0; i < 50; i++ {
		snapshot.Tasks = append(snapshot.Tasks, managed.Task{State: "submitted", Text: strings.Repeat("x", 10000), Result: strings.Repeat("y", 10000)})
	}
	output := formatManagedActivity(snapshot)
	if len(output) > 35000 || strings.Count(output, "instruction=") != 20 || !strings.Contains(output, "...") {
		t.Fatalf("activity output was not bounded: bytes=%d, items=%d", len(output), strings.Count(output, "instruction="))
	}
}

func TestManagedActivityDistinguishesUnlimitedUnknownAndTimedRuns(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"0001-01-01T00:00:00Z", "Run deadline: none (no automatic deadline)"},
		{"", "Run deadline: unknown"},
		{"2026-11-01T12:00:00Z", `Run deadline: "2026-11-01T12:00:00Z"`},
	} {
		if output := formatManagedActivity(managedActivitySnapshot{RuntimeDeadline: tc.value}); !strings.Contains(output, tc.want) {
			t.Fatalf("deadline %q: %s", tc.value, output)
		}
	}
}
