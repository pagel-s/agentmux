//go:build linux

package workflow

import (
	"context"
	"crypto/x509"
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

// This is an installed SDK conformance test, not a live model test. A private
// Responses fixture is the only network destination admitted to the namespace.
func TestWorkflowIsolationRealInstalledCodexInspectedHTTPS(t *testing.T) {
	if os.Getenv("AGENTMUX_CODEX_NATIVE_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_CODEX_NATIVE_INTEGRATION=1 for installed Codex HTTPS conformance")
	}
	requireFullWorkflowIsolation(t)
	native, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("installed Codex CLI unavailable")
	}
	command, dependencies, err := managedsetup.Discover("codex", native)
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
	const ownerSentinel = "OWNER_CONFIG_MUST_NOT_REACH_TLS_FIXTURE"
	if err := os.WriteFile(filepath.Join(ownerHome, "config.toml"), []byte("developer_instructions=\""+ownerSentinel+"\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", ownerHome)
	t.Setenv("CODEX_HOME", ownerHome)
	t.Setenv("OPENAI_API_KEY", "ambient-key-must-not-reach-native")
	versionContext, closeVersion := context.WithTimeout(context.Background(), 5*time.Second)
	version := exec.CommandContext(versionContext, command[0], append(append([]string(nil), command[1:]...), "--version")...)
	version.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + ownerHome, "CODEX_HOME=" + ownerHome}
	installedVersion, err := version.Output()
	closeVersion()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(installedVersion)), "codex") {
		t.Fatalf("installed native version unavailable: %v", err)
	}
	t.Logf("installed %s; synthetic HTTPS only", strings.TrimSpace(string(installedVersion)))

	const answer = "INSTALLED_CODEX_INSPECTED_TLS_OK"
	const fixtureKey = "synthetic-https-only-key"
	var mutex sync.Mutex
	var modelInputs []string
	var fixtureErrors []string
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		if r.Method != "POST" || r.URL.Path != "/v1/responses" || len(modelInputs) >= 2 || r.Header.Get("Authorization") != "Bearer "+fixtureKey {
			fixtureErrors = append(fixtureErrors, "unexpected fixture route, allowance or synthetic authorization")
			http.Error(w, "synthetic route denied", http.StatusForbidden)
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		var request map[string]json.RawMessage
		if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &request) != nil {
			fixtureErrors = append(fixtureErrors, "invalid bounded Responses request")
			http.Error(w, "invalid synthetic request", http.StatusBadRequest)
			return
		}
		modelInputs = append(modelInputs, string(request["input"]))
		message := map[string]any{"id": "msg_https_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": answer, "annotations": []any{}}}}
		response := map[string]any{"id": "resp_https_fixture", "object": "response", "created_at": time.Now().Unix(), "status": "completed", "output": []any{message}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}
		w.Header().Set("Content-Type", "text/event-stream")
		for index, event := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": response["id"], "object": "response", "created_at": time.Now().Unix(), "status": "in_progress", "output": []any{}}},
			{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": message["id"], "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}},
			{"type": "response.output_text.delta", "item_id": message["id"], "output_index": 0, "content_index": 0, "delta": answer},
			{"type": "response.output_item.done", "output_index": 0, "item": message},
			{"type": "response.completed", "response": response},
		} {
			event["sequence_number"] = index
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
			w.(http.Flusher).Flush()
		}
	}))
	defer api.Close()
	if err := api.Certificate().VerifyHostname("example.com"); err != nil {
		t.Fatal("local TLS fixture must identify the pinned synthetic hostname", err)
	}
	_, port, err := net.SplitHostPort(strings.TrimPrefix(api.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	origin := "https://example.com:" + port
	roots := x509.NewCertPool()
	roots.AddCert(api.Certificate())
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	socket := filepath.Join(proxyDirectory, "egress.sock")
	proxy, err := egress.StartStrict(ctx, socket, []egress.Endpoint{{Origin: origin, IPs: []string{"127.0.0.1"}}}, egress.StrictOptions{UpstreamRoots: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	certificate := filepath.Join(proxyDirectory, "public-ca.pem")
	if err := os.WriteFile(certificate, proxy.PublicCertificate(), 0600); err != nil {
		t.Fatal(err)
	}
	request := harness.Request{Harness: "codex", Model: "fixture-model", Command: command, BaseURL: origin + "/v1", APIKeyEnv: "AGENTMUX_TLS_FIXTURE_KEY", Env: map[string]string{"AGENTMUX_TLS_FIXTURE_KEY": fixtureKey, "CODEX_HOME": ownerHome}, Prompt: "Return only the synthetic fixture marker.", Workdir: workspace, ExternalSandbox: true, ReapDescendants: true, Wrap: func(spec harness.Spec) (harness.Spec, error) {
		// These exec options disable configuration/session reuse; they never
		// select the owner's default provider or read a native login snapshot.
		position := -1
		for i, argument := range spec.Args {
			if argument == "exec" {
				position = i + 1
				break
			}
		}
		if position < 0 {
			return harness.Spec{}, fmt.Errorf("native fixture requires Codex exec argv")
		}
		spec.Args = append(append(append([]string(nil), spec.Args[:position]...), "--ignore-user-config", "--ignore-rules", "--ephemeral", "--skip-git-repo-check"), spec.Args[position:]...)
		return sandbox.WrapWorkflow(spec, sandbox.WorkflowOptions{Workspace: workspace, StateDir: state, Network: "allowlist", EgressSocket: socket, RelayExecutable: relay, EgressCA: certificate, ReadOnlyPaths: dependencies})
	}}
	result, runErr := harness.Run(ctx, request)
	mutex.Lock()
	defer mutex.Unlock()
	if runErr != nil || result.Text != answer || len(result.Events) == 0 || len(modelInputs) != 1 || len(fixtureErrors) != 0 {
		t.Fatalf("installed Codex strict TLS conformance failed: error=%v text=%q events=%d requests=%d fixture=%v", runErr, result.Text, len(result.Events), len(modelInputs), fixtureErrors)
	}
	if strings.Contains(modelInputs[0], ownerSentinel) || strings.Contains(modelInputs[0], "ambient-key-must-not-reach-native") {
		t.Fatal("owner configuration or ambient credential reached the local model fixture")
	}
	var completed bool
	for _, raw := range result.Events {
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &event) == nil && event.Type == "turn.completed" {
			completed = true
		}
	}
	if !completed {
		t.Fatal("installed native turn completed without a recognized completion event")
	}
}
