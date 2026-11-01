package managed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
)

func TestNativeLauncherRecoveryPreservesArgumentsAndDefinition(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "version-two")
	if err := os.WriteFile(entry, []byte("#!/bin/sh\noutput=$1\nshift\nprintf '%s\\n' \"$@\" > \"$output\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "native")
	if err := os.Symlink(entry, launcher); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "received-arguments")
	ownerMount := t.TempDir()
	agent := managedconfig.Agent{Harness: "command", Interaction: "interactive", Sandbox: "local", Workdir: root,
		Command: []string{filepath.Join(root, "removed-version-one"), marker, "prefix with spaces", "--owner-flag"}, ReadOnlyPaths: []string{ownerMount}}
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "recovery", InteractiveWorkspace: true, Agents: map[string]managedconfig.Agent{"native": agent}}
	stateRoot, err := os.MkdirTemp("", "amx-repair-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stateRoot)
	store, err := Init(filepath.Join(stateRoot, "s"), cfg)
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
		if _, problem := client.Call(ctx, Request{Operation: "identity"}); problem == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("broker did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Recovery opens the human terminal, not the instruction queue. Existing
	// instructions must remain available for the human/native agent to inspect.
	beforeTask := taskIn(t, call(t, store, "", "instruct", map[string]any{"to": []string{"native"}, "text": "Do not consume this instruction during launcher recovery."}))
	before, err := store.Agent("native")
	if err != nil {
		t.Fatal(err)
	}
	if err := RunWorker(ctx, WorkerOptions{StateDir: store.dir, AgentID: "native", NativeLauncher: launcher}); err != nil {
		t.Fatalf("explicit recovery failed: %v", err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "prefix with spaces\n--owner-flag\n" {
		t.Fatalf("configured argument prefix changed: output=%q err=%v", data, err)
	}
	after, err := store.Agent("native")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("invocation recovery rewrote the saved definition: before=%+v after=%+v err=%v", before, after, err)
	}
	var tasks struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.Unmarshal(call(t, store, "", "task.list", map[string]any{}), &tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks.Tasks) != 1 || !reflect.DeepEqual(tasks.Tasks[0], beforeTask) {
		t.Fatalf("launcher recovery consumed or completed an instruction: %+v", tasks.Tasks)
	}
}

func TestNativeLauncherOverrideDoesNotChangeSandboxOrDropOwnerMounts(t *testing.T) {
	launcher := filepath.Join(t.TempDir(), "native")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	original := managedconfig.Agent{Harness: "claude", Interaction: "interactive", Sandbox: "local", Command: []string{"old", "--owner-setting", "literal value"}, ReadOnlyPaths: []string{"owner-selected-mount"}}
	recovered, err := interactiveLauncher(original, launcher)
	if err != nil || !reflect.DeepEqual(recovered.Command, []string{launcher, "--owner-setting", "literal value"}) || !reflect.DeepEqual(recovered.ReadOnlyPaths, original.ReadOnlyPaths) {
		t.Fatalf("explicit recovery changed owner intent: %+v err=%v", recovered, err)
	}
	recovered.Command[1] = "changed"
	if original.Command[1] != "--owner-setting" {
		t.Fatal("recovery reused the saved command's backing array")
	}
	for _, change := range []func(*managedconfig.Agent){
		func(a *managedconfig.Agent) { a.Sandbox = "bubblewrap" },
		func(a *managedconfig.Agent) { a.Interaction = "managed" },
		func(a *managedconfig.Agent) { a.Interaction = "" },
	} {
		a := original
		change(&a)
		if _, err := interactiveLauncher(a, launcher); err == nil {
			t.Fatalf("launcher override crossed its trusted interactive boundary: %+v", a)
		}
	}
	for _, invalid := range []string{"relative-launcher", filepath.Dir(launcher), filepath.Join(t.TempDir(), "missing"), launcher + "\x00"} {
		if _, err := interactiveLauncher(original, invalid); err == nil {
			t.Fatalf("invalid launcher accepted: %q", invalid)
		}
	}
}

func TestManagedWorkerRejectsNativeLauncherBeforeClaimingWork(t *testing.T) {
	store := testStore(t, testConfig())
	err := RunWorker(context.Background(), WorkerOptions{StateDir: store.dir, AgentID: "alice", NativeLauncher: "/bin/true"})
	if err == nil || !strings.Contains(err.Error(), "trusted-local interactive") {
		t.Fatalf("managed worker silently accepted native launcher override: %v", err)
	}
}
