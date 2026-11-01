package managed

import (
	"agentmux/internal/managedconfig"
	"context"
	"encoding/json"
	"testing"
)

func capacityIn(t *testing.T, s *Store, viewer, id string) Capacity {
	t.Helper()
	var out Capacity
	if err := json.Unmarshal(call(t, s, viewer, "capacity.get", map[string]any{"agent": id}), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestCapacityReportsLifetimeLimitsAndReadOnlyOwnInspection(t *testing.T) {
	cfg := dynamicConfig()
	cfg.Limits.MaxSpawns = 2
	cfg.Limits.MaxSpawnDepth = 2
	cfg.Limits.MaxTeams = 2
	cfg.Limits.MaxTeamMembers = 3
	s := testStore(t, cfg)
	child := spawned(t, s, "alice", Capabilities{SpawnTemplates: []string{"echo"}, JoinTeams: true})
	team := teamIn(t, call(t, s, "alice", "team.create", map[string]any{"name": "One"}))
	team = addTeamMember(t, s, team, "bob")
	parent := capacityIn(t, s, "alice", "alice")
	nested := capacityIn(t, s, child.ID, child.ID)
	if parent.SpawnRemaining != 1 || parent.SpawnDepthRemaining != 2 || parent.TeamsRemaining != 1 || parent.TeamSlots[team.ID] != 1 || !parent.CanSpawn || nested.SpawnDepthRemaining != 1 {
		t.Fatalf("wrong capacity parent%+v child%+v", parent, nested)
	}
	expectFault(t, s, "alice", "capacity.get", "forbidden", map[string]any{"agent": "bob"})
	call(t, s, "", "agent.stop", map[string]any{"id": child.ID})
	if after := capacityIn(t, s, "alice", "alice"); after.SpawnRemaining != 1 {
		t.Fatal("revocation refunded lifetime spawn")
	}
	before, _ := count(context.Background(), s.db, "event")
	for i := 0; i < 3; i++ {
		capacityIn(t, s, "alice", "alice")
	}
	after, _ := count(context.Background(), s.db, "event")
	if before != after {
		t.Fatal("capacity reads mutated events")
	}
}
func TestLiveAdmissionBudgetsCASPermanentChargesAndClaimParity(t *testing.T) {
	s := testStore(t, budgetConfig())
	original := s.Config()
	budgetInstruction(t, s, "bob")
	task, lease := autonomyNext(t, s, "bob")
	autonomyFinish(t, s, "bob", task, lease)
	lower := map[string]any{"version": 1, "session_budget": managedconfig.Budget{MaxTokens: 39, MaxCostMicrousd: 1000}}
	expectFault(t, s, "bob", "capacity.update", "forbidden", lower)
	expectFault(t, s, "", "capacity.update", "conflict", lower)
	call(t, s, "", "capacity.update", map[string]any{"version": 1, "session_budget": managedconfig.Budget{MaxTokens: 60, MaxCostMicrousd: 1000}})
	expectFault(t, s, "", "capacity.update", "conflict", map[string]any{"version": 1, "session_budget": managedconfig.Budget{MaxTokens: 100}})
	budgetInstruction(t, s, "bob")
	before := capacityIn(t, s, "bob", "bob")
	if !before.AdmissionBlocked || before.AdmissionReason != "session admission budget exhausted" {
		t.Fatalf("missing admission reason %+v", before)
	}
	expectFault(t, s, "bob", "worker.next", "limit", map[string]any{})
	call(t, s, "", "capacity.update", map[string]any{"version": 2, "agent": "bob", "agent_budget": managedconfig.AgentBudget{MaxTurnTokens: 20, MaxTurnCostMicrousd: 300, MaxSubtreeTokens: 60}})
	if now := capacityIn(t, s, "bob", "bob"); now.AdmissionBlocked || now.ResourceVersion != 3 {
		t.Fatalf("live allowance not applied %+v", now)
	}
	task, lease = autonomyNext(t, s, "bob")
	autonomyFinish(t, s, "bob", task, lease)
	if used := budgetUsage(t, s, ""); used.Tokens != 60 || used.Turns != 2 {
		t.Fatalf("live budget rewrote charges %+v", used)
	}
	expectFault(t, s, "", "capacity.update", "conflict", map[string]any{"version": 3, "agent": "bob", "agent_budget": managedconfig.AgentBudget{MaxTurnTokens: 20, MaxTurnCostMicrousd: 300, MaxSubtreeTokens: 59}})
	if s.Config().Budget != original.Budget || s.Config().Agents["bob"].Budget != original.Agents["bob"].Budget {
		t.Fatal("live controls mutated launch inputs")
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if status := capacityIn(t, reopened, "bob", "bob"); !status.AdmissionBlocked || status.ResourceVersion != 3 {
		t.Fatal("live budgets lost on reopen")
	}
}
func TestCapacityDeadlineOnlyShortensAdmissionAndCorruptionFailsClosed(t *testing.T) {
	s := testStore(t, budgetConfig())
	original, err := s.RuntimeDeadline()
	if err != nil {
		t.Fatal(err)
	}
	call(t, s, "", "capacity.update", map[string]any{"version": 1, "admission_seconds": 30})
	if actual, _ := s.RuntimeDeadline(); !actual.Equal(original) {
		t.Fatal("admission edit rewrote process lifetime")
	}
	if status := capacityIn(t, s, "bob", "bob"); status.RuntimeRemainingSeconds == nil || *status.RuntimeRemainingSeconds > 30 || status.AdmissionDeadline == "" {
		t.Fatal("missing admission deadline")
	}
	expectFault(t, s, "", "capacity.update", "invalid", map[string]any{"version": 2, "admission_seconds": 60})
	call(t, s, "", "capacity.update", map[string]any{"version": 2, "admission_seconds": 0})
	expectFault(t, s, "", "agent.spawn", "limit", map[string]any{"template": "echo"})
	if _, err = s.db.Exec("DELETE FROM records WHERE kind='resource_control'"); err != nil {
		t.Fatal(err)
	}
	if _, problem := s.turnBudgetAllowance(context.Background(), s.db, "bob"); problem == nil || problem.Code != "internal" {
		t.Fatal("missing live resource state reset admission")
	}
}

func TestLiveAdmissionDeadlineReadinessAndClaimMatch(t *testing.T) {
	s := testStore(t, budgetConfig())
	budgetInstruction(t, s, "bob")
	call(t, s, "", "capacity.update", map[string]any{"version": 1, "admission_seconds": 0})
	status := autonomyStatusIn(t, s, "bob")
	if status.Ready || status.State != "halted" || status.Reason != "session runtime exhausted" {
		t.Fatalf("deadline readiness stayed live %+v", status)
	}
	cap := capacityIn(t, s, "bob", "bob")
	if !cap.AdmissionBlocked || cap.AdmissionReason != "session runtime budget exhausted" {
		t.Fatalf("deadline diagnostic wrong %+v", cap)
	}
	expectFault(t, s, "bob", "worker.next", "limit", map[string]any{})
}
