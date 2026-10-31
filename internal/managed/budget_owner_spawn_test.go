package managed

import (
	"testing"

	"agentmux/internal/managedconfig"
)

func TestOwnerSpawnedChildBudgetStopsAtOwner(t *testing.T) {
	for _, tc := range []struct {
		name             string
		session, subtree int64
	}{{"no reservation", 0, 0}, {"session reservation", 100, 0}, {"child subtree", 100, 60}} {
		t.Run(tc.name, func(t *testing.T) {
			c := dynamicConfig()
			c.Budget.MaxTokens = tc.session
			if tc.session > 0 {
				for id, a := range c.Agents {
					a.Budget.MaxTurnTokens = 40
					c.Agents[id] = a
				}
			}
			template := c.Spawn.Templates["echo"]
			if tc.session > 0 || tc.subtree > 0 {
				template.Budget = managedconfig.AgentBudget{MaxTurnTokens: 40, MaxSubtreeTokens: tc.subtree}
			}
			c.Spawn.Templates["echo"] = template
			s := testStore(t, c)
			child := spawned(t, s, "", Capabilities{})
			if child.Parent != ownerID {
				t.Fatal("fixture is not owner-spawned")
			}
			budgetInstruction(t, s, child.ID)
			task, lease := autonomyNext(t, s, child.ID)
			if task.ID == "" {
				t.Fatal("owner child could not claim")
			}
			autonomyFinish(t, s, child.ID, task, lease)
			want := int64(0)
			if tc.session > 0 || tc.subtree > 0 {
				want = 40
			}
			if used := budgetUsage(t, s, child.ID); used.Tokens != want {
				t.Fatal("child subtree accounting missing")
			}
			if used := budgetUsage(t, s, ""); used.Tokens != want {
				t.Fatal("owner descendant escaped session accounting")
			}
			if tc.subtree > 0 {
				budgetInstruction(t, s, child.ID)
				expectFault(t, s, child.ID, "worker.next", "limit", map[string]any{})
			}
		})
	}
}
