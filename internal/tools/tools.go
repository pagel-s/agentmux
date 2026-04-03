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

func Definitions(format string) (string, error) {
	// For now, we output standard JSON Schema (OpenAI format).
	
	tools := []Tool{
		{
			Type: "function",
			Function: FunctionSchema{
				Name:        "agentmux_kv_set",
				Description: "Sets a shared key-value pair in the session's Blackboard.",
				Parameters: ParameterSchema{
					Type: "object",
					Properties: map[string]PropertyDef{
						"session": PropertyDef{Type: "string", Description: "The name of the current tmux session (available in $AGENTMUX_SESSION)."},
						"key":     PropertyDef{Type: "string", Description: "The key to set (alphanumeric and underscores only)."},
						"value":   PropertyDef{Type: "string", Description: "The value to store."},
					},
					Required: []string{"session", "key", "value"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSchema{
				Name:        "agentmux_kv_get",
				Description: "Retrieves a shared value from the session's Blackboard.",
				Parameters: ParameterSchema{
					Type: "object",
					Properties: map[string]PropertyDef{
						"session": PropertyDef{Type: "string", Description: "The name of the current tmux session."},
						"key":     PropertyDef{Type: "string", Description: "The key to retrieve."},
					},
					Required: []string{"session", "key"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSchema{
				Name:        "agentmux_wait",
				Description: "Blocks until a specific regex pattern appears in a target pane's terminal output.",
				Parameters: ParameterSchema{
					Type: "object",
					Properties: map[string]PropertyDef{
						"session": PropertyDef{Type: "string", Description: "The name of the current tmux session."},
						"pane":    PropertyDef{Type: "string", Description: "The name of the target pane to observe."},
						"pattern": PropertyDef{Type: "string", Description: "The regular expression pattern to wait for."},
						"timeout": PropertyDef{Type: "string", Description: "Optional timeout (e.g., '30s', '5m')."},
					},
					Required: []string{"session", "pane", "pattern"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSchema{
				Name:        "agentmux_scale",
				Description: "Dynamically adds or removes instances of a specific agent in the session.",
				Parameters: ParameterSchema{
					Type: "object",
					Properties: map[string]PropertyDef{
						"session": PropertyDef{Type: "string", Description: "The name of the current tmux session."},
						"agent":   PropertyDef{Type: "string", Description: "The name of the agent to scale."},
						"count":   PropertyDef{Type: "string", Description: "The target number of instances for this agent (e.g., '3')."},
					},
					Required: []string{"session", "agent", "count"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSchema{
				Name:        "agentmux_send",
				Description: "Sends a command or text directly to a target pane.",
				Parameters: ParameterSchema{
					Type: "object",
					Properties: map[string]PropertyDef{
						"session": PropertyDef{Type: "string", Description: "The name of the current tmux session."},
						"pane":    PropertyDef{Type: "string", Description: "The name of the target pane."},
						"command": PropertyDef{Type: "string", Description: "The text or command to send."},
					},
					Required: []string{"session", "pane", "command"},
				},
			},
		},
	}

	b, err := json.MarshalIndent(tools, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func Prompt() string {
	return `You are an AI agent running inside an Agentmux orchestrated terminal workspace.
Your current tmux session name is available in the environment variable $AGENTMUX_SESSION.
Your current pane name is available in $AGENTMUX_PANE.

You have access to the 'agentmux' CLI tool to coordinate with other agents in this session.
You can use the provided tools to interact with the shared Blackboard (KV-store), synchronize with peers using 'wait', or issue commands.
Always use your $AGENTMUX_SESSION when a tool requires the 'session' parameter.`
}
