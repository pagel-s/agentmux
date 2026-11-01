package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the actual append contract against the installed app-server. This
// deliberately uses only a synthetic local Responses provider and private home.
func TestInstalledCodexOwnerContextAcrossNewResumeFork(t *testing.T) {
	if os.Getenv("AGENTMUX_CODEX_NATIVE_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_CODEX_NATIVE_INTEGRATION=1 for installed Codex contracts")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	inputs := make(chan string, 8)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad fixture request", 400)
			return
		}
		raw, _ := json.Marshal(body["input"])
		inputs <- string(raw)
		message := map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": "SYNTHETIC LOCAL ANSWER", "annotations": []any{}}}}
		response := map[string]any{"id": "resp_fixture", "object": "response", "created_at": time.Now().Unix(), "status": "completed", "output": []any{message}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []map[string]any{{"type": "response.created", "response": map[string]any{"id": "resp_fixture", "object": "response", "created_at": time.Now().Unix(), "status": "in_progress", "output": []any{}}}, {"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}}, {"type": "response.output_text.delta", "item_id": "msg_fixture", "output_index": 0, "content_index": 0, "delta": "SYNTHETIC LOCAL ANSWER"}, {"type": "response.output_item.done", "output_index": 0, "item": message}, {"type": "response.completed", "response": response}} {
			encoded, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e["type"], encoded)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer api.Close()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("developer_instructions=\"OWNER_NATIVE_CONFIG_SENTINEL\"\nmodel_provider=\"fixture\"\nmodel=\"fixture-model\"\n[model_providers.fixture]\nname=\"Local fixture\"\nbase_url=%q\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n[analytics]\nenabled=false\n", api.URL+"/v1")
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	first := newInstalledContextFixture(t, ctx, binary, dir, home, "ATTACHMENT_ONE_CONTEXT_SENTINEL")
	root := first.call(t, "thread/start", map[string]any{"cwd": dir, "approvalPolicy": "never", "sandbox": "read-only", "threadSource": "user"})
	id := fixtureThreadID(t, root)
	first.turn(t, id)
	assertContext := func(marker string) {
		t.Helper()
		select {
		case input := <-inputs:
			if !strings.Contains(input, "OWNER_NATIVE_CONFIG_SENTINEL") || !strings.Contains(input, marker) {
				t.Fatalf("native owner instructions or current attachment context lost: owner=%v context=%v", strings.Contains(input, "OWNER_NATIVE_CONFIG_SENTINEL"), strings.Contains(input, marker))
			}
		case <-ctx.Done():
			t.Fatal("missing local model input")
		}
	}
	assertContext("ATTACHMENT_ONE_CONTEXT_SENTINEL")
	first.stop()
	second := newInstalledContextFixture(t, ctx, binary, dir, home, "ATTACHMENT_TWO_CONTEXT_SENTINEL")
	defer second.stop()
	second.call(t, "thread/resume", map[string]any{"threadId": id, "cwd": dir, "excludeTurns": true})
	second.turn(t, id)
	assertContext("ATTACHMENT_TWO_CONTEXT_SENTINEL")
	fork := second.call(t, "thread/fork", map[string]any{"threadId": id, "cwd": dir, "excludeTurns": true})
	forkID := fixtureThreadID(t, fork)
	if forkID == id {
		t.Fatal("fixture did not create a native fork")
	}
	second.turn(t, forkID)
	assertContext("ATTACHMENT_TWO_CONTEXT_SENTINEL")
}

type installedContextFixture struct {
	p        *proxy
	messages chan message
	ctx      context.Context
	cancel   context.CancelFunc
	command  *exec.Cmd
	stdin    io.WriteCloser
	next     int
	stopped  bool
}

func newInstalledContextFixture(t *testing.T, parent context.Context, binary, dir, home, ownerContext string) *installedContextFixture {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	command := exec.CommandContext(ctx, binary, "app-server", "--listen", "stdio://")
	command.Dir = dir
	command.Env = append(os.Environ(), "CODEX_HOME="+home, "HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_CACHE_HOME="+dir)
	configureServerProcess(command)
	command.Stderr = io.Discard
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	f := &installedContextFixture{p: newProxy(stdin), messages: make(chan message, 256), ctx: ctx, cancel: cancel, command: command, stdin: stdin}
	f.p.ctx = ctx
	f.p.ownerContext = ownerContext
	go func() {
		scan := bufio.NewScanner(stdout)
		scan.Buffer(make([]byte, 64<<10), maxMessage)
		for scan.Scan() {
			data, forward, err := f.p.serverMessage(scan.Bytes())
			if err != nil {
				return
			}
			if forward {
				var m message
				if json.Unmarshal(data, &m) == nil {
					select {
					case f.messages <- m:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	f.call(t, "initialize", map[string]any{"clientInfo": map[string]any{"name": "agentmux_context_fixture", "title": "Fixture", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}})
	if err = f.p.forwardClient([]byte(`{"method":"initialized","params":{}}`)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.stop)
	return f
}
func (f *installedContextFixture) stop() {
	if !f.stopped {
		f.stopped = true
		f.cancel()
		f.stdin.Close()
		f.command.Wait()
	}
}
func (f *installedContextFixture) call(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	f.next++
	id := fmt.Sprintf("fixture-%d", f.next)
	raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := f.p.forwardClient(raw); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case m := <-f.messages:
			var got string
			json.Unmarshal(m.ID, &got)
			if got == id {
				if m.Error != nil {
					t.Fatal(m.Error)
				}
				return m.Result
			}
		case <-f.ctx.Done():
			t.Fatalf("native fixture RPC timed out: %s", method)
			return nil
		}
	}
}
func (f *installedContextFixture) turn(t *testing.T, id string) {
	t.Helper()
	f.call(t, "turn/start", map[string]any{"threadId": id, "input": []map[string]any{{"type": "text", "text": "SYNTHETIC USER FIXTURE"}}})
	for {
		select {
		case m := <-f.messages:
			if m.Method == "turn/completed" {
				var e struct {
					ThreadID string `json:"threadId"`
				}
				json.Unmarshal(m.Params, &e)
				if e.ThreadID == id {
					return
				}
			}
		case <-f.ctx.Done():
			t.Fatal("native fixture turn timed out")
			return
		}
	}
}
func fixtureThreadID(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var response struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Thread.ID == "" {
		t.Fatal("missing native fixture root identity")
	}
	return response.Thread.ID
}
