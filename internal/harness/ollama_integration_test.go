//go:build linux

package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentmux/internal/harness"
	"agentmux/internal/managedcheck"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedsetup"
	"agentmux/internal/modelmeta"
	"agentmux/internal/sandbox"
)

// Explicit opt-in uses an already running real Ollama server, optionally via
// an owner-created SSH loopback forward. No installation, model downloads,
// service restarts or global option changes occur. At most two upstream model
// requests are admitted, each requesting no more than the selected output bound
// (64 by default, at most 256 for an explicit owner-selected local test).
func TestPiRealOllamaPrivatePersistentTwoTurns(t *testing.T) {
	if os.Getenv("AGENTMUX_OLLAMA_LIVE") != "1" {
		t.Skip("set AGENTMUX_OLLAMA_LIVE=1 with an existing owner-selected Ollama service")
	}
	endpoint := os.Getenv("AGENTMUX_OLLAMA_URL")
	model := os.Getenv("AGENTMUX_OLLAMA_MODEL")
	runRealCompatiblePrivatePersistent(t, endpoint, model, "ollama", "ollama/api/show")
}

func TestPiRealVLLMPrivatePersistentTwoTurns(t *testing.T) {
	if os.Getenv("AGENTMUX_VLLM_LIVE") != "1" {
		t.Skip("set AGENTMUX_VLLM_LIVE=1 with an existing owner-selected vLLM service")
	}
	runRealCompatiblePrivatePersistent(t, os.Getenv("AGENTMUX_VLLM_URL"), os.Getenv("AGENTMUX_VLLM_MODEL"), "openai-compatible", "openai/models")
}

