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

func TestParseWaitScopeDefaultsToOutput(t *testing.T) {
	scope, err := ParseWaitScope("")
	if err != nil {
		t.Fatalf("ParseWaitScope returned error: %v", err)
	}
	if scope != WaitScopeOutput {
		t.Fatalf("scope = %q, want %q", scope, WaitScopeOutput)
	}

	if _, err := ParseWaitScope("bogus"); err == nil {
		t.Fatal("ParseWaitScope(bogus) returned nil error, want validation failure")
	}
}

func TestCompileWaitPatternUsesMultilineAnchors(t *testing.T) {
	re, err := compileWaitPattern("^ARCH_DONE$")
	if err != nil {
		t.Fatalf("compileWaitPattern returned error: %v", err)
	}
	if match := re.FindString("before\nARCH_DONE\nafter\n"); match != "ARCH_DONE" {
		t.Fatalf("FindString returned %q, want %q", match, "ARCH_DONE")
	}
}

func TestStripActiveInputBlockRemovesCodexComposer(t *testing.T) {
	content := strings.Join([]string{
		"• Prior output",
		"",
		"› Read blackboard key \"probe\" and print exactly TOOL_OK:ok-42",
		"",
		"  gpt-5-codex high · 98% left · ~/code/agentmux",
	}, "\n") + "\n"

	filtered, stripped := stripActiveInputBlock(content, PaneStatus{Provider: "codex"}, paneCursorInfo{Visible: true, Y: 4})
	if !stripped {
		t.Fatal("stripActiveInputBlock reported no strip, want active composer removed")
	}
	if strings.Contains(filtered, "Read blackboard key") {
		t.Fatalf("filtered content still includes active Codex composer: %q", filtered)
	}
	if !strings.Contains(filtered, "Prior output") {
		t.Fatalf("filtered content removed prior output unexpectedly: %q", filtered)
	}
}

func TestIsAmbiguousInteractiveShellCapture(t *testing.T) {
	content := "echo WAIT_FALSE_POSITIVE\n\n\n"
	if !isAmbiguousInteractiveShellCapture(content, PaneStatus{CurrentCmd: "bash"}, paneCursorInfo{Visible: true, Y: 1}) {
		t.Fatal("queued shell input should be treated as ambiguous visible text")
	}

	executed := "echo WAIT_FALSE_POSITIVE\nWAIT_FALSE_POSITIVE\n\n"
	if isAmbiguousInteractiveShellCapture(executed, PaneStatus{CurrentCmd: "bash"}, paneCursorInfo{Visible: true, Y: 2}) {
		t.Fatal("executed shell output should not be treated as ambiguous")
	}
}

func TestStripActiveInputBlockKeepsShellOutputBeforeFreshPrompt(t *testing.T) {
	content := strings.Join([]string{
		"$ echo WAIT_FALSE_POSITIVE",
		"WAIT_FALSE_POSITIVE",
		"$",
	}, "\n") + "\n"

	filtered, stripped := stripActiveInputBlock(content, PaneStatus{CurrentCmd: "bash"}, paneCursorInfo{Visible: true, Y: 2})
	if !stripped {
		t.Fatal("stripActiveInputBlock reported no strip, want fresh prompt removed")
	}
	if !strings.Contains(filtered, "WAIT_FALSE_POSITIVE") {
		t.Fatalf("filtered content removed shell output unexpectedly: %q", filtered)
	}
	if strings.HasSuffix(strings.TrimSpace(filtered), "$") {
		t.Fatalf("filtered content still includes the fresh prompt: %q", filtered)
	}
}
