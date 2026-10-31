package managed

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestLiveEdgesAreOwnerOnlyVersionedAndDurable(t *testing.T) {
	s := testStore(t, testConfig())
	before := s.Config()
	update := map[string]any{"version": 1, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": false}
	expectFault(t, s, "alice", "control.update", "forbidden", update)
	call(t, s, "", "control.update", update)
	expectFault(t, s, "alice", "instruct", "forbidden", map[string]any{"to": []string{"bob"}, "text": "revoked"})
	call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "notes still allowed"})
	expectFault(t, s, "", "control.update", "conflict", update)
	update["version"], update["from"], update["to"], update["allow"] = 2, "bob", "alice", true
	call(t, s, "", "control.update", update)
	call(t, s, "bob", "instruct", map[string]any{"to": []string{"alice"}, "text": "now authorized"})
	if !reflect.DeepEqual(before, s.Config()) {
		t.Fatal("live edit changed immutable launch configuration")
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	expectFault(t, reopened, "alice", "instruct", "forbidden", map[string]any{"to": []string{"bob"}, "text": "still revoked"})
	call(t, reopened, "bob", "instruct", map[string]any{"to": []string{"alice"}, "text": "grant survived reopen"})
}

func TestLiveScopesRevokeCurrentReadsAndWritesWithoutExposingPrivateMemory(t *testing.T) {
	s := testStore(t, testConfig())
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "note", "value": "shared", "version": 0})
	call(t, s, "alice", "board.post", map[string]any{"board": "team", "text": "existing shared history"})
	for i, kind := range []string{"memory", "board"} {
		call(t, s, "", "control.update", map[string]any{"version": i + 1, "kind": kind, "agent": "bob", "namespace": "team", "read": false, "write": false})
	}
	expectFault(t, s, "bob", "memory.get", "forbidden", map[string]any{"namespace": "team", "key": "note"})
	expectFault(t, s, "bob", "message.list", "forbidden", map[string]any{"board": "team"})
	expectFault(t, s, "bob", "board.post", "forbidden", map[string]any{"board": "team", "text": "revoked"})
	expectFault(t, s, "", "control.update", "invalid", map[string]any{"version": 3, "kind": "memory", "agent": "bob", "namespace": "private", "read": true})
	call(t, s, "bob", "memory.put", map[string]any{"namespace": "private", "key": "secret", "value": "private-bob", "version": 0})
	expectFault(t, s, "alice", "boundaries.get", "forbidden", map[string]any{"agent": "bob"})
	call(t, s, "", "control.update", map[string]any{"version": 3, "kind": "memory", "agent": "bob", "namespace": "team", "read": true, "write": true})
	call(t, s, "bob", "memory.put", map[string]any{"namespace": "team", "key": "from-bob", "value": "authorized", "version": 0})
	if strings.Contains(string(call(t, s, "alice", "memory.list", map[string]any{"namespace": "team"})), "private-bob") {
		t.Fatal("shared memory exposed private data")
	}
}

func TestBoundarySnapshotsExposeOnlyOwnEffectivePermissions(t *testing.T) {
	s := testStore(t, testConfig())
	raw := call(t, s, "alice", "boundaries.get", map[string]any{})
	var b Boundaries
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	if b.Version != 1 || b.Agent != "alice" || len(b.Targets) != 1 || b.Targets[0].To != "bob" {
		t.Fatalf("wrong boundaries: %s", raw)
	}
	call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": false})
	raw = call(t, s, "alice", "boundaries.get", map[string]any{})
	_ = json.Unmarshal(raw, &b)
	if b.Version != 2 || contains(b.Targets[0].Actions, "instruct") {
		t.Fatalf("stale boundaries: %s", raw)
	}
	if strings.Contains(string(raw), "secret-value") {
		t.Fatal("boundaries exposed environment values")
	}
	expectFault(t, s, "alice", "control.get", "forbidden", map[string]any{})
	expectFault(t, s, "", "control.update", "invalid", map[string]any{"version": 2, "kind": "edge", "from": "alice", "to": "nonexistent", "action": "instruct", "allow": true})
}

func TestLiveEditReceiptReplaysOnceAndDoesNotCancelAcceptedTasks(t *testing.T) {
	s := testStore(t, testConfig())
	task := instruction(t, s)
	owner := credential(t, s, "")
	args := map[string]any{"version": 1, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": false}
	one, p := invoke(s, owner, "control.update", args, "same-edit")
	if p != nil {
		t.Fatal(p)
	}
	two, p := invoke(s, owner, "control.update", args, "same-edit")
	if p != nil || string(one) != string(two) {
		t.Fatalf("unstable receipt: %s %s %v", one, two, p)
	}
	current := taskIn(t, call(t, s, "", "task.get", map[string]string{"id": task.ID}))
	if current.State != "proposed" {
		t.Fatal("access edit silently cancelled accepted work")
	}
	call(t, s, "", "task.cancel", map[string]any{"id": current.ID, "version": current.Version})
}
