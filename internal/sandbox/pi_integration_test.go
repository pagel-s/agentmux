//go:build linux

package sandbox_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentmux/internal/harness"
	"agentmux/internal/sandbox"
)

// This regression needs actual Pi RPC and filesystem writes. A get_state-only
// or help-only check misses an auth.json write when a prompt starts. The model
// endpoint here is a local fake SSE server; no provider account is contacted.
func TestPiSandboxRPCPromptUsesWritablePrivateAgentDirectory(t *testing.T) {
	runPiSandboxRPC(t, false)
}

func TestPiSandboxPersistentConversationRetainsTwoTurnContext(t *testing.T) {
	runPiSandboxRPC(t, true)
}

func runPiSandboxRPC(t *testing.T, persistent bool) {
	t.Helper()
	if os.Getenv("AGENTMUX_PI_SANDBOX_LIVE") != "1" {
		t.Skip("set AGENTMUX_PI_SANDBOX_LIVE=1 for installed Pi + real bubblewrap + local fake SSE")
	}
	pi, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	cli, err := filepath.EvalSymlinks(pi)
	if err != nil || filepath.Ext(cli) != ".js" {
		t.Fatal("installed Pi must resolve to its real JavaScript entry point")
	}
	packageRoot := filepath.Dir(cli)
	for depth := 0; depth < 8; depth++ {
		if info, err := os.Stat(filepath.Join(packageRoot, "package.json")); err == nil && info.Mode().IsRegular() {
			break
		}
		parent := filepath.Dir(packageRoot)
		if parent == packageRoot || depth == 7 {
			t.Fatal("could not identify installed Pi package root")
		}
		packageRoot = parent
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	node, err = filepath.EvalSymlinks(node)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "amx-pi-sandbox-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	state, workspace := filepath.Join(root, "state"), filepath.Join(root, "work")
	for _, directory := range []string{state, workspace} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	credential := filepath.Join(state, "agent.json")
	if err := os.WriteFile(credential, []byte(`{"token":"fixture-only-no-provider-access"}`), 0600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(state, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var requests atomic.Int32
	var priorContextSeen atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sequence := requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "unexpected API route", http.StatusNotFound)
			return
		}
		var request struct {
			Model    string            `json:"model"`
			Stream   bool              `json:"stream"`
			Messages []json.RawMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != "agentmux-sandbox-test" || !request.Stream {
			http.Error(w, "unexpected model request", http.StatusBadRequest)
			return
		}
		if persistent && sequence == 2 {
			messages, _ := json.Marshal(request.Messages)
			if !strings.Contains(string(messages), "FIRST_TURN_MARKER_29a1") || !strings.Contains(string(messages), "PI_SANDBOX_RPC_OK") {
				http.Error(w, "previous native context is absent", http.StatusBadRequest)
				return
			}
			priorContextSeen.Store(true)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"id":"chatcmpl-agentmux","object":"chat.completion.chunk","created":1,"model":"agentmux-sandbox-test","choices":[{"index":0,"delta":{"role":"assistant","content":"PI_SANDBOX_RPC_OK"},"finish_reason":null}]}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"id":"chatcmpl-agentmux","object":"chat.completion.chunk","created":1,"model":"agentmux-sandbox-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "data: [DONE]")
		fmt.Fprintln(w)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := sandbox.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	configSource := ""
	req := harness.Request{
		Harness: "pi", Model: "agentmux-sandbox-test", Prompt: "Reply with PI_SANDBOX_RPC_OK.",
		BaseURL: server.URL + "/v1", Workdir: workspace,
		Command: []string{node, cli, "--no-tools", "--no-extensions", "--no-context-files", "--no-skills", "--no-prompt-templates", "--no-approve", "--offline"},
		Env:     map[string]string{"PI_TELEMETRY": "0"},
		Wrap: func(spec harness.Spec) (harness.Spec, error) {
			for _, file := range spec.HomeFiles {
				if file.Target == "/home/agent/.pi/agent/models.json" {
					configSource = file.Source
				}
			}
			return sandbox.Wrap(spec, sandbox.Options{Mode: "bubblewrap", Network: "host", Workspace: workspace, StateDir: state, CredentialFile: credential, SocketPath: socket, ReadOnlyPaths: []string{node, packageRoot}})
		},
	}
	if persistent {
		dir := filepath.Join(state, "sessions", "worker", "g-1")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		req.Conversation = &harness.Conversation{Mode: "create", Dir: dir}
		req.Prompt = "Remember the private conversation marker FIRST_TURN_MARKER_29a1. Reply with PI_SANDBOX_RPC_OK."
	}
	result, err := harness.Run(ctx, req)
	if configSource == "" {
		t.Fatal("endpoint models.json was not passed as a file-only private-home resource")
	}
	if _, statErr := os.Stat(configSource); !os.IsNotExist(statErr) {
		t.Fatalf("generated endpoint configuration survived harness cleanup: %v", statErr)
	}
	if err != nil {
		t.Fatalf("sandboxed Pi prompt failed: %v (events=%d)", err, len(result.Events))
	}
	if result.Text != "PI_SANDBOX_RPC_OK" || result.SessionID == "" || requests.Load() != 1 {
		t.Fatalf("unexpected sandboxed Pi result: text=%q session=%q requests=%d", result.Text, result.SessionID, requests.Load())
	}
	stateSeen, promptAccepted, settled := false, false, false
	for _, raw := range result.Events {
		var event struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Success bool   `json:"success"`
		}
		if json.Unmarshal(raw, &event) != nil {
			t.Fatal("invalid Pi RPC event")
		}
		stateSeen = stateSeen || (event.Type == "response" && event.Command == "get_state" && event.Success)
		promptAccepted = promptAccepted || (event.Type == "response" && event.Command == "prompt" && event.Success)
		settled = settled || event.Type == "agent_settled"
	}
	if !stateSeen || !promptAccepted || !settled {
		t.Fatalf("incomplete real Pi RPC lifecycle: state=%t prompt=%t settled=%t", stateSeen, promptAccepted, settled)
	}
	if persistent {
		req.Conversation = &harness.Conversation{Mode: "resume", ID: result.SessionID, Dir: req.Conversation.Dir}
		req.Prompt = "Use the earlier conversation context for this follow-up."
		second, err := harness.Run(ctx, req)
		if err != nil || second.SessionID != result.SessionID || second.Text != "PI_SANDBOX_RPC_OK" || requests.Load() != 2 || !priorContextSeen.Load() {
			t.Fatalf("persistent native Pi conversation was not preserved: error=%v sameID=%t requests=%d context=%t", err, second.SessionID == result.SessionID, requests.Load(), priorContextSeen.Load())
		}
		if _, err := os.Stat(configSource); !os.IsNotExist(err) {
			t.Fatal("second-turn endpoint config survived cleanup")
		}
	}
}
