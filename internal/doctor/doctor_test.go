package doctor

import (
	"errors"
	"strings"
	"testing"

	"agentmux/internal/provider"
	"agentmux/internal/resolve"
	"agentmux/internal/scaffold"
	"agentmux/internal/validate"
)

func TestCollectEnvironmentAndDiagnostics(t *testing.T) {
	report := Collect(Input{
		AgentmuxVersion: "agentmux test",
	}, Probes{
		Getenv: func(key string) string {
			if key == "SHELL" {
				return ""
			}
			return ""
		},
		Getwd: func() (string, error) {
			return "/tmp/work", nil
		},
		LookPath: func(name string) (string, error) {
			switch name {
			case "go":
				return "/usr/bin/go", nil
			case "tmux":
				return "/usr/bin/tmux", nil
			case "amx":
				return "", errors.New("missing")
			default:
				return "", errors.New("missing")
			}
		},
		RunOutput: func(path string, args ...string) (string, error) {
			switch path {
			case "/usr/bin/go":
				return "go version go1.18.1 linux/amd64\n", nil
			case "/usr/bin/tmux":
				return "tmux 3.1c\n", nil
			default:
				return "", errors.New("unexpected")
			}
		},
		ListTemplates: func() ([]scaffold.Template, error) {
			return []scaffold.Template{
				{Name: "repo", Source: scaffold.SourceRepo},
				{Name: "global", Source: scaffold.SourceGlobal},
			}, nil
		},
		TMuxBinary: "tmux",
		TMuxSocket: "/tmp/agentmux.sock",
	})

	if report.Environment.Shell != "/bin/sh" {
		t.Fatalf("Shell = %q, want %q", report.Environment.Shell, "/bin/sh")
	}
	if report.Environment.Cwd != "/tmp/work" {
		t.Fatalf("Cwd = %q, want %q", report.Environment.Cwd, "/tmp/work")
	}
	if report.Environment.ProjectTemplates != 1 || report.Environment.GlobalTemplates != 1 {
		t.Fatalf("template counts = (%d, %d), want (1, 1)", report.Environment.ProjectTemplates, report.Environment.GlobalTemplates)
	}
	if len(report.Diagnostics) != 3 {
		t.Fatalf("len(Diagnostics) = %d, want 3", len(report.Diagnostics))
	}
	if report.Diagnostics[0].Status != StatusWarning || !strings.Contains(report.Diagnostics[0].Message, "tmux version") {
		t.Fatalf("Diagnostics[0] = %#v, want tmux warning", report.Diagnostics[0])
	}
	if report.Diagnostics[1].Status != StatusWarning || !strings.Contains(report.Diagnostics[1].Message, "Go version") {
		t.Fatalf("Diagnostics[1] = %#v, want go warning", report.Diagnostics[1])
	}
	if report.Diagnostics[2].Status != StatusWarning || !strings.Contains(report.Diagnostics[2].Message, "amx is not in your PATH") {
		t.Fatalf("Diagnostics[2] = %#v, want PATH warning", report.Diagnostics[2])
	}
}

func TestCollectConfigReportAndProviderDiagnostics(t *testing.T) {
	report := Collect(Input{
		AgentmuxVersion: "agentmux test",
		ConfigPath:      "team.yaml",
		Resolved: &resolve.Config{
			Session: "demo",
			Agents: map[string]resolve.Agent{
				"coder": {
					Provider: "codex",
					Model:    "gpt-4o",
				},
			},
			Windows: []resolve.Window{
				{
					Name: "main",
					Panes: []resolve.Pane{
						{Provider: "gemini", Model: "gemini-1.5-pro"},
						{Provider: "command"},
					},
				},
			},
		},
		Issues: []validate.Issue{
			{Severity: validate.SeverityWarning, Message: `pane 1 in window "main" uses provider alias "openai"; prefer "codex"`},
		},
	}, Probes{
		Getenv: func(string) string { return "/bin/bash" },
		Getwd:  func() (string, error) { return "/repo", nil },
		LookPath: func(name string) (string, error) {
			switch name {
			case "go":
				return "/usr/bin/go", nil
			case "tmux":
				return "/usr/bin/tmux", nil
			case "amx":
				return "/usr/local/bin/agentmux", nil
			case "codex":
				return "/usr/local/bin/codex", nil
			case "gemini":
				return "/usr/local/bin/gemini", nil
			default:
				return "", errors.New("missing")
			}
		},
		RunOutput: func(path string, args ...string) (string, error) {
			switch path {
			case "/usr/bin/go":
				return "go version go1.24.1 linux/amd64\n", nil
			case "/usr/bin/tmux":
				return "tmux 3.4\n", nil
			case "/usr/local/bin/codex":
				return "--model\n", nil
			case "/usr/local/bin/gemini":
				return "--model\n--prompt-interactive\n", nil
			default:
				return "", errors.New("unexpected")
			}
		},
		ListTemplates: func() ([]scaffold.Template, error) { return nil, nil },
		TMuxBinary:    "tmux",
	})

	if report.Config == nil {
		t.Fatal("Config = nil, want config report")
	}
	if got := report.Config.Warnings; len(got) != 1 || !strings.Contains(got[0], "provider alias") {
		t.Fatalf("Warnings = %#v, want alias warning", got)
	}
	if report.Config.Requirements.HasMissing() {
		t.Fatalf("Requirements = %#v, want none missing", report.Config.Requirements)
	}
	if len(report.Config.Providers) != 2 {
		t.Fatalf("len(Providers) = %d, want 2", len(report.Config.Providers))
	}
	if report.Config.Providers[0].Usage.Name != "codex" || report.Config.Providers[1].Usage.Name != "gemini" {
		t.Fatalf("Providers = %#v, want sorted codex/gemini", report.Config.Providers)
	}
	if len(report.Config.Providers[0].ModelWarnings) != 1 || !strings.Contains(report.Config.Providers[0].ModelWarnings[0], "gpt-4o") {
		t.Fatalf("codex ModelWarnings = %#v, want gpt-4o warning", report.Config.Providers[0].ModelWarnings)
	}
	found := false
	for _, issue := range report.Config.Providers[1].Health.Issues {
		if issue.Severity == "error" && strings.Contains(issue.Message, "--allowed-mcp-server-names") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("gemini Health.Issues = %#v, want missing --allowed-mcp-server-names error", report.Config.Providers[1].Health.Issues)
	}
}

