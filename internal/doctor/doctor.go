package doctor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"agentmux/internal/envcheck"
	"agentmux/internal/provider"
	"agentmux/internal/resolve"
	"agentmux/internal/scaffold"
	"agentmux/internal/tmux"
	"agentmux/internal/validate"
)

type Status string

const (
	StatusSuccess Status = "success"
	StatusWarning Status = "warning"
	StatusError   Status = "error"
)

type Message struct {
	Status  Status
	Message string
}

type Environment struct {
	Shell             string
	AgentmuxVersion   string
	AgentmuxPath      string
	GoBinary          string
	GoVersion         string
	GoVersionNumber   float64
	TMuxBinary        string
	TMuxVersion       string
	TMuxVersionNumber float64
	TMuxSocket        string
	Cwd               string
	ProjectTemplates  int
	GlobalTemplates   int
}

type ProviderUsage struct {
	Name             string
	Models           []string
	ReasoningEfforts []string
}

type ProviderReport struct {
	Usage         ProviderUsage
	Health        provider.HealthReport
	ModelWarnings []string
}

type ConfigReport struct {
	Path            string
	ResolveExitCode int
	Requirements    envcheck.Report
	Warnings        []string
	Providers       []ProviderReport
}

type Report struct {
	Environment Environment
	Diagnostics []Message
	Config      *ConfigReport
}

type Input struct {
	AgentmuxVersion string
	ConfigPath      string
	Resolved        *resolve.Config
	Issues          []validate.Issue
	ResolveExitCode int
}

type Probes struct {
	Getenv        func(string) string
	Getwd         func() (string, error)
	LookPath      provider.LookPathFunc
	RunOutput     provider.RunFunc
	ListTemplates func() ([]scaffold.Template, error)
	TMuxBinary    string
	TMuxSocket    string
}

func DefaultProbes() Probes {
	return Probes{
		Getenv:        os.Getenv,
		Getwd:         os.Getwd,
		LookPath:      exec.LookPath,
		RunOutput:     runOutput,
		ListTemplates: scaffold.List,
		TMuxBinary:    tmux.Binary(),
		TMuxSocket:    tmux.Socket(),
	}
}

func Collect(input Input, probes Probes) Report {
	probes = probes.withDefaults()

	env := collectEnvironment(input.AgentmuxVersion, probes)
	report := Report{
		Environment: env,
		Diagnostics: collectDiagnostics(env),
	}

	if input.ConfigPath == "" {
		return report
	}

	cfg := &ConfigReport{
		Path:            input.ConfigPath,
		ResolveExitCode: input.ResolveExitCode,
	}
	if input.ResolveExitCode == 0 && input.Resolved != nil {
		cfg.Requirements = envcheck.Check(input.Resolved.Requires, input.Resolved.Params)
		cfg.Warnings = warningMessages(input.Issues)
		cfg.Providers = collectProviderReports(*input.Resolved, probes)
	}
	report.Config = cfg

	return report
}

