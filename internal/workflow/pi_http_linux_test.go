//go:build linux

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentmux/internal/egress"
	"agentmux/internal/harness"
	"agentmux/internal/managedsetup"
	"agentmux/internal/sandbox"
)

// This provider-free installed SDK conformance test exercises Pi's real
// bundled undici proxy dispatcher, which uses CONNECT even for HTTP origins.
// Only a bounded local Chat Completions fixture is reachable from Bubblewrap.
func TestWorkflowIsolationRealInstalledPiInspectedHTTPConnect(t *testing.T) {
	if os.Getenv("AGENTMUX_PI_NATIVE_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_PI_NATIVE_INTEGRATION=1 for installed Pi HTTP CONNECT conformance")
	}
	requireFullWorkflowIsolation(t)
	native, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("installed Pi CLI unavailable")
	}
	command, dependencies, err := managedsetup.Discover("pi", native)
	if err != nil {
		t.Fatal(err)
	}
	relay := os.Getenv("AGENTMUX_INTEGRATION_BINARY")
	if relay == "" {
		t.Fatal("AGENTMUX_INTEGRATION_BINARY is required for actual namespace relay")
	}
	relay, err = filepath.Abs(relay)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	state, workspace := filepath.Join(root, "run"), filepath.Join(root, "run", "worktree")
	proxyDirectory, ownerHome := filepath.Join(root, "proxy"), filepath.Join(root, "owner-home")
	for _, path := range []string{state, workspace, proxyDirectory, ownerHome} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	const ownerSentinel = "OWNER_CONFIG_MUST_NOT_REACH_PI_HTTP_FIXTURE"
	if err := os.WriteFile(filepath.Join(ownerHome, "AGENTS.md"), []byte(ownerSentinel), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", ownerHome)
	t.Setenv("PI_CODING_AGENT_DIR", ownerHome)
	t.Setenv("OPENAI_API_KEY", "ambient-key-must-not-reach-pi")
	versionContext, closeVersion := context.WithTimeout(context.Background(), 5*time.Second)
	version := exec.CommandContext(versionContext, command[0], append(append([]string(nil), command[1:]...), "--version")...)
	version.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + ownerHome, "PI_CODING_AGENT_DIR=" + ownerHome, "PI_OFFLINE=1"}
	installedVersion, err := version.Output()
	closeVersion()
	if err != nil || strings.TrimSpace(string(installedVersion)) == "" {
		t.Fatalf("installed Pi version unavailable: %v", err)
	}
	t.Logf("installed Pi %s; synthetic HTTP CONNECT only", strings.TrimSpace(string(installedVersion)))

	const answer = "INSTALLED_PI_INSPECTED_HTTP_CONNECT_OK"
	const toolMarker = "INSTALLED_PI_PRIVATE_TOOL_OK"
	const fixtureKey = "synthetic-pi-http-only-key"
	var mutex sync.Mutex
	var modelInputs []string
	var fixtureErrors []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || len(modelInputs) >= 2 || r.Header.Get("Authorization") != "Bearer "+fixtureKey {
			fixtureErrors = append(fixtureErrors, "unexpected fixture route, allowance or synthetic authorization")
			http.Error(w, "synthetic route denied", http.StatusForbidden)
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		var request struct {
			Model    string            `json:"model"`
			Stream   bool              `json:"stream"`
			Messages []json.RawMessage `json:"messages"`
		}
		if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &request) != nil || request.Model != "fixture-model" || !request.Stream {
			fixtureErrors = append(fixtureErrors, "invalid bounded Chat Completions request")
			http.Error(w, "invalid synthetic request", http.StatusBadRequest)
			return
		}
		modelInputs = append(modelInputs, string(data))
		delta := map[string]any{"role": "assistant", "content": answer}
		finish := "stop"
		if len(modelInputs) == 1 {
			arguments, _ := json.Marshal(map[string]string{"command": "printf '" + toolMarker + "' > pi-fixture.txt; cat pi-fixture.txt"})
			delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_pi_fixture", "type": "function", "function": map[string]any{"name": "bash", "arguments": string(arguments)}}}}
			finish = "tool_calls"
		} else {
			var hasToolResult bool
			for _, raw := range request.Messages {
				var message struct {
					Role       string `json:"role"`
					Content    string `json:"content"`
					ToolCallID string `json:"tool_call_id"`
				}
				if json.Unmarshal(raw, &message) == nil && message.Role == "tool" && message.ToolCallID == "call_pi_fixture" && strings.Contains(message.Content, toolMarker) {
					hasToolResult = true
				}
			}
			if !hasToolResult {
				fixtureErrors = append(fixtureErrors, "second request omitted the private tool result")
				http.Error(w, "synthetic tool result missing", http.StatusBadRequest)
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for index, choice := range []map[string]any{
			{"index": 0, "delta": delta, "finish_reason": nil},
			{"index": 0, "delta": map[string]any{}, "finish_reason": finish},
		} {
			chunk := map[string]any{"id": fmt.Sprintf("chat_pi_fixture_%d", len(modelInputs)), "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": "fixture-model", "choices": []any{choice}}
			if index == 1 {
				chunk["usage"] = map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		w.(http.Flusher).Flush()
	}))
	defer api.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(api.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://pi-fixture.invalid:" + port
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	socket := filepath.Join(proxyDirectory, "egress.sock")
	proxy, err := egress.StartStrict(ctx, socket, []egress.Endpoint{{Origin: origin, IPs: []string{"127.0.0.1"}}}, egress.StrictOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	certificate := filepath.Join(proxyDirectory, "public-ca.pem")
	if err := os.WriteFile(certificate, proxy.PublicCertificate(), 0600); err != nil {
		t.Fatal(err)
	}
	request := harness.Request{Harness: "pi", Model: "fixture-model", Command: command, BaseURL: origin + "/v1", APIKeyEnv: "AGENTMUX_PI_HTTP_FIXTURE_KEY", Env: map[string]string{"AGENTMUX_PI_HTTP_FIXTURE_KEY": fixtureKey, "PI_CODING_AGENT_DIR": ownerHome}, Prompt: "Use the synthetic fixture tool once, then return the synthetic completion marker.", Workdir: workspace, ReapDescendants: true, Wrap: func(spec harness.Spec) (harness.Spec, error) {
		return sandbox.WrapWorkflow(spec, sandbox.WorkflowOptions{Workspace: workspace, StateDir: state, Network: "allowlist", EgressSocket: socket, RelayExecutable: relay, EgressCA: certificate, ReadOnlyPaths: dependencies})
	}}
	result, runErr := harness.Run(ctx, request)
	mutex.Lock()
	defer mutex.Unlock()
	if runErr != nil || result.Text != answer || len(modelInputs) != 2 || len(fixtureErrors) != 0 {
		t.Fatalf("installed Pi strict HTTP CONNECT conformance failed: error=%v text=%q events=%d requests=%d fixture=%v", runErr, result.Text, len(result.Events), len(modelInputs), fixtureErrors)
	}
	for _, input := range modelInputs {
		if strings.Contains(input, ownerSentinel) || strings.Contains(input, "ambient-key-must-not-reach-pi") {
			t.Fatal("owner configuration or ambient credential reached the local model fixture")
		}
	}
	data, err := os.ReadFile(filepath.Join(workspace, "pi-fixture.txt"))
	if err != nil || string(data) != toolMarker {
		t.Fatalf("installed Pi tool did not write the private fixture marker: %q %v", data, err)
	}
	var toolCompleted, settled bool
	for _, raw := range result.Events {
		var event struct {
			Type    string `json:"type"`
			IsError bool   `json:"isError"`
		}
		if json.Unmarshal(raw, &event) == nil {
			toolCompleted = toolCompleted || event.Type == "tool_execution_end" && !event.IsError
			settled = settled || event.Type == "agent_settled"
		}
	}
	if !toolCompleted || !settled {
		t.Fatal("installed Pi omitted successful tool execution or terminal completion evidence")
	}
}