func TestCollectProviderUsagesDedupesAndSorts(t *testing.T) {
	usages := collectProviderUsages(resolve.Config{
		Agents: map[string]resolve.Agent{
			"coder": {Provider: "openai", Model: "gpt-5-codex"},
			"chat":  {Provider: "command"},
		},
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{Provider: "codex", Model: "gpt-5-codex"},
					{Provider: "gemini-cli", Model: "gemini-2.5-pro"},
				},
			},
		},
	})

	if len(usages) != 2 {
		t.Fatalf("len(usages) = %d, want 2", len(usages))
	}
	if usages[0].Name != "codex" || usages[1].Name != "gemini" {
		t.Fatalf("usages = %#v, want sorted codex then gemini", usages)
	}
	if len(usages[0].Models) != 1 || usages[0].Models[0] != "gpt-5-codex" {
		t.Fatalf("codex models = %#v, want deduped gpt-5-codex", usages[0].Models)
	}
}

func TestRenderTextIncludesKeySections(t *testing.T) {
	out := RenderText(Report{
		Environment: Environment{
			Shell:           "/bin/bash",
			AgentmuxVersion: "agentmux test",
			GoBinary:        "/usr/bin/go",
			GoVersion:       "go version go1.24.1 linux/amd64",
			TMuxBinary:      "/usr/bin/tmux",
			TMuxVersion:     "tmux 3.4",
			Cwd:             "/repo",
		},
		Diagnostics: []Message{
			{Status: StatusSuccess, Message: "tmux version is optimal."},
		},
		Config: &ConfigReport{
			Path:     "team.yaml",
			Warnings: []string{"provider alias warning"},
			Providers: []ProviderReport{
				{
					Usage: ProviderUsage{Name: "codex", Models: []string{"gpt-5-codex"}, ReasoningEfforts: []string{"high"}},
					Health: provider.HealthReport{
						Spec: provider.Spec{
							ID:                        "codex",
							DisplayName:               "Codex CLI",
							Binary:                    "codex",
							IntegrationMode:           "native-mcp",
							PersonaChannel:            "bootstrap-prompt",
							PersonaSource:             "config-file",
							ToolChannel:               "bootstrap-prompt",
							PreferredToolInput:        "stdin",
							CoordinationMode:          "initial prompt bootstrap + agentmux CLI bridge instructions",
							ToolFormat:                "codex",
							ReasoningParameter:        "-c model_reasoning_effort=<value>",
							SupportedReasoningEfforts: []string{"minimal", "low", "medium", "high", "xhigh"},
						},
						Launch: provider.LaunchProfile{
							Mode:           "cli-bridge",
							PersonaChannel: "bootstrap-prompt",
							PersonaSource:  "config-file",
							ToolChannel:    "bootstrap-prompt",
							PreferredInput: "stdin",
							Coordination:   "initial prompt bootstrap + agentmux CLI bridge instructions",
						},
						BinaryPath: "/usr/local/bin/codex",
					},
				},
			},
		},
	})

	for _, want := range []string{
		"shell:         /bin/bash",
		"Diagnostics:",
		"[✓] tmux version is optimal.",
		`Requirements for "team.yaml":`,
		"Config warnings:",
		"Provider diagnostics:",
		"codex (Codex CLI)",
		"binary: /usr/local/bin/codex",
		"integration: cli-bridge",
		"persona_channel: bootstrap-prompt",
		"persona_source: config-file",
		"tool_channel: bootstrap-prompt",
		"preferred_tool_input: stdin",
		"tools_format: codex",
		"reasoning_control: -c model_reasoning_effort=<value>",
		"supported_reasoning_efforts: minimal, low, medium, high, xhigh",
		"reasoning_efforts: high",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("RenderText output %q does not include %q", out, want)
		}
	}
}

func TestVersionParsers(t *testing.T) {
	if got := parseGoVersionNumber("go version go1.24.3 linux/amd64"); got != 1.24 {
		t.Fatalf("parseGoVersionNumber = %v, want 1.24", got)
	}
	if got := parseTMuxVersionNumber("tmux 3.2a"); got != 3.2 {
		t.Fatalf("parseTMuxVersionNumber = %v, want 3.2", got)
	}
}
