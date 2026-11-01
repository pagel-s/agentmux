package managed

import (
	"agentmux/internal/managedconfig"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestWorkspaceGroupOverlappingWritesBoundAuthorizationReads(t *testing.T) {
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "group-write-scale", Agents: map[string]managedconfig.Agent{}, Policy: managedconfig.Policy{Default: "deny"}}
	for i := 0; i < 55; i++ {
		cfg.Agents[fmt.Sprintf("a%03d", i)] = managedconfig.Agent{Harness: "command", Command: []string{"/bin/true"}}
	}
	s := testStore(t, cfg)
	for n := 0; n < 6; n++ {
		ids := []string{}
		for i := n + 23; i < n+44; i++ {
			ids = append(ids, fmt.Sprintf("a%03d", i))
		}
		ids = append(ids, "a054")
		args, _ := json.Marshal(map[string]any{"version": n + 1, "kind": "collaboration", "agents": ids, "enabled": true})
		tx, err := s.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		q := &countedBoundaryQueries{queryer: tx}
		start := time.Now()
		result, problem := s.control(context.Background(), q, actor{ID: ownerID}, Request{Operation: "control.update", Args: args})
		if problem != nil {
			tx.Rollback()
			t.Fatal(problem)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		c := result.(ControlState)
		if len(c.Edges) != 0 || len(c.Scopes) != 0 {
			t.Fatal("groups expanded into global overrides")
		}
		t.Logf("group%d: %s, %dreads", n+1, time.Since(start), q.reads)
		if q.reads > 800 {
			t.Errorf("group%d repeated authorization reads: %d", n+1, q.reads)
		}
	}
	for _, action := range []string{"message.send", "ping.send"} {
		if !s.can(context.Background(), s.db, "a028", action, "a054") {
			t.Fatalf("missing%s grant", action)
		}
	}
	if s.can(context.Background(), s.db, "a028", "instruct", "a054") || s.can(context.Background(), s.db, "a000", "message.send", "a054") {
		t.Fatal("overlap introduced unintended authorization")
	}
}

func TestWorkspaceGroupContributionsRespectExplicitOverridesAndIsolation(t *testing.T) {
	s := workspaceGroupStore(t)
	c := connectWorkspaceGroup(t, s, 1, "alice", "bob")
	call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": true})
	c = connectWorkspaceGroup(t, s, 3, "alice", "bob")
	if !s.can(context.Background(), s.db, "alice", "instruct", "bob") {
		t.Fatal("group overwrote explicit owner grant")
	}
	call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "independent", "agents": []string{"alice"}})
	for _, action := range []string{"message.send", "ping.send", "instruct"} {
		if s.can(context.Background(), s.db, "alice", action, "bob") {
			t.Fatalf("isolation retained%s", action)
		}
	}
	c = connectWorkspaceGroup(t, s, 5, "alice", "bob")
	if !s.can(context.Background(), s.db, "alice", "instruct", "bob") {
		t.Fatal("reconnect did not restore suspended explicit intent")
	}
	current, err := readControls(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	for len(current.Edges)+len(current.Scopes) < 4096 {
		current.Edges = append(current.Edges, EdgeControl{From: fmt.Sprintf("unused%d", len(current.Edges)), To: "bob", Action: "message.send"})
	}
	if err = saveControls(context.Background(), s.db, current); err != nil {
		t.Fatal(err)
	}
	expectFault(t, s, "", "control.update", "limit", map[string]any{"version": current.Version, "kind": "edge", "from": "eve", "to": "dave", "action": "instruct", "allow": true})
	saved, err := readControls(context.Background(), s.db)
	if err != nil || saved.Version != current.Version || len(saved.Edges)+len(saved.Scopes) != 4096 {
		t.Fatal("limit rejection partially persisted overrides")
	}
}

func TestWorkspaceGroupOverlapDisconnectRemovesOnlyOneContribution(t *testing.T) {
	s := workspaceGroupStore(t)
	one := connectWorkspaceGroup(t, s, 1, "alice", "bob")
	two := connectWorkspaceGroup(t, s, 2, "alice", "bob", "eve")
	call(t, s, "", "control.update", map[string]any{"version": 3, "kind": "collaboration", "agents": []string{"alice", "bob"}, "enabled": false})
	for _, action := range []string{"message.send", "ping.send"} {
		call(t, s, "alice", action, map[string]any{"to": []string{"bob"}, "text": "other group remains"})
	}
	expectFault(t, s, "alice", "board.post", "forbidden", map[string]any{"board": one.Group.Board, "text": "closed"})
	call(t, s, "alice", "board.post", map[string]any{"board": two.Group.Board, "text": "open"})
	call(t, s, "", "control.update", map[string]any{"version": 4, "kind": "edge", "from": "alice", "to": "bob", "action": "message.send", "allow": false})
	connectWorkspaceGroup(t, s, 5, "alice", "bob")
	expectFault(t, s, "alice", "message.send", "forbidden", map[string]any{"to": []string{"bob"}, "text": "explicit deny wins over both groups"})
	call(t, s, "", "control.update", map[string]any{"version": 6, "kind": "edge", "from": "alice", "to": "bob", "action": "message.send", "reset": true})
	call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "reset reveals group grant"})
}
