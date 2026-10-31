package managed

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentmux/internal/egress"
	"agentmux/internal/managedconfig"
)

func TestRealManagedHardeningWorkerAndIsolatedBroker(t *testing.T) {
	binary := os.Getenv("AGENTMUX_INTEGRATION_BINARY")
	if binary == "" {
		t.Skip("set AGENTMUX_INTEGRATION_BINARY to a freshly built agentmux binary")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "amx-hardening-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprint(w, "local-proof") }))
	defer endpoint.Close()
	script := fmt.Sprintf(`import errno, os, urllib.request
assert urllib.request.urlopen(%q, timeout=3).read() == b'local-proof'
path='/workspace/overflow'
try:
    with open(path,'wb',buffering=0) as f:
        while f.tell() < 3*1024*1024: f.write(b'x'*65536)
    raise AssertionError('workspace not capped')
except OSError as error:
    assert error.errno == errno.ENOSPC
finally:
    if os.path.exists(path): os.unlink(path)
with open('/workspace/proof.txt','w') as f: f.write('bounded worker checkpoint')
print('COMBINED_HARDENING_OK')
`, endpoint.URL)
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "hardening", Budget: managedconfig.Budget{MaxTokens: 1}, Agents: map[string]managedconfig.Agent{"worker": {Harness: "command", Command: []string{"/usr/bin/python3", "-c", script}, Sandbox: "bubblewrap", Network: "allowlist", Egress: managedconfig.Egress{Allow: []egress.Endpoint{{Origin: endpoint.URL}}}, Budget: managedconfig.AgentBudget{MaxTurnTokens: 1}, Disk: managedconfig.Disk{WorkspaceBytes: 1 << 20, HistoryBytes: 1 << 20, TempBytes: 1 << 20, MaxEntries: 100}}}}
	state := filepath.Join(root, "state")
	s, err := Init(state, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	brokerCtx, stopBroker := context.WithCancel(ctx)
	broker := exec.CommandContext(brokerCtx, binary, "managed", "serve", "--state", state, "--sandbox", "bubblewrap")
	broker.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	if err = broker.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stopBroker(); _ = broker.Wait() }()
	owner := credential(t, s, "")
	client := NewClient(owner)
	ready := false
	for i := 0; i < 100; i++ {
		if _, f := client.Call(ctx, Request{Operation: "identity", Args: []byte(`{}`)}); f == nil {
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("isolated broker did not become ready")
	}
	task := taskIn(t, call(t, s, "", "instruct", map[string]any{"to": []string{"worker"}, "text": "run one local bounded proof"}))
	if err = RunWorker(ctx, WorkerOptions{StateDir: state, AgentID: "worker", Executable: binary, Once: true}); err != nil {
		t.Fatal(err)
	}
	task = taskIn(t, call(t, s, "", "task.get", map[string]any{"id": task.ID}))
	if task.State != "submitted" || !strings.Contains(task.Result, "COMBINED_HARDENING_OK") {
		t.Fatalf("actual task did not submit: state=%s result=%s", task.State, task.Result)
	}
	if b, err := os.ReadFile(filepath.Join(state, "workspaces", "worker", "proof.txt")); err != nil || string(b) != "bounded worker checkpoint" {
		t.Fatal("worker submitted before publishing its capsule")
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected endpoint calls")
	}
	call(t, s, "", "instruct", map[string]any{"to": []string{"worker"}, "text": "budget must deny this turn"})
	if err = RunWorker(ctx, WorkerOptions{StateDir: state, AgentID: "worker", Executable: binary, Once: true}); err == nil {
		t.Fatal("second turn bypassed session reservation")
	}
	if calls.Load() != 1 {
		t.Fatal("budget rejection happened after external work")
	}
	if used := budgetUsage(t, s, ""); used.Tokens != 1 || used.Turns != 1 {
		t.Fatal("incorrect admission accounting")
	}
	if _, err = os.Stat(filepath.Join(state, "capsules", "worker")); !os.IsNotExist(err) {
		t.Fatal("unused capsule was left quarantined after denied admission")
	}
}
