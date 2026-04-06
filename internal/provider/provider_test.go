package provider

import "testing"

func TestLookupNormalizesAliases(t *testing.T) {
	tests := []struct {
		input      string
		wantID     string
		wantBinary string
	}{
		{input: "claude-cli", wantID: "claude", wantBinary: "claude"},
		{input: "gemini-cli", wantID: "gemini", wantBinary: "gemini"},
		{input: "openai", wantID: "codex", wantBinary: "codex"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			spec, ok := Lookup(tc.input)
			if !ok {
				t.Fatalf("Lookup(%q) = not found, want spec", tc.input)
			}
			if spec.ID != tc.wantID {
				t.Fatalf("spec.ID = %q, want %q", spec.ID, tc.wantID)
			}
			if spec.Binary != tc.wantBinary {
				t.Fatalf("spec.Binary = %q, want %q", spec.Binary, tc.wantBinary)
			}
		})
	}
}

func TestAliasHelpers(t *testing.T) {
	target, ok := AliasTarget("openai")
	if !ok || target != "codex" {
		t.Fatalf("AliasTarget(openai) = (%q, %v), want (%q, true)", target, ok, "codex")
	}
	if !IsAlias("gemini-cli") {
		t.Fatal("IsAlias(gemini-cli) = false, want true")
	}
	if IsAlias("gemini") {
		t.Fatal("IsAlias(gemini) = true, want false")
	}
}

func TestRequiresCommandAndDirectPaneRules(t *testing.T) {
	if !RequiresCommand("ssh") {
		t.Fatal("RequiresCommand(ssh) = false, want true")
	}
	if AllowsDirectPaneProvider("ssh") {
		t.Fatal("AllowsDirectPaneProvider(ssh) = true, want false")
	}
	if RequiresCommand("claude") {
		t.Fatal("RequiresCommand(claude) = true, want false")
	}
	if !AllowsDirectPaneProvider("claude") {
		t.Fatal("AllowsDirectPaneProvider(claude) = false, want true")
	}
}

func TestIntegrationMetadata(t *testing.T) {
	if got := IntegrationMode("codex"); got != "native-mcp" {
		t.Fatalf("IntegrationMode(codex) = %q, want %q", got, "native-mcp")
	}
	if got := IntegrationMode("gemini"); got != "native-mcp" {
		t.Fatalf("IntegrationMode(gemini) = %q, want %q", got, "native-mcp")
	}
	if got := PersonaChannel("claude"); got != "system-prompt" {
		t.Fatalf("PersonaChannel(claude) = %q, want %q", got, "system-prompt")
	}
	if got := PersonaChannel("gemini"); got != "bootstrap-prompt" {
		t.Fatalf("PersonaChannel(gemini) = %q, want %q", got, "bootstrap-prompt")
	}
	if got := PersonaSource("gemini"); got != "config-file" {
		t.Fatalf("PersonaSource(gemini) = %q, want %q", got, "config-file")
	}
	if got := ToolChannel("claude"); got != "appended-system-prompt" {
		t.Fatalf("ToolChannel(claude) = %q, want %q", got, "appended-system-prompt")
	}
	if got := PreferredToolInput("codex"); got != "stdin" {
		t.Fatalf("PreferredToolInput(codex) = %q, want %q", got, "stdin")
	}
}

