package provider

import (
	"sort"
	"strings"
)

type Mode int

const (
	ModeUnknown Mode = iota
	ModeCommand
	ModeNative
)

type LaunchMode string

const (
	LaunchModeUnknown   LaunchMode = ""
	LaunchModeCLIBridge LaunchMode = "cli-bridge"
	LaunchModeNativeMCP LaunchMode = "native-mcp"
)

type Spec struct {
	ID                        string
	DisplayName               string
	Binary                    string
	Mode                      Mode
	IntegrationMode           string
	FallbackIntegrationMode   string
	PersonaChannel            string
	PersonaSource             string
	ToolChannel               string
	PreferredToolInput        string
	ToolFormat                string
	CoordinationMode          string
	ReasoningParameter        string
	SupportedReasoningEfforts []string
	ReasoningNotes            string
	HelpArgs                  []string
	RequiredHelpTokens        []string
}

type LaunchProfile struct {
	Spec           Spec
	Mode           string
	PersonaChannel string
	PersonaSource  string
	ToolChannel    string
	PreferredInput string
	Coordination   string
	FallbackReason string
}

var registry = map[string]Spec{
	"command": {ID: "command", DisplayName: "Command", Mode: ModeCommand},
	"shell":   {ID: "shell", DisplayName: "Shell", Mode: ModeCommand},
	"ssh":     {ID: "ssh", DisplayName: "SSH", Binary: "ssh", Mode: ModeCommand},
	"aichat":  {ID: "aichat", DisplayName: "AIChat", Binary: "aichat", Mode: ModeCommand},
	"claude": {
		ID:                        "claude",
		DisplayName:               "Claude Code",
		Binary:                    "claude",
		Mode:                      ModeNative,
		IntegrationMode:           string(LaunchModeNativeMCP),
		FallbackIntegrationMode:   string(LaunchModeCLIBridge),
		PersonaChannel:            "system-prompt",
		PersonaSource:             "config-file",
		ToolChannel:               "appended-system-prompt",
		PreferredToolInput:        "stdin",
		ToolFormat:                "claude",
		CoordinationMode:          "system prompt + appended agentmux CLI bridge instructions",
		ReasoningParameter:        "--effort",
		SupportedReasoningEfforts: []string{"low", "medium", "high", "max"},
		HelpArgs:                  []string{"--help"},
		RequiredHelpTokens:        []string{"--append-system-prompt", "--model", "--effort"},
	},
	"gemini": {
		ID:                      "gemini",
		DisplayName:             "Gemini CLI",
		Binary:                  "gemini",
		Mode:                    ModeNative,
		IntegrationMode:         string(LaunchModeNativeMCP),
		FallbackIntegrationMode: string(LaunchModeCLIBridge),
		PersonaChannel:          "bootstrap-prompt",
		PersonaSource:           "config-file",
		ToolChannel:             "bootstrap-prompt",
		PreferredToolInput:      "stdin",
		ToolFormat:              "gemini",
		CoordinationMode:        "interactive prompt bootstrap + agentmux CLI bridge instructions",
		HelpArgs:                []string{"--help"},
		RequiredHelpTokens:      []string{"--prompt-interactive", "--model", "--include-directories", "--allowed-mcp-server-names", "gemini mcp"},
	},
	"codex": {
		ID:                        "codex",
		DisplayName:               "Codex CLI",
		Binary:                    "codex",
		Mode:                      ModeNative,
		IntegrationMode:           string(LaunchModeNativeMCP),
		FallbackIntegrationMode:   string(LaunchModeCLIBridge),
		PersonaChannel:            "bootstrap-prompt",
		PersonaSource:             "config-file",
		ToolChannel:               "bootstrap-prompt",
		PreferredToolInput:        "stdin",
		ToolFormat:                "codex",
		CoordinationMode:          "initial prompt bootstrap + agentmux CLI bridge instructions",
		ReasoningParameter:        "-c model_reasoning_effort=<value>",
		SupportedReasoningEfforts: []string{"minimal", "low", "medium", "high", "xhigh"},
		ReasoningNotes:            "Codex reasoning-effort support is model-snapshot dependent; current snapshots can reject xhigh.",
		HelpArgs:                  []string{"--help"},
		RequiredHelpTokens:        []string{"--model", "--config"},
	},
	"gptme": {
		ID:                 "gptme",
		DisplayName:        "GPTMe",
		Binary:             "gptme",
		Mode:               ModeNative,
		IntegrationMode:    string(LaunchModeCLIBridge),
		PersonaChannel:     "bootstrap-prompt",
		PersonaSource:      "config-file",
		ToolChannel:        "bootstrap-prompt",
		PreferredToolInput: "stdin",
		ToolFormat:         "openai",
		CoordinationMode:   "initial prompt bootstrap + agentmux CLI bridge instructions",
		HelpArgs:           []string{"--help"},
	},
}