func RenderText(report Report) string {
	var b strings.Builder

	fmt.Fprintf(&b, "shell:         %s\n", report.Environment.Shell)
	fmt.Fprintf(&b, "agentmux:      %s\n", report.Environment.AgentmuxVersion)
	fmt.Fprintf(&b, "go_binary:     %s\n", report.Environment.GoBinary)
	fmt.Fprintf(&b, "go_version:    %s\n", report.Environment.GoVersion)
	fmt.Fprintf(&b, "tmux_binary:   %s\n", report.Environment.TMuxBinary)
	fmt.Fprintf(&b, "tmux_version:  %s\n", report.Environment.TMuxVersion)
	if report.Environment.TMuxSocket != "" {
		fmt.Fprintf(&b, "tmux_socket:   %s\n", report.Environment.TMuxSocket)
	}
	fmt.Fprintf(&b, "cwd:           %s\n", report.Environment.Cwd)
	if report.Environment.ProjectTemplates > 0 {
		fmt.Fprintf(&b, "project_templates: %d found in .agentmux/templates/\n", report.Environment.ProjectTemplates)
	}
	if report.Environment.GlobalTemplates > 0 {
		fmt.Fprintf(&b, "global_templates:  %d found in ~/.config/agentmux/templates/\n", report.Environment.GlobalTemplates)
	}

	b.WriteString("\nDiagnostics:\n")
	for _, diagnostic := range report.Diagnostics {
		fmt.Fprintf(&b, "  [%s] %s\n", statusSymbol(diagnostic.Status), diagnostic.Message)
	}

	if report.Config == nil {
		return b.String()
	}

	fmt.Fprintf(&b, "\nRequirements for %q:\n", report.Config.Path)
	if report.Config.ResolveExitCode != 0 {
		fmt.Fprintf(&b, "  [!] ERROR: could not resolve config: exit code %d\n", report.Config.ResolveExitCode)
		return b.String()
	}

	if !report.Config.Requirements.HasMissing() {
		b.WriteString("  [✓] all requirements met.\n")
	} else {
		for _, missing := range report.Config.Requirements.MissingBinaries {
			fmt.Fprintf(&b, "  [!] MISSING BINARY: %s\n", missing)
		}
		for _, missing := range report.Config.Requirements.MissingEnv {
			fmt.Fprintf(&b, "  [!] MISSING ENV:    %s\n", missing)
		}
		for _, missing := range report.Config.Requirements.MissingParams {
			fmt.Fprintf(&b, "  [!] MISSING PARAM:  %s\n", missing)
		}
	}

	if len(report.Config.Warnings) > 0 {
		b.WriteString("\nConfig warnings:\n")
		for _, warning := range report.Config.Warnings {
			fmt.Fprintf(&b, "  [!] %s\n", warning)
		}
	}

	if len(report.Config.Providers) > 0 {
		b.WriteString("\nProvider diagnostics:\n")
		for _, entry := range report.Config.Providers {
			fmt.Fprintf(&b, "  %s (%s)\n", entry.Health.Spec.ID, entry.Health.Spec.DisplayName)
			if entry.Health.Spec.Binary != "" {
				if entry.Health.BinaryPath != "" {
					fmt.Fprintf(&b, "    binary: %s\n", entry.Health.BinaryPath)
				} else {
					fmt.Fprintf(&b, "    binary: %s\n", entry.Health.Spec.Binary)
				}
			}
			launch := entry.Health.Launch
			if launch.Mode == "" {
				launch = provider.ResolveLaunch(entry.Health.Spec.ID)
			}
			if launch.Mode != "" {
				fmt.Fprintf(&b, "    integration: %s\n", launch.Mode)
			}
			if launch.PersonaChannel != "" {
				fmt.Fprintf(&b, "    persona_channel: %s\n", launch.PersonaChannel)
			}
			if launch.PersonaSource != "" {
				fmt.Fprintf(&b, "    persona_source: %s\n", launch.PersonaSource)
			}
			if launch.ToolChannel != "" {
				fmt.Fprintf(&b, "    tool_channel: %s\n", launch.ToolChannel)
			}
			if launch.PreferredInput != "" {
				fmt.Fprintf(&b, "    preferred_tool_input: %s\n", launch.PreferredInput)
			}
			if launch.Coordination != "" {
				fmt.Fprintf(&b, "    coordination: %s\n", launch.Coordination)
			}
			if launch.FallbackReason != "" {
				fmt.Fprintf(&b, "    fallback_reason: %s\n", launch.FallbackReason)
			}
			if entry.Health.Spec.ToolFormat != "" {
				fmt.Fprintf(&b, "    tools_format: %s\n", entry.Health.Spec.ToolFormat)
			}
			if entry.Health.Spec.ReasoningParameter != "" {
				fmt.Fprintf(&b, "    reasoning_control: %s\n", entry.Health.Spec.ReasoningParameter)
			}
			if len(entry.Health.Spec.SupportedReasoningEfforts) > 0 {
				fmt.Fprintf(&b, "    supported_reasoning_efforts: %s\n", strings.Join(entry.Health.Spec.SupportedReasoningEfforts, ", "))
			}
			if len(entry.Usage.Models) > 0 {
				fmt.Fprintf(&b, "    models: %s\n", strings.Join(entry.Usage.Models, ", "))
			}
			if len(entry.Usage.ReasoningEfforts) > 0 {
				fmt.Fprintf(&b, "    reasoning_efforts: %s\n", strings.Join(entry.Usage.ReasoningEfforts, ", "))
			}
			if len(entry.Health.Issues) == 0 {
				b.WriteString("    [✓] CLI compatibility checks passed.\n")
			} else {
				for _, issue := range entry.Health.Issues {
					fmt.Fprintf(&b, "    [%s] %s\n", statusSymbol(Status(issue.Severity)), issue.Message)
				}
			}
			for _, warning := range entry.ModelWarnings {
				fmt.Fprintf(&b, "    [!] %s\n", warning)
			}
		}
	}

	return b.String()
}

