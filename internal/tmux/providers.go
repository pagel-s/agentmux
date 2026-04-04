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

	switch provider {
	case "claude", "claude-cli":
		return compileClaudeCommand(pane)
	case "gemini", "gemini-cli":
		return compileGeminiCommand(pane)
	case "openai", "aichat":
		return compileAIChatCommand(pane)
	case "gptme":
		return compileGPTMeCommand(pane)
	default:
		// Not a built-in JIT provider, let the standard command handling take over
		return ""
	}
}

func compileClaudeCommand(pane resolve.Pane) string {
	cmd := []string{"claude-cli", "chat"}
	
	if pane.Persona != "" {
		cmd = append(cmd, "--system", fmt.Sprintf(`"$(cat %s) ; agentmux prompt"`, singleQuote(pane.Persona)))
	} else {
		cmd = append(cmd, "--system", `"$(agentmux prompt)"`)
	}
	
	cmd = append(cmd, "--tools", `"$(agentmux tools --format anthropic)"`)
	
	if pane.Model != "" {
		cmd = append(cmd, "--model", singleQuote(pane.Model))
	}
	
	return strings.Join(cmd, " ")
}

func compileGeminiCommand(pane resolve.Pane) string {
	cmd := []string{"gemini-cli", "chat"}
	
	if pane.Persona != "" {
		cmd = append(cmd, "--system", fmt.Sprintf(`"$(cat %s) ; agentmux prompt"`, singleQuote(pane.Persona)))
	} else {
		cmd = append(cmd, "--system", `"$(agentmux prompt)"`)
	}
	
	cmd = append(cmd, "--tools", `"$(agentmux tools --format openai)"`)
	
	if pane.Model != "" {
		cmd = append(cmd, "--model", singleQuote(pane.Model))
	}
	
	return strings.Join(cmd, " ")
}

func compileAIChatCommand(pane resolve.Pane) string {
	cmd := []string{"aichat"}
	
	if pane.Persona != "" {
		cmd = append(cmd, "--role", fmt.Sprintf(`"$(cat %s) ; agentmux prompt"`, singleQuote(pane.Persona)))
	} else {
		cmd = append(cmd, "--role", `"$(agentmux prompt)"`)
	}
	
	// Assuming aichat supports a --tools flag like this, or we might need to adjust for its specific syntax
	cmd = append(cmd, "--tools", `"$(agentmux tools --format openai)"`)
	
	if pane.Model != "" {
		cmd = append(cmd, "--model", singleQuote(pane.Model))
	}
	
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
