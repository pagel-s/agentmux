package managedconfig

import "fmt"

// Templates are immutable owner-approved process definitions. Agent requests
// may select one but cannot provide commands, credentials or filesystem mounts.
type Spawn struct {
	Enabled   bool                `yaml:"enabled" json:"enabled"`
	Templates map[string]Agent    `yaml:"templates" json:"templates,omitempty"`
	Allow     map[string][]string `yaml:"allow" json:"allow,omitempty"`
}
type Teams struct {
	Enabled  bool     `yaml:"enabled" json:"enabled"`
	Creators []string `yaml:"creators" json:"creators,omitempty"`
	History  string   `yaml:"history" json:"history"`
}

func ValidID(id string) bool { return idPattern.MatchString(id) }

func (c Config) validateDynamic() error {
	if c.Teams.History != "since-join" && c.Teams.History != "all" {
		return fmt.Errorf("teams.history must be since-join or all")
	}
	for _, v := range []struct {
		name       string
		value, max int
	}{
		{"max_concurrent_tasks", c.Limits.MaxConcurrentTasks, 256}, {"max_spawns", c.Limits.MaxSpawns, 1024},
		{"max_spawn_depth", c.Limits.MaxSpawnDepth, 16}, {"max_teams", c.Limits.MaxTeams, 1024}, {"max_team_members", c.Limits.MaxTeamMembers, 256},
	} {
		if v.value <= 0 || v.value > v.max {
			return fmt.Errorf("limits.%s must be between 1 and %d", v.name, v.max)
		}
	}
	for _, id := range sortedKeys(c.Spawn.Templates) {
		a := c.Spawn.Templates[id]
		if !ValidID(id) {
			return invalidID("spawn template", id)
		}
		if a.Sandbox != "bubblewrap" {
			return fmt.Errorf("spawn template %q requires sandbox: bubblewrap", id)
		}
		if a.Workdir != "" {
			return fmt.Errorf("spawn template %q cannot select a workdir; children receive private workspaces", id)
		}
		// Reuse complete agent validation without recursively carrying templates.
		test := Config{Version: 2, Mode: "managed", Session: "template", Connections: c.Connections, Agents: map[string]Agent{"child": a}}
		if err := test.Validate(); err != nil {
			return fmt.Errorf("spawn template %q: %w", id, err)
		}
	}
	for _, id := range sortedKeys(c.Spawn.Allow) {
		if _, ok := c.Agents[id]; !ok {
			return fmt.Errorf("spawn.allow: unknown agent %q", id)
		}
		for _, name := range c.Spawn.Allow[id] {
			if _, ok := c.Spawn.Templates[name]; !ok {
				return fmt.Errorf("spawn.allow.%s: unknown template %q", id, name)
			}
		}
	}
	if err := c.validateSelectors(c.Teams.Creators); err != nil {
		return fmt.Errorf("teams.creators: %w", err)
	}
	return nil
}
