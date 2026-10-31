package managed

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
)

func nativeOnlyConfig() managedconfig.Config {
	return managedconfig.Config{Version: 2, Mode: "managed", Session: "daily", InteractiveWorkspace: true, Agents: map[string]managedconfig.Agent{
		"native": {Harness: "command", Interaction: "interactive", Command: []string{"/bin/cat"}},
	}, Limits: managedconfig.Limits{MaxRuntimeSeconds: 1}}
}

func TestInteractiveWorkspaceStartupDoesNotCreateOrRenewDeadline(t *testing.T) {
	s := testStore(t, nativeOnlyConfig())
	for i := 0; i < 2; i++ {
		if err := s.startRuntime(context.Background()); err != nil {
			t.Fatal(err)
		}
		deadline, err := s.RuntimeDeadline()
		if err != nil || !deadline.IsZero() {
			t.Fatalf("human workspace acquired a synthetic deadline: %s %v", deadline, err)
		}
	}
	explicit := time.Now().Add(time.Hour).UTC()
	if _, err := s.db.Exec("INSERT INTO metadata(key,value) VALUES('deadline',?)", explicit.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := s.startRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline, err := s.RuntimeDeadline()
	if err != nil || !deadline.Equal(explicit) {
		t.Fatalf("native workspace changed explicit deadline: %s %v", deadline, err)
	}
}

func TestOrdinaryManagedStartupRetainsFiniteDurableDeadline(t *testing.T) {
	cfg := nativeOnlyConfig()
	cfg.InteractiveWorkspace = false
	s := testStore(t, cfg)
	before := time.Now()
	if err := s.startRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline, err := s.RuntimeDeadline()
	if err != nil || deadline.Before(before) || deadline.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("ordinary runtime became unbounded: %s %v", deadline, err)
	}
	if err = s.startRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	again, err := s.RuntimeDeadline()
	if err != nil || !again.Equal(deadline) {
		t.Fatalf("ordinary runtime renewed deadline: %s %s %v", deadline, again, err)
	}
}

func TestInteractiveWorkspaceContextKeepsCancellationAndExplicitDeadlines(t *testing.T) {
	cfg := nativeOnlyConfig()
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := interactiveAgentContext(parent, cfg, time.Time{})
	defer cancel()
	if _, limited := ctx.Deadline(); limited {
		t.Fatal("native workspace acquired an arbitrary worker lifetime")
	}
	cancelParent()
	if ctx.Err() != context.Canceled {
		t.Fatalf("owner cancellation did not reach native terminal: %v", ctx.Err())
	}
	explicit := time.Now().Add(time.Minute)
	bounded, stop := interactiveAgentContext(context.Background(), cfg, explicit)
	defer stop()
	if deadline, ok := bounded.Deadline(); !ok || !deadline.Equal(explicit) {
		t.Fatal("native terminal ignored explicit broker deadline")
	}
	parent, parentStop := context.WithDeadline(context.Background(), explicit.Add(-time.Second))
	defer parentStop()
	bounded, stop = interactiveAgentContext(parent, cfg, explicit)
	defer stop()
	if deadline, _ := bounded.Deadline(); !deadline.Equal(explicit.Add(-time.Second)) {
		t.Fatal("native terminal extended parent deadline")
	}
	cfg.InteractiveWorkspace = false
	bounded, stop = interactiveAgentContext(context.Background(), cfg, time.Time{})
	defer stop()
	if deadline, ok := bounded.Deadline(); !ok || deadline.After(time.Now().Add(2*time.Second)) {
		t.Fatal("ordinary interactive worker lost runtime bound")
	}
}

// Exercise the complete native worker with a model-free foreground process,
// not merely the context helper. Its duration exceeds the legacy one-second
// limit in this fixture, and the broker must stay usable throughout.
func TestInteractiveWorkspaceNativeProcessOutlivesLegacyRuntimeLimit(t *testing.T) {
	root, err := os.MkdirTemp("", "amx-native-lifetime-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	cfg := nativeOnlyConfig()
	agent := cfg.Agents["native"]
	agent.Command = []string{"/bin/sleep", "1.2"}
	cfg.Agents["native"] = agent
	store, err := Init(filepath.Join(root, "s"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, store) }()
	defer func() { cancel(); <-done }()
	credential, err := LoadCredential(CredentialPath(store.dir, ""))
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(credential)
	for {
		_, problem := client.Call(ctx, Request{Operation: "identity"})
		if problem == nil {
			break
		}
		select {
		case serveErr := <-done:
			done <- serveErr
			t.Fatalf("broker failed: %v", serveErr)
		default:
		}
		if ctx.Err() != nil {
			t.Fatal(problem)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = RunWorker(ctx, WorkerOptions{StateDir: store.dir, AgentID: "native"}); err != nil {
		t.Fatalf("human native process was interrupted by legacy runtime cap: %v", err)
	}
	if _, problem := client.Call(ctx, Request{Operation: "identity"}); problem != nil {
		t.Fatalf("human workspace expired: %v", problem)
	}
}
