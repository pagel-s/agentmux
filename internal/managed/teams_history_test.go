package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func historyPost(t *testing.T, store *Store, board, text string) Message {
	t.Helper()
	var response struct {
		Message Message `json:"message"`
	}
	if err := json.Unmarshal(call(t, store, "", "board.post", map[string]any{"board": board, "text": text}), &response); err != nil || response.Message.ID == "" {
		t.Fatalf("post failed: %+v, %v", response, err)
	}
	return response.Message
}

func TestChildHistoryTracksParentRejoinDespiteCachedJoinReceipt(t *testing.T) {
	s := testStore(t, dynamicConfig())
	team := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "Parent history boundary"}))
	team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": "alice", "version": team.Version}))
	team = teamIn(t, call(t, s, "alice", "team.join", map[string]any{"id": team.ID, "version": team.Version}))
	child := spawned(t, s, "alice", Capabilities{JoinTeams: true})
	team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": child.ID, "version": team.Version}))
	childCredential := credential(t, s, child.ID)
	joinArgs := map[string]any{"id": team.ID, "version": team.Version}
	joinReceipt, problem := invoke(s, childCredential, "team.join", joinArgs, "child-original-join")
	if problem != nil {
		t.Fatal(problem)
	}
	if strings.Contains(string(joinReceipt), "joined_after") {
		t.Fatal("join receipt leaked private owner sequence metadata")
	}
	team = teamIn(t, joinReceipt)
	before := historyPost(t, s, team.Board, "visible before parent departure")
	if raw := call(t, s, child.ID, "message.list", map[string]any{"board": team.Board}); !strings.Contains(string(raw), before.Text) {
		t.Fatal("initial child membership cannot read its permitted history")
	}

	team = teamIn(t, call(t, s, "alice", "team.leave", map[string]any{"id": team.ID, "version": team.Version}))
	expectFault(t, s, child.ID, "message.list", "forbidden", map[string]any{"board": team.Board})
	// A cached successful membership receipt is recoverable evidence, not a
	// fresh authorization decision or a replay of the membership mutation.
	replayed, problem := invoke(s, childCredential, "team.join", joinArgs, "child-original-join")
	if problem != nil || !bytes.Equal(replayed, joinReceipt) {
		t.Fatalf("cached join receipt changed: %s, %v", replayed, problem)
	}
	expectFault(t, s, child.ID, "message.list", "forbidden", map[string]any{"board": team.Board})
	absent := historyPost(t, s, team.Board, "posted while parent was absent")

	team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": "alice", "version": team.Version}))
	team = teamIn(t, call(t, s, "alice", "team.join", map[string]any{"id": team.ID, "version": team.Version}))
	after := historyPost(t, s, team.Board, "visible after parent returned")
	assertCurrentView := func() {
		t.Helper()
		for _, args := range []map[string]any{{"board": team.Board}, {}} {
			raw := call(t, s, child.ID, "message.list", args)
			if !strings.Contains(string(raw), after.Text) || strings.Contains(string(raw), before.Text) || strings.Contains(string(raw), absent.Text) {
				t.Fatalf("child bypassed the current parent's history floor: %s", raw)
			}
			if strings.Contains(string(raw), "joined_after") {
				t.Fatal("message results leaked private sequence metadata")
			}
		}
	}
	assertCurrentView()
	// The child's older own join cutoff must not survive a parent departure
	// and rejoin, even if its original join response is requested again.
	replayed, problem = invoke(s, childCredential, "team.join", joinArgs, "child-original-join")
	if problem != nil || !bytes.Equal(replayed, joinReceipt) {
		t.Fatalf("cached receipt unavailable after parent rejoin: %v", problem)
	}
	assertCurrentView()
	for _, message := range []Message{before, absent} {
		expectFault(t, s, child.ID, "message.list", "invalid", map[string]any{"after": message.ID})
	}
	if raw := call(t, s, child.ID, "team.list", map[string]any{}); strings.Contains(string(raw), "joined_after") {
		t.Fatal("team listing leaked global sequence metadata")
	}
}

func TestMalformedTeamHistoryFailsClosedWithoutBlockingStaticBoards(t *testing.T) {
	s := testStore(t, dynamicConfig())
	team := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "Corrupt history boundary"}))
	team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": "alice", "version": team.Version}))
	team = teamIn(t, call(t, s, "alice", "team.join", map[string]any{"id": team.ID, "version": team.Version}))
	message := historyPost(t, s, team.Board, "visible only with valid history metadata")
	if raw := call(t, s, "alice", "message.list", map[string]any{"board": team.Board}); !strings.Contains(string(raw), message.Text) {
		t.Fatal("valid baseline history was not readable")
	}
	// Simulate storage corruption of the private history map. The public Team
	// fields and membership remain readable, but the history decoder fails.
	if _, err := s.db.ExecContext(context.Background(), "UPDATE records SET data=json_set(data,'$.joined_after',?) WHERE kind='team' AND id=?", "invalid-map", team.ID); err != nil {
		t.Fatal(err)
	}
	if raw := call(t, s, "alice", "message.list", map[string]any{"board": team.Board}); strings.Contains(string(raw), message.Text) {
		t.Fatalf("corrupt history metadata widened access: %s", raw)
	}
	expectFault(t, s, "alice", "message.list", "invalid", map[string]any{"after": message.ID})
	call(t, s, "bob", "board.post", map[string]any{"board": "team", "text": "static board remains available"})
	if raw := call(t, s, "alice", "message.list", map[string]any{"board": "team"}); !strings.Contains(string(raw), "static board remains available") {
		t.Fatalf("confirmed non-team board was incorrectly history-filtered: %s", raw)
	}
}
