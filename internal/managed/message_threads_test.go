package managed

import (
	"encoding/json"
	"testing"
)

func messageIn(t *testing.T, raw []byte) Message {
	t.Helper()
	var result struct {
		Message Message `json:"message"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result.Message
}

func TestReplyAncestryIsBrokerBoundAndAudiencePreserving(t *testing.T) {
	s := testStore(t, testConfig())
	first := messageIn(t, call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "a note"}))
	if first.ThreadID != first.ID || first.ReplyTo != "" {
		t.Fatalf("new root: %+v", first)
	}
	secondRaw := call(t, s, "alice", "instruct", map[string]any{"to": []string{"bob"}, "text": "now act", "reply_to": first.ID})
	second, task := messageIn(t, secondRaw), taskIn(t, secondRaw)
	if second.ThreadID != first.ID || second.ReplyTo != first.ID || task.ThreadID != first.ID || task.ReplyTo != first.ID {
		t.Fatalf("message/task ancestry diverged: %+v %+v", second, task)
	}
	third := messageIn(t, call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "task follow-up", "reply_to": task.ID}))
	if third.ThreadID != first.ID || third.ReplyTo != task.ID || third.Text != "task follow-up" {
		t.Fatalf("task reply ancestry: %+v", third)
	}
	// Even the owner must make a new, explicitly unthreaded disclosure to share
	// a private conversation with somebody who cannot see its referenced item.
	expectFault(t, s, "", "message.send", "forbidden", map[string]any{"to": []string{"eve"}, "text": "do not widen", "reply_to": first.ID})
	expectFault(t, s, "eve", "board.post", "not_found", map[string]any{"board": "public", "text": "guess private ID", "reply_to": first.ID})
	expectFault(t, s, "alice", "message.send", "invalid", map[string]any{"to": []string{"bob"}, "text": "forge root", "thread_id": "arbitrary"})
}

func TestBoardReplyRequiresOriginalBoardOrVisibleDirectAudience(t *testing.T) {
	s := testStore(t, testConfig())
	parent := messageIn(t, call(t, s, "alice", "board.post", map[string]any{"board": "team", "text": "team context"}))
	board := messageIn(t, call(t, s, "alice", "board.post", map[string]any{"board": "team", "text": "same audience", "reply_to": parent.ID}))
	private := messageIn(t, call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "private side note", "reply_to": parent.ID}))
	if board.Board != "team" || board.ThreadID != parent.ID || private.Board != "" || private.ThreadID != parent.ID {
		t.Fatal("reply scope or ancestry lost")
	}
	expectFault(t, s, "", "board.post", "forbidden", map[string]any{"board": "public", "text": "widen", "reply_to": parent.ID})
	expectFault(t, s, "", "message.send", "forbidden", map[string]any{"to": []string{"eve"}, "text": "widen", "reply_to": parent.ID})
	expectFault(t, s, "alice", "message.send", "not_found", map[string]any{"to": []string{"bob"}, "text": "missing", "reply_to": "msg_nonexistent"})
}
