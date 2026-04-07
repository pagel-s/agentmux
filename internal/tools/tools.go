package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	prov "agentmux/internal/provider"
)

type Tool struct {
	Type     string         `json:"type"`
	Function FunctionSchema `json:"function"`
}

type FunctionSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  ParameterSchema `json:"parameters"`
}

type ParameterSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]PropertyDef `json:"properties"`
	Required   []string               `json:"required,omitempty"`
}

type PropertyDef struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema ParameterSchema `json:"input_schema"`
}

type ToolParameter struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

type ToolSpec struct {
	Name        string
	SchemaName  string
	Command     string
	Description string
	Aliases     []string
	Parameters  []ToolParameter
}

type PromptOptions struct {
	Provider string
	Mode     string
}

var registry = []ToolSpec{
	{
		Name:        "kv_set",
		SchemaName:  "agentmux_kv_set",
		Command:     "kv-set",
		Description: "Sets a shared key-value pair in the session blackboard.",
		Aliases:     []string{"kv-set", "agentmux_kv_set"},
		Parameters: []ToolParameter{
			{Name: "session", Type: "string", Description: "Optional tmux session name. Defaults to $AGENTMUX_SESSION when omitted.", Required: false},
			{Name: "key", Type: "string", Description: "The key to set (alphanumeric and underscores only).", Required: true},
			{Name: "value", Type: "string", Description: "The value to store.", Required: true},
		},
	},
	{
		Name:        "kv_get",
		SchemaName:  "agentmux_kv_get",
		Command:     "kv-get",
		Description: "Retrieves a shared value from the session blackboard.",
		Aliases:     []string{"kv-get", "agentmux_kv_get"},
		Parameters: []ToolParameter{
			{Name: "session", Type: "string", Description: "Optional tmux session name. Defaults to $AGENTMUX_SESSION when omitted.", Required: false},
			{Name: "key", Type: "string", Description: "The key to retrieve.", Required: true},
		},
	},
	{
		Name:        "wait",
		SchemaName:  "agentmux_wait",
		Command:     "wait",
		Description: "Blocks until a regex pattern appears in a target pane's emitted output. By default, active input/composer text is ignored.",
		Aliases:     []string{"agentmux_wait"},
		Parameters: []ToolParameter{
			{Name: "session", Type: "string", Description: "Optional tmux session name. Defaults to $AGENTMUX_SESSION when omitted.", Required: false},
			{Name: "pane", Type: "string", Description: "The name of the target pane to observe.", Required: true},
			{Name: "pattern", Type: "string", Description: "The regular expression pattern to wait for.", Required: true},
			{Name: "scope", Type: "string", Description: "Optional wait scope: output (default) ignores active input/composer text; visible matches any visible pane text."},
			{Name: "timeout", Type: "string", Description: "Optional timeout such as 30s or 5m."},
		},
	},
	{
		Name:        "scale",
		SchemaName:  "agentmux_scale",
		Command:     "scale",
		Description: "Dynamically adds or removes instances of a specific agent in the session.",
		Aliases:     []string{"agentmux_scale"},
		Parameters: []ToolParameter{
			{Name: "session", Type: "string", Description: "Optional tmux session name. Defaults to $AGENTMUX_SESSION when omitted.", Required: false},
			{Name: "agent", Type: "string", Description: "The name of the agent to scale.", Required: true},
			{Name: "count", Type: "integer", Description: "The target number of instances for this agent.", Required: true},
			{Name: "window", Type: "string", Description: "Optional window name to host the scaled panes."},
		},
	},
	{
		Name:        "send",
		SchemaName:  "agentmux_send",
		Command:     "send",
		Description: "Sends command text directly to a target pane.",
		Aliases:     []string{"agentmux_send"},
		Parameters: []ToolParameter{
			{Name: "session", Type: "string", Description: "Optional tmux session name. Defaults to $AGENTMUX_SESSION when omitted.", Required: false},
			{Name: "pane", Type: "string", Description: "The name of the target pane.", Required: true},
			{Name: "command", Type: "string", Description: "The text or command to send.", Required: true},
			{Name: "no_enter", Type: "boolean", Description: "When true, send the text without pressing Enter."},
		},
	},
}

func Specs() []ToolSpec {
	out := make([]ToolSpec, 0, len(registry))
	for _, spec := range registry {
		clone := spec
		clone.Aliases = append([]string(nil), spec.Aliases...)
		clone.Parameters = append([]ToolParameter(nil), spec.Parameters...)
		out = append(out, clone)
	}
	return out
}

func Lookup(name string) (ToolSpec, bool) {
	normalized := normalizeToolName(name)
	if normalized == "" {
		return ToolSpec{}, false
	}

	for _, spec := range registry {
		if normalizeToolName(spec.Name) == normalized || normalizeToolName(spec.SchemaName) == normalized {
			return spec, true
		}
		for _, alias := range spec.Aliases {
			if normalizeToolName(alias) == normalized {
				return spec, true
			}
		}
	}

	return ToolSpec{}, false
}

