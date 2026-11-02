package tools

import (
	"strings"
	"testing"
)

func TestDefinitionsAcceptProviderAliases(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		wantToken string
	}{
		{name: "claude alias", format: "claude", wantToken: `"input_schema"`},
		{name: "anthropic format", format: "anthropic", wantToken: `"input_schema"`},
		{name: "codex alias", format: "codex", wantToken: `"function"`},
		{name: "gemini alias", format: "gemini", wantToken: `"function"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Definitions(tc.format)
			if err != nil {
				t.Fatalf("Definitions(%q) returned error: %v", tc.format, err)
			}
			if !strings.Contains(out, tc.wantToken) {
				t.Fatalf("Definitions(%q) output %q does not include %q", tc.format, out, tc.wantToken)
			}
		})
	}
}

func TestDefinitionsRejectUnsupportedFormat(t *testing.T) {
	if _, err := Definitions("bogus"); err == nil {
		t.Fatal("Definitions(bogus) returned nil error, want unsupported format")
	}
}

func TestLookupAcceptsCanonicalAndLegacyNames(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "kv_set", want: "kv_set"},
		{input: "kv-set", want: "kv_set"},
		{input: "agentmux_kv_set", want: "kv_set"},
		{input: "send", want: "send"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			spec, ok := Lookup(tc.input)
			if !ok {
				t.Fatalf("Lookup(%q) = false, want true", tc.input)
			}
			if spec.Name != tc.want {
				t.Fatalf("Lookup(%q).Name = %q, want %q", tc.input, spec.Name, tc.want)
			}
		})
	}
}

func TestPromptUsesCLIContractByDefault(t *testing.T) {
	out, err := Prompt(PromptOptions{})
	if err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}

	for _, want := range []string{
		"Use exactly one input mode per call:",
		"--from-stdin",
		"--from-file",
		"Preferred tool-call input mode for this provider: stdin.",
		"The `wait` tool ignores active input/composer text by default;",
		"- kv_set",
		"- kv_get",
		"- wait",
		"- scale",
		"- send",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Prompt output %q does not include %q", out, want)
		}
	}
	if strings.Contains(out, "Legacy Tool Schemas") {
		t.Fatalf("Prompt output %q unexpectedly includes legacy schema block", out)
	}
}

func TestPromptSupportsNativeMCPMode(t *testing.T) {
	out, err := Prompt(PromptOptions{Provider: "claude", Mode: "native-mcp"})
	if err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}
	for _, want := range []string{
		"Agentmux coordination tools are available natively through MCP in this session.",
		"Use the provided tools directly instead of shelling out to `amx tool-call`.",
		"The `wait` tool ignores active input/composer text by default;",
		"Call `kv_get` directly with `key=current_task`.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Prompt output %q does not include %q", out, want)
		}
	}
}

func TestPromptIncludesProviderChannels(t *testing.T) {
	out, err := Prompt(PromptOptions{Provider: "claude"})
	if err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}
	for _, want := range []string{
		"Your role/persona instructions are delivered via appended-system-prompt.",
		"The Agentmux coordination contract is delivered via native-mcp.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Prompt output %q does not include %q", out, want)
		}
	}
}

func TestPromptDefaultsGeminiToNativeMCP(t *testing.T) {
	out, err := Prompt(PromptOptions{Provider: "gemini"})
	if err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}
	for _, want := range []string{
		"Your role/persona instructions are delivered via workspace-context-file.",
		"The Agentmux coordination contract is delivered via native-mcp.",
		"Agentmux coordination tools are available natively through MCP in this session.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Prompt output %q does not include %q", out, want)
		}
	}
}

func TestPromptIncludesCLIBridgeChannelsForFallbackMode(t *testing.T) {
	out, err := Prompt(PromptOptions{Provider: "claude", Mode: "cli-bridge"})
	if err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}
	for _, want := range []string{
		"Your role/persona instructions are delivered via appended-system-prompt.",
		"The Agentmux coordination contract is delivered via appended-system-prompt.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Prompt output %q does not include %q", out, want)
		}
	}
}

func TestPromptResolvesUnverifiedNativeMCPToCLIBridge(t *testing.T) {
	for _, provider := range []string{"antigravity", "agy", "pi"} {
		t.Run(provider, func(t *testing.T) {
			out, err := Prompt(PromptOptions{Provider: provider, Mode: "native-mcp"})
			if err != nil {
				t.Fatalf("Prompt returned error: %v", err)
			}
			if strings.Contains(out, "available natively through MCP") {
				t.Fatalf("Prompt claims unavailable native MCP: %q", out)
			}
			if !strings.Contains(out, "call `amx tool-call`") {
				t.Fatalf("Prompt does not explain the CLI bridge: %q", out)
			}
		})
	}
}
