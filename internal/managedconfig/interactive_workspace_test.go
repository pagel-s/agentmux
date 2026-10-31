package managedconfig

import (
	"encoding/json"
	"testing"
)

func nativeWorkspaceConfig() Config {
	return Config{Version: 2, Mode: "managed", Session: "daily", InteractiveWorkspace: true, Agents: map[string]Agent{
		"claude": {Harness: "claude", Interaction: "interactive", Sandbox: "local"},
		"codex":  {Harness: "codex", Interaction: "interactive", Sandbox: "local"},
	}}
}

func TestInteractiveWorkspaceRequiresHumanControlledNativeAgents(t *testing.T) {
	cfg := nativeWorkspaceConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"managed-agent", "implicit-managed-agent", "spawn", "autonomy", "negative-runtime-limit"} {
		t.Run(kind, func(t *testing.T) {
			cfg := nativeWorkspaceConfig()
			agent := cfg.Agents["claude"]
			switch kind {
			case "managed-agent":
				agent.Interaction = "managed"
			case "implicit-managed-agent":
				agent.Interaction = ""
			case "spawn":
				cfg.Spawn.Enabled = true
			case "autonomy":
				agent.Autonomy.Enabled = true
			case "negative-runtime-limit":
				cfg.Limits.MaxRuntimeSeconds = -1
			}
			cfg.Agents["claude"] = agent
			if err := cfg.Validate(); err == nil {
				t.Fatalf("unbounded native mode accepted %s", kind)
			}
		})
	}
}

func TestInteractiveWorkspaceEncodingAndFiniteResourceLimits(t *testing.T) {
	cfg, err := loadText(t, `version: 2
mode: managed
session: daily
interactive_workspace: true
agents:
  native:
    harness: claude
    interaction: interactive
`)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.InteractiveWorkspace || cfg.Limits.MaxRuntimeSeconds <= 0 || cfg.Limits.MaxTasks <= 0 || cfg.Limits.MaxRecords <= 0 {
		t.Fatalf("native mode lost finite resource limits: %+v", cfg)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var restored Config
	if err = json.Unmarshal(raw, &restored); err != nil || !restored.InteractiveWorkspace {
		t.Fatalf("native mode lost in config persistence: %s (%v)", raw, err)
	}
	managed := validConfig().Normalized()
	if managed.InteractiveWorkspace || managed.Limits.MaxRuntimeSeconds <= 0 {
		t.Fatal("ordinary automation became unbounded")
	}
}
