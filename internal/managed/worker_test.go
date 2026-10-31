package managed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"agentmux/internal/harness"
	"agentmux/internal/managedconfig"
)

type workerCallFunc func(context.Context, Request) (json.RawMessage, *Fault)

func (f workerCallFunc) Call(ctx context.Context, r Request) (json.RawMessage, *Fault) {
	return f(ctx, r)
}

func workerTestStore(t *testing.T, command []string, mode string) (*Store, Credential, Credential) {
	t.Helper()
	root, err := os.MkdirTemp("", "amx-worker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "worker-test", Agents: map[string]managedconfig.Agent{
		"worker": {Harness: "command", Command: command, Role: "Test helper", Sandbox: mode},
	}}
	store, err := Init(filepath.Join(root, "state"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	owner, err := LoadCredential(CredentialPath(store.dir, ownerID))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := LoadCredential(CredentialPath(store.dir, "worker"))
	if err != nil {
		t.Fatal(err)
	}
	return store, owner, worker
}

func workerTestCall(t *testing.T, s *Store, token, operation string, args any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	raw, problem := s.Call(context.Background(), token, Request{Operation: operation, RequestID: randomID("test_worker"), Args: data})
	if problem != nil {
		t.Fatalf("%s: %v", operation, problem)
	}
	return raw
}

func workerTestTask(t *testing.T, s *Store, owner, worker Credential) (Task, string) {
	t.Helper()
	workerTestCall(t, s, owner.Token, "instruct", map[string]any{"to": []string{"worker"}, "text": "Return a useful result."})
	var claimed struct {
		Task  Task   `json:"task"`
		Lease string `json:"lease"`
	}
	if err := json.Unmarshal(workerTestCall(t, s, worker.Token, "task.claim", map[string]any{}), &claimed); err != nil {
		t.Fatal(err)
	}
	if claimed.Task.ID == "" || claimed.Lease == "" {
		t.Fatal("task was not claimed")
	}
	return claimed.Task, claimed.Lease
}

func workerTestCaller(store *Store, credential Credential) workerCaller {
	return workerCallFunc(func(ctx context.Context, r Request) (json.RawMessage, *Fault) {
		return store.Call(ctx, credential.Token, r)
	})
}

func TestWorkerPendingReadDoesNotClaimAndPagesToOwnTask(t *testing.T) {
	var calls int
	caller := workerCallFunc(func(_ context.Context, request Request) (json.RawMessage, *Fault) {
		calls++
		if request.Operation != "task.list" || request.RequestID != "" {
			t.Fatal("idle discovery must use read operations without idempotency keys")
		}
		var input struct {
			After string `json:"after"`
		}
		if err := json.Unmarshal(request.Args, &input); err != nil {
			t.Fatal(err)
		}
		if input.After == "" {
			return json.RawMessage(`{"tasks":[{"id":"other","to":"peer","state":"proposed","delivery":"pending"}],"cursor":"other"}`), nil
		}
		if input.After != "other" {
			t.Fatal("invalid pagination cursor")
		}
		return json.RawMessage(`{"tasks":[{"id":"own","to":"worker","state":"proposed","delivery":"pending"}],"cursor":"own"}`), nil
	})
	found, err := workerHasPending(context.Background(), caller, "worker")
	if err != nil || !found || calls != 2 {
		t.Fatalf("pending = %v, calls %d, err %v", found, calls, err)
	}
}

func TestWorkerRequestUsesOnlyExplicitEnvAndOwnBridge(t *testing.T) {
	t.Setenv("OWNER_ONLY_SECRET", "must-not-copy")
	agent := managedconfig.Agent{Harness: "claude", Connection: "model", Env: map[string]string{
		"EXPLICIT": "keep", "AGENTMUX_SOCKET": "owner.sock", "AGENTMUX_CREDENTIAL_FILE": "owner.json", "AGENTMUX_CLI": "arbitrary",
	}}
	cfg := managedconfig.Config{Connections: map[string]managedconfig.Connection{"model": {Protocol: "anthropic", APIKeyEnv: "MODEL_KEY"}}}
	req := workerRequest(agent, cfg, "worker.json", "broker.sock", "/tools/agentmux")
	if req.Env["OWNER_ONLY_SECRET"] != "" || req.Env["EXPLICIT"] != "keep" || req.Env["AGENTMUX_CREDENTIAL_FILE"] != "worker.json" || req.Env["AGENTMUX_SOCKET"] != "broker.sock" || req.Env["AGENTMUX_CLI"] != "/tools/agentmux" || req.APIKeyEnv != "MODEL_KEY" {
		t.Fatalf("unexpected worker request: %+v", req)
	}
	if agent.Env["AGENTMUX_CREDENTIAL_FILE"] != "owner.json" {
		t.Fatal("worker modified stored agent environment")
	}
}

func TestWorkerPromptRetainsProvenanceAndHidesOtherBoards(t *testing.T) {
	cfg := managedconfig.Config{Session: "test", Agents: map[string]managedconfig.Agent{"worker": {}, "peer": {}}, Boards: map[string]managedconfig.Access{
		"visible": {Readers: []string{"worker"}}, "hidden": {Readers: []string{"peer"}},
	}}
	prompt := workerPrompt("worker", "Reviewer", cfg, Task{ID: "task_1", MessageID: "msg_1", From: "peer", Text: "Do the work"})
	for _, expected := range []string{"Reviewer", "task_1", "msg_1", "agent-authored", "message.list --stdin", "visible", "Do the work"} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("prompt missing %q", expected)
		}
	}
	if strings.Contains(prompt, "hidden") {
		t.Fatal("private board names must not enter another agent's prompt")
	}
}

