package harness

import (
	"strings"
	"testing"
)

func TestNativeOnlyHasNoPromptInjectionAndRetainsCustomizations(t *testing.T) {
	for _, name := range []string{"claude", "codex", "pi", "antigravity"} {
		spec, err := BuildInteractive(Request{Harness: name, Prompt: "", InheritEnvironment: true})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		joined := strings.Join(spec.Args, " ")
		for _, bad := range []string{"append-system-prompt", "developer_instructions", "--print", "--disable-slash-commands", "--safe-mode", "--no-extensions"} {
			if strings.Contains(joined, bad) {
				t.Fatalf("%s native-only injected or disabled context: %s", name, joined)
			}
		}
	}
}

func TestCodexCustomResponsesEndpointUsesPerProcessConfigAndSelectedKey(t *testing.T) {
	for _, build := range []func(Request) (Spec, error){Build, BuildInteractive} {
		req := Request{Harness: "codex", Model: "local-model", BaseURL: "http://127.0.0.1:8000/v1", APIKeyEnv: "SELECTED_PROVIDER", Env: map[string]string{"SELECTED_PROVIDER": "private-selected-value"}}
		spec, err := build(req)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(spec.Args, " ")
		if !strings.Contains(joined, `wire_api="responses"`) || !strings.Contains(joined, `requires_openai_auth=false`) || !strings.Contains(joined, `base_url="http://127.0.0.1:8000/v1"`) || strings.Contains(joined, "private-selected-value") {
			t.Fatalf("endpoint schema/key isolation: %s", joined)
		}
		found := false
		for _, value := range spec.Env {
			if value == "AGENTMUX_CODEX_API_KEY=private-selected-value" {
				found = true
			}
		}
		if !found {
			t.Fatal("selected credential missing")
		}
	}
}
