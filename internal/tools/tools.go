package tools

import (
	"encoding/json"
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

func Definitions(format string) (string, error) {
	props := []struct {
		name string
		desc string
		params map[string]PropertyDef
		req []string
	}{
		{
			"agentmux_kv_set",
			"Sets a shared key-value pair in the session's Blackboard.",
			map[string]PropertyDef{
				"session": {Type: "string", Description: "The name of the current tmux session (available in $AGENTMUX_SESSION)."},
				"key":     {Type: "string", Description: "The key to set (alphanumeric and underscores only)."},
				"value":   {Type: "string", Description: "The value to store."},
			},
			[]string{"session", "key", "value"},
		},
		{
			"agentmux_kv_get",
			"Retrieves a shared value from the session's Blackboard.",
			map[string]PropertyDef{
				"session": {Type: "string", Description: "The name of the current tmux session."},
				"key":     {Type: "string", Description: "The key to retrieve."},
			},
			[]string{"session", "key"},
		},
		{
			"agentmux_wait",
			"Blocks until a specific regex pattern appears in a target pane's terminal output.",
			map[string]PropertyDef{
				"session": {Type: "string", Description: "The name of the current tmux session."},
				"pane":    {Type: "string", Description: "The name of the target pane to observe."},
				"pattern": {Type: "string", Description: "The regular expression pattern to wait for."},
				"timeout": {Type: "string", Description: "Optional timeout (e.g., '30s', '5m')."},
			},
			[]string{"session", "pane", "pattern"},
		},
		{
			"agentmux_scale",
			"Dynamically adds or removes instances of a specific agent in the session.",
			map[string]PropertyDef{
				"session": {Type: "string", Description: "The name of the current tmux session."},
				"agent":   {Type: "string", Description: "The name of the agent to scale."},
				"count":   {Type: "string", Description: "The target number of instances for this agent (e.g., '3')."},
			},
			[]string{"session", "agent", "count"},
		},
		{
			"agentmux_send",
			"Sends a command or text directly to a target pane.",
			map[string]PropertyDef{
				"session": {Type: "string", Description: "The name of the current tmux session."},
				"pane":    {Type: "string", Description: "The name of the target pane."},
				"command": {Type: "string", Description: "The text or command to send."},
			},
			[]string{"session", "pane", "command"},
		},
	}

	if format == "anthropic" || format == "claude" {
		tools := make([]AnthropicTool, 0, len(props))
		for _, p := range props {
			tools = append(tools, AnthropicTool{
				Name:        p.name,
				Description: p.desc,
				InputSchema: ParameterSchema{
					Type:       "object",
					Properties: p.params,
					Required:   p.req,
				},
			})
		}
		b, err := json.MarshalIndent(tools, "", "  ")
		return string(b), err
	}

	// Default: OpenAI format
	tools := make([]Tool, 0, len(props))
	for _, p := range props {
		tools = append(tools, Tool{
			Type: "function",
			Function: FunctionSchema{
				Name:        p.name,
				Description: p.desc,
				Parameters: ParameterSchema{
					Type:       "object",
					Properties: p.params,
					Required:   p.req,
				},
			},
		})
	}
	b, err := json.MarshalIndent(tools, "", "  ")
	return string(b), err
}

func Prompt() string {
	return `You are an AI agent running inside an Agentmux orchestrated terminal workspace.
Your current tmux session name is available in the environment variable $AGENTMUX_SESSION.
Your current pane name is available in $AGENTMUX_PANE.

You have access to the 'agentmux' CLI tool to coordinate with other agents in this session.
You can use the provided tools to interact with the shared Blackboard (KV-store), synchronize with peers using 'wait', or issue commands.
Always use your $AGENTMUX_SESSION when a tool requires the 'session' parameter.`
}
