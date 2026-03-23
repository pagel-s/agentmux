package resolve

import (
	"os"
	"testing"

	"agentmux/internal/config"
)

func TestProjectResolvesEnvAndCwdPrecedence(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Session: "demo",
		Params: map[string]string{
			"project_dir": ".",
		},
		Env: map[string]string{
			"GLOBAL": "1",
			"MODEL":  "gpt-5",
		},
		Agents: map[string]config.Agent{
			"reviewer": {
				Provider: "command",
				Command:  "review",
				Cwd:      "{{project_dir}}",
				Env: map[string]string{
					"MODEL": "gpt-5-mini",
				},
				Title: "Reviewer",
			},
		},
		Layout: []config.Window{
			{
				Window: "main",
				Root:   "/tmp/workspace",
				Env: map[string]string{
					"MODEL":       "gpt-5-window",
					"WINDOW_ONLY": "1",
				},
				Panes: []config.Pane{
					{
						Name:  "reviewer-main",
						Agent: "reviewer",
						Cwd:   "/tmp/pane",
						Env: map[string]string{
							"MODEL": "local-model",
						},
					},
				},
			},
		},
	}

	resolved, err := Project(cfg, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	pane := resolved.Windows[0].Panes[0]
	if pane.Cwd != "/tmp/pane" {
		t.Fatalf("pane.Cwd = %q, want %q", pane.Cwd, "/tmp/pane")
	}
	if pane.Name != "reviewer-main" {
		t.Fatalf("pane.Name = %q, want %q", pane.Name, "reviewer-main")
	}
	if pane.Title != "Reviewer" {
		t.Fatalf("pane.Title = %q, want %q", pane.Title, "Reviewer")
	}
	if pane.Env["GLOBAL"] != "1" {
		t.Fatalf("pane.Env[GLOBAL] = %q, want %q", pane.Env["GLOBAL"], "1")
	}
	if pane.Env["MODEL"] != "local-model" {
		t.Fatalf("pane.Env[MODEL] = %q, want %q", pane.Env["MODEL"], "local-model")
	}
	if pane.Env["WINDOW_ONLY"] != "1" {
		t.Fatalf("pane.Env[WINDOW_ONLY] = %q, want %q", pane.Env["WINDOW_ONLY"], "1")
	}
	if resolved.Windows[0].Env["MODEL"] != "gpt-5-window" {
		t.Fatalf("window env MODEL = %q, want %q", resolved.Windows[0].Env["MODEL"], "gpt-5-window")
	}
}

func TestProjectResolvesPaneNameTemplates(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Session: "demo",
		Params: map[string]string{
			"role": "review",
		},
		Layout: []config.Window{
			{
				Window: "main",
				Panes: []config.Pane{
					{
						Name:    "{{role}}-pane",
						Command: "pwd",
					},
				},
			},
		},
	}

	resolved, err := Project(cfg, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	if got := resolved.Windows[0].Panes[0].Name; got != "review-pane" {
		t.Fatalf("pane name = %q, want %q", got, "review-pane")
	}
	if got := resolved.Windows[0].Panes[0].Title; got != "review-pane" {
		t.Fatalf("pane title = %q, want %q", got, "review-pane")
	}
}

func TestProjectWindowEnvOverridesAgentAndSessionDefaults(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Session: "demo",
		Env: map[string]string{
			"MODEL": "gpt-5",
		},
		Agents: map[string]config.Agent{
			"reviewer": {
				Provider: "command",
				Command:  "review",
				Env: map[string]string{
					"MODEL":  "gpt-5-mini",
					"AGENT":  "1",
					"SHARED": "agent",
				},
			},
		},
		Layout: []config.Window{
			{
				Window: "main",
				Env: map[string]string{
					"MODEL":  "gpt-5-window",
					"SHARED": "window",
					"WIN":    "1",
				},
				Panes: []config.Pane{
					{
						Agent: "reviewer",
					},
				},
			},
		},
	}

	resolved, err := Project(cfg, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	pane := resolved.Windows[0].Panes[0]
	if got := pane.Env["MODEL"]; got != "gpt-5-window" {
		t.Fatalf("pane.Env[MODEL] = %q, want %q", got, "gpt-5-window")
	}
	if got := pane.Env["SHARED"]; got != "window" {
		t.Fatalf("pane.Env[SHARED] = %q, want %q", got, "window")
	}
	if got := pane.Env["AGENT"]; got != "1" {
		t.Fatalf("pane.Env[AGENT] = %q, want %q", got, "1")
	}
	if got := pane.Env["WIN"]; got != "1" {
		t.Fatalf("pane.Env[WIN] = %q, want %q", got, "1")
	}
}

func TestProjectHandlesPaneEnvWithoutSessionEnv(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Session: "demo",
		Layout: []config.Window{
			{
				Window: "main",
				Panes: []config.Pane{
					{
						Command: "env",
						Env: map[string]string{
							"DEBUG": "1",
						},
					},
				},
			},
		},
	}

	resolved, err := Project(cfg, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	if got := resolved.Windows[0].Panes[0].Env["DEBUG"]; got != "1" {
		t.Fatalf("pane env DEBUG = %q, want %q", got, "1")
	}
	if got := resolved.Windows[0].Panes[0].OnExit; got != config.OnExitShell {
		t.Fatalf("pane on_exit = %q, want %q", got, config.OnExitShell)
	}
}

func TestProjectResolvesRelativeCwdAgainstLaunchDirectory(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}

	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Chdir returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	cfg := &config.Config{
		Version: 1,
		Session: "demo",
		Agents: map[string]config.Agent{
			"coder": {
				Provider: "command",
				Command:  "echo hi",
				Cwd:      ".",
			},
		},
		Layout: []config.Window{
			{
				Window: "main",
				Root:   ".",
				Panes: []config.Pane{
					{
						Agent: "coder",
					},
				},
			},
		},
	}

	resolved, err := Project(cfg, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	if got := resolved.Windows[0].Root; got != tempDir {
		t.Fatalf("window root = %q, want %q", got, tempDir)
	}
	if got := resolved.Windows[0].Panes[0].Cwd; got != tempDir {
		t.Fatalf("pane cwd = %q, want %q", got, tempDir)
	}
}

func TestProjectResolvesOnExitPrecedence(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Session: "demo",
		Agents: map[string]config.Agent{
			"reviewer": {
				Provider: "command",
				Command:  "review",
				OnExit:   "restart",
			},
		},
		Layout: []config.Window{
			{
				Window: "main",
				Panes: []config.Pane{
					{
						Name:  "agent-default",
						Agent: "reviewer",
					},
					{
						Name:   "pane-override",
						Agent:  "reviewer",
						OnExit: "remain",
					},
					{
						Name:    "command-default",
						Command: "pwd",
					},
				},
			},
		},
	}

	resolved, err := Project(cfg, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	panes := resolved.Windows[0].Panes
	if got := panes[0].OnExit; got != config.OnExitRestart {
		t.Fatalf("agent-default on_exit = %q, want %q", got, config.OnExitRestart)
	}
	if got := panes[1].OnExit; got != config.OnExitRemain {
		t.Fatalf("pane-override on_exit = %q, want %q", got, config.OnExitRemain)
	}
	if got := panes[2].OnExit; got != config.OnExitShell {
		t.Fatalf("command-default on_exit = %q, want %q", got, config.OnExitShell)
	}
}
