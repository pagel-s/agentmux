package tmux

import (
	"agentmux/internal/resolve"
	"fmt"
	"strings"
)

// compileProviderCommand returns a shell command string for recognized LLM providers.
// If the provider is not recognized, it returns an empty string.
func compileProviderCommand(session string, pane resolve.Pane) string {
	provider := strings.ToLower(strings.TrimSpace(pane.Provider))
	
	if provider == "" || provider == "command" || provider == "shell" {
		return ""
	}

	switch {
	case strings.HasPrefix(provider, "claude"):
		return compileClaudeCommand(pane)
	case strings.HasPrefix(provider, "gemini"):
		return compileGeminiCommand(pane)
	case provider == "openai" || provider == "codex":
		return compileCodexCommand(pane)
	case provider == "gptme":
		return compileGPTMeCommand(pane)
	default:
		return ""
	}
}

func compileClaudeCommand(pane resolve.Pane) string {
	// Anthropic's Claude Code (binary: claude)
	cmd := []string{"claude"}
	
	if pane.Persona != "" {
		cmd = append(cmd, "--system-prompt", fmt.Sprintf(`"$(cat %s)"`, singleQuote(pane.Persona)))
	}
	
	// Append Agentmux context and tool descriptions to the system prompt
	cmd = append(cmd, "--append-system-prompt", fmt.Sprintf(`"$(agentmux prompt)

Available Coordination Tools:
$(agentmux tools)"`))
	
	if pane.Model != "" {
		cmd = append(cmd, "--model", singleQuote(pane.Model))
	}
	
	return strings.Join(cmd, " ")
}

func compileGeminiCommand(pane resolve.Pane) string {
	// Google's Gemini CLI (binary: gemini)
	cmd := []string{"gemini"}
	
	if pane.Model != "" {
		cmd = append(cmd, "--model", singleQuote(pane.Model))
	}

	// Gemini takes the prompt as a positional argument.
	// We inject the persona, environment awareness, and tools directly into the initial prompt.
	var prompt string
	if pane.Persona != "" {
		prompt = fmt.Sprintf(`"$(cat %s)

$(agentmux prompt)

Available Coordination Tools:
$(agentmux tools)"`, singleQuote(pane.Persona))
	} else {
		prompt = `"$(agentmux prompt)

Available Coordination Tools:
$(agentmux tools)"`
	}
	cmd = append(cmd, prompt)
	
	return strings.Join(cmd, " ")
}

func compileCodexCommand(pane resolve.Pane) string {
	// OpenAI's Codex CLI (binary: codex)
	cmd := []string{"codex"}
	
	if pane.Model != "" {
		cmd = append(cmd, "--model", singleQuote(pane.Model))
	}
	
	// Codex takes the prompt as a positional argument.
	// We inject the persona, environment awareness, and tools directly into the initial prompt.
	var prompt string
	if pane.Persona != "" {
		prompt = fmt.Sprintf(`"$(cat %s)

$(agentmux prompt)

Available Coordination Tools:
$(agentmux tools)"`, singleQuote(pane.Persona))
	} else {
		prompt = `"$(agentmux prompt)

Available Coordination Tools:
$(agentmux tools)"`
	}
	cmd = append(cmd, prompt)
	
	return strings.Join(cmd, " ")
}

func compileGPTMeCommand(pane resolve.Pane) string {
	cmd := []string{"gptme"}
	
	// GPTMe typically takes prompt directly or via files
	if pane.Persona != "" {
		cmd = append(cmd, fmt.Sprintf(`"$(cat %s) ; agentmux prompt"`, singleQuote(pane.Persona)))
	} else {
		cmd = append(cmd, `"$(agentmux prompt)"`)
	}
	
	if pane.Model != "" {
		cmd = append(cmd, "--model", singleQuote(pane.Model))
	}
	
	return strings.Join(cmd, " ")
}
