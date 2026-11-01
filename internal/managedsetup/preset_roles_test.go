package managedsetup

import (
	"path/filepath"
	"testing"
)

func TestManagedRolePresetsRemainIdleAndExplicit(t *testing.T) {
	for name, count := range map[string]int{"reviewer-pair": 2, "research-team": 3} {
		s, err := Preset(name, "study", filepath.Join(t.TempDir(), "workspaces"), "", "local", t.TempDir())
		if err != nil || len(s.Agents) != count {
			t.Fatalf("%s: %+v %v", name, s, err)
		}
		seen := map[string]bool{}
		for i, a := range s.Agents {
			if a.Role == "" || a.Interaction != "managed" || seen[a.ID] {
				t.Fatalf("%s preset lacks distinct role or managed mode: %+v", name, a)
			}
			seen[a.ID] = true
			s.Agents[i].Binary = fakeBinary(t, a.Harness)
		}
		plan, err := Build(s)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Config.InteractiveWorkspace || plan.Config.Spawn.Enabled || plan.Config.Teams.Enabled || len(plan.Config.Policy.Grants) != 0 {
			t.Fatal("preset silently enabled autonomous authority")
		}
	}
	if _, err := Preset("unknown", "x", "/tmp/x", "", "local", "/tmp/home"); err == nil {
		t.Fatal("unknown preset accepted")
	}
}
