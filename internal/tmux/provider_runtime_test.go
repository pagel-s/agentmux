package tmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	prov "agentmux/internal/provider"
	"agentmux/internal/resolve"
)

func TestPrepareProviderRuntimeWritesGeminiSettings(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	profile := prov.ProfileForMode("gemini", "native-mcp")
	personaPath := filepath.Join(t.TempDir(), "persona.md")
	if err := os.WriteFile(personaPath, []byte("# persona\n"), 0o644); err != nil {
		t.Fatalf("WriteFile persona returned error: %v", err)
	}
	if err := prepareProviderRuntime("ui-factory", resolve.Pane{Provider: "gemini", Persona: personaPath}, profile); err != nil {
		t.Fatalf("prepareProviderRuntime returned error: %v", err)
	}

	path := geminiSystemSettingsPath("ui-factory")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) returned error: %v", path, err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("settings JSON is invalid: %v", err)
	}

	mcpServers, ok := decoded["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers = %#v, want object", decoded["mcpServers"])
	}
	agentmux, ok := mcpServers["agentmux"].(map[string]any)
	if !ok {
		t.Fatalf("agentmux server = %#v, want object", mcpServers["agentmux"])
	}
	if got := agentmux["command"]; got != "agentmux" {
		t.Fatalf("agentmux command = %#v, want %q", got, "agentmux")
	}

	context, ok := decoded["context"].(map[string]any)
	if !ok {
		t.Fatalf("context = %#v, want object", decoded["context"])
	}
	fileNames, ok := context["fileName"].([]any)
	if !ok || len(fileNames) < 1 || fileNames[0] != geminiPersonaFileName {
		t.Fatalf("context.fileName = %#v, want %q first", context["fileName"], geminiPersonaFileName)
	}

	artifactPath := geminiPersonaPath("ui-factory", resolve.Pane{Provider: "gemini", Persona: personaPath})
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) returned error: %v", artifactPath, err)
	}
	if string(artifact) != "# persona\n" {
		t.Fatalf("artifact content = %q, want persona file content", string(artifact))
	}
}

func TestBuildPaneCommandIncludesGeminiSettingsEnvInNativeMode(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("SHELL", "/bin/bash")

	command := buildPaneCommandWithProfile("ui-factory", resolve.Pane{
		Provider: "gemini",
		Persona:  "personas/ui.md",
		Cwd:      "/tmp/project",
		OnExit:   "shell",
	}, prov.ProfileForMode("gemini", "native-mcp"))

	path := geminiSystemSettingsPath("ui-factory")
	if !strings.Contains(command, "GEMINI_CLI_SYSTEM_SETTINGS_PATH="+quoteEnvValue(path)) {
		t.Fatalf("command missing Gemini settings env: %q", command)
	}
	if !strings.Contains(command, "--include-directories "+singleQuote(filepath.Dir(geminiPersonaPath("ui-factory", resolve.Pane{Provider: "gemini", Persona: "personas/ui.md"})))) {
		t.Fatalf("command missing Gemini include directory: %q", command)
	}
	if strings.Contains(command, `$(cat 'personas/ui.md')`) {
		t.Fatalf("command unexpectedly inlines persona into Gemini prompt: %q", command)
	}
	if !strings.Contains(command, "AGENTMUX_SESSION='ui-factory'") {
		t.Fatalf("command missing AGENTMUX_SESSION export: %q", command)
	}
}

func TestGeminiSystemSettingsPathUsesTempDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	path := geminiSystemSettingsPath("ui factory")
	if !strings.HasPrefix(path, filepath.Join(tmp, "agentmux", "gemini")+string(os.PathSeparator)) {
		t.Fatalf("geminiSystemSettingsPath = %q, want prefix under TMPDIR", path)
	}
	if !strings.Contains(path, "ui_factory") {
		t.Fatalf("geminiSystemSettingsPath = %q, want sanitized session component", path)
	}
}