func (p Probes) withDefaults() Probes {
	if p.Getenv == nil {
		p.Getenv = os.Getenv
	}
	if p.Getwd == nil {
		p.Getwd = os.Getwd
	}
	if p.LookPath == nil {
		p.LookPath = exec.LookPath
	}
	if p.RunOutput == nil {
		p.RunOutput = runOutput
	}
	if p.ListTemplates == nil {
		p.ListTemplates = scaffold.List
	}
	if p.TMuxBinary == "" {
		p.TMuxBinary = tmux.Binary()
	}
	if p.TMuxSocket == "" {
		p.TMuxSocket = tmux.Socket()
	}
	return p
}

func runOutput(path string, args ...string) (string, error) {
	cmd := exec.Command(path, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func collectEnvironment(agentmuxVersion string, probes Probes) Environment {
	shell := probes.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	cwd := "."
	if value, err := probes.Getwd(); err == nil {
		cwd = value
	}

	tmuxPath, tmuxVersion, tmuxVersionNumber := localTMuxVersion(probes)
	goPath, goVersion, goVersionNumber := localGoVersion(probes)
	agentmuxPath, _ := probes.LookPath("agentmux")
	projectTemplates, globalTemplates := templateCounts(probes)

	return Environment{
		Shell:             shell,
		AgentmuxVersion:   agentmuxVersion,
		AgentmuxPath:      agentmuxPath,
		GoBinary:          goPath,
		GoVersion:         goVersion,
		GoVersionNumber:   goVersionNumber,
		TMuxBinary:        tmuxPath,
		TMuxVersion:       tmuxVersion,
		TMuxVersionNumber: tmuxVersionNumber,
		TMuxSocket:        probes.TMuxSocket,
		Cwd:               cwd,
		ProjectTemplates:  projectTemplates,
		GlobalTemplates:   globalTemplates,
	}
}

func collectDiagnostics(env Environment) []Message {
	diagnostics := make([]Message, 0, 3)

	switch {
	case env.TMuxBinary == "":
		diagnostics = append(diagnostics, Message{
			Status:  StatusError,
			Message: "CRITICAL: tmux is not installed. agentmux requires tmux to function.",
		})
	case env.TMuxVersionNumber < 3.2:
		diagnostics = append(diagnostics, Message{
			Status:  StatusWarning,
			Message: fmt.Sprintf("WARNING: tmux version %s detected. 3.2 or newer is recommended for full pane title support.", env.TMuxVersion),
		})
	default:
		diagnostics = append(diagnostics, Message{
			Status:  StatusSuccess,
			Message: "tmux version is optimal.",
		})
	}

	switch {
	case env.GoBinary == "not found":
		diagnostics = append(diagnostics, Message{
			Status:  StatusWarning,
			Message: "WARNING: Go is not installed or not in PATH. Building agentmux from source requires Go 1.24+.",
		})
	case env.GoVersionNumber > 0 && env.GoVersionNumber < 1.24:
		diagnostics = append(diagnostics, Message{
			Status:  StatusWarning,
			Message: fmt.Sprintf("WARNING: Go version %s detected. This repo targets Go 1.24+.", env.GoVersion),
		})
	case env.GoVersionNumber >= 1.24:
		diagnostics = append(diagnostics, Message{
			Status:  StatusSuccess,
			Message: "Go version meets the documented build baseline.",
		})
	}

	if env.AgentmuxPath == "" {
		diagnostics = append(diagnostics, Message{
			Status:  StatusWarning,
			Message: "WARNING: agentmux is not in your PATH. You must call it via absolute path or add its directory to PATH.",
		})
	} else {
		diagnostics = append(diagnostics, Message{
			Status:  StatusSuccess,
			Message: fmt.Sprintf("agentmux is in PATH at %s", env.AgentmuxPath),
		})
	}

	return diagnostics
}

func collectProviderReports(resolved resolve.Config, probes Probes) []ProviderReport {
	usages := collectProviderUsages(resolved)
	reports := make([]ProviderReport, 0, len(usages))
	for _, usage := range usages {
		entry := ProviderReport{
			Usage:  usage,
			Health: provider.CheckCLIWith(usage.Name, probes.LookPath, probes.RunOutput),
		}

		seenWarnings := make(map[string]struct{})
		for _, model := range usage.Models {
			if warning := provider.ModelWarning(usage.Name, model); warning != "" {
				if _, ok := seenWarnings[warning]; ok {
					continue
				}
				seenWarnings[warning] = struct{}{}
				entry.ModelWarnings = append(entry.ModelWarnings, warning)
			}
		}
		for _, effort := range usage.ReasoningEfforts {
			if warning := provider.ReasoningWarning(usage.Name, strings.Join(usage.Models, ","), effort); warning != "" {
				if _, ok := seenWarnings[warning]; ok {
					continue
				}
				seenWarnings[warning] = struct{}{}
				entry.ModelWarnings = append(entry.ModelWarnings, warning)
			}
		}
		reports = append(reports, entry)
	}

	return reports
}

func collectProviderUsages(resolved resolve.Config) []ProviderUsage {
	providers := make(map[string]map[string]struct{})
	add := func(name string, model string) {
		name = provider.Normalize(name)
		if name == "" || name == "command" || name == "shell" {
			return
		}
		if _, ok := provider.Lookup(name); !ok {
			return
		}
		models, ok := providers[name]
		if !ok {
			models = make(map[string]struct{})
			providers[name] = models
		}
		if strings.TrimSpace(model) != "" {
			models[model] = struct{}{}
		}
	}

	for _, agent := range resolved.Agents {
		add(agent.Provider, agent.Model)
	}
	for _, window := range resolved.Windows {
		for _, pane := range window.Panes {
			add(pane.Provider, pane.Model)
		}
	}

	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]ProviderUsage, 0, len(names))
	for _, name := range names {
		modelNames := make([]string, 0, len(providers[name]))
		for model := range providers[name] {
			modelNames = append(modelNames, model)
		}
		sort.Strings(modelNames)
		out = append(out, ProviderUsage{
			Name:             name,
			Models:           modelNames,
			ReasoningEfforts: collectReasoningEfforts(resolved, name),
		})
	}

	return out
}

