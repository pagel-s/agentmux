package managedconfig

import "testing"

func TestNativeDeliveryRequiresSupportedInteractiveConfiguration(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		cfg := Config{Version: 2, Mode: "managed", Session: "native", Agents: map[string]Agent{"native": {Harness: harness, Interaction: "interactive", NativeDelivery: &NativeDelivery{Mode: "review", MaxTurns: 10, MaxHops: 3}}}}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		for _, change := range []func(*Agent){
			func(a *Agent) { a.NativeOnly = true }, func(a *Agent) { a.Interaction = "managed" }, func(a *Agent) { a.Harness = "antigravity" },
			func(a *Agent) { a.NativeDelivery.Mode = "always" }, func(a *Agent) { a.NativeDelivery.MaxTurns = -1 }, func(a *Agent) { a.NativeDelivery.MaxHops = 65 }, func(a *Agent) { a.NativeDelivery.CooldownSeconds = -1 },
		} {
			copy := cfg.Normalized()
			a := copy.Agents["native"]
			change(&a)
			copy.Agents["native"] = a
			if err := copy.Validate(); err == nil {
				t.Fatalf("invalid native delivery accepted: %+v", a)
			}
		}
	}
}
