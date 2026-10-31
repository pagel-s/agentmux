package agybridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const testKey = "test-private-bridge-key-12345678901234567890"

func TestNativeProcess(t *testing.T) {
	if os.Getenv("AGENTMUX_AGY_BRIDGE_HELPER") != "1" {
		return
	}
	settings := false
	for i, arg := range os.Args {
		if arg == "--settings" && i+1 < len(os.Args) && os.Args[i+1] == denySettings {
			settings = true
		}
		if arg == "--dangerously-skip-permissions" {
			os.Exit(11)
		}
	}
	if !settings {
		os.Exit(12)
	}
	var input map[string]any
	json.NewDecoder(os.Stdin).Decode(&input)
	if input["event"] != "user" {
		os.Exit(13)
	}
	if marker := os.Getenv("AGENTMUX_AGY_BRIDGE_MARKER"); marker != "" {
		os.WriteFile(marker, []byte("started"), 0600)
	}
	if os.Getenv("AGENTMUX_AGY_BRIDGE_WAIT") == "1" {
		time.Sleep(10 * time.Second)
	}
	if os.Getenv("AGENTMUX_AGY_BRIDGE_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "private-native-credential-value")
		os.Exit(7)
	}
	if os.Getenv("AGENTMUX_AGY_BRIDGE_NATIVE_TOOL") == "1" {
		fmt.Println(`{"event":"step_update","step_update":{"step_type":"tool","tool_name":"run_command","state":"ACTIVE"}}`)
	}
	text := os.Getenv("AGENTMUX_AGY_BRIDGE_REPLY")
	if os.Getenv("AGENTMUX_AGY_BRIDGE_TOOL_FIXTURE") == "1" {
		message, _ := input["message"].(map[string]any)
		prompt, _ := message["content"].(string)
		if strings.Contains(prompt, `"role":"tool"`) {
			text = `{"type":"final","text":"BRIDGE_TOOL_OK"}`
		} else {
			text = `{"type":"tool_calls","calls":[{"name":"bash","arguments":{"command":"printf BRIDGE_TOOL_OK > /workspace/bridge-proof.txt"}}]}`
		}
	}
	if text == "" {
		text = `{"type":"final","text":"BRIDGE_OK"}`
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"event": "result", "result": map[string]any{"status": "SUCCESS", "response": text, "conversation_id": "private-native-ref", "usage": map[string]int{"input_tokens": 10, "output_tokens": 3, "total_tokens": 13}}})
	os.Exit(0)
}

func fakeOptions() Options {
	return Options{Key: testKey, Models: []string{"exact-flash"}, Command: []string{os.Args[0], "-test.run=TestNativeProcess", "--"}, Env: map[string]string{"AGENTMUX_AGY_BRIDGE_HELPER": "1"}, Timeout: 2 * time.Second, MaxRequests: 10}
}
func requestBridge(b *Bridge, body, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "http://127.0.0.1/v1/chat/completions", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	record := httptest.NewRecorder()
	b.ServeHTTP(record, req)
	return record
}

const chat = `{"model":"exact-flash","messages":[{"role":"user","content":"Only reply."}],"stream":true}`

func TestAuthenticatedBridgeStreamsNativeAnswerAndUsage(t *testing.T) {
	b, err := New(fakeOptions())
	if err != nil {
		t.Fatal(err)
	}
	result := requestBridge(b, chat, testKey)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "BRIDGE_OK") || !strings.Contains(result.Body.String(), "[DONE]") || !strings.Contains(result.Body.String(), `"prompt_tokens":10`) {
		t.Fatalf("code=%d %s", result.Code, result.Body.String())
	}
	if strings.Contains(result.Body.String(), "private-native-ref") || strings.Contains(result.Body.String(), testKey) {
		t.Fatal("native identity or key leaked")
	}
	for _, key := range []string{"", "wrong-key"} {
		if response := requestBridge(b, chat, key); response.Code != 401 {
			t.Fatal("unauthenticated inference accepted")
		}
	}
}

