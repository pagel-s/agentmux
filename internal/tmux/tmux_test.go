package tmux

import (
	"strings"
	"testing"

	"agentmux/internal/resolve"
)

func TestBuildPaneCommand(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")

	command := buildPaneCommand(resolve.Pane{
		Command: "echo ${OPENAI_API_KEY}",
		Cwd:     "/tmp/project",
		Env: map[string]string{
			"OPENAI_API_KEY": "${OPENAI_API_KEY}",
			"MODEL":          "gpt-5",
		},
		Shell: "/bin/bash",
	})

	if !strings.Contains(command, "cd '/tmp/project'") {
		t.Fatalf("command missing cwd change: %q", command)
	}
	if !strings.Contains(command, `OPENAI_API_KEY="${OPENAI_API_KEY}"`) {
		t.Fatalf("command missing passthrough env: %q", command)
	}
	if !strings.Contains(command, `MODEL='gpt-5'`) {
		t.Fatalf("command missing quoted literal env: %q", command)
	}
	if !strings.Contains(command, `'/bin/bash' -lc 'echo ${OPENAI_API_KEY}'`) {
		t.Fatalf("command missing shell wrapper: %q", command)
	}
	if !strings.Contains(command, `; exec '/bin/bash'`) {
		t.Fatalf("command missing fallback interactive shell: %q", command)
	}
}

func TestQuoteEnvValueDoesNotExpandNonVariableDollarText(t *testing.T) {
	quoted := quoteEnvValue("price$5")

	if quoted != `'price$5'` {
		t.Fatalf("quoteEnvValue = %q, want single-quoted literal", quoted)
	}
}
