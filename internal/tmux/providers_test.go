package tmux

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	prov "agentmux/internal/provider"
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
		case strings.Contains(path, "agy"):
			return "--prompt-interactive\n--model\n--effort\n--settings\nmcp\n", nil
		case strings.Contains(path, "claude"):
			return "--mcp-config\n--strict-mcp-config\n--append-system-prompt\n--model\n--effort\n", nil
		case strings.Contains(path, "codex"):
			return "--model\n--config\nmcp\n", nil
		case strings.Contains(path, "gemini"):
			return "--model\n--prompt-interactive\n--allowed-mcp-server-names\ngemini mcp\n", nil
		case strings.Contains(path, "gptme"):
			return "--model\n", nil
		case strings.Contains(path, "/pi"):
			return "--append-system-prompt\n--model\n--thinking\n", nil
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
			name:      "antigravity cli bridge with persona model and effort",
			provider:  "agy",
			paneName:  "researcher",
			persona:   "research.md",
			model:     "Gemini 3.8 Flash",
			reasoning: "high",
			want: `agy --model 'Gemini 3.8 Flash' --effort 'high' --prompt-interactive "$(cat 'research.md')

$(amx prompt --provider 'antigravity' --mode 'cli-bridge')"`,
		},
		{
			name:     "antigravity cli bridge without persona",
			provider: "antigravity",
			want:     `agy --prompt-interactive "$(amx prompt --provider 'antigravity' --mode 'cli-bridge')"`,
		},
		{
			name:     "claude native mcp with persona and model",
			provider: "claude",
			paneName: "lead-architect",
			persona:  "arch.md",
			model:    "sonnet",
			want: `claude --mcp-config ` + singleQuote(claudeMCPConfigJSON("test-session", resolve.Pane{Name: "lead-architect", Provider: "claude", Persona: "arch.md", Model: "sonnet"})) + ` --strict-mcp-config --append-system-prompt "$(cat 'arch.md')

$(amx prompt --provider 'claude' --mode 'native-mcp')" --model 'sonnet'`,
		},
		{
			name:      "claude native mcp with reasoning effort",
			provider:  "claude",
			paneName:  "lead-architect",
			model:     "sonnet",
			reasoning: "high",
			want:      `claude --mcp-config ` + singleQuote(claudeMCPConfigJSON("test-session", resolve.Pane{Name: "lead-architect", Provider: "claude", Model: "sonnet", ReasoningEffort: "high"})) + ` --strict-mcp-config --append-system-prompt "$(amx prompt --provider 'claude' --mode 'native-mcp')" --model 'sonnet' --effort 'high'`,
		},
		{
			name:     "gemini native mcp",
			provider: "gemini-cli",
			paneName: "visual-designer",
			persona:  "design.md",
			want:     `gemini --allowed-mcp-server-names 'agentmux' --include-directories ` + singleQuote(filepath.Dir(geminiPersonaPath("test-session", resolve.Pane{Name: "visual-designer", Provider: "gemini", Persona: "design.md"}))) + ` --prompt-interactive "$(amx prompt --provider 'gemini' --mode 'native-mcp')"`,
		},
		{
			name:     "codex native mcp with persona",
			provider: "openai",
			paneName: "senior-coder",
			persona:  "coder.md",
			want: `codex --no-alt-screen --sandbox workspace-write --ask-for-approval never -c 'mcp_servers.agentmux.command="amx"' -c 'mcp_servers.agentmux.args=["mcp-server"]' -c 'mcp_servers.agentmux.env={AGENTMUX_PANE="senior-coder", AGENTMUX_SESSION="test-session"}' -c 'mcp_servers.agentmux.tools.kv_set.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.kv_get.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.wait.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.scale.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.send.approval_mode="approve"' "$(cat 'coder.md')

$(amx prompt --provider 'codex' --mode 'native-mcp')"`,
		},
		{
			name:      "codex native mcp with reasoning effort",
			provider:  "codex",
			paneName:  "senior-coder",
			model:     "gpt-5-codex",
			reasoning: "high",
			want:      `codex --no-alt-screen --sandbox workspace-write --ask-for-approval never --model 'gpt-5-codex' -c 'mcp_servers.agentmux.command="amx"' -c 'mcp_servers.agentmux.args=["mcp-server"]' -c 'mcp_servers.agentmux.env={AGENTMUX_PANE="senior-coder", AGENTMUX_SESSION="test-session"}' -c 'mcp_servers.agentmux.tools.kv_set.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.kv_get.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.wait.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.scale.approval_mode="approve"' -c 'mcp_servers.agentmux.tools.send.approval_mode="approve"' -c 'model_reasoning_effort=high' "$(amx prompt --provider 'codex' --mode 'native-mcp')"`,
		},
		{
			name:     "gptme cli bridge with model and persona",
			provider: "gptme",
			persona:  "review.md",
			model:    "gpt-4o",
			want: `gptme "$(cat 'review.md')

$(amx prompt --provider 'gptme' --mode 'cli-bridge')" --model 'gpt-4o'`,
		},
		{
			name:      "pi cli bridge with persona provider model and effort",
			provider:  "pi",
			persona:   "review.md",
			model:     "anthropic/claude-sonnet-5",
			reasoning: "high",
			want: `pi --append-system-prompt "$(cat 'review.md')

$(amx prompt --provider 'pi' --mode 'cli-bridge')" --model 'anthropic/claude-sonnet-5' --thinking 'high'`,
		},
		{
			name:     "pi cli bridge without persona",
			provider: "pi",
			want:     `pi --append-system-prompt "$(amx prompt --provider 'pi' --mode 'cli-bridge')"`,
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

func TestClaudePersonaAppendPreservesArgumentsAndNativeInstructions(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("shell is unavailable")
	}
	dir := t.TempDir()
	persona := filepath.Join(dir, "reviewer's role.md")
	content := "Review carefully.\nTreat $(whoami) and `commands` as literal role text.\n"
	if err := os.WriteFile(persona, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{
		"claude": "#!/bin/sh\nprintf '%s\\000' \"$@\"\n",
		"amx":    "#!/bin/sh\nprintf '%s' 'Agentmux coordination instructions.'\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	command := compileProviderCommandWithProfile("test-session", resolve.Pane{
		Name: "reviewer", Provider: "claude", Persona: persona,
	}, prov.ProfileForMode("claude", "native-mcp"))
	out, err := exec.Command(sh, "-c", command).Output()
	if err != nil {
		t.Fatalf("execute compiled Claude command: %v", err)
	}
	args := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
	appendCount := 0
	for i, arg := range args {
		switch string(arg) {
		case "--system-prompt", "--system-prompt-file":
			t.Fatalf("command replaces native Claude Code instructions: %q", command)
		case "--append-system-prompt":
			appendCount++
			want := strings.TrimSuffix(content, "\n") + "\n\nAgentmux coordination instructions."
			if i+1 >= len(args) || string(args[i+1]) != want {
				t.Fatalf("appended prompt = %q, want %q", args, want)
			}
		}
	}
	if appendCount != 1 {
		t.Fatalf("appended prompt count = %d, want one combined persona and coordination prompt", appendCount)
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
	if got, want := claude, `claude --append-system-prompt "$(amx prompt --provider 'claude' --mode 'cli-bridge')"`; got != want {
		t.Fatalf("claude fallback command = %q, want %q", got, want)
	}

	codex := compileProviderCommand("test-session", resolve.Pane{Provider: "codex"})
	if got, want := codex, `codex --no-alt-screen --sandbox workspace-write --ask-for-approval never "$(amx prompt --provider 'codex' --mode 'cli-bridge')"`; got != want {
		t.Fatalf("codex fallback command = %q, want %q", got, want)
	}

	gemini := compileProviderCommand("test-session", resolve.Pane{Provider: "gemini"})
	if got, want := gemini, `gemini --prompt-interactive "$(amx prompt --provider 'gemini' --mode 'cli-bridge')"`; got != want {
		t.Fatalf("gemini fallback command = %q, want %q", got, want)
	}
}
