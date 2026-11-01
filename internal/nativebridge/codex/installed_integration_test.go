package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentmux/internal/nativebridge"
)

// This is the real installed native TUI and app-server connected through our
// proxy, with every model request answered by a synthetic loopback fixture.
func TestInstalledCodexNativeQueuePreservesDraft(t *testing.T) {
	if os.Getenv("AGENTMUX_CODEX_NATIVE_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_CODEX_NATIVE_INTEGRATION=1 for installed Codex native TUI contract")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var modelInputs []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		input, _ := json.Marshal(body["input"])
		mu.Lock()
		modelInputs = append(modelInputs, string(input))
		n := len(modelInputs)
		mu.Unlock()
		msg := map[string]any{"id": fmt.Sprintf("msg_fixture_%d", n), "type": "message", "role": "assistant", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": "LOCAL BRIDGE FIXTURE ANSWER", "annotations": []any{}}}}
		response := map[string]any{"id": fmt.Sprintf("resp_fixture_%d", n), "object": "response", "created_at": time.Now().Unix(), "status": "completed", "output": []any{msg}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{{"type": "response.created", "response": map[string]any{"id": response["id"], "object": "response", "created_at": time.Now().Unix(), "status": "in_progress", "output": []any{}}}, {"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": msg["id"], "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}}, {"type": "response.output_text.delta", "item_id": msg["id"], "output_index": 0, "content_index": 0, "delta": "LOCAL BRIDGE FIXTURE ANSWER"}, {"type": "response.output_item.done", "output_index": 0, "item": msg}, {"type": "response.completed", "response": response}} {
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
			if flush, ok := w.(http.Flusher); ok {
				flush.Flush()
			}
		}
	}))
	defer api.Close()
	config := fmt.Sprintf("developer_instructions=\"OWNER_NATIVE_CONFIG_FIXTURE\"\nmodel_provider=\"fixture\"\nmodel=\"fixture-model\"\n[model_providers.fixture]\nname=\"Synthetic local fixture\"\nbase_url=%q\nwire_api=\"responses\"\nrequires_openai_auth=false\nsupports_websockets=false\n[analytics]\nenabled=false\n", api.URL+"/v1")
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	wrapper := filepath.Join(dir, "codex-record-fixture")
	if err = os.WriteFile(wrapper, []byte("#!"+python+"\n"+recordFixtureProtocol), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, python, "-c", ptyFixtureDriver, helper)
	command.Dir = dir
	command.Env = append(os.Environ(), "AGENTMUX_CODEX_PTY_HELPER=1", "AGENTMUX_CODEX_PTY_BINARY="+wrapper, "AGENTMUX_CODEX_REAL_BINARY="+binary, "AGENTMUX_CODEX_PTY_DIR="+dir, "CODEX_HOME="+home, "TERM=xterm-256color", ContextEnv+"=AGENTMUX_NATIVE_ROLE_FIXTURE")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("real native bridge failed: %v\n%s", err, output)
	}
	t.Log(string(output))
	mu.Lock()
	defer mu.Unlock()
	if len(modelInputs) == 0 {
		t.Fatal("native delivery never reached fixture provider")
	}
	for _, input := range modelInputs {
		if strings.Contains(input, "MY UNSENT HUMAN DRAFT") {
			t.Fatal("unsent terminal draft reached model")
		}
	}
	if !strings.Contains(strings.Join(modelInputs, "\n"), "AGENTMUX_NATIVE_ROLE_FIXTURE") {
		t.Fatal("owner-configured Agentmux context was lost through the remote transport")
	}
	if !strings.Contains(strings.Join(modelInputs, "\n"), "OWNER_NATIVE_CONFIG_FIXTURE") {
		t.Fatal("Agentmux context erased the owner's native developer instructions")
	}
	protocol, err := os.ReadFile(filepath.Join(dir, "native-rpc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	verifiedPolicy := false
	for _, line := range strings.Split(string(protocol), "\n") {
		var rpc struct {
			Method string `json:"method"`
			Params struct {
				Sandbox        string `json:"sandbox"`
				ApprovalPolicy string `json:"approvalPolicy"`
				Ephemeral      bool   `json:"ephemeral"`
				Source         string `json:"threadSource"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(line), &rpc) == nil && rpc.Method == "thread/start" && !rpc.Params.Ephemeral && rpc.Params.Source == "user" {
			if rpc.Params.Sandbox != "read-only" || rpc.Params.ApprovalPolicy != "never" {
				t.Fatalf("native sandbox/approval flags did not reach thread/start: %s", line)
			}
			verifiedPolicy = true
		}
	}
	if !verifiedPolicy {
		t.Fatal("native TUI did not send a verifiable thread/start policy")
	}
}

type installedFixtureConnection struct {
	dir  string
	mu   sync.Mutex
	sent bool
}

func (f *installedFixtureConnection) record(value any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, err := os.OpenFile(filepath.Join(f.dir, "events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(value)
}
func (f *installedFixtureConnection) Register(_ context.Context, r nativebridge.Registration) error {
	return f.record(r)
}
func (f *installedFixtureConnection) Poll(_ context.Context, r nativebridge.Readiness) (*nativebridge.Delivery, error) {
	if f.sent || r.State != "idle" {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(f.dir, "allow-delivery")); err != nil {
		return nil, nil
	}
	f.sent = true
	return &nativebridge.Delivery{ID: "fixture-delivery", TaskID: "fixture-task", From: "claude", To: "codex", Kind: "instruction", Text: "Answer with the synthetic fixture text."}, nil
}
func (f *installedFixtureConnection) Report(_ context.Context, e nativebridge.Event) error {
	return f.record(e)
}
func (f *installedFixtureConnection) Status(context.Context) (nativebridge.Status, error) {
	return nativebridge.Status{}, nil
}

func TestInstalledCodexPTYHelper(t *testing.T) {
	if os.Getenv("AGENTMUX_CODEX_PTY_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := Run(ctx, []string{os.Getenv("AGENTMUX_CODEX_PTY_BINARY"), "--no-daemon", "--no-alt-screen", "--sandbox", "read-only", "--ask-for-approval", "never"}, &installedFixtureConnection{dir: os.Getenv("AGENTMUX_CODEX_PTY_DIR")}); err != nil {
		t.Fatal(err)
	}
}

// Only the isolated fixture records protocol data. Production never logs native
// input or provider RPC contents. This wrapper forwards the real installed CLI.
const recordFixtureProtocol = `
import os,subprocess,sys,threading
binary=os.environ['AGENTMUX_CODEX_REAL_BINARY']
if len(sys.argv)<2 or sys.argv[1]!='app-server':os.execv(binary,[binary]+sys.argv[1:])
child=subprocess.Popen([binary]+sys.argv[1:],stdin=subprocess.PIPE)
def forward():
 try:
  with open(os.path.join(os.environ['AGENTMUX_CODEX_PTY_DIR'],'native-rpc.jsonl'),'ab',buffering=0) as record:
   for line in sys.stdin.buffer:
    record.write(line);child.stdin.write(line);child.stdin.flush()
 finally:child.stdin.close()
threading.Thread(target=forward,daemon=True).start()
sys.exit(child.wait())
`

const ptyFixtureDriver = `
import errno,fcntl,json,os,pty,select,signal,struct,subprocess,sys,termios,time
master,slave=pty.openpty()
fcntl.ioctl(slave,termios.TIOCSWINSZ,struct.pack('HHHH',35,110,0,0))
def terminal_session():
 os.setsid()
 fcntl.ioctl(slave,termios.TIOCSCTTY,0)
p=subprocess.Popen([sys.argv[1],'-test.run=^TestInstalledCodexPTYHelper$','-test.v'],stdin=slave,stdout=slave,stderr=slave,preexec_fn=terminal_session)
os.close(slave)
root=os.environ['AGENTMUX_CODEX_PTY_DIR'];raw=b'';draft=False;passed=False;deadline=time.time()+25
try:
 while time.time()<deadline:
  if select.select([master],[],[],.1)[0]:
   try:chunk=os.read(master,65536)
   except OSError as e:
    if e.errno==errno.EIO:break
    raise
   if not chunk:break
   raw+=chunk
   if b'\x1b[6n' in chunk:os.write(master,b'\x1b[1;1R')
  event_path=os.path.join(root,'events.jsonl')
  events=open(event_path).read() if os.path.exists(event_path) else ''
  if not draft and '"adapter":"codex"' in events and b'Ask Codex to do anything' in raw:
   os.write(master,b'MY UNSENT HUMAN DRAFT');time.sleep(.3);open(os.path.join(root,'allow-delivery'),'w').close();draft=True
  if '"phase":"completed"' in events and b'LOCAL BRIDGE FIXTURE ANSWER' in raw:
   last=raw.rfind(b'LOCAL BRIDGE FIXTURE ANSWER')
   if b'MY UNSENT HUMAN DRAFT' in raw[last:]:passed=True;break
  if p.poll() is not None:break
 if not passed:
  print(raw[-14000:].decode(errors='replace'));print(events);raise RuntimeError('native completion/draft assertion failed')
 print('Real Codex TUI: attributed native turn completed; original unsent draft survived; no keystroke delivery.')
finally:
 p.terminate()
 try:p.wait(timeout=4)
 except subprocess.TimeoutExpired:p.kill();p.wait()
 os.close(master)
`