func TestResolveLaunchWithDetectsNativeMCP(t *testing.T) {
	lookPath := func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}

	claude := ResolveLaunchWith("claude", lookPath, func(path string, args ...string) (string, error) {
		return "--mcp-config\n--strict-mcp-config\n--append-system-prompt\n", nil
	})
	if claude.Mode != string(LaunchModeNativeMCP) {
		t.Fatalf("claude launch mode = %q, want %q", claude.Mode, LaunchModeNativeMCP)
	}
	if claude.ToolChannel != "native-mcp" {
		t.Fatalf("claude tool channel = %q, want %q", claude.ToolChannel, "native-mcp")
	}

	codex := ResolveLaunchWith("codex", lookPath, func(path string, args ...string) (string, error) {
		return "--config\nmcp\n", nil
	})
	if codex.Mode != string(LaunchModeNativeMCP) {
		t.Fatalf("codex launch mode = %q, want %q", codex.Mode, LaunchModeNativeMCP)
	}
	if codex.PreferredInput != "native-mcp" {
		t.Fatalf("codex preferred input = %q, want %q", codex.PreferredInput, "native-mcp")
	}

	gemini := ResolveLaunchWith("gemini", lookPath, func(path string, args ...string) (string, error) {
		return "--prompt-interactive\n--allowed-mcp-server-names\ngemini mcp\n", nil
	})
	if gemini.Mode != string(LaunchModeNativeMCP) {
		t.Fatalf("gemini launch mode = %q, want %q", gemini.Mode, LaunchModeNativeMCP)
	}
	if gemini.ToolChannel != "native-mcp" {
		t.Fatalf("gemini tool channel = %q, want %q", gemini.ToolChannel, "native-mcp")
	}
}

func TestResolveLaunchWithFallsBackToCLIBridge(t *testing.T) {
	lookPath := func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}

	claude := ResolveLaunchWith("claude", lookPath, func(path string, args ...string) (string, error) {
		return "--append-system-prompt\n--model\n", nil
	})
	if claude.Mode != string(LaunchModeCLIBridge) {
		t.Fatalf("claude launch mode = %q, want %q", claude.Mode, LaunchModeCLIBridge)
	}
	if claude.FallbackReason == "" {
		t.Fatal("claude fallback reason = empty, want explanation")
	}

	gemini := ResolveLaunchWith("gemini", lookPath, func(path string, args ...string) (string, error) {
		return "--prompt-interactive\n--model\n", nil
	})
	if gemini.Mode != string(LaunchModeCLIBridge) {
		t.Fatalf("gemini launch mode = %q, want %q", gemini.Mode, LaunchModeCLIBridge)
	}
	if gemini.FallbackReason == "" {
		t.Fatal("gemini fallback reason = empty, want explanation")
	}
}

func TestProfileForModeUsesRequestedChannels(t *testing.T) {
	profile := ProfileForMode("codex", "native-mcp")
	if profile.Mode != "native-mcp" {
		t.Fatalf("profile.Mode = %q, want %q", profile.Mode, "native-mcp")
	}
	if profile.ToolChannel != "native-mcp" {
		t.Fatalf("profile.ToolChannel = %q, want %q", profile.ToolChannel, "native-mcp")
	}

	fallback := ProfileForMode("claude", "cli-bridge")
	if fallback.ToolChannel != "appended-system-prompt" {
		t.Fatalf("fallback.ToolChannel = %q, want %q", fallback.ToolChannel, "appended-system-prompt")
	}

	gemini := ProfileForMode("gemini", "native-mcp")
	if gemini.PersonaChannel != "workspace-context-file" {
		t.Fatalf("gemini.PersonaChannel = %q, want %q", gemini.PersonaChannel, "workspace-context-file")
	}
	if gemini.PersonaSource != "session-runtime-file" {
		t.Fatalf("gemini.PersonaSource = %q, want %q", gemini.PersonaSource, "session-runtime-file")
	}
}

func TestValidateReasoningEffort(t *testing.T) {
	if err := ValidateReasoningEffort("claude", "max"); err != nil {
		t.Fatalf("ValidateReasoningEffort(claude, max) returned error: %v", err)
	}
	if err := ValidateReasoningEffort("gemini", "high"); err == nil {
		t.Fatal("ValidateReasoningEffort(gemini, high) returned nil error, want unsupported")
	}
	if err := ValidateReasoningEffort("codex", "ultra"); err == nil {
		t.Fatal("ValidateReasoningEffort(codex, ultra) returned nil error, want unsupported")
	}
}
