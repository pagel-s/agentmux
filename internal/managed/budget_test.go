package managed

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"agentmux/internal/egress"
	"agentmux/internal/managedconfig"
)

func budgetConfig() managedconfig.Config {
	c := testConfig()
	c.Budget = managedconfig.Budget{MaxTokens: 100, MaxCostMicrousd: 1000}
	for id, a := range c.Agents {
		a.Budget = managedconfig.AgentBudget{MaxTurnTokens: 40, MaxTurnCostMicrousd: 300}
		c.Agents[id] = a
	}
	return c
}
func budgetUsage(t *testing.T, s *Store, id string) BudgetCharge {
	t.Helper()
	var out struct {
		Used BudgetCharge `json:"used"`
	}
	if err := json.Unmarshal(call(t, s, id, "budget.status", map[string]any{}), &out); err != nil {
		t.Fatal(err)
	}
	return out.Used
}
func budgetInstruction(t *testing.T, s *Store, id string) Task {
	t.Helper()
	return taskIn(t, call(t, s, "", "instruct", map[string]any{"to": []string{id}, "text": "bounded turn"}))
}

func TestBudgetClaimsSharedAcrossAgentsAndUnknownUsage(t *testing.T) {
	s := testStore(t, budgetConfig())
	for _, id := range []string{"alice", "bob"} {
		budgetInstruction(t, s, id)
		task, lease := autonomyNext(t, s, id)
		autonomyFinish(t, s, id, task, lease)
	}
	if used := budgetUsage(t, s, ""); used.Tokens != 80 || used.CostMicrousd != 600 || used.Turns != 2 {
		t.Fatalf("wrong total %+v", used)
	}
	budgetInstruction(t, s, "alice")
	expectFault(t, s, "alice", "worker.next", "limit", map[string]any{})
	if used := budgetUsage(t, s, ""); used.Tokens != 80 || used.Turns != 2 {
		t.Fatal("failed admission charged or completion refunded")
	}
	expectFault(t, s, "alice", "budget.status", "forbidden", map[string]any{"agent": "bob"})
}

func TestBudgetReplayDoesNotChargeAndUncertainRetryDoes(t *testing.T) {
	c := budgetConfig()
	c.Budget.MaxTokens = 200
	s := testStore(t, c)
	budgetInstruction(t, s, "bob")
	cred := credential(t, s, "bob")
	first, f := invoke(s, cred, "worker.next", map[string]any{}, "once")
	if f != nil {
		t.Fatal(f)
	}
	task := taskIn(t, first)
	if _, f = invoke(s, cred, "worker.next", map[string]any{}, "once"); f != nil {
		t.Fatal(f)
	}
	if used := budgetUsage(t, s, ""); used.Tokens != 40 {
		t.Fatal("idempotent replay charged twice")
	}
	if err := s.RecoverInflight(context.Background()); err != nil {
		t.Fatal(err)
	}
	task = taskIn(t, call(t, s, "", "task.get", map[string]any{"id": task.ID}))
	call(t, s, "", "task.resolve", map[string]any{"id": task.ID, "version": task.Version, "resolution": "retry"})
	if task, _ = autonomyNext(t, s, "bob"); task.ID == "" {
		t.Fatal("retry not claimed")
	}
	if used := budgetUsage(t, s, ""); used.Tokens != 80 || used.Turns != 2 {
		t.Fatalf("uncertain attempt was refunded: %+v", used)
	}
}

func TestBudgetDescendantsShareAncestorCeiling(t *testing.T) {
	c := budgetConfig()
	c.Budget.MaxTokens = 1000
	c.Budget.MaxCostMicrousd = 10000
	parent := c.Agents["alice"]
	parent.Budget.MaxSubtreeTokens = 70
	c.Agents["alice"] = parent
	s := testStore(t, c)
	child := AgentInfo{ID: "child", Parent: "alice", State: "active", Depth: 1, Config: c.Agents["bob"]}
	if err := save(context.Background(), s.db, "agent", "agent_child", "alice", "child", "", child); err != nil {
		t.Fatal(err)
	}
	if _, err := s.rotate("child"); err != nil {
		t.Fatal(err)
	}
	budgetInstruction(t, s, "child")
	task, lease := autonomyNext(t, s, "child")
	autonomyFinish(t, s, "child", task, lease)
	budgetInstruction(t, s, "alice")
	expectFault(t, s, "alice", "worker.next", "limit", map[string]any{})
	if used := budgetUsage(t, s, "alice"); used.Tokens != 40 {
		t.Fatalf("parent ignored child %+v", used)
	}
}

