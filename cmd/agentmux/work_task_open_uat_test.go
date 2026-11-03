package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedlaunch"
)

// Exercise the built command's terminal path, not just a seeded model. The
// fixture broker has no launched worker, native provider or workspace process.
func TestWorkTaskOpenExactOlderTaskRealTMux(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("requires explicit disposable tmux integration")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatal("tmux is required for the explicitly enabled integration")
	}
	binary := os.Getenv("AGENTMUX_TEST_BINARY")
	if binary == "" {
		binary = "../../bin/agentmux"
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	state, owner, _ := inventoryBroker(t)
	rpc := func(client *managed.Client, operation string, args any) json.RawMessage {
		t.Helper()
		raw, err := workCall(context.Background(), client, operation, args)
		if err != nil {
			t.Fatalf("%s: %v", operation, err)
		}
		return raw
	}
	const instruction = "EXACT_OLDER_INSTRUCTION: inspect this retained task, never the task mentioning its ID."
	const result = "EXACT_OLDER_RESULT: this is saved fixture evidence; no provider executed."
	rpc(owner, "instruct", map[string]any{"to": []string{"peer"}, "text": instruction})
	credential, err := managed.LoadCredential(managed.CredentialPath(state, "peer"))
	if err != nil {
		t.Fatal(err)
	}
	peer := managed.NewClient(credential)
	var claimed struct {
		Task  managed.Task `json:"task"`
		Lease string       `json:"lease"`
	}
	if err = json.Unmarshal(rpc(peer, "task.claim", map[string]any{}), &claimed); err != nil || claimed.Task.ID == "" {
		t.Fatalf("claim fixture task: %+v %v", claimed, err)
	}
	id := claimed.Task.ID
	rpc(peer, "task.complete", map[string]any{"id": id, "version": claimed.Task.Version, "lease": claimed.Lease, "status": "failed", "result": result})
	for i := 0; i < 45; i++ {
		rpc(owner, "instruct", map[string]any{"to": []string{"peer"}, "text": fmt.Sprintf("WRONG_NEWER_INSTRUCTION %02d references %s", i, id)})
	}
	var current struct {
		Task managed.Task `json:"task"`
	}
	before := rpc(owner, "task.get", map[string]any{"id": id})
	if err = json.Unmarshal(before, &current); err != nil || current.Task.Version <= 1 {
		t.Fatalf("fixture did not create a refreshed version: %s %v", before, err)
	}
	allBefore := rpc(owner, "task.list", map[string]any{"limit": 100, "order": "desc"})
	recent := rpc(owner, "task.list", map[string]any{"limit": 40, "order": "desc"})
	if bytes.Contains(recent, []byte(`"id":"`+id+`"`)) {
		t.Fatal("target fixture must be older than the ordinary first task page")
	}

	socket := filepath.Join(t.TempDir(), "tmux.sock")
	tmux := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket, "-f", "/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
		raw, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(raw)), err
	}
	checkedTMux := func(args ...string) string {
		t.Helper()
		out, err := tmux(args...)
		if err != nil {
			t.Fatalf("private tmux %q: %v\n%s", args, err, out)
		}
		return out
	}
	t.Cleanup(func() { _, _ = tmux("kill-server") })
	pane := checkedTMux("new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "exact-task-open", "-x", "150", "-y", "44", "/bin/sh")
	checkedTMux("set-window-option", "-t", pane, "remain-on-exit", "on")
	command := "exec " + managedlaunch.ShellCommand([]string{binary, "task", id, "--open", "--state", state})
	checkedTMux("respawn-pane", "-k", "-t", pane, command)
	waitScreen := func(label string, match func(string) bool) string {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		var screen string
		for time.Now().Before(deadline) {
			screen, err = tmux("capture-pane", "-p", "-t", pane)
			if err == nil && match(screen) {
				return screen
			}
			time.Sleep(60 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s: %v\n%s", label, err, screen)
		return ""
	}
	main := waitScreen("exact older task scope", func(screen string) bool {
		return strings.Contains(screen, "Exact task: "+id) && strings.Contains(screen, fmt.Sprintf("version %d", current.Task.Version)) && strings.Contains(screen, result)
	})
	if strings.Contains(main, "WRONG_NEWER_INSTRUCTION") {
		t.Fatal("opening the exact task substituted a newer task referring to its ID")
	}
	checkedTMux("send-keys", "-t", pane, "Enter")
	waitScreen("full exact task evidence", func(screen string) bool {
		return strings.Contains(screen, "Task "+id) && strings.Contains(screen, instruction) && strings.Contains(screen, result)
	})
	checkedTMux("send-keys", "-t", pane, "Escape")
	waitScreen("return to pinned task", func(screen string) bool { return strings.Contains(screen, "Exact task: "+id) })
	checkedTMux("send-keys", "-t", pane, "C-k")
	checkedTMux("send-keys", "-t", pane, "-l", "Resolve uncertain task as failed")
	waitScreen("task recovery route", func(screen string) bool {
		return strings.Contains(screen, "Resolve uncertain task as failed") && strings.Contains(screen, "Unavailable:")
	})
	// The terminal failed fixture has no uncertainty to resolve. Merely opening
	// the recovery route must neither invent eligibility nor send a mutation.
	checkedTMux("send-keys", "-t", pane, "Escape")
	waitScreen("return from actions without mutation", func(screen string) bool { return strings.Contains(screen, "Exact task: "+id) })
	checkedTMux("send-keys", "-t", pane, "q")
	waitScreen("observer exit", func(string) bool {
		dead, err := tmux("display-message", "-p", "-t", pane, "#{pane_dead}")
		return err == nil && dead == "1"
	})
	if status := checkedTMux("display-message", "-p", "-t", pane, "#{pane_dead_status}"); status != "0" {
		t.Fatalf("observer exited unsuccessfully: %s", status)
	}
	if after := rpc(owner, "task.get", map[string]any{"id": id}); !bytes.Equal(before, after) {
		t.Fatalf("opening or reading recovery changed exact task evidence:\nbefore %s\nafter %s", before, after)
	}
	if allAfter := rpc(owner, "task.list", map[string]any{"limit": 100, "order": "desc"}); !bytes.Equal(allBefore, allAfter) {
		t.Fatal("task navigation changed task history or queued another instruction")
	}
}
