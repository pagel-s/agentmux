package managed

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
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
		// Each shifted member set adds one new member's directed message/ping
		// edges to the21shared members, plus44new namespace grants.
		if len(c.Edges) != 924+84*n || len(c.Scopes) != 44*(n+1) {
			t.Fatalf("group%d controls: edges%d scopes%d", n+1, len(c.Edges), len(c.Scopes))
		}
		t.Logf("group%d: %s, %dreads, %doverrides", n+1, time.Since(start), q.reads, len(c.Edges)+len(c.Scopes))
		if q.reads > 800 {
			t.Errorf("group%d repeated immutable authorization inputs: %dreads", n+1, q.reads)
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

func TestWorkspaceGroupEdgePreflightTracksDenyTransitionsAndLimitRollback(t *testing.T) {
	s := workspaceGroupStore(t)
	c := connectWorkspaceGroup(t, s, 1, "alice", "bob")
	call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": true})
	c = connectWorkspaceGroup(t, s, 3, "alice", "bob")
	if s.can(context.Background(), s.db, "alice", "instruct", "bob") {
		t.Fatal("collaboration retained a stale command allow")
	}
	call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": true})
	call(t, s, "", "control.update", map[string]any{"version": 5, "kind": "independent", "agents": []string{"alice"}})
	for _, action := range []string{"message.send", "ping.send", "instruct"} {
		if s.can(context.Background(), s.db, "alice", action, "bob") {
			t.Fatalf("independence retained stale%s allow", action)
		}
	}
	// A phase cache must agree with uncached authorization after every new call.
	ctx := boundaryReadContext(context.Background())
	for _, from := range []string{"alice", "bob", "eve", "dave"} {
		for _, to := range []string{"alice", "bob", "eve", "dave"} {
			for _, action := range []string{"message.send", "ping.send", "instruct"} {
				if s.can(ctx, s.db, from, action, to) != s.can(context.Background(), s.db, from, action, to) {
					t.Fatal("immutable edge preflight differs from live policy")
				}
			}
		}
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
	before, _ := workspaceGroups(context.Background(), s.db)
	expectFault(t, s, "", "control.update", "limit", map[string]any{"version": current.Version, "kind": "collaboration", "agents": []string{"eve", "dave"}, "enabled": true})
	after, _ := workspaceGroups(context.Background(), s.db)
	saved, err := readControls(context.Background(), s.db)
	if err != nil || len(after) != len(before) || saved.Version != current.Version || len(saved.Edges)+len(saved.Scopes) != 4096 {
		t.Fatalf("limit rejection partially persisted group or permissions: groups%d/%d version%d/%d error%v", len(after), len(before), saved.Version, current.Version, err)
	}
}

func TestWorkspaceGroupEdgePreflightMatchesUncachedPolicy(t *testing.T) {
	s := testStore(t, testConfig())
	ids := []string{"alice", "bob", "eve"}
	expected := map[EdgeControl]bool{}
	// Compute the existing algorithm's decisions against uncached policy before
	// the mutation; creating a namespace group does not change messaging rights.
	for _, from := range ids {
		for _, to := range ids {
			if from == to {
				continue
			}
			for _, action := range []string{"message.send", "ping.send", "instruct"} {
				allow := action != "instruct"
				if allow || s.can(context.Background(), s.db, from, action, to) {
					expected[EdgeControl{From: from, To: to, Action: action, Allow: allow}] = true
				}
			}
		}
	}
	c := connectWorkspaceGroup(t, s, 1, ids...)
	if len(c.Edges) != len(expected) {
		t.Fatalf("edge count%d want%d", len(c.Edges), len(expected))
	}
	for _, edge := range c.Edges {
		if !expected[edge] {
			t.Fatalf("edge differs from uncached policy: %+v", edge)
		}
	}
	if len(c.Scopes) != 2*len(ids) {
		t.Fatalf("scope count%d", len(c.Scopes))
	}
	seen := map[string]bool{}
	for _, scope := range c.Scopes {
		if !contains(ids, scope.Agent) || !scope.Read || !scope.Write || scope.Kind == "board" && scope.Namespace != c.Group.Board || scope.Kind == "memory" && scope.Namespace != c.Group.Memory {
			t.Fatalf("unexpected scope: %+v", scope)
		}
		seen[scope.Agent+":"+scope.Kind] = true
	}
	if len(seen) != 2*len(ids) {
		t.Fatal("missing or duplicated scope grants")
	}
}