func TestBridgeRejectsBadModelsSchemasAndArguments(t *testing.T) {
	tool := `{"type":"function","function":{"name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string","maxLength":100}},"required":["command"],"additionalProperties":false}}}`
	body := `{"model":"exact-flash","messages":[{"role":"user","content":"call tool"}],"tools":[` + tool + `]}`
	for _, reply := range []string{`{"type":"tool_calls","calls":[{"name":"unknown","arguments":{}}]}`, `{"type":"tool_calls","calls":[{"name":"bash","arguments":{"command":7}}]}`, `{"type":"tool_calls","calls":[{"name":"bash","arguments":{"command":"true","extra":1}}]}`} {
		opts := fakeOptions()
		opts.Env["AGENTMUX_AGY_BRIDGE_REPLY"] = reply
		b, _ := New(opts)
		if result := requestBridge(b, body, testKey); result.Code != 502 {
			t.Fatalf("invalid tool accepted: %d %s", result.Code, result.Body.String())
		}
	}
	opts := fakeOptions()
	opts.Env["AGENTMUX_AGY_BRIDGE_REPLY"] = `{"type":"tool_calls","calls":[{"name":"bash","arguments":{"command":"touch inside-only"}}]}`
	b, _ := New(opts)
	result := requestBridge(b, body, testKey)
	if result.Code != 200 || !strings.Contains(result.Body.String(), `"finish_reason":"tool_calls"`) || !strings.Contains(result.Body.String(), "touch inside-only") {
		t.Fatalf("valid tool envelope lost: %d %s", result.Code, result.Body.String())
	}
	for _, bad := range []string{strings.Replace(chat, "exact-flash", "outside-model", 1), strings.Replace(body, `"type":"string"`, `"$ref":"unverified"`, 1)} {
		if response := requestBridge(b, bad, testKey); response.Code != 400 {
			t.Fatal("unsupported request accepted")
		}
	}
}

func TestBridgeNativeFailuresAreSecretSafeAndQuotaFinite(t *testing.T) {
	opts := fakeOptions()
	opts.MaxRequests = 1
	opts.Env["AGENTMUX_AGY_BRIDGE_FAIL"] = "1"
	b, _ := New(opts)
	result := requestBridge(b, chat, testKey)
	if result.Code != 502 || strings.Contains(result.Body.String(), "private-native-credential-value") {
		t.Fatal("native diagnostic exposed")
	}
	if result := requestBridge(b, chat, testKey); result.Code != 429 {
		t.Fatal("failed inference did not consume request allowance")
	}
	opts = fakeOptions()
	opts.Env["AGENTMUX_AGY_BRIDGE_REPLY"] = strings.Repeat("x", MaxResponseBytes+1)
	b, _ = New(opts)
	if result := requestBridge(b, chat, testKey); result.Code != 502 {
		t.Fatal("unbounded response accepted")
	}
}

func TestBridgeRejectsNativeToolsAndDoesNotInventUnknownUsage(t *testing.T) {
	opts := fakeOptions()
	opts.Env["AGENTMUX_AGY_BRIDGE_NATIVE_TOOL"] = "1"
	bridge, _ := New(opts)
	if result := requestBridge(bridge, chat, testKey); result.Code != 502 {
		t.Fatal("native tool event accepted in inference-only service")
	}
	for _, raw := range []string{"", `{}`, `{"input_tokens":-1}`, `{"input_tokens":9223372036854775807,"output_tokens":1}`} {
		if usage := usageFields(json.RawMessage(raw)); usage != nil {
			t.Fatalf("invented valid usage from %q", raw)
		}
	}
}

func TestBridgeCancellationStopsInference(t *testing.T) {
	opts := fakeOptions()
	opts.Env["AGENTMUX_AGY_BRIDGE_WAIT"] = "1"
	b, _ := New(opts)
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("POST", "http://127.0.0.1/v1/chat/completions", strings.NewReader(chat)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+testKey)
	done := make(chan struct{})
	go func() { defer close(done); b.ServeHTTP(httptest.NewRecorder(), request) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled request left native inference running")
	}
}

func TestBridgeListenRejectsPublicAndWildcardAddresses(t *testing.T) {
	for _, address := range []string{":1234", "0.0.0.0:1234", "[::]:1234", "example.com:1234", "192.168.1.2:1234"} {
		if listener, err := Listen(address); err == nil {
			listener.Close()
			t.Fatal("non-loopback bind accepted")
		}
	}
}
