package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"agentmux/internal/toolruntime"
)

func TestHandleRequestInitialize(t *testing.T) {
	server := New("agentmux test", toolruntime.Executor{})

	resp, ok := server.handleRequest([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if !ok {
		t.Fatal("handleRequest returned ok=false, want response")
	}
	if resp.Error != nil {
		t.Fatalf("resp.Error = %#v, want nil", resp.Error)
	}

	result, ok := resp.Result.(initializeResult)
	if !ok {
		t.Fatalf("resp.Result type = %T, want initializeResult", resp.Result)
	}
	if result.ProtocolVersion != protocolVersion {
		t.Fatalf("ProtocolVersion = %q, want %q", result.ProtocolVersion, protocolVersion)
	}
	if result.ServerInfo.Name != "agentmux" {
		t.Fatalf("ServerInfo.Name = %q, want %q", result.ServerInfo.Name, "agentmux")
	}
}

func TestHandleToolCallReturnsStructuredContent(t *testing.T) {
	server := New("agentmux test", toolruntime.Executor{
		Getenv: func(string) string { return "" },
		GetKV: func(session string, key string, verbose bool) (string, error) {
			if session != "demo" || key != "task" {
				t.Fatalf("GetKV(%q, %q) called, want (%q, %q)", session, key, "demo", "task")
			}
			return "go", nil
		},
	})

	result, err := server.handleToolCall(json.RawMessage(`{"name":"kv_get","arguments":{"session":"demo","key":"task"}}`))
	if err != nil {
		t.Fatalf("handleToolCall returned error: %v", err)
	}
	if got, ok := result["structuredContent"].(map[string]any); !ok || got["key"] != "task" {
		t.Fatalf("structuredContent = %#v, want decoded kv-get payload", result["structuredContent"])
	}
	content, ok := result["content"].([]map[string]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v, want one text entry", result["content"])
	}
	if !strings.Contains(content[0]["text"].(string), `"value": "go"`) {
		t.Fatalf("content text = %q, want pretty JSON result", content[0]["text"])
	}
}
