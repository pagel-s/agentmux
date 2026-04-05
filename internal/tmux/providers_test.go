package tmux

import (
	"agentmux/internal/resolve"
	"strings"
	"testing"
)

func TestCompileProviderCommand(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		persona  string
		model    string
		want     string
	}{
		{
			name:     "claude with persona and model",
			provider: "claude",
			persona:  "arch.md",
			model:    "claude-3-5-sonnet",
			want: `claude --system-prompt "$(cat arch.md)" --append-system-prompt "$(agentmux prompt)

Available Coordination Tools:
$(agentmux tools)" --model 'claude-3-5-sonnet'`,
		},
		{
			name:     "gemini no persona",
			provider: "gemini",
			persona:  "",
			want: `gemini "$(agentmux prompt)

Available Coordination Tools:
$(agentmux tools)"`,
		},
		{
			name:     "openai (codex) with persona",
			provider: "openai",
			persona:  "coder.md",
			want: `codex "$(cat coder.md)

$(agentmux prompt)

Available Coordination Tools:
$(agentmux tools)"`,
		},
		{
			name:     "gptme with model",
			provider: "gptme",
			model:    "gpt-4o",
			want:     `gptme "$(agentmux prompt)" --model 'gpt-4o'`,
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
				Provider: tc.provider,
				Persona:  tc.persona,
				Model:    tc.model,
			}
			
			got := compileProviderCommand("test-session", pane)
			
			// Simple exact match check. We might need to adjust quotes depending on how singleQuote handles it, 
			// but compileProviderCommand mostly uses double quotes around the raw string except for singleQuote(pane.Persona)
			
			// Let's manually construct what we expect with singleQuotes correctly evaluated if needed
			// Wait, compileClaudeCommand uses singleQuote(pane.Persona). So "arch.md" becomes "'arch.md'"
			want := tc.want
			if tc.persona != "" {
				want = strings.ReplaceAll(want, tc.persona, singleQuote(tc.persona))
			}
			
			if got != want {
				t.Errorf("\ngot:  %q\nwant: %q", got, want)
			}
		})
	}
}
