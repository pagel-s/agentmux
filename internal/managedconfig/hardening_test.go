package managedconfig

import (
	"agentmux/internal/egress"
	"testing"
)

func TestHardeningRejectsUnenforceableClaims(t *testing.T) {
	for _, a := range []Agent{{Sandbox: "local", Network: "allowlist", Egress: Egress{Allow: []egress.Endpoint{{Origin: "https://example.com"}}}}, {Sandbox: "bubblewrap", Network: "none", Egress: Egress{Allow: []egress.Endpoint{{Origin: "https://example.com"}}}}, {Budget: AgentBudget{Enforcement: "hard"}}, {Budget: AgentBudget{MaxSubtreeTokens: 10}}} {
		if validateAgentHardening(a, Budget{}) == nil {
			t.Fatalf("unsafe configuration accepted: %+v", a)
		}
	}
	if err := validateAgentHardening(Agent{Sandbox: "bubblewrap", Network: "allowlist", Egress: Egress{Allow: []egress.Endpoint{{Origin: "http://127.0.0.1:8080"}}}, Budget: AgentBudget{MaxTurnTokens: 10, Enforcement: "reservation"}}, Budget{MaxTokens: 20}); err != nil {
		t.Fatal(err)
	}
}
