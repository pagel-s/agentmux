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
				Panes: []config.Pane{
					{
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
	if pane.Title != "Reviewer" {
		t.Fatalf("pane.Title = %q, want %q", pane.Title, "Reviewer")
	}
	if pane.Env["GLOBAL"] != "1" {
		t.Fatalf("pane.Env[GLOBAL] = %q, want %q", pane.Env["GLOBAL"], "1")
	}
	if pane.Env["MODEL"] != "local-model" {
		t.Fatalf("pane.Env[MODEL] = %q, want %q", pane.Env["MODEL"], "local-model")
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
