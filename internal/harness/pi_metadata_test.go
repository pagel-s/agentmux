package harness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/modelmeta"
)

func TestPiPrivateEndpointUsesExplicitMetadataAndDisablesTools(t *testing.T) {
	yes, no := true, false
	req := Request{Harness: "pi", Model: "exact", BaseURL: "http://127.0.0.1:8080/v1", ModelMetadata: modelmeta.Metadata{ContextWindow: 32768, MaxOutput: 4096, Input: []string{"text", "image"}, Reasoning: &yes, Tools: &no}}
	spec, err := Build(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(spec.Args, " "), "--no-tools") {
		t.Fatal("explicit no-tools not applied")
	}
	cleanup, err := preparePiEndpoint(req, &spec)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var dir string
	for _, entry := range spec.Env {
		if strings.HasPrefix(entry, "PI_CODING_AGENT_DIR=") {
			dir = strings.TrimPrefix(entry, "PI_CODING_AGENT_DIR=")
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Providers map[string]struct {
			Models []struct {
				Context   int      `json:"contextWindow"`
				Output    int      `json:"maxTokens"`
				Input     []string `json:"input"`
				Reasoning bool     `json:"reasoning"`
			}
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	model := config.Providers["agentmux"].Models[0]
	if model.Context != 32768 || model.Output != 4096 || !model.Reasoning || len(model.Input) != 2 {
		t.Fatalf("model=%+v", model)
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("private endpoint config was not removed")
	}
}