func TestWorkerResultsAreBoundedUTF8AndKnownSecretsAreRedacted(t *testing.T) {
	native := workerResult(`{"usage":"short-native-secret"}`, harness.Request{}, "", 1024, "short-native-secret")
	if strings.Contains(native, "short-native-secret") || !json.Valid([]byte(native)) {
		t.Fatal("native credential redaction did not preserve safe JSON")
	}
	req := harness.Request{APIKeyEnv: "MODEL_KEY", Env: map[string]string{"MODEL_KEY": "model-secret", "CUSTOM_TOKEN": "another-secret"}}
	text := workerResult("own-token model-secret another-secret 知識知識知識", req, "own-token", 64)
	if strings.Contains(text, "own-token") || strings.Contains(text, "model-secret") || strings.Contains(text, "another-secret") || !utf8.ValidString(text) || len(text) > 64 {
		t.Fatalf("unsafe result %q", text)
	}
	for _, limit := range []int{0, 1, 2, 3, 4, 12, 16} {
		result := workerResult("知識知識知識知識", harness.Request{}, "", limit)
		if len(result) > limit || !utf8.ValidString(result) {
			t.Fatalf("limit %d produced invalid text %q", limit, result)
		}
	}
	if strings.Contains(workerFailure(errors.New("stderr with a private-secret")), "private-secret") {
		t.Fatal("untrusted harness error must not be published")
	}
}

func TestWorkerExecutesAndSubmitsLeasedCommand(t *testing.T) {
	store, owner, credential := workerTestStore(t, []string{"/bin/cat"}, "local")
	task, lease := workerTestTask(t, store, owner, credential)
	caller := workerTestCaller(store, credential)
	req := harness.Request{Harness: "command", Command: []string{"/bin/cat"}, Prompt: "A verified transport result.", Workdir: store.cfg.Agents["worker"].Workdir}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := executeWorkerTask(ctx, caller, req, task, lease, credential.Token, 1024); err != nil {
		t.Fatal(err)
	}
	current, err := workerGetTask(ctx, caller, task.ID)
	if err != nil || current.State != "submitted" || current.Delivery != "acknowledged" || current.Result != req.Prompt {
		t.Fatalf("task = %+v, err %v", current, err)
	}
}

func TestWorkerCancellationAcknowledgesActualProcessStop(t *testing.T) {
	store, owner, credential := workerTestStore(t, []string{"/bin/cat"}, "local")
	task, lease := workerTestTask(t, store, owner, credential)
	caller := workerTestCaller(store, credential)
	marker := filepath.Join(store.cfg.Agents["worker"].Workdir, "started")
	req := harness.Request{Harness: "command", Command: []string{"/bin/sh", "-c", `printf started > "$1"; sleep 30`, "worker", marker}, Workdir: store.cfg.Agents["worker"].Workdir}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- executeWorkerTask(ctx, caller, req, task, lease, credential.Token, 1024) }()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("worker exited before process started: %v", err)
		case <-ctx.Done():
			t.Fatal("process did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	current, err := workerGetTask(ctx, caller, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	workerTestCall(t, store, owner.Token, "task.cancel", map[string]any{"id": task.ID, "version": current.Version})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("cancelled process did not stop")
	}
	current, err = workerGetTask(ctx, caller, task.ID)
	if err != nil || current.State != "cancelled" || current.Delivery != "stopped" {
		t.Fatalf("cancellation not acknowledged: %+v, %v", current, err)
	}
}

