package managed

import "testing"

func TestBudgetConcurrentAdmissionsAcrossConnectionsAndRestart(t *testing.T) {
	c := budgetConfig()
	for id, a := range c.Agents {
		a.Budget.MaxTurnTokens = 60
		c.Agents[id] = a
	}
	s := testStore(t, c)
	for _, id := range []string{"alice", "bob"} {
		budgetInstruction(t, s, id)
	}
	other, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	alice, bob := credential(t, s, "alice"), credential(t, s, "bob")
	start := make(chan struct{})
	out := make(chan *Fault, 2)
	go func() { <-start; _, f := invoke(s, alice, "task.claim", map[string]any{}, "parallel-a"); out <- f }()
	go func() { <-start; _, f := invoke(other, bob, "task.claim", map[string]any{}, "parallel-b"); out <- f }()
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		if f := <-out; f == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("wanted exactly one admission, got %d", success)
	}
	if used := budgetUsage(t, s, ""); used.Tokens != 60 || used.Turns != 1 {
		t.Fatalf("over-admitted concurrent turns %+v", used)
	}
	other.Close()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if used := budgetUsage(t, reopened, ""); used.Tokens != 60 || used.Turns != 1 {
		t.Fatal("restart reset budget")
	}
}
