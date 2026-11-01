package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"agentmux/internal/managedconfig"
)

type countedBoundaryQueries struct {
	queryer
	reads int
}

func (q *countedBoundaryQueries) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.reads++
	return q.queryer.QueryContext(ctx, query, args...)
}

func (q *countedBoundaryQueries) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	q.reads++
	return q.queryer.QueryRowContext(ctx, query, args...)
}

func TestBoundaryReadInputsScaleLinearlyWithRegisteredAgents(t *testing.T) {
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "boundary-scale", Agents: make(map[string]managedconfig.Agent)}
	for i := 1; i <= 225; i++ {
		cfg.Agents[fmt.Sprintf("scale%03d", i)] = managedconfig.Agent{Harness: "command", Command: []string{"/bin/true"}}
	}
	s := testStore(t, cfg)
	q := &countedBoundaryQueries{queryer: s.db}
	result, problem := s.control(context.Background(), q, actor{ID: ownerID}, Request{Operation: "boundaries.get", Args: json.RawMessage(`{"agent":"scale225"}`)})
	if problem != nil {
		t.Fatal(problem)
	}
	boundaries := result.(Boundaries)
	if boundaries.Agent != "scale225" || len(boundaries.Targets) != 225 {
		t.Fatalf("incomplete boundaries: %+v", boundaries)
	}
	// Each identity needs its record, optional label and creation metadata once.
	// A fixed allowance covers controls, namespace recognition and list queries.
	if q.reads > 4*len(cfg.Agents)+30 {
		t.Fatalf("boundary evaluation repeated policy reads per action: %d queries for %d agents", q.reads, len(cfg.Agents))
	}
	t.Logf("225-agent effective boundaries required %d policy reads", q.reads)
}

func TestBoundaryReadCacheMatchesAuthorizationAndNeverSurvivesARequest(t *testing.T) {
	cfg := testConfig()
	cfg.Teams = managedconfig.Teams{Enabled: true, Creators: []string{"alice"}}
	s := testStore(t, cfg)
	var created struct {
		Team Team `json:"team"`
	}
	if err := json.Unmarshal(call(t, s, "alice", "team.create", map[string]any{"name": "Consent scope"}), &created); err != nil {
		t.Fatal(err)
	}
	check := func() Boundaries {
		t.Helper()
		var b Boundaries
		if err := json.Unmarshal(call(t, s, "", "boundaries.get", map[string]any{"agent": "alice"}), &b); err != nil {
			t.Fatal(err)
		}
		for _, target := range b.Targets {
			want := []string{}
			for _, action := range []string{"message.send", "ping.send", "instruct"} {
				if s.can(context.Background(), s.db, "alice", action, target.To) {
					want = append(want, action)
				}
			}
			if !reflect.DeepEqual(target.Actions, want) {
				t.Fatalf("cached target differs from live authorization: %+v want %v", target, want)
			}
		}
		for _, entry := range []struct {
			scopes []BoundaryScope
			memory bool
		}{{b.Boards, false}, {b.Memory, true}} {
			for _, scope := range entry.scopes {
				if scope.Read != s.namespace(context.Background(), s.db, "alice", scope.Name, entry.memory, false) || scope.Write != s.namespace(context.Background(), s.db, "alice", scope.Name, entry.memory, true) {
					t.Fatalf("cached scope differs from live authorization: %+v", scope)
				}
			}
		}
		return b
	}
	before := check()
	call(t, s, "", "control.update", map[string]any{"version": before.Version, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": false})
	updated := check()
	if updated.Version <= before.Version {
		t.Fatal("new request retained old policy revision")
	}
	for _, target := range updated.Targets {
		if target.To == "bob" && contains(target.Actions, "instruct") {
			t.Fatal("new request retained revoked instruction access")
		}
	}
	call(t, s, "", "team.dissolve", map[string]any{"id": created.Team.ID, "version": created.Team.Version})
	dissolved := check()
	for _, scope := range dissolved.Boards {
		if scope.Name == created.Team.Board {
			t.Fatal("new request retained a dissolved team's scope")
		}
	}
	call(t, s, "", "agent.stop", map[string]any{"id": "bob"})
	for _, target := range check().Targets {
		if target.To == "bob" {
			t.Fatal("new request retained a revoked target")
		}
	}
}

func TestBoundaryReadFailuresStayFailClosedWithinOneEvaluation(t *testing.T) {
	s := testStore(t, testConfig())
	call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": false})
	call(t, s, "", "boundaries.get", map[string]any{"agent": "alice"})
	if _, err := s.db.Exec("UPDATE records SET data='null' WHERE id='live_access'"); err != nil {
		t.Fatal(err)
	}
	expectFault(t, s, "", "boundaries.get", "internal", map[string]any{"agent": "alice"})
	q := &countedBoundaryQueries{queryer: s.db}
	ctx := boundaryReadContext(context.Background())
	_, first := controls(ctx, q)
	reads := q.reads
	_, second := controls(ctx, q)
	if first == nil || second == nil || q.reads != reads {
		t.Fatal("failed policy read was not retained as a failure within the evaluation")
	}
	if s.can(ctx, q, "alice", "message.send", "bob") || s.namespace(ctx, q, "alice", "team", false, false) {
		t.Fatal("failed cached policy read fell back to launch grants")
	}
}
