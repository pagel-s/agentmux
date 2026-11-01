package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestArgumentsPreserveNativeConfigAndResume(t *testing.T) {
	server, terminal, err := arguments([]string{"--no-daemon", "--no-alt-screen", "-c", `developer_instructions="role"`, "--config=model_provider=fixture", "--enable", "feature", "--model", "chosen", "resume", "exact-session"})
	if err != nil {
		t.Fatal(err)
	}
	wantServer := []string{"app-server", "--listen", "stdio://", "-c", `developer_instructions="role"`, "--config=model_provider=fixture", "--enable", "feature"}
	if !reflect.DeepEqual(server, wantServer) {
		t.Fatalf("server config lost: %q", server)
	}
	if strings.Contains(strings.Join(terminal, " "), "--no-daemon") || !strings.Contains(strings.Join(terminal, " "), "--model chosen resume exact-session") {
		t.Fatalf("native selection changed: %q", terminal)
	}
	_, terminal, err = arguments([]string{"--", "--no-daemon"})
	if err != nil || !reflect.DeepEqual(terminal, []string{"--", "--no-daemon"}) {
		t.Fatalf("prompt treated as options: %q %v", terminal, err)
	}
}

func TestUnsupportedConfigurationRetainsNativeTerminal(t *testing.T) {
	for _, args := range [][]string{{"--profile", "owner"}, {"--remote", "unix:///owner.sock"}} {
		dir := t.TempDir()
		record := filepath.Join(dir, "args")
		binary := filepath.Join(dir, "codex")
		if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+record+"'\nprintf 'native preserved'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		var output, diagnostics bytes.Buffer
		if err := run(context.Background(), append([]string{binary}, args...), &fakeConnection{}, strings.NewReader(""), &output, &diagnostics); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(record)
		if err != nil || string(got) != strings.Join(args, "\n")+"\n" || output.String() != "native preserved" {
			t.Fatalf("native launch changed: %q %q %v", got, output.String(), err)
		}
		if !strings.Contains(diagnostics.String(), "manual Agentmux inbox") {
			t.Fatalf("missing capability explanation: %q", diagnostics.String())
		}
	}
}

func TestOlderNativeWithoutRemoteRetainsExactOriginalLaunch(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	probeHome := filepath.Join(dir, "probe-home")
	binary := filepath.Join(dir, "old-codex")
	script := "#!/bin/sh\nif [ \"$1\" = --help ]; then\nprintf '%s\\n' \"$CODEX_HOME\" > '" + probeHome + "'\nprintf 'Options:\\n      --model <MODEL>\\n'\nexit 0\nfi\nprintf '%s\\n' \"$@\" > '" + record + "'\nprintf 'old native preserved'\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"--no-daemon", "--no-alt-screen", "--model", "owner-model", "resume", "owner-session"}
	var output, diagnostics bytes.Buffer
	conn := &fakeConnection{}
	if err := run(context.Background(), append([]string{binary}, args...), conn, strings.NewReader(""), &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(record)
	if err != nil || string(got) != strings.Join(args, "\n")+"\n" || output.String() != "old native preserved" {
		t.Fatalf("older native workflow changed: args=%q output=%q err=%v", got, output.String(), err)
	}
	if !strings.Contains(diagnostics.String(), "does not advertise") || !strings.Contains(diagnostics.String(), "manual Agentmux inbox") {
		t.Fatalf("missing capability explanation: %q", diagnostics.String())
	}
	private, err := os.ReadFile(probeHome)
	if err != nil || strings.TrimSpace(string(private)) == os.Getenv("CODEX_HOME") {
		t.Fatalf("probe used owner native home: %q %v", private, err)
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.registrations) != 0 {
		t.Fatal("unsupported CLI advertised native delivery")
	}
}

func TestUnverifiableNativeHelpDoesNotReplaceStandardWorkflow(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "codex")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nif [ \"$1\" = --help ]; then exit 2; fi\nprintf 'native still works'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	if err := run(context.Background(), []string{binary, "--no-daemon"}, &fakeConnection{}, strings.NewReader(""), &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	if output.String() != "native still works" || !strings.Contains(diagnostics.String(), "could not be verified") {
		t.Fatalf("unverifiable native CLI was lost: %q %q", output.String(), diagnostics.String())
	}
}

