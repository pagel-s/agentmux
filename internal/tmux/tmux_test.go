package tmux

import (
	"strings"
	"testing"

	"agentmux/internal/config"
	"agentmux/internal/resolve"
)

func TestBuildPaneCommand(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")

	command := buildPaneCommand("test-session", resolve.Pane{
		Command: "echo ${OPENAI_API_KEY}",
		Cwd:     "/tmp/project",
		Env: map[string]string{
			"OPENAI_API_KEY": "${OPENAI_API_KEY}",
			"MODEL":          "gpt-5",
		},
		Shell:  "/bin/bash",
		OnExit: config.OnExitShell,
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

func TestBuildPaneCommandRemainOmitsFallbackShell(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")

	command := buildPaneCommand("test-session", resolve.Pane{
		Command: "printf 'done\\n'",
		Cwd:     "/tmp/project",
		OnExit:  config.OnExitRemain,
	})

	if strings.Contains(command, "; exec ") {
		t.Fatalf("remain command should not include fallback shell: %q", command)
	}
	if !strings.Contains(command, "printf 'done\\n'") {
		t.Fatalf("remain command missing original command: %q", command)
	}
}

func TestBuildPaneCommandRestartWrapsCommandInLoop(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")

	command := buildPaneCommand("test-session", resolve.Pane{
		Command: "printf 'done\\n'",
		OnExit:  config.OnExitRestart,
	})

	for _, want := range []string{
		"while :; do",
		"restarting in 1s",
		"sleep 1",
		"; exec '/bin/bash'",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("restart command %q does not include %q", command, want)
		}
	}
}

func TestQuoteEnvValueDoesNotExpandNonVariableDollarText(t *testing.T) {
	quoted := quoteEnvValue("price$5")

	if quoted != `'price$5'` {
		t.Fatalf("quoteEnvValue = %q, want single-quoted literal", quoted)
	}
}

func TestTailLinesFromCapture(t *testing.T) {
	content := "line-1\nline-2\nline-3\n"

	if got := tailLinesFromCapture(content, 2); got != "line-2\nline-3\n" {
		t.Fatalf("tailLinesFromCapture(..., 2) = %q, want %q", got, "line-2\nline-3\n")
	}
	if got := tailLinesFromCapture(content, 5); got != content {
		t.Fatalf("tailLinesFromCapture(..., 5) = %q, want original content", got)
	}
	if got := tailLinesFromCapture(content, 0); got != content {
		t.Fatalf("tailLinesFromCapture(..., 0) = %q, want original content", got)
	}
}

func TestTailLinesFromCaptureDropsTrailingBlankRows(t *testing.T) {
	content := "line-1\nline-2\n\n\n"

	if got := tailLinesFromCapture(content, 1); got != "line-2\n" {
		t.Fatalf("tailLinesFromCapture(..., 1) = %q, want %q", got, "line-2\n")
	}
}
