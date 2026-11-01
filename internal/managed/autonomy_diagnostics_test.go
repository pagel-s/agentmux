package managed

import (
	"encoding/json"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestAutonomyAdmissionStatusMatchesParentChildClaim(t *testing.T) {
	for _, tc := range []struct {
		name    string
		subtree int64
		blocked bool
	}{
		{"child exhausts parent", 10, true},
		{"one parent turn remains", 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := dynamicConfig()
			c.Budget.MaxTokens = 20
			for id, a := range c.Agents {
				a.Budget.MaxTurnTokens = 10
				a.Autonomy = managedconfig.Autonomy{Enabled: true, MaxTurns: 5}
				c.Agents[id] = a
			}
			parent := c.Agents["alice"]
			parent.Budget.MaxSubtreeTokens = tc.subtree
			c.Agents["alice"] = parent
			template := c.Spawn.Templates["echo"]
			template.Budget.MaxTurnTokens = 10
			c.Spawn.Templates["echo"] = template
			s := testStore(t, c)
			child := spawned(t, s, "alice", Capabilities{})
			budgetInstruction(t, s, child.ID)
			task, lease := autonomyNext(t, s, child.ID)
			autonomyFinish(t, s, child.ID, task, lease)
			instruction := budgetInstruction(t, s, "alice")

			var records, requests int
			if err := s.db.QueryRow("SELECT count(*) FROM records").Scan(&records); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow("SELECT count(*) FROM requests").Scan(&requests); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				status := autonomyStatusIn(t, s, "alice")
				if status.Ready == tc.blocked || status.AdmissionBlocked != tc.blocked || status.PendingInstructions != 1 {
					t.Fatalf("readiness disagrees with ancestry allowance: %+v", status)
				}
				if tc.blocked && (status.State != "halted" || status.Reason != "agent ancestry admission budget exhausted" || status.AdmissionReason != status.Reason) {
					t.Fatalf("missing ancestry denial: %+v", status)
				}
				if used := budgetUsage(t, s, ""); used != (BudgetCharge{Tokens: 10, Turns: 1}) {
					t.Fatalf("status changed admission charges: %+v", used)
				}
			}
			var afterRecords, afterRequests int
			if err := s.db.QueryRow("SELECT count(*) FROM records").Scan(&afterRecords); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow("SELECT count(*) FROM requests").Scan(&afterRequests); err != nil {
				t.Fatal(err)
			}
			if records != afterRecords || requests != afterRequests {
				t.Fatal("read-only admission discovery wrote records or receipts")
			}
			if tc.blocked {
				for _, op := range []string{"worker.next", "task.claim"} {
					_, problem := invoke(s, credential(t, s, "alice"), op, map[string]any{}, randomID("denied"))
					if problem == nil || problem.Code != "limit" || problem.Message != "agent ancestry admission budget exhausted" {
						t.Fatalf("%s changed its admission denial: %v", op, problem)
					}
				}
				if used := budgetUsage(t, s, ""); used != (BudgetCharge{Tokens: 10, Turns: 1}) {
					t.Fatalf("denied claim charged usage: %+v", used)
				}
				return
			}
			task, lease = autonomyNext(t, s, "alice")
			if task.ID != instruction.ID {
				t.Fatal("eligible parent could not claim its instruction")
			}
			autonomyFinish(t, s, "alice", task, lease)
			if used := budgetUsage(t, s, ""); used != (BudgetCharge{Tokens: 20, Turns: 2}) {
				t.Fatalf("eligible parent changed reservation behavior: %+v", used)
			}
		})
	}
}

func TestAutonomyPausedStatusDistinguishesInstructionQueue(t *testing.T) {
	s := testStore(t, autonomyConfig())
	before := autonomyStatusIn(t, s, "bob")
	call(t, s, "", "autonomy.control", map[string]any{"agent": "bob", "mode": "paused", "version": before.Version})
	budgetInstruction(t, s, "bob")
	budgetInstruction(t, s, "bob")
	call(t, s, "alice", "ping.send", map[string]any{"to": []string{"bob"}, "text": "queued signal"})
	status := autonomyStatusIn(t, s, "bob")
	if status.Mode != "paused" || status.Ready || status.PendingInstructions != 2 || status.PendingObservations != 1 || status.Pending != 1 {
		t.Fatalf("paused queues conflated or hidden: %+v", status)
	}
	var fields map[string]any
	if err := json.Unmarshal(call(t, s, "bob", "autonomy.status", map[string]any{}), &fields); err != nil {
		t.Fatal(err)
	}
	encoded := fields["status"].(map[string]any)
	if encoded["pending"] != float64(1) || encoded["pending_observations"] != float64(1) || encoded["pending_instructions"] != float64(2) {
		t.Fatalf("compatibility and explicit queue fields missing: %v", encoded)
	}
	if task, _ := autonomyNext(t, s, "bob"); task.ID != "" {
		t.Fatal("diagnostics changed paused dispatch behavior")
	}
}
