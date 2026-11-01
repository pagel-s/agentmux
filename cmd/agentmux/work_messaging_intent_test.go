package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	tea "github.com/charmbracelet/bubbletea"
)

func TestWorkReplyIntentAndBoardAudienceAreExplicit(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	var out, diagnostics bytes.Buffer
	run := func(operation string, args ...string) {
		t.Helper()
		out.Reset()
		diagnostics.Reset()
		args = append(args, "--state", state)
		if code := runWorkSession(operation, args, strings.NewReader(""), &out, &diagnostics); code != exitSuccess {
			t.Fatalf("%s: %d %s", operation, code, &diagnostics)
		}
	}
	run("send", "peer", "Information only", "--mode", "note")
	snapshot := uat20Activity(t, client)
	if len(snapshot.Tasks) != 0 || len(snapshot.Messages) != 1 || snapshot.Messages[0].Kind != "note" {
		t.Fatalf("note created task: %+v", snapshot)
	}
	parent := snapshot.Messages[0]
	run("reply", parent.ID, "Act on this", "--mode", "instruction")
	snapshot = uat20Activity(t, client)
	if len(snapshot.Tasks) != 1 || snapshot.Tasks[0].Text != "Act on this" || snapshot.Tasks[0].ReplyTo != parent.ID || snapshot.Tasks[0].ThreadID != parent.ID {
		t.Fatalf("instruction ancestry: %+v", snapshot)
	}
	boardRaw, err := workCall(context.Background(), client, "board.post", map[string]any{"board": "team", "text": "shared root"})
	if err != nil {
		t.Fatal(err)
	}
	var board struct {
		Message managed.Message `json:"message"`
	}
	if err := json.Unmarshal(boardRaw, &board); err != nil {
		t.Fatal(err)
	}
	run("reply", board.Message.ID, "Retain shared scope", "--board", "team", "--mode", "note")
	snapshot = uat20Activity(t, client)
	if snapshot.Messages[0].Board != "team" || snapshot.Messages[0].ReplyTo != board.Message.ID || len(snapshot.Tasks) != 1 {
		t.Fatalf("shared reply scope: %+v", snapshot)
	}
	if !workSendRequested([]string{"peer", "text", "--mode", "note"}) {
		t.Fatal("intent flag routed to legacy terminal input")
	}
}

func TestConsoleIntentHelpAndFailedDraftPreserveExactContext(t *testing.T) {
	m := console{width: 90, height: 30, agents: []managed.AgentInfo{{ID: "native", Config: managedconfig.Agent{Interaction: "interactive"}}}}
	m.compose("reply", "native", "msg_original", "literal ? text")
	if m.composeMode != "note" || !strings.Contains(m.View(), "note → native") {
		t.Fatal("native reply intent is not visible")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if m.composeMode != "instruction" || string(m.text) != "literal ? text" || m.replyID != "msg_original" {
		t.Fatal("intent changed reply identity or text")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if m.reader == nil || !strings.Contains(m.reader.body, "never") && !strings.Contains(m.reader.body, "Neither") {
		t.Fatal("context help missing native queue semantics")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.reader != nil || m.composer != "reply" || m.composeMode != "instruction" || string(m.text) != "literal ? text" {
		t.Fatal("closing help discarded draft")
	}
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if m.reader != nil || string(m.text) != "literal ? text?" {
		t.Fatal("printable question mark was consumed as help")
	}
	m.failedDraft = &consoleDraft{kind: "reply", target: "native", reference: "msg_original", text: "retained", mode: "instruction"}
	m.compose("send", "another", "", "unrelated")
	m.restoreDraft()
	if m.composeMode != "instruction" || m.replyID != "msg_original" || m.composeTarget != "native" || string(m.text) != "retained" {
		t.Fatal("failed reply lost frozen intent/context")
	}
	m.composer = ""
	m, _ = consolePress(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if m.reader == nil {
		t.Fatal("workbench help unavailable")
	}
}