func TestWorkerStopsOnBrokerLossWithoutCompletingOrRetrying(t *testing.T) {
	store, owner, credential := workerTestStore(t, []string{"/bin/cat"}, "local")
	task, lease := workerTestTask(t, store, owner, credential)
	var reads, writes atomic.Int32
	caller := workerCallFunc(func(ctx context.Context, r Request) (json.RawMessage, *Fault) {
		if r.Operation == "task.get" && reads.Add(1) >= 2 {
			return nil, fault("transport", "private raw transport data")
		}
		if isWrite(r.Operation) {
			writes.Add(1)
		}
		return store.Call(ctx, credential.Token, r)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := executeWorkerTask(ctx, caller, harness.Request{Harness: "command", Command: []string{"/bin/sleep", "30"}, Workdir: store.cfg.Agents["worker"].Workdir}, task, lease, credential.Token, 1024)
	if err == nil || !strings.Contains(err.Error(), "transport") || strings.Contains(err.Error(), "private raw") || writes.Load() != 0 {
		t.Fatalf("unexpected uncertainty handling: %v, writes %d", err, writes.Load())
	}
	current, err := workerGetTask(ctx, workerTestCaller(store, credential), task.ID)
	if err != nil || current.State != "running" || current.Delivery != "dispatching" {
		t.Fatalf("uncertain delivery must not be completed or replayed: %+v, %v", current, err)
	}
}

func TestWorkerPreflightFailureDoesNotClaimTask(t *testing.T) {
	store, owner, credential := workerTestStore(t, []string{"/definitely/not/a/harness"}, "local")
	workerTestCall(t, store, owner.Token, "instruct", map[string]any{"to": []string{"worker"}, "text": "Some work"})
	err := RunWorker(context.Background(), WorkerOptions{StateDir: store.dir, AgentID: "worker", Once: true})
	if err == nil {
		t.Fatal("missing harness should fail preflight")
	}
	raw := workerTestCall(t, store, credential.Token, "task.list", map[string]any{"state": "proposed"})
	var tasks struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &tasks); err != nil || len(tasks.Tasks) != 1 || tasks.Tasks[0].Delivery != "pending" {
		t.Fatalf("preflight must not claim pending work: %s, %v", raw, err)
	}
}

func TestRunWorkerUnixIntegration(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_MANAGED_INTEGRATION=1 for real Unix broker integration")
	}
	modes := []string{"local"}
	if os.Getenv("AGENTMUX_SANDBOX_INTEGRATION") == "1" {
		modes = append(modes, "bubblewrap")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			store, owner, credential := workerTestStore(t, []string{"/bin/cat"}, mode)
			serveCtx, stop := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- Serve(serveCtx, store) }()
			t.Cleanup(func() {
				stop()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("broker shutdown: %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Error("broker failed to stop")
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client := NewClient(credential)
			for {
				if _, problem := client.Call(ctx, Request{Operation: "identity"}); problem == nil {
					break
				}
				if err := workerWait(ctx); err != nil {
					t.Fatal(err)
				}
			}
			// An idle --once worker does not burn an idempotency-cache entry.
			if err := RunWorker(ctx, WorkerOptions{StateDir: store.dir, AgentID: "worker", Once: true}); err != nil {
				t.Fatal(err)
			}
			var requests int
			if err := store.db.QueryRow("SELECT count(*) FROM requests").Scan(&requests); err != nil || requests != 0 {
				t.Fatalf("idle worker created request records: %d, %v", requests, err)
			}
			workerTestCall(t, store, owner.Token, "instruct", map[string]any{"to": []string{"worker"}, "text": "Integration transport task."})
			if err := RunWorker(ctx, WorkerOptions{StateDir: store.dir, AgentID: "worker", Once: true}); err != nil {
				t.Fatal(err)
			}
			raw := workerTestCall(t, store, owner.Token, "task.list", map[string]any{})
			var response struct {
				Tasks []Task `json:"tasks"`
			}
			if err := json.Unmarshal(raw, &response); err != nil || len(response.Tasks) != 1 || response.Tasks[0].State != "submitted" || !strings.Contains(response.Tasks[0].Result, "Integration transport task.") {
				t.Fatalf("unexpected roundtrip: %s, %v", raw, err)
			}
		})
	}
}
