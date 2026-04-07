package tmux

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/resolve"
)

func TestCompileProviderCommand(t *testing.T) {
	originalLookPath := providerLookPath
	originalRun := providerRun
	t.Cleanup(func() {
		providerLookPath = originalLookPath
		providerRun = originalRun
	})

	providerLookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}
	providerRun = func(path string, args ...string) (string, error) {
		switch {
		case strings.Contains(path, "claude"):
			return "--mcp-config\n--strict-mcp-config\n--append-system-prompt\n--model\n--effort\n", nil
		case strings.Contains(path, "codex"):
			return "--model\n--config\nmcp\n", nil
		case strings.Contains(path, "gemini"):
			return "--model\n--prompt-interactive\n--allowed-mcp-server-names\ngemini mcp\n", nil
		case strings.Contains(path, "gptme"):
			return "--model\n", nil
		default:
			return "", errors.New("unexpected provider")
		}
	}

	tests := []struct {
		name      string
		provider  string
		paneName  string
		persona   string
		model     string
		reasoning string
		want      string
	}{
		{
			name:     "claude native mcp with persona and model",
			provider: "claude",
			paneName: "lead-architect",
			persona:  "arch.md",
			model:    "sonnet",
			want:     `claude --system-prompt "$(cat 'arch.md')" --mcp-config ` + singleQuote(claudeMCPConfigJSON("test-session", resolve.Pane{Name: "lead-architect", Provider: "claude", Persona: "arch.md", Model: "sonnet"})) + ` --strict-mcp-config --append-system-prompt "$(agentmux prompt --provider 'claude' --mode 'native-mcp')" --model 'sonnet'`,
		},
		{
			name:      "claude native mcp with reasoning effort",
			provider:  "claude",
			paneName:  "lead-architect",
			model:     "sonnet",
			reasoning: "high",
			want:      `claude --mcp-config ` + singleQuote(claudeMCPConfigJSON("test-session", resolve.Pane{Name: "lead-architect", Provider: "claude", Model: "sonnet", ReasoningEffort: "high"})) + ` --strict-mcp-config --append-system-prompt "$(agentmux prompt --provider 'claude' --mode 'native-mcp')" --model 'sonnet' --effort 'high'`,
		},
		{
			name:     "gemini native mcp",
			provider: "gemini-cli",
			paneName: "visual-designer",
			persona:  "design.md",
			want:     `gemini --allowed-mcp-server-names 'agentmux' --include-directories ` + singleQuote(filepath.Dir(geminiPersonaPath("test-session", resolve.Pane{Name: "visual-designer", Provider: "gemini", Persona: "design.md"}))) + ` --prompt-interactive "$(agentmux prompt --provider 'gemini' --mode 'native-mcp')"`,
		},
		{
			name:     "codex native mcp with persona",
			provider: "openai",
			paneName: "senior-coder",
			persona:  "coder.md",
			want: `codex --no-alt-screen --sandbox workspace-write --ask-for-approval never -c 'mcp_servers.agentmux.command="agentmux"' -c 'mcp_servers.agentmux.args=["mcp-server"]' -c 'mcp_servers.agentmux.env={AGENTMUX_PANE="senior-coder", AGENTMUX_SESSION="test-session"}' -c 'mcp_servers.agentmux.tools.kv_set.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.kv_get.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.wait.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.scale.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.send.approval_mode="approve"' "$(cat 'coder.md')

$(agentmux prompt --provider 'codex' --mode 'native-mcp')"`,
		},
		{
			name:      "codex native mcp with reasoning effort",
			provider:  "codex",
			paneName:  "senior-coder",
			model:     "gpt-5-codex",
			reasoning: "high",
			want:      `codex --no-alt-screen --sandbox workspace-write --ask-for-approval never --model 'gpt-5-codex' -c 'mcp_servers.agentmux.command="agentmux"' -c 'mcp_servers.agentmux.args=["mcp-server"]' -c 'mcp_servers.agentmux.env={AGENTMUX_PANE="senior-coder", AGENTMUX_SESSION="test-session"}' -c 'mcp_servers.agentmux.tools.kv_set.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.kv_get.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.wait.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.scale.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.send.approval_mode="approve"' -c 'model_reasoning_effort=high' "$(agentmux prompt --provider 'codex' --mode 'native-mcp')"`,
		},
		{
			name:     "gptme cli bridge with model and persona",
			provider: "gptme",
			persona:  "review.md",
			model:    "gpt-4o",
			want: `gptme "$(cat 'review.md')

$(agentmux prompt --provider 'gptme' --mode 'cli-bridge')" --model 'gpt-4o'`,
		},
		{
			name:     "unknown provider",
			provider: "bash",
			want:     "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pane := resolve.Pane{
				Name:            tc.paneName,
				Provider:        tc.provider,
				Persona:         tc.persona,
				Model:           tc.model,
				ReasoningEffort: tc.reasoning,
			}

			got := compileProviderCommand("test-session", pane)
			if got != tc.want {
				t.Errorf("\ngot:  %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestCompileProviderCommandFallsBackToCLIBridge(t *testing.T) {
	originalLookPath := providerLookPath
	originalRun := providerRun
	t.Cleanup(func() {
		providerLookPath = originalLookPath
		providerRun = originalRun
	})

	providerLookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}
	providerRun = func(path string, args ...string) (string, error) {
		switch {
		case strings.Contains(path, "claude"):
			return "--append-system-prompt\n--model\n--effort\n", nil
		case strings.Contains(path, "codex"):
			return "--model\n", nil
		case strings.Contains(path, "gemini"):
			return "--model\n--prompt-interactive\n", nil
		default:
			return "", nil
		}
	}

	claude := compileProviderCommand("test-session", resolve.Pane{Provider: "claude"})
	if got, want := claude, `claude --append-system-prompt "$(agentmux prompt --provider 'claude' --mode 'cli-bridge')"`; got != want {
		t.Fatalf("claude fallback command = %q, want %q", got, want)
	}

	codex := compileProviderCommand("test-session", resolve.Pane{Provider: "codex"})
	if got, want := codex, `codex --no-alt-screen --sandbox workspace-write --ask-for-approval never "$(agentmux prompt --provider 'codex' --mode 'cli-bridge')"`; got != want {
		t.Fatalf("codex fallback command = %q, want %q", got, want)
	}

	gemini := compileProviderCommand("test-session", resolve.Pane{Provider: "gemini"})
	if got, want := gemini, `gemini --prompt-interactive "$(agentmux prompt --provider 'gemini' --mode 'cli-bridge')"`; got != want {
		t.Fatalf("gemini fallback command = %q, want %q", got, want)
	}
}
