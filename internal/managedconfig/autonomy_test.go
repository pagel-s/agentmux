package managedconfig

import "testing"

func TestAutonomyIsOptInBoundedAndCopied(t *testing.T) {
	c := validConfig()
	c = c.Normalized()
	if c.Agents["worker"].Autonomy.Enabled {
		t.Fatal("autonomy enabled by normalization")
	}
	a := c.Agents["worker"]
	a.Autonomy = Autonomy{Enabled: true, Boards: []string{"team"}, MaxBacklog: 2}
	c.Agents["worker"] = a
	n := c.Normalized()
	if n.Agents["worker"].Autonomy.BatchSize != 2 {
		t.Fatal("default batch exceeds explicit backlog")
	}
	a.Autonomy.Boards[0] = "changed"
	if n.Agents["worker"].Autonomy.Boards[0] != "team" {
		t.Fatal("subscription mutated frozen configuration")
	}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		value Autonomy
	}{
		{"turns", Autonomy{MaxTurns: -1}}, {"unbounded backlog", Autonomy{MaxBacklog: 1001}},
		{"batch", Autonomy{MaxBacklog: 1, BatchSize: 2}}, {"cooldown", Autonomy{CooldownSeconds: -1}},
		{"parallel", Autonomy{MaxInFlight: 2}}, {"hops", Autonomy{MaxHops: 101}},
		{"invalid scope", Autonomy{Boards: []string{"../private"}}}, {"duplicate", Autonomy{Boards: []string{"team", "team"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.value.validate("worker"); err == nil {
				t.Fatal("unsafe bound accepted")
			}
		})
	}
}
