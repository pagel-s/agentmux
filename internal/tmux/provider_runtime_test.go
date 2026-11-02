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
	pane := resolve.Pane{Name: "visual-designer", Provider: "gemini", Persona: personaPath}
	if err := prepareProviderRuntime("ui-factory", pane, profile); err != nil {
		t.Fatalf("prepareProviderRuntime returned error: %v", err)
	}

	path := geminiSystemSettingsPath("ui-factory", pane)
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
	if got := agentmux["command"]; got != "amx" {
		t.Fatalf("agentmux command = %#v, want %q", got, "amx")
	}
	env, ok := agentmux["env"].(map[string]any)
	if !ok {
		t.Fatalf("agentmux env = %#v, want object", agentmux["env"])
	}
	if got := env["AGENTMUX_SESSION"]; got != "ui-factory" {
		t.Fatalf("env.AGETNMUX_SESSION = %#v, want %q", got, "ui-factory")
	}
	if got := env["AGENTMUX_PANE"]; got != "visual-designer" {
		t.Fatalf("env.AGENTMUX_PANE = %#v, want %q", got, "visual-designer")
	}

	context, ok := decoded["context"].(map[string]any)
	if !ok {
		t.Fatalf("context = %#v, want object", decoded["context"])
	}
	fileNames, ok := context["fileName"].([]any)
	if !ok || len(fileNames) < 1 || fileNames[0] != geminiPersonaFileName {
		t.Fatalf("context.fileName = %#v, want %q first", context["fileName"], geminiPersonaFileName)
	}

	artifactPath := geminiPersonaPath("ui-factory", pane)
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
		Name:     "visual-designer",
		Provider: "gemini",
		Persona:  "personas/ui.md",
		Cwd:      "/tmp/project",
		OnExit:   "shell",
	}, prov.ProfileForMode("gemini", "native-mcp"))

	path := geminiSystemSettingsPath("ui-factory", resolve.Pane{Name: "visual-designer", Provider: "gemini", Persona: "personas/ui.md"})
	if !strings.Contains(command, "GEMINI_CLI_SYSTEM_SETTINGS_PATH="+quoteEnvValue(path)) {
		t.Fatalf("command missing Gemini settings env: %q", command)
	}
	if !strings.Contains(command, "--include-directories "+singleQuote(filepath.Dir(geminiPersonaPath("ui-factory", resolve.Pane{Name: "visual-designer", Provider: "gemini", Persona: "personas/ui.md"})))) {
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

	path := geminiSystemSettingsPath("ui factory", resolve.Pane{Name: "designer", Provider: "gemini"})
	if !strings.HasPrefix(path, filepath.Join(tmp, "agentmux", "gemini")+string(os.PathSeparator)) {
		t.Fatalf("geminiSystemSettingsPath = %q, want prefix under TMPDIR", path)
	}
	if !strings.Contains(path, "ui_factory") {
		t.Fatalf("geminiSystemSettingsPath = %q, want sanitized session component", path)
	}
	if !strings.Contains(path, "designer") {
		t.Fatalf("geminiSystemSettingsPath = %q, want pane-specific path", path)
	}
}

func TestPrepareAntigravityRuntimeDoesNotCreateSharedConfiguration(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	profile := prov.ProfileForMode("antigravity", "native-mcp")
	pane := resolve.Pane{Name: "researcher", Provider: "antigravity", Persona: "role.md"}
	if err := prepareProviderRuntime("research-team", pane, profile); err != nil {
		t.Fatalf("prepareProviderRuntime: %v", err)
	}
	descriptor := describeProviderRuntime("research-team", pane, profile)
	if len(descriptor.Env) != 0 || descriptor.PersonaArtifact != "" || len(descriptor.IncludeDirectories) != 0 {
		t.Fatalf("Antigravity runtime unexpectedly uses configuration artifacts: %#v", descriptor)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("Antigravity runtime created configuration files: %#v", entries)
	}
}