func Definitions(format string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "openai"
	}

	switch format {
	case "anthropic", "claude":
		tools := make([]AnthropicTool, 0, len(registry))
		for _, spec := range registry {
			tools = append(tools, AnthropicTool{
				Name:        spec.SchemaName,
				Description: spec.Description,
				InputSchema: ParameterSchema{
					Type:       "object",
					Properties: schemaProperties(spec.Parameters),
					Required:   schemaRequired(spec.Parameters),
				},
			})
		}
		b, err := json.MarshalIndent(tools, "", "  ")
		return string(b), err
	case "openai", "codex", "gemini":
		tools := make([]Tool, 0, len(registry))
		for _, spec := range registry {
			tools = append(tools, Tool{
				Type: "function",
				Function: FunctionSchema{
					Name:        spec.SchemaName,
					Description: spec.Description,
					Parameters: ParameterSchema{
						Type:       "object",
						Properties: schemaProperties(spec.Parameters),
						Required:   schemaRequired(spec.Parameters),
					},
				},
			})
		}
		b, err := json.MarshalIndent(tools, "", "  ")
		return string(b), err
	default:
		return "", fmt.Errorf("unsupported tools format %q", format)
	}
}

func Prompt(opts PromptOptions) (string, error) {
	var b strings.Builder
	personaChannel := "startup prompt"
	toolChannel := "startup instructions"
	preferredInput := "stdin"
	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	profile := prov.ProfileForMode(opts.Provider, mode)
	if profile.Mode != "" {
		if profile.PersonaChannel != "" {
			personaChannel = profile.PersonaChannel
		}
		if profile.ToolChannel != "" {
			toolChannel = profile.ToolChannel
		}
		if profile.PreferredInput != "" {
			preferredInput = profile.PreferredInput
		}
	}

	b.WriteString("You are an AI agent running inside an Agentmux orchestrated terminal workspace.\n")
	b.WriteString("Your current tmux session name is available in the environment variable $AGENTMUX_SESSION.\n")
	b.WriteString("Your current pane name is available in $AGENTMUX_PANE.\n\n")
	fmt.Fprintf(&b, "Your role/persona instructions are delivered via %s.\n", personaChannel)
	fmt.Fprintf(&b, "The Agentmux coordination contract is delivered via %s.\n\n", toolChannel)
	if mode == "" {
		mode = prov.IntegrationMode(opts.Provider)
	}

	switch mode {
	case string(prov.LaunchModeNativeMCP):
		b.WriteString("Agentmux coordination tools are available natively through MCP in this session.\n")
		b.WriteString("Use the provided tools directly instead of shelling out to `agentmux tool-call`.\n")
		b.WriteString("If a tool accepts `session`, you may omit it and Agentmux will default to $AGENTMUX_SESSION.\n")
		b.WriteString("The `wait` tool ignores active input/composer text by default; use `scope=visible` only when you truly want raw screen matching.\n")
		b.WriteString("Use normal shell commands for work and MCP tools only for coordination.\n\n")
	default:
		b.WriteString("Agentmux coordination actions are real CLI operations, not native function calls.\n")
		b.WriteString("When you need shared state, synchronization, scaling, or cross-pane interaction, call `agentmux tool-call`.\n")
		b.WriteString("Use exactly one input mode per call:\n")
		b.WriteString("  --from-stdin   preferred for most providers and shells\n")
		b.WriteString("  --from-file    preferred for larger or multiline payloads\n")
		b.WriteString("  --from-json    acceptable only for short inline payloads\n")
		b.WriteString("If `session` is omitted, Agentmux will default it from $AGENTMUX_SESSION.\n")
		b.WriteString("The `wait` tool ignores active input/composer text by default; use `scope=visible` only for raw screen matching.\n")
		b.WriteString("Use normal shell commands for work; use Agentmux tool calls only for coordination.\n\n")
		fmt.Fprintf(&b, "Preferred tool-call input mode for this provider: %s.\n\n", preferredInput)
	}
	b.WriteString("Available Agentmux coordination tools:\n")

	for _, spec := range registry {
		fmt.Fprintf(&b, "- %s %s: %s\n", spec.Name, renderParameterContract(spec.Parameters), spec.Description)
	}

	switch mode {
	case string(prov.LaunchModeNativeMCP):
		b.WriteString("\nExample:\n")
		b.WriteString("Call `kv_get` directly with `key=current_task`. The session will default automatically.\n")
	default:
		b.WriteString("\nExamples:\n")
		b.WriteString("Small payload via stdin:\n")
		b.WriteString("printf '%s\\n' \"{\\\"key\\\":\\\"current_task\\\"}\" | agentmux tool-call kv_get --from-stdin\n")
		b.WriteString("\nLarger payload via file:\n")
		b.WriteString("agentmux tool-call kv_set --from-file /tmp/agentmux-tool.json\n")
	}
	return b.String(), nil
}

func schemaProperties(params []ToolParameter) map[string]PropertyDef {
	properties := make(map[string]PropertyDef, len(params))
	for _, param := range params {
		properties[param.Name] = PropertyDef{
			Type:        param.Type,
			Description: param.Description,
		}
	}
	return properties
}

func schemaRequired(params []ToolParameter) []string {
	required := make([]string, 0, len(params))
	for _, param := range params {
		if param.Required {
			required = append(required, param.Name)
		}
	}
	return required
}

func renderParameterContract(params []ToolParameter) string {
	parts := make([]string, 0, len(params))
	for _, param := range params {
		part := fmt.Sprintf("%q:%q", param.Name, param.Type)
		if !param.Required {
			part = fmt.Sprintf("%q:%q?", param.Name, param.Type)
		}
		parts = append(parts, part)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func normalizeToolName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "-", "_")
	return name
}
