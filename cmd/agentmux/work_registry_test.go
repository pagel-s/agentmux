package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/sessioninventory"
)

func TestWorkRegistryPreservesConfiguredSessionsBeyondFirstPage(t *testing.T) {
	root, err := os.MkdirTemp("", "amx-registry-pages-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	state := filepath.Join(root, "state")
	agents := make(map[string]managedconfig.Agent)
	for i := 1; i <= 225; i++ {
		agents[fmt.Sprintf("scale%03d", i)] = managedconfig.Agent{
			Harness: "command", Command: []string{"/bin/true"}, Sandbox: "local",
			Workdir: root, Role: "Synthetic metadata only; no worker is launched. 東京😀",
		}
	}
	store, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: "pages", Agents: agents})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- managed.Serve(ctx, store) }()
	t.Cleanup(func() { cancel(); <-done; _ = store.Close() })
	credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		t.Fatal(err)
	}
	client := managed.NewClient(credential)
	for deadline := time.Now().Add(3 * time.Second); ; {
		if _, err = workCall(ctx, client, "identity", map[string]any{}); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	previous := discoverWorkSessions
	discoverWorkSessions = func(context.Context, string) []sessioninventory.Session { return nil }
	t.Cleanup(func() { discoverWorkSessions = previous })

	registry, err := workRegistry(ctx, client)
	if err != nil || len(registry) != 225 || registry[224].ID != "scale225" {
		t.Fatalf("registry truncated beyond first page: count=%d err=%v", len(registry), err)
	}
	var output, diagnostics bytes.Buffer
	if code := runWorkInventory([]string{"--state", state}, &output, &diagnostics); code != 0 {
		t.Fatalf("inventory failed: %d %s", code, diagnostics.String())
	}
	if got := strings.Count(output.String(), "\n  ID scale"); got != 225 || !strings.Contains(output.String(), "ID scale225 ·") {
		t.Fatalf("CLI hid registered tail identity: count=%d", got)
	}
	snapshot := fetchWorkSnapshot(state, client, "scale225")
	if snapshot.err != nil || len(snapshot.agents) != 225 || snapshot.selected != "scale225" || snapshot.boundaries.Agent != "scale225" {
		t.Fatalf("snapshot lost selected tail identity: count=%d selected=%s boundaries=%s err=%v", len(snapshot.agents), snapshot.selected, snapshot.boundaries.Agent, snapshot.err)
	}
}
