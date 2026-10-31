package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// This opt-in conformance check exercises an actual installed Pi CLI against a
// local fake model. It never contacts a model provider or downloads weights.
func TestPiCLIWithCompatibleEndpoint(t *testing.T) {
	if os.Getenv("AGENTMUX_PI_LIVE") != "1" {
		t.Skip("set AGENTMUX_PI_LIVE=1 to check installed Pi against a local mock endpoint")
	}
	binary, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected API route", http.StatusNotFound)
			return
		}
		var request struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != "agentmux-test" || !request.Stream {
			http.Error(w, "unexpected model request", http.StatusBadRequest)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"id":"chatcmpl-agentmux","object":"chat.completion.chunk","created":1,"model":"agentmux-test","choices":[{"index":0,"delta":{"role":"assistant","content":"PI_RPC_OK"},"finish_reason":null}]}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"id":"chatcmpl-agentmux","object":"chat.completion.chunk","created":1,"model":"agentmux-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "data: [DONE]")
		fmt.Fprintln(w)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := Run(ctx, Request{
		Harness: "pi", Model: "agentmux-test", Prompt: "Reply with PI_RPC_OK.",
		BaseURL: server.URL + "/v1", Workdir: t.TempDir(),
		Command: []string{binary, "--no-tools", "--no-extensions", "--no-context-files", "--no-skills", "--no-prompt-templates", "--no-approve", "--offline"},
		Env:     map[string]string{"PI_TELEMETRY": "0"},
	})
	if err != nil {
		t.Fatalf("Pi CLI request failed: %v (events=%d, text=%q)", err, len(result.Events), result.Text)
	}
	if result.Text != "PI_RPC_OK" || result.SessionID == "" || requests.Load() != 1 {
		t.Fatalf("Pi CLI result=%#v, requests=%d", result, requests.Load())
	}
	t.Logf("Pi CLI endpoint/RPC conformance passed: events=%d, usage=%s", len(result.Events), result.Usage)
}
