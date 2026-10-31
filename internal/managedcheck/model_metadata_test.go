package managedcheck

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"agentmux/internal/managedconfig"
	"agentmux/internal/modelmeta"
)

func TestModelMetadataExactIDAndBounds(t *testing.T) {
	data := []byte(`{"data":[{"id":"other","max_model_len":999999},{"id":"exact","max_model_len":32768,"max_output_tokens":4096,"input_modalities":["text","image"],"supports_reasoning":false,"supports_tools":true}]}`)
	m, err := parseModelListMetadata(data, "exact")
	if err != nil {
		t.Fatal(err)
	}
	if m.ContextWindow != 32768 || m.MaxOutput != 4096 || len(m.Input) != 2 || m.Reasoning == nil || *m.Reasoning || m.Tools == nil || !*m.Tools {
		t.Fatalf("metadata=%+v", m)
	}
	if _, err := parseModelListMetadata(data, "alias"); err == nil {
		t.Fatal("inferred alias")
	}
	if _, err := parseModelListMetadata([]byte(`{"data":[{"id":"exact","max_model_len":999999999}]}`), "exact"); err == nil {
		t.Fatal("unbounded metadata accepted")
	}
	unknown, err := parseModelListMetadata([]byte(`{"data":[{"id":"exact","reasoning":null}]}`), "exact")
	if err != nil || unknown.Reasoning != nil {
		t.Fatal("null capability became known false")
	}
}

func TestOllamaMetadataRuntimeContextAndCapabilities(t *testing.T) {
	m, err := parseOllamaMetadata([]byte(`{"model_info":{"general.architecture":"qwen2","qwen2.context_length":32768},"parameters":"num_ctx 4096\nnum_predict 1024\n","capabilities":["completion","vision","tools"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.ContextWindow != 4096 || m.MaxOutput != 1024 || len(m.Input) != 2 || m.Tools == nil || !*m.Tools || m.Reasoning == nil || *m.Reasoning {
		t.Fatalf("metadata=%+v", m)
	}
}

func TestModelMetadataDiscoveryPrivateOnlyAndOwnerOverrides(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer private-key" {
			t.Error("unexpected metadata request")
		}
		fmt.Fprint(w, `{"data":[{"id":"exact","max_model_len":65536,"supports_reasoning":true,"supports_tools":true}]}`)
	}))
	defer server.Close()
	no := false
	connection := managedconfig.Connection{Protocol: "openai-compatible", BaseURL: server.URL + "/v1", ModelMetadata: modelmeta.Metadata{ContextWindow: 16384, MaxOutput: 1024, Reasoning: &no, Tools: &no}}
	result := DiscoverModelMetadata(context.Background(), connection, "exact", "private-key")
	if result.Source != "openai/models" || result.Warning != "" || result.Metadata.ContextWindow != 16384 || *result.Metadata.Reasoning || *result.Metadata.Tools || requests.Load() != 1 {
		t.Fatalf("result=%+v requests=%d", result, requests.Load())
	}
	connection.BaseURL = "https://example.com/v1"
	result = DiscoverModelMetadata(context.Background(), connection, "exact", "private-key")
	if result.Warning == "" || requests.Load() != 1 {
		t.Fatal("public endpoint contacted or unreported")
	}
}

func TestModelMetadataRedirectNotFollowedAndFallbackConservative(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer server.Close()
	result := DiscoverModelMetadata(context.Background(), managedconfig.Connection{Protocol: "openai-compatible", BaseURL: server.URL}, "unknown", "secret")
	m := result.Metadata.Effective()
	if result.Warning == "" || redirected.Load() != 0 || m.ContextWindow != 8192 || m.MaxOutput != 2048 || *m.Reasoning || len(m.Input) != 1 {
		t.Fatalf("result=%+v", result)
	}
}

func TestOllamaMetadataDiscoveryUsesOnlyShow(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "POST" || r.URL.Path != "/api/show" {
			t.Error("unexpected metadata route")
		}
		fmt.Fprint(w, `{"model_info":{"test.context_length":4096},"capabilities":["completion"]}`)
	}))
	defer server.Close()
	result := DiscoverModelMetadata(context.Background(), managedconfig.Connection{Protocol: "ollama", BaseURL: server.URL + "/v1"}, "exact", "")
	if result.Source != "ollama/api/show" || result.Metadata.ContextWindow != 4096 || result.Metadata.Tools == nil || *result.Metadata.Tools || result.Warning == "" || requests.Load() != 1 {
		t.Fatalf("result=%+v", result)
	}
}

func TestSmallPiContextIsReportedWithoutPaddingEngineMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"exact","max_model_len":4096}]}`)
	}))
	defer server.Close()
	result := DiscoverModelMetadata(context.Background(), managedconfig.Connection{Protocol: "openai-compatible", BaseURL: server.URL + "/v1"}, "exact", "")
	if result.Source != "openai/models" || result.Metadata.ContextWindow != 4096 || result.Warning == "" {
		t.Fatalf("small actual engine context was hidden or enlarged: %+v", result)
	}
	cfg := testConfig(t, "managedcheck-pi")
	agent := cfg.Agents["worker"]
	agent.Harness, agent.Model, agent.Connection = "pi", "exact", "local"
	cfg.Agents["worker"] = agent
	cfg.Connections = map[string]managedconfig.Connection{"local": {Protocol: "openai-compatible", BaseURL: server.URL + "/v1"}}
	report := Check(context.Background(), cfg, Options{})
	if report.OK || findCheck(report, "worker", "model.metadata").Status != StatusError {
		t.Fatalf("doctor accepted a context that Pi reserves entirely: %+v", report)
	}
}