var aliases = map[string]string{
	"claude-cli": "claude",
	"gemini-cli": "gemini",
	"openai":     "codex",
}

func Lookup(name string) (Spec, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return Spec{}, false
	}
	if spec, ok := registry[key]; ok {
		return spec, true
	}
	if canonical, ok := aliases[key]; ok {
		spec, ok := registry[canonical]
		return spec, ok
	}
	return Spec{}, false
}

func Normalize(name string) string {
	if spec, ok := Lookup(name); ok {
		return spec.ID
	}
	return strings.ToLower(strings.TrimSpace(name))
}

func AliasTarget(name string) (string, bool) {
	target, ok := aliases[strings.ToLower(strings.TrimSpace(name))]
	return target, ok
}

func IsAlias(name string) bool {
	_, ok := AliasTarget(name)
	return ok
}

func Supported(name string) bool {
	_, ok := Lookup(name)
	return ok
}

func RequiresCommand(name string) bool {
	spec, ok := Lookup(name)
	return ok && spec.Mode == ModeCommand
}

func UsesNativeIntegration(name string) bool {
	spec, ok := Lookup(name)
	return ok && spec.Mode == ModeNative
}

func AllowsDirectPaneProvider(name string) bool {
	return UsesNativeIntegration(name)
}

func ImplicitBinary(name string) string {
	spec, ok := Lookup(name)
	if !ok {
		return ""
	}
	return spec.Binary
}

func ToolFormat(name string) string {
	spec, ok := Lookup(name)
	if !ok {
		return ""
	}
	return spec.ToolFormat
}

func IntegrationMode(name string) string {
	spec, ok := Lookup(name)
	if !ok {
		return ""
	}
	return spec.IntegrationMode
}

func PersonaChannel(name string) string {
	spec, ok := Lookup(name)
	if !ok {
		return ""
	}
	return spec.PersonaChannel
}

func PersonaSource(name string) string {
	spec, ok := Lookup(name)
	if !ok {
		return ""
	}
	return spec.PersonaSource
}

func CoordinationMode(name string) string {
	spec, ok := Lookup(name)
	if !ok {
		return ""
	}
	return spec.CoordinationMode
}

func ResolveLaunch(name string) LaunchProfile {
	return ResolveLaunchWith(name, nil, nil)
}

func ProfileForMode(name string, mode string) LaunchProfile {
	spec, ok := Lookup(name)
	if !ok {
		return LaunchProfile{}
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = spec.IntegrationMode
	}
	return defaultLaunchProfile(spec, mode, "")
}

func ResolveLaunchWith(name string, lookPath LookPathFunc, run RunFunc) LaunchProfile {
	spec, ok := Lookup(name)
	if !ok {
		return LaunchProfile{}
	}

	base := defaultLaunchProfile(spec, spec.IntegrationMode, "")
	if spec.Mode != ModeNative || spec.Binary == "" || lookPath == nil || run == nil {
		return base
	}

	path, err := lookPath(spec.Binary)
	if err != nil || path == "" {
		return defaultLaunchProfile(spec, fallbackLaunchMode(spec), "provider binary not found during launch probe")
	}

	out, err := run(path, spec.HelpArgs...)
	if strings.TrimSpace(out) == "" && err != nil {
		return defaultLaunchProfile(spec, fallbackLaunchMode(spec), "unable to inspect provider help output for native mode")
	}

	switch spec.ID {
	case "claude":
		if strings.Contains(out, "--mcp-config") && strings.Contains(out, "--strict-mcp-config") {
			return defaultLaunchProfile(spec, string(LaunchModeNativeMCP), "")
		}
		return defaultLaunchProfile(spec, fallbackLaunchMode(spec), "Claude CLI missing MCP config flags; falling back to CLI bridge")
	case "codex":
		if strings.Contains(out, "--config") && strings.Contains(out, "mcp") {
			return defaultLaunchProfile(spec, string(LaunchModeNativeMCP), "")
		}
		return defaultLaunchProfile(spec, fallbackLaunchMode(spec), "Codex CLI missing MCP/config support; falling back to CLI bridge")
	case "gemini":
		if strings.Contains(out, "--allowed-mcp-server-names") && strings.Contains(out, "gemini mcp") {
			return defaultLaunchProfile(spec, string(LaunchModeNativeMCP), "")
		}
		return defaultLaunchProfile(spec, fallbackLaunchMode(spec), "Gemini CLI missing MCP support; falling back to CLI bridge")
	default:
		return base
	}
}

func ToolChannel(name string) string {
	spec, ok := Lookup(name)
	if !ok {
		return ""
	}
	return spec.ToolChannel
}

