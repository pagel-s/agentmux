//go:build linux

package agybridge_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentmux/internal/agybridge"
	"agentmux/internal/harness"
	"agentmux/internal/managedsetup"
	"agentmux/internal/sandbox"
)

// Real Pi + Bubblewrap + fake native agy verifies that returned OpenAI tool
// calls run only in the calling agent's workspace, with no paid model calls.
func TestPiSandboxBridgeExecutesToolOnlyInsideAgent(t *testing.T) {
	if os.Getenv("AGENTMUX_PI_SANDBOX_LIVE") != "1" {
		t.Skip("set AGENTMUX_PI_SANDBOX_LIVE=1 for actual Pi/Bubblewrap with fake agy")
	}
	opts := agybridge.Options{Key: bridgeTestKey, Models: []string{"exact-flash"}, Command: []string{os.Args[0], "-test.run=TestNativeProcess", "--"}, Env: map[string]string{"AGENTMUX_AGY_BRIDGE_HELPER": "1", "AGENTMUX_AGY_BRIDGE_TOOL_FIXTURE": "1"}, Timeout: 10 * time.Second, MaxRequests: 2}
	request, stop, count := piBridgeRequest(t, opts, true)
	defer stop()
	request.Prompt = "Use the declared bash tool to write the bridge acceptance marker, then report success."
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := harness.Run(ctx, request)
	if err != nil {
		t.Fatalf("bridge/Pi tool roundtrip failed: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(request.Workdir, "bridge-proof.txt"))
	if err != nil || string(marker) != "BRIDGE_TOOL_OK" || strings.TrimSpace(result.Text) != "BRIDGE_TOOL_OK" || count.Load() != 2 {
		t.Fatalf("workspace proof=%q error=%v response=%q requests=%d", marker, err, result.Text, count.Load())
	}
	if _, err := os.Stat("/workspace/bridge-proof.txt"); err == nil {
		t.Fatal("tool marker appeared in the host's /workspace")
	}
	t.Log("actual isolated Pi executed the validated bash call; native bridge helper executed no tools")
}

// This explicit opt-in makes at most TWO paid/native Flash calls. No retries
// can exceed the bridge's finite allowance. Native auth stays on the host;
// Pi's conversation and writable tools remain in its separate Bubblewrap home.
func TestAntigravityBridgeRealPiPersistentTwoTurns(t *testing.T) {
	if os.Getenv("AGENTMUX_AGY_PI_LIVE") != "1" {
		t.Skip("set AGENTMUX_AGY_PI_LIVE=1 to authorize at most two native Flash calls")
	}
	env := map[string]string{}
	for _, name := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"} {
		if value := os.Getenv(name); value != "" {
			env[name] = value
		}
	}
	opts := agybridge.Options{Key: bridgeTestKey, Models: []string{"gemini-3.8-flash-low"}, Command: []string{"agy"}, Env: env, Timeout: 40 * time.Second, MaxRequests: 2}
	request, stop, count := piBridgeRequest(t, opts, false)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	request.Prompt = "Remember the exact marker PRIVATE_BRIDGE_72af. Reply exactly BRIDGE_ONE."
	first, err := harness.Run(ctx, request)
	if err != nil {
		t.Fatalf("first isolated Pi/native bridge turn failed: %v", err)
	}
	if strings.TrimSpace(first.Text) != "BRIDGE_ONE" || first.SessionID == "" || count.Load() != 1 {
		t.Fatalf("first response mismatch; requests=%d sessionpresent=%t", count.Load(), first.SessionID != "")
	}
	request.Conversation = &harness.Conversation{Mode: "resume", ID: first.SessionID, Dir: request.Conversation.Dir}
	request.Prompt = "What exact private marker did I ask you to remember in the preceding turn? Reply only with that marker."
	second, err := harness.Run(ctx, request)
	if err != nil {
		t.Fatalf("second isolated Pi/native bridge turn failed: %v", err)
	}
	if strings.TrimSpace(second.Text) != "PRIVATE_BRIDGE_72af" || second.SessionID != first.SessionID || count.Load() != 2 {
		t.Fatalf("private context not preserved; requests=%d sameID=%t", count.Load(), second.SessionID == first.SessionID)
	}
	t.Logf("two real native Flash bridge calls + separate isolated Pi sessions passed; requests=%d, no native tools enabled", count.Load())
}

const bridgeTestKey = "private-bridge-fixture-key-12345678901234567890"

func piBridgeRequest(t *testing.T, opts agybridge.Options, tools bool) (harness.Request, func(), *atomic.Int32) {
	t.Helper()
	command, deps, err := managedsetup.Discover("pi", "")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "amx-agy-pi-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	state, workspace := filepath.Join(root, "state"), filepath.Join(root, "work")
	for _, dir := range []string{state, workspace} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	credential := filepath.Join(state, "agent.json")
	if err := os.WriteFile(credential, []byte(`{"token":"fixture-agent-only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(state, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	bridge, err := agybridge.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	count := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			count.Add(1)
		}
		bridge.ServeHTTP(w, r)
	}))
	sessions := filepath.Join(state, "sessions", "worker", "g-1")
	if err := os.MkdirAll(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	command = append(command, "--no-extensions", "--no-context-files", "--no-skills", "--no-prompt-templates", "--no-approve", "--offline")
	if tools {
		command = append(command, "--tools", "bash")
	} else {
		command = append(command, "--no-tools")
	}
	request := harness.Request{Harness: "pi", Model: opts.Models[0], BaseURL: server.URL + "/v1", APIKeyEnv: "BRIDGE_PRIVATE_KEY", Env: map[string]string{"BRIDGE_PRIVATE_KEY": bridgeTestKey, "PI_TELEMETRY": "0"}, Command: command, Workdir: workspace, Conversation: &harness.Conversation{Mode: "create", Dir: sessions}, Wrap: func(spec harness.Spec) (harness.Spec, error) {
		return sandbox.Wrap(spec, sandbox.Options{Mode: "bubblewrap", Network: "host", Workspace: workspace, StateDir: state, CredentialFile: credential, SocketPath: socket, ReadOnlyPaths: deps})
	}}
	return request, server.Close, count
}
