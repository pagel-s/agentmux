package harness

import (
	"encoding/json"
	"fmt"
)

// Custom Codex endpoints use the documented native Responses provider schema.
// Only per-process config is changed; keys stay in the selected environment.
// Chat Completions endpoints belong on the Pi adapter, not this wire protocol.
func codexEndpointArgs(req Request, args []string) []string {
	if req.BaseURL == "" {
		return args
	}
	base, _ := json.Marshal(req.BaseURL) // JSON strings are TOML basic strings.
	auth := ""
	if req.APIKeyEnv != "" {
		auth = `,env_key="AGENTMUX_CODEX_API_KEY"`
	}
	provider := fmt.Sprintf(`{name="Agentmux endpoint",base_url=%s,wire_api="responses",requires_openai_auth=false,supports_websockets=false%s}`, base, auth)
	return append(args, "-c", `model_provider="agentmux_endpoint"`, "-c", "model_providers.agentmux_endpoint="+provider)
}
