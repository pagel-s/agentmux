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

	doc := &config.Document{
		Path:   "/tmp/project/config.yaml",
		Config: *cfg,
	}

	resolved, err := Project(doc, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	if got := resolved.Params["config_dir"]; got != "/tmp/project" {
		t.Fatalf("config_dir = %q, want %q", got, "/tmp/project")
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

	doc := &config.Document{
		Path:   "/tmp/project/config.yaml",
		Config: *cfg,
	}

	resolved, err := Project(doc, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	if got := resolved.Params["config_dir"]; got != "/tmp/project" {
		t.Fatalf("config_dir = %q, want %q", got, "/tmp/project")
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

	doc := &config.Document{
		Path:   "/tmp/project/config.yaml",
		Config: *cfg,
	}

	resolved, err := Project(doc, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	if got := resolved.Params["config_dir"]; got != "/tmp/project" {
		t.Fatalf("config_dir = %q, want %q", got, "/tmp/project")
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

	doc := &config.Document{
		Path:   "/tmp/project/config.yaml",
		Config: *cfg,
	}

	resolved, err := Project(doc, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	if got := resolved.Params["config_dir"]; got != "/tmp/project" {
		t.Fatalf("config_dir = %q, want %q", got, "/tmp/project")
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

	doc := &config.Document{
		Path:   "/tmp/project/config.yaml",
		Config: *cfg,
	}

	resolved, err := Project(doc, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	if got := resolved.Params["config_dir"]; got != "/tmp/project" {
		t.Fatalf("config_dir = %q, want %q", got, "/tmp/project")
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

	doc := &config.Document{
		Path:   "/tmp/project/config.yaml",
		Config: *cfg,
	}

	resolved, err := Project(doc, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	if got := resolved.Params["config_dir"]; got != "/tmp/project" {
		t.Fatalf("config_dir = %q, want %q", got, "/tmp/project")
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

func TestProjectImplicitRequirements(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Session: "demo",
		Agents: map[string]config.Agent{
			"ai": {
				Provider: "claude-cli",
			},
			"ops": {
				Provider: "ssh",
				Command:  "ssh prod",
			},
		},
		Layout: []config.Window{
			{
				Window: "main",
				Panes: []config.Pane{
					{
						Agent: "ai",
					},
					{
						Provider: "gemini",
					},
					{
						Agent: "ops",
					},
				},
			},
		},
	}

	doc := &config.Document{
		Path:   "/tmp/project/config.yaml",
		Config: *cfg,
	}

	resolved, err := Project(doc, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	bins := resolved.Requires.Binaries
	seenClaude := false
	seenGemini := false
	seenSSH := false
	for _, b := range bins {
		if b == "claude" {
			seenClaude = true
		}
		if b == "gemini" {
			seenGemini = true
		}
		if b == "ssh" {
			seenSSH = true
		}
	}

	if !seenClaude {
		t.Errorf("missing implicit requirement: claude")
	}
	if !seenGemini {
		t.Errorf("missing implicit requirement: gemini")
	}
	if !seenSSH {
		t.Errorf("missing implicit requirement: ssh")
	}
}

func TestProjectPaneOverridesAgentModelAndPersona(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Session: "demo",
		Agents: map[string]config.Agent{
			"coder": {
				Provider:        "openai",
				Persona:         "personas/base.md",
				Model:           "gpt-5",
				ReasoningEffort: "medium",
			},
		},
		Layout: []config.Window{
			{
				Window: "main",
				Panes: []config.Pane{
					{
						Agent:           "coder",
						Persona:         "personas/review.md",
						Model:           "gpt-5-codex",
						ReasoningEffort: "high",
					},
				},
			},
		},
	}

	doc := &config.Document{
		Path:   "/tmp/project/config.yaml",
		Config: *cfg,
	}

	resolved, err := Project(doc, nil)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}

	pane := resolved.Windows[0].Panes[0]
	if got := pane.Provider; got != "codex" {
		t.Fatalf("pane.Provider = %q, want %q", got, "codex")
	}
	if got := pane.Model; got != "gpt-5-codex" {
		t.Fatalf("pane.Model = %q, want %q", got, "gpt-5-codex")
	}
	if got := pane.Persona; got != "/tmp/project/personas/review.md" {
		t.Fatalf("pane.Persona = %q, want %q", got, "/tmp/project/personas/review.md")
	}
	if got := pane.ReasoningEffort; got != "high" {
		t.Fatalf("pane.ReasoningEffort = %q, want %q", got, "high")
	}
}