func runRealCompatiblePrivatePersistent(t *testing.T, endpoint, model, protocol, metadataSource string) {
	t.Helper()
	base, err := url.Parse(endpoint)
	if err != nil || base.Scheme != "http" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || net.ParseIP(base.Hostname()) == nil || !net.ParseIP(base.Hostname()).IsLoopback() || model == "" {
		t.Fatal("select exact model and numeric HTTP loopback API base")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	maxOutput := 64
	if value := os.Getenv("AGENTMUX_ENGINE_MAX_OUTPUT"); value != "" {
		maxOutput, err = strconv.Atoi(value)
		if err != nil || maxOutput < 1 || maxOutput > 256 {
			t.Fatal("explicit engine output bound must be 1..256")
		}
	}
	no := false
	// Use the engine's discovered real context window. A fixture override of
	// 4096 caused Pi 1.4.0's 4096-token safety reserve to clamp output to one
	// token, independently of the advertised output allowance.
	connection := managedconfig.Connection{Protocol: protocol, BaseURL: endpoint, ModelMetadata: modelmeta.Metadata{MaxOutput: maxOutput, Input: []string{"text"}, Reasoning: &no, Tools: &no}}
	key := ""
	if path := os.Getenv("AGENTMUX_ENGINE_KEY_FILE"); path != "" {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 256 {
			t.Fatal("engine key fixture must be an explicit private file")
		}
		value, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("engine key fixture unavailable")
		}
		key = strings.TrimSpace(string(value))
	}
	discovery := managedcheck.DiscoverModelMetadata(ctx, connection, model, key)
	if discovery.Source != metadataSource || discovery.Warning != "" {
		t.Fatalf("real endpoint metadata unavailable: source=%s warning=%s", discovery.Source, discovery.Warning)
	}
	var requests atomic.Int32
	proxy := httputil.NewSingleHostReverseProxy(base)
	proxy.Transport = &http.Transport{Proxy: nil}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "upstream Ollama transport failed", 502)
	}
	// NewSingleHostReverseProxy normally joins base.Path; the calling API base
	// already supplies /v1, so keep the exact incoming OpenAI route instead.
	proxy.Director = func(r *http.Request) { r.URL.Scheme = base.Scheme; r.URL.Host = base.Host; r.Host = base.Host }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != "POST" || requests.Add(1) > 2 {
			http.Error(w, "bounded fixture route/allowance", 429)
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 512*1024))
		if err != nil {
			http.Error(w, "body read failed", 400)
			return
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(data, &payload) != nil {
			http.Error(w, "JSON required", 400)
			return
		}
		var output int
		json.Unmarshal(payload["max_tokens"], &output)
		if output == 0 {
			json.Unmarshal(payload["max_completion_tokens"], &output)
		}
		if output < 1 || output > maxOutput {
			http.Error(w, "Pi did not enforce selected output request bound", 400)
			return
		}
		var stream bool
		json.Unmarshal(payload["stream"], &stream)
		t.Logf("sanitized upstream request: context=%d max_output=%d stream=%t", discovery.Metadata.Effective().ContextWindow, output, stream)
		// Pin deterministic sampling for this tiny-model transport fixture.
		// This owner-controlled test proxy changes no service/global setting.
		payload["temperature"] = json.RawMessage("0")
		data, err = json.Marshal(payload)
		if err != nil {
			http.Error(w, "fixture encoding failed", 500)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		r.ContentLength = int64(len(data))
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	command, deps, err := managedsetup.Discover("pi", "")
	if err != nil {
		t.Fatal(err)
	}
	command = append(command, "--no-tools", "--no-extensions", "--no-context-files", "--no-skills", "--no-prompt-templates", "--no-approve", "--offline")
	// This transport/continuation fixture also supports tiny CPU models. A
	// minimal owner-selected system prompt avoids spending its short output
	// allowance responding to Pi's full developer-assistant instructions.
	command = append(command, "--system-prompt", "You are a concise assistant. Follow exact output instructions and remember previous messages.")
	root, err := os.MkdirTemp("", "amx-ollama-pi-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	state, workspace := filepath.Join(root, "state"), filepath.Join(root, "work")
	for _, dir := range []string{state, workspace} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	credential := filepath.Join(state, "agent.json")
	os.WriteFile(credential, []byte(`{"token":"fixture-local-only"}`), 0600)
	socket := filepath.Join(state, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	sessions := filepath.Join(state, "sessions", "worker", "g-1")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	request := harness.Request{Harness: "pi", Model: model, BaseURL: server.URL + "/v1", ModelMetadata: discovery.Metadata, Env: map[string]string{"PI_TELEMETRY": "0"}, Command: command, Workdir: workspace, Conversation: &harness.Conversation{Mode: "create", Dir: sessions}, Prompt: "Remember the exact marker PRIVATE_ENDPOINT_7ab2. Reply only ENDPOINT_ONE.", Wrap: func(spec harness.Spec) (harness.Spec, error) {
		return sandbox.Wrap(spec, sandbox.Options{Mode: "bubblewrap", Network: "host", Workspace: workspace, StateDir: state, CredentialFile: credential, SocketPath: socket, ReadOnlyPaths: deps})
	}}
	if key != "" {
		request.APIKeyEnv = "ENGINE_PRIVATE_KEY"
		request.Env[request.APIKeyEnv] = key
	}
	first, err := harness.Run(ctx, request)
	t.Logf("first synthetic turn: events=%d output_bytes=%d usage=%s", len(first.Events), len(first.Text), first.Usage)
	if err != nil {
		t.Fatalf("first real endpoint/Pi turn failed: %v; synthetic reply=%q", err, first.Text)
	}
	if !strings.Contains(first.Text, "ENDPOINT_ONE") || first.SessionID == "" || requests.Load() != 1 {
		t.Fatalf("first task contract failed; response=%q requests=%d", first.Text, requests.Load())
	}
	request.Conversation = &harness.Conversation{Mode: "resume", ID: first.SessionID, Dir: sessions}
	request.Prompt = "What exact marker did I ask you to remember in the preceding turn? Reply only with that marker."
	second, err := harness.Run(ctx, request)
	t.Logf("second synthetic turn: events=%d output_bytes=%d usage=%s", len(second.Events), len(second.Text), second.Usage)
	if err != nil {
		t.Fatalf("second real endpoint/Pi turn failed: %v", err)
	}
	if !strings.Contains(second.Text, "PRIVATE_ENDPOINT_7ab2") || second.SessionID != first.SessionID || requests.Load() != 2 {
		t.Fatalf("private context failed; response=%q sameID=%t requests=%d", second.Text, second.SessionID == first.SessionID, requests.Load())
	}
	t.Logf("real compatible engine model=%s metadata=%s isolated Pi context retained across two processes; upstream requests=%d, requestedoutput<=%deach", model, discovery.Source, requests.Load(), maxOutput)
}
