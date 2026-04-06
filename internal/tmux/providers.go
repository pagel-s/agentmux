package tmux

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	prov "agentmux/internal/provider"
	"agentmux/internal/resolve"
)

var (
	providerLookPath = exec.LookPath
	providerRun      = func(path string, args ...string) (string, error) {
		cmd := exec.Command(path, args...)
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		return buf.String(), err
	}
)

// compileProviderCommand returns a shell command string for recognized LLM providers.
// If the provider is not recognized, it returns an empty string.
func compileProviderCommand(session string, pane resolve.Pane) string {
	return compileProviderCommandWithProfile(session, pane, resolveLaunchProfile(pane.Provider))
}

func resolveLaunchProfile(name string) prov.LaunchProfile {
	return prov.ResolveLaunchWith(name, providerLookPath, providerRun)
}

func compileProviderCommandWithProfile(session string, pane resolve.Pane, profile prov.LaunchProfile) string {
	_ = session

	spec, ok := prov.Lookup(pane.Provider)
	if !ok || spec.Mode != prov.ModeNative {
		return ""
	}
	if profile.Spec.ID == "" {
		profile = prov.ResolveLaunch(pane.Provider)
	}

	switch spec.ID {
	case "claude":
		return compileClaudeCommand(spec, pane, profile)
	case "gemini":
		return compileGeminiCommand(spec, pane, profile, session)
	case "codex":
		return compileCodexCommand(spec, pane, profile)
	case "gptme":
		return compileGPTMeCommand(spec, pane, profile)
	default:
		return ""
	}
}

func compileClaudeCommand(spec prov.Spec, pane resolve.Pane, profile prov.LaunchProfile) string {
	cmd := []string{"claude"}

	if pane.Persona != "" {
		cmd = append(cmd, "--system-prompt", fmt.Sprintf(`"$(cat %s)"`, singleQuote(pane.Persona)))
	}
	if profile.Mode == string(prov.LaunchModeNativeMCP) {
		cmd = append(cmd, "--mcp-config", singleQuote(claudeMCPConfigJSON()), "--strict-mcp-config")
	}

	cmd = append(cmd, "--append-system-prompt", providerContextPrompt(spec.ID, profile.Mode))

	if pane.Model != "" {
		cmd = append(cmd, "--model", singleQuote(pane.Model))
	}
	if pane.ReasoningEffort != "" {
		cmd = append(cmd, "--effort", singleQuote(pane.ReasoningEffort))
	}

	return strings.Join(cmd, " ")
}

func compileGeminiCommand(spec prov.Spec, pane resolve.Pane, profile prov.LaunchProfile, session string) string {
	cmd := []string{"gemini"}
	runtime := describeProviderRuntime(session, pane, profile)

	if pane.Model != "" {
		cmd = append(cmd, "--model", singleQuote(pane.Model))
	}
	if profile.Mode == string(prov.LaunchModeNativeMCP) {
		cmd = append(cmd, "--allowed-mcp-server-names", singleQuote("agentmux"))
	}
	for _, dir := range runtime.IncludeDirectories {
		cmd = append(cmd, "--include-directories", singleQuote(dir))
	}

	cmd = append(cmd, "--prompt-interactive", providerInitialPrompt(pane, spec.ID, profile))

	return strings.Join(cmd, " ")
}

func compileCodexCommand(spec prov.Spec, pane resolve.Pane, profile prov.LaunchProfile) string {
	cmd := []string{"codex"}

	if pane.Model != "" {
		cmd = append(cmd, "--model", singleQuote(pane.Model))
	}
	if profile.Mode == string(prov.LaunchModeNativeMCP) {
		cmd = append(cmd, codexMCPConfigArgs()...)
	}
	if pane.ReasoningEffort != "" {
		cmd = append(cmd, "-c", singleQuote("model_reasoning_effort="+pane.ReasoningEffort))
	}

	cmd = append(cmd, providerInitialPrompt(pane, spec.ID, profile))

	return strings.Join(cmd, " ")
}

func compileGPTMeCommand(spec prov.Spec, pane resolve.Pane, profile prov.LaunchProfile) string {
	cmd := []string{"gptme"}

	cmd = append(cmd, providerInitialPrompt(pane, spec.ID, profile))

	if pane.Model != "" {
		cmd = append(cmd, "--model", singleQuote(pane.Model))
	}

	return strings.Join(cmd, " ")
}

func providerInitialPrompt(pane resolve.Pane, providerName string, profile prov.LaunchProfile) string {
	if pane.Persona != "" && profile.PersonaChannel == "bootstrap-prompt" {
		return fmt.Sprintf(`"$(cat %s)

$(%s)"`, singleQuote(pane.Persona), providerPromptCommand(providerName, profile.Mode))
	}

	return providerContextPrompt(providerName, profile.Mode)
}

func providerContextPrompt(providerName string, mode string) string {
	return fmt.Sprintf(`"$(%s)"`, providerPromptCommand(providerName, mode))
}

func providerPromptCommand(providerName string, mode string) string {
	cmd := []string{"agentmux", "prompt", "--provider", singleQuote(providerName)}
	if strings.TrimSpace(mode) != "" {
		cmd = append(cmd, "--mode", singleQuote(mode))
	}
	return strings.Join(cmd, " ")
}

func claudeMCPConfigJSON() string {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"agentmux": map[string]any{
				"command": "agentmux",
				"args":    []string{"mcp-server"},
			},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return `{"mcpServers":{"agentmux":{"command":"agentmux","args":["mcp-server"]}}}`
	}
	return string(data)
}

func codexMCPConfigArgs() []string {
	return []string{
		"-c", singleQuote(`mcp_servers.agentmux.command="agentmux"`),
		"-c", singleQuote(`mcp_servers.agentmux.args=["mcp-server"]`),
	}
}
