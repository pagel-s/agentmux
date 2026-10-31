package managedconfig

import "testing"

func TestDynamicConfigurationIsExplicitAndCloned(t *testing.T) {
	c := validConfig()
	c.Spawn = Spawn{Enabled: true, Templates: map[string]Agent{"helper": {Harness: "command", Sandbox: "bubblewrap", Command: []string{"/bin/cat"}, Env: map[string]string{"A": "B"}}}, Allow: map[string][]string{"lead": {"helper"}}}
	c.Teams = Teams{Enabled: true, Creators: []string{"lead"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	copy := c.Normalized()
	copy.Spawn.Templates["helper"].Command[0] = "mutated"
	copy.Spawn.Templates["helper"].Env["A"] = "mutated"
	copy.Spawn.Allow["lead"][0] = "mutated"
	copy.Teams.Creators[0] = "mutated"
	if c.Spawn.Templates["helper"].Command[0] != "/bin/cat" || c.Spawn.Templates["helper"].Env["A"] != "B" || c.Spawn.Allow["lead"][0] != "helper" || c.Teams.Creators[0] != "lead" {
		t.Fatal("dynamic policy shares caller-owned references")
	}
	for _, mutate := range []func(*Config){
		func(c *Config) {
			a := c.Spawn.Templates["helper"]
			a.Sandbox = "local"
			c.Spawn.Templates["helper"] = a
		},
		func(c *Config) {
			a := c.Spawn.Templates["helper"]
			a.Workdir = "/home"
			c.Spawn.Templates["helper"] = a
		},
		func(c *Config) { c.Spawn.Allow["intruder"] = []string{"helper"} },
		func(c *Config) { c.Spawn.Allow["lead"] = []string{"unknown"} },
		func(c *Config) { c.Limits.MaxSpawnDepth = 100 },
	} {
		bad := c.Normalized()
		mutate(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatal("unsafe dynamic configuration accepted")
		}
	}
}