func collectReasoningEfforts(resolved resolve.Config, providerName string) []string {
	efforts := make(map[string]struct{})
	add := func(name string, effort string) {
		if provider.Normalize(name) != providerName {
			return
		}
		effort = strings.TrimSpace(effort)
		if effort != "" {
			efforts[effort] = struct{}{}
		}
	}

	for _, agent := range resolved.Agents {
		add(agent.Provider, agent.ReasoningEffort)
	}
	for _, window := range resolved.Windows {
		for _, pane := range window.Panes {
			add(pane.Provider, pane.ReasoningEffort)
		}
	}

	names := make([]string, 0, len(efforts))
	for effort := range efforts {
		names = append(names, effort)
	}
	sort.Strings(names)
	return names
}

func warningMessages(issues []validate.Issue) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue.Severity == validate.SeverityWarning {
			out = append(out, issue.Message)
		}
	}
	return out
}

func templateCounts(probes Probes) (project int, global int) {
	templates, err := probes.ListTemplates()
	if err != nil {
		return 0, 0
	}
	for _, template := range templates {
		switch template.Source {
		case scaffold.SourceRepo:
			project++
		case scaffold.SourceGlobal:
			global++
		}
	}
	return project, global
}

func statusSymbol(status Status) string {
	switch status {
	case StatusSuccess:
		return "✓"
	case StatusWarning, StatusError:
		return "!"
	default:
		return "i"
	}
}

func localGoVersion(probes Probes) (path string, version string, parsed float64) {
	path, err := probes.LookPath("go")
	if err != nil {
		return "not found", "not found", 0
	}

	out, err := probes.RunOutput(path, "version")
	if err != nil {
		return path, "unknown", 0
	}

	version = strings.TrimSpace(out)
	return path, version, parseGoVersionNumber(version)
}

func localTMuxVersion(probes Probes) (path string, version string, parsed float64) {
	path, err := probes.LookPath(probes.TMuxBinary)
	if err != nil {
		return "", "not found", 0
	}

	out, err := probes.RunOutput(path, "-V")
	if err != nil {
		return path, "not found", 0
	}

	version = strings.TrimSpace(out)
	return path, version, parseTMuxVersionNumber(version)
}

func parseGoVersionNumber(version string) float64 {
	fields := strings.Fields(strings.TrimSpace(version))
	if len(fields) < 3 {
		return 0
	}

	raw := strings.TrimPrefix(fields[2], "go")
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return 0
	}

	var parsed float64
	fmt.Sscanf(parts[0]+"."+parts[1], "%f", &parsed)
	return parsed
}

func parseTMuxVersionNumber(version string) float64 {
	fields := strings.Fields(strings.TrimSpace(version))
	if len(fields) < 2 {
		return 0
	}

	var parsed float64
	fmt.Sscanf(fields[1], "%f", &parsed)
	return parsed
}