func TestBudgetMissingChildReservationFailsClosed(t *testing.T) {
	c := testConfig()
	parent := c.Agents["alice"]
	parent.Budget = managedconfig.AgentBudget{MaxTurnTokens: 40, MaxSubtreeTokens: 70}
	c.Agents["alice"] = parent
	s := testStore(t, c)
	child := AgentInfo{ID: "child", Parent: "alice", State: "active", Depth: 1, Config: c.Agents["bob"]}
	if err := save(context.Background(), s.db, "agent", "agent_child", "alice", "child", "", child); err != nil {
		t.Fatal(err)
	}
	if _, err := s.rotate("child"); err != nil {
		t.Fatal(err)
	}
	budgetInstruction(t, s, "child")
	expectFault(t, s, "child", "worker.next", "limit", map[string]any{})
}

func TestBudgetArithmeticOverflowAndUntrustedUsageCannotRefund(t *testing.T) {
	if _, err := addCharge(BudgetCharge{Tokens: math.MaxInt64}, BudgetCharge{Tokens: 1}); err == nil {
		t.Fatal("overflow wrapped")
	}
	s := testStore(t, budgetConfig())
	budgetInstruction(t, s, "bob")
	task, lease := autonomyNext(t, s, "bob")
	call(t, s, "bob", "task.complete", map[string]any{"id": task.ID, "version": task.Version, "lease": lease, "status": "failed", "result": "unknown native outcome", "usage": map[string]any{"total_tokens": 0, "cost_usd": -1000}})
	if used := budgetUsage(t, s, ""); used.Tokens != 40 || used.CostMicrousd != 300 {
		t.Fatal("untrusted usage changed reservation")
	}
}

func TestSpawnCannotDelegateMoreNetworkAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, parent, child   string
		parentPins, childPins []string
		allowed               bool
	}{
		{"none to none", "none", "none", nil, nil, true},
		{"none to allowlist", "none", "allowlist", nil, nil, false},
		{"none to host", "none", "host", nil, nil, false},
		{"allowlist to none", "allowlist", "none", nil, nil, true},
		{"same unpinned origin", "allowlist", "allowlist", nil, nil, true},
		{"narrow pins", "allowlist", "allowlist", []string{"192.0.2.1", "192.0.2.2"}, []string{"192.0.2.1"}, true},
		{"discard pins", "allowlist", "allowlist", []string{"192.0.2.1"}, nil, false},
		{"pin unpinned DNS private", "allowlist", "allowlist", nil, []string{"127.0.0.1"}, false},
		{"other pins", "allowlist", "allowlist", []string{"192.0.2.1"}, []string{"192.0.2.2"}, false},
		{"allowlist to host", "allowlist", "host", nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := dynamicConfig()
			parent := c.Agents["alice"]
			parent.Sandbox = "bubblewrap"
			parent.Network = tc.parent
			if tc.parent == "allowlist" {
				parent.Egress.Allow = []egress.Endpoint{{Origin: "https://example.com", IPs: tc.parentPins}}
			}
			c.Agents["alice"] = parent
			child := c.Spawn.Templates["echo"]
			child.Network = tc.child
			if tc.child == "allowlist" {
				child.Egress.Allow = []egress.Endpoint{{Origin: "https://example.com", IPs: tc.childPins}}
			}
			c.Spawn.Templates["echo"] = child
			s := testStore(t, c)
			if tc.allowed {
				spawned(t, s, "alice", Capabilities{})
			} else {
				expectFault(t, s, "alice", "agent.spawn", "forbidden", map[string]any{"template": "echo"})
			}
		})
	}
}

func TestSpawnUnknownParentNetworkFailsClosed(t *testing.T) {
	s := testStore(t, dynamicConfig())
	a := s.cfg.Agents["alice"]
	a.Sandbox = "bubblewrap"
	a.Network = "future-unknown"
	s.cfg.Agents["alice"] = a
	expectFault(t, s, "alice", "agent.spawn", "forbidden", map[string]any{"template": "echo"})
}