func TestNativeProxyPreservesApprovalsIDsAndRootOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inputReader, inputWriter := io.Pipe()
	defer inputReader.Close()
	defer inputWriter.Close()
	outputReader, outputWriter := io.Pipe()
	defer outputReader.Close()
	defer outputWriter.Close()
	p := newProxy(inputWriter)
	server := httptest.NewServer(p)
	defer server.Close()
	defer p.close()
	go p.readServer(ctx, outputReader)
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	decoder := json.NewDecoder(inputReader)
	send := func(value any) {
		t.Helper()
		raw, _ := json.Marshal(value)
		if err := ws.Write(ctx, websocket.MessageText, raw); err != nil {
			t.Fatal(err)
		}
	}
	backend := func(value any) {
		t.Helper()
		raw, _ := json.Marshal(value)
		if _, err := outputWriter.Write(append(raw, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	read := func() map[string]any {
		t.Helper()
		_, raw, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err = json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	send(map[string]any{"id": 7, "method": "thread/start", "params": map[string]any{"cwd": "/owner/project", "config": map[string]any{"model": "chosen"}}})
	var request message
	if err = decoder.Decode(&request); err != nil {
		t.Fatal(err)
	}
	if string(request.ID) == "7" {
		t.Fatal("native request ID was not namespaced")
	}
	backend(map[string]any{"id": json.RawMessage(request.ID), "result": map[string]any{"thread": map[string]any{"id": "root", "status": map[string]any{"type": "idle"}}}})
	if response := read(); response["id"] != float64(7) {
		t.Fatalf("native response ID changed: %#v", response)
	}
	backend(map[string]any{"method": "thread/started", "params": map[string]any{"thread": map[string]any{"id": "child", "parentThreadId": "root"}}})
	read()
	p.mu.Lock()
	root := p.current
	p.mu.Unlock()
	if root != "root" {
		t.Fatalf("subagent stole native recipient: %q", root)
	}
	approval := map[string]any{"id": 99, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "root", "command": "owner command"}}
	backend(approval)
	if got := read(); !reflect.DeepEqual(got, map[string]any{"id": float64(99), "method": approval["method"], "params": approval["params"]}) {
		t.Fatalf("approval changed: %#v", got)
	}
	send(map[string]any{"id": 99, "result": map[string]any{"decision": "decline"}})
	if err = decoder.Decode(&request); err != nil || string(request.ID) != "99" || string(request.Result) != `{"decision":"decline"}` {
		t.Fatalf("owner approval response changed: %+v %v", request, err)
	}
	second, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if second != nil {
		second.CloseNow()
	}
	if err == nil {
		t.Fatal("second native terminal was allowed to share the private endpoint")
	}
}

func TestLateAdapterResponseNeverReachesNativeUI(t *testing.T) {
	p := newProxy(io.Discard)
	raw, _ := json.Marshal(map[string]any{"id": p.prefix + "bridge_40", "result": map[string]any{"queuedSubmission": map[string]any{"id": "late"}}})
	if _, forward, err := p.serverMessage(raw); err != nil || forward {
		t.Fatalf("late adapter response escaped: forward=%v err=%v", forward, err)
	}
}

func TestNativeBackgroundTitleCannotReplaceRecipient(t *testing.T) {
	for _, params := range []string{`{"ephemeral":true,"threadSource":"thread_title"}`, `{"ephemeral":true}`, `{"threadSource":"thread_title"}`} {
		var upstream bytes.Buffer
		p := newProxy(&upstream)
		p.current = "human-root"
		client, _ := json.Marshal(message{ID: json.RawMessage(`10`), Method: "thread/start", Params: json.RawMessage(params)})
		if err := p.forwardClient(client); err != nil {
			t.Fatal(err)
		}
		var sent message
		if err := json.Unmarshal(bytes.TrimSpace(upstream.Bytes()), &sent); err != nil {
			t.Fatal(err)
		}
		response, _ := json.Marshal(map[string]any{"id": json.RawMessage(sent.ID), "result": map[string]any{"thread": map[string]any{"id": "internal-title", "status": map[string]any{"type": "idle"}}}})
		if _, forward, err := p.serverMessage(response); err != nil || !forward {
			t.Fatalf("internal native response was not preserved: %v", err)
		}
		if p.current != "human-root" {
			t.Fatalf("native background title stole recipient: %q", p.current)
		}
	}
}