func PreferredToolInput(name string) string {
	spec, ok := Lookup(name)
	if !ok {
		return ""
	}
	return spec.PreferredToolInput
}

func ReasoningParameter(name string) string {
	spec, ok := Lookup(name)
	if !ok {
		return ""
	}
	return spec.ReasoningParameter
}

func SupportedReasoningEfforts(name string) []string {
	spec, ok := Lookup(name)
	if !ok || len(spec.SupportedReasoningEfforts) == 0 {
		return nil
	}
	return append([]string(nil), spec.SupportedReasoningEfforts...)
}

func ValidateReasoningEffort(name string, effort string) error {
	spec, ok := Lookup(name)
	if !ok {
		return nil
	}

	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return nil
	}
	if len(spec.SupportedReasoningEfforts) == 0 {
		return &ReasoningEffortUnsupportedError{Provider: spec.ID}
	}
	for _, allowed := range spec.SupportedReasoningEfforts {
		if effort == allowed {
			return nil
		}
	}
	return &ReasoningEffortError{
		Provider: spec.ID,
		Effort:   effort,
		Allowed:  append([]string(nil), spec.SupportedReasoningEfforts...),
	}
}

type ReasoningEffortError struct {
	Provider string
	Effort   string
	Allowed  []string
}

func (e *ReasoningEffortError) Error() string {
	return "provider " + quote(e.Provider) + " reasoning_effort " + quote(e.Effort) + " is unsupported; allowed values: " + strings.Join(e.Allowed, ", ")
}

type ReasoningEffortUnsupportedError struct {
	Provider string
}

func (e *ReasoningEffortUnsupportedError) Error() string {
	return "provider " + quote(e.Provider) + " does not support reasoning_effort"
}

func ReasoningWarning(name string, model string, effort string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return ""
	}

	spec, ok := Lookup(name)
	if !ok {
		return ""
	}
	if spec.ReasoningNotes != "" && spec.ID == "codex" && effort == "xhigh" {
		return spec.ReasoningNotes
	}
	return ""
}

func ModelWarning(name string, model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return ""
	}

	switch Normalize(name) {
	case "claude":
		if strings.Contains(model, "claude-3-") || strings.Contains(model, "claude-2") {
			return "legacy Claude model naming detected; prefer current aliases like sonnet or opus"
		}
	case "gemini":
		if strings.Contains(model, "1.5") {
			return "legacy Gemini 1.5 model naming detected; verify a current Gemini CLI model such as gemini-2.5-pro"
		}
	case "codex":
		if strings.Contains(model, "gpt-4o") {
			return "gpt-4o is a general-purpose model choice; prefer a Codex-oriented model such as gpt-5-codex"
		}
	}

	return ""
}

func CanonicalNames() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func quote(value string) string {
	return `"` + value + `"`
}

func defaultLaunchProfile(spec Spec, mode string, fallbackReason string) LaunchProfile {
	profile := LaunchProfile{
		Spec:           spec,
		Mode:           mode,
		FallbackReason: strings.TrimSpace(fallbackReason),
	}

	switch mode {
	case string(LaunchModeNativeMCP):
		switch spec.ID {
		case "claude":
			profile.PersonaChannel = "system-prompt"
			profile.PersonaSource = "config-file"
			profile.ToolChannel = "native-mcp"
			profile.PreferredInput = "native-mcp"
			profile.Coordination = "native MCP tools + appended operational instructions"
		case "gemini":
			profile.PersonaChannel = "workspace-context-file"
			profile.PersonaSource = "session-runtime-file"
			profile.ToolChannel = "native-mcp"
			profile.PreferredInput = "native-mcp"
			profile.Coordination = "native MCP tools + native workspace persona context + bootstrap operational instructions"
		case "codex":
			profile.PersonaChannel = "bootstrap-prompt"
			profile.PersonaSource = "config-file"
			profile.ToolChannel = "native-mcp"
			profile.PreferredInput = "native-mcp"
			profile.Coordination = "native MCP tools + bootstrap operational instructions"
		default:
			profile.PersonaChannel = spec.PersonaChannel
			profile.PersonaSource = spec.PersonaSource
			profile.ToolChannel = spec.ToolChannel
			profile.PreferredInput = spec.PreferredToolInput
			profile.Coordination = spec.CoordinationMode
		}
	default:
		profile.PersonaChannel = spec.PersonaChannel
		profile.PersonaSource = spec.PersonaSource
		profile.ToolChannel = spec.ToolChannel
		profile.PreferredInput = spec.PreferredToolInput
		profile.Coordination = spec.CoordinationMode
	}

	return profile
}

func fallbackLaunchMode(spec Spec) string {
	if strings.TrimSpace(spec.FallbackIntegrationMode) != "" {
		return spec.FallbackIntegrationMode
	}
	return spec.IntegrationMode
}
