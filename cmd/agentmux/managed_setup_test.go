package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"agentmux/internal/managedconfig"
	"agentmux/internal/managedsetup"
)

func TestManagedSetupNoninteractiveWritesPrivateValidatedConfig(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "claude")
	script := `#!/bin/sh
for arg do
 case "$arg" in
  --help) echo '--safe-mode --print --input-format --output-format --verbose --no-session-persistence --permission-mode --model'; exit 0;;
  --version) echo '2.1.316'; exit 0;;
  status) echo '{"loggedIn":true}'; exit 0;;
 esac
done
exit 9
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	spec := managedsetup.Spec{Session: "wizard", WorkspaceRoot: filepath.Join(dir, "workspaces"), Sandbox: "local", BrokerSandbox: "local", SharedBoard: true, Agents: []managedsetup.Agent{{ID: "lead", Harness: "claude", Role: "developer", Binary: binary, Auth: "native-local", CodingTools: true}}}
	data, _ := json.Marshal(spec)
	output := filepath.Join(dir, "team.yaml")
	var out, diagnostics bytes.Buffer
	code := runManagedSetupIO(context.Background(), []string{"--spec", "-", "--output", output}, bytes.NewReader(data), &out, &diagnostics)
	if code != exitSuccess {
		t.Fatalf("code=%d output=%s diagnostics=%s", code, out.String(), diagnostics.String())
	}
	var result managedTestEnvelope
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || !result.OK {
		t.Fatalf("invalid result: %v %s", err, out.String())
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private output: %v", err)
	}
	cfg, err := managedconfig.Load(output)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents["lead"].Role != "developer" || len(cfg.Policy.Grants) != 0 {
		t.Fatal("setup did not preserve role or deny default")
	}
	if _, err := os.Stat(filepath.Join(dir, "workspaces", "lead")); err != nil {
		t.Fatal(err)
	}
}

func TestManagedSetupPreflightFailureDoesNotCreateFiles(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "claude")
	os.WriteFile(binary, []byte("#!/bin/sh\nexit 2\n"), 0700)
	spec := managedsetup.Spec{Session: "wizard", WorkspaceRoot: filepath.Join(dir, "workspaces"), Sandbox: "local", BrokerSandbox: "local", Agents: []managedsetup.Agent{{ID: "lead", Harness: "claude", Binary: binary, Auth: "native-local"}}}
	data, _ := json.Marshal(spec)
	output := filepath.Join(dir, "team.yaml")
	var out bytes.Buffer
	if code := runManagedSetupIO(context.Background(), []string{"--spec", "-", "--output", output}, bytes.NewReader(data), &out, &bytes.Buffer{}); code != exitValidation {
		t.Fatalf("code=%d %s", code, out.String())
	}
	for _, path := range []string{output, spec.WorkspaceRoot} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("failed preflight created files")
		}
	}
}

func TestManagedSetupDoesNotEchoUnknownCredentialValues(t *testing.T) {
	var out bytes.Buffer
	code := runManagedSetupIO(context.Background(), []string{"--spec", "-"}, strings.NewReader(`{"api_key":"never-print-test-secret"}`), &out, &bytes.Buffer{})
	if code != exitValidation || strings.Contains(out.String(), "never-print-test-secret") {
		t.Fatal("unknown credential echoed")
	}
}

func TestManagedSetupInstalledPiMetadataOnly(t *testing.T) {
	if os.Getenv("AGENTMUX_PI_SANDBOX_LIVE") != "1" {
		t.Skip("set AGENTMUX_PI_SANDBOX_LIVE=1 for installed Pi metadata + real namespace probe")
	}
	var generations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" {
			generations.Add(1)
			http.Error(w, "generation forbidden", 400)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"setup-only","max_model_len":16384,"supports_tools":false}]}`)
	}))
	defer server.Close()
	dir := t.TempDir()
	spec := managedsetup.Spec{Session: "installed-pi", WorkspaceRoot: filepath.Join(dir, "workspaces"), Sandbox: "bubblewrap", BrokerSandbox: "bubblewrap", InitGit: true, Agents: []managedsetup.Agent{{ID: "worker", Role: "developer", Harness: "pi", Model: "setup-only", Auth: "none", BaseURL: server.URL + "/v1", Conversation: "persistent"}}}
	data, _ := json.Marshal(spec)
	output := filepath.Join(dir, "private.yaml")
	var out, diagnostics bytes.Buffer
	code := runManagedSetupIO(context.Background(), []string{"--spec", "-", "--output", output}, bytes.NewReader(data), &out, &diagnostics)
	if code != exitSuccess {
		t.Fatalf("native setup failed: code=%d result=%s diagnostics=%s", code, out.String(), diagnostics.String())
	}
	cfg, err := managedconfig.Load(output)
	if err != nil {
		t.Fatal(err)
	}
	agent := cfg.Agents["worker"]
	connection := cfg.Connections[agent.Connection]
	if len(agent.Command) < 2 || !strings.HasSuffix(agent.Command[1], ".js") || len(agent.ReadOnlyPaths) < 2 || connection.ModelMetadata.ContextWindow != 16384 || connection.ModelMetadata.Tools == nil || *connection.ModelMetadata.Tools || generations.Load() != 0 {
		t.Fatal("native closure/metadata was incomplete or setup attempted generation")
	}
}
