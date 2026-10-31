package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Pi reads models.json from PI_CODING_AGENT_DIR. Each endpoint-backed turn gets
// a private directory instead of changing ~/.pi/agent or a project's settings.
// API base URLs are used exactly as supplied: Ollama/vLLM normally require /v1.
// This adapter speaks the OpenAI chat-completions protocol, not /api/generate.
func preparePiEndpoint(req Request, spec *Spec) (func(), error) {
	noop := func() {}
	if spec.Name != "pi" || req.BaseURL == "" {
		return noop, nil
	}
	dir, err := os.MkdirTemp("", "agentmux-pi-*")
	if err != nil {
		return noop, fmt.Errorf("prepare Pi endpoint: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	apiKey := "agentmux-local"
	if req.APIKeyEnv != "" {
		apiKey = "${AGENTMUX_PI_API_KEY}"
	}
	config := map[string]any{"providers": map[string]any{"agentmux": map[string]any{
		"baseUrl": req.BaseURL,
		"api":     "openai-completions",
		"apiKey":  apiKey,
		"models": []any{map[string]any{
			"id": req.Model, "name": req.Model, "reasoning": false,
			"input": []string{"text"},
			"cost":  map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
			// Conservative defaults for an unadvertised compatible endpoint;
			// full model metadata negotiation is a separate adapter feature.
			"contextWindow": 8192, "maxTokens": 2048,
		}},
	}}}
	data, err := json.Marshal(config)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "models.json"), data, 0600)
	}
	if err != nil {
		cleanup()
		return noop, fmt.Errorf("write private Pi endpoint configuration: %w", err)
	}
	spec.Env = mergeEnv(spec.Env, map[string]string{"PI_CODING_AGENT_DIR": dir, "PI_OFFLINE": "1"})
	spec.HomeFiles = append(spec.HomeFiles, HomeFile{Source: filepath.Join(dir, "models.json"), Target: "/home/agent/.pi/agent/models.json"})
	return cleanup, nil
}
