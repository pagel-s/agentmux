package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func TestWorkCommandsKeepInteractiveMessagesOutOfTaskQueue(t *testing.T) {
	state := filepath.Join(t.TempDir(), "s")
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "human", Agents: map[string]managedconfig.Agent{
		"terminal": {Harness: "command", Interaction: "interactive", Command: []string{"/bin/cat"}},
		"worker":   {Harness: "command", Command: []string{"/bin/cat"}},
	}, Boards: map[string]managedconfig.Access{"team": {Readers: []string{"*"}, Writers: []string{"*"}}}}
	store, err := managed.Init(state, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- managed.Serve(ctx, store) }()
	t.Cleanup(func() { cancel(); <-done; store.Close() })
	credential, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		t.Fatal(err)
	}
	client := managed.NewClient(credential)
	until := time.Now().Add(3 * time.Second)
	for {
		if _, err = workCall(ctx, client, "identity", map[string]any{}); err == nil {
			break
		}
		if time.Now().After(until) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var out, diagnostics bytes.Buffer
	for _, args := range [][]string{{"terminal", "hello native", "--state", state}, {"worker", "hello managed", "--state", state}} {
		if code := runWorkSession("send", args, strings.NewReader(""), &out, &diagnostics); code != 0 {
			t.Fatalf("send: %d %s", code, diagnostics.String())
		}
	}
	raw, err := workCall(ctx, client, "activity", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot managedActivitySnapshot
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Tasks) != 1 || snapshot.Tasks[0].To != "worker" {
		t.Fatalf("interactive message became a task: %s", raw)
	}
	if !strings.Contains(out.String(), "Message queued for terminal") || !strings.Contains(out.String(), "Instruction queued for worker") {
		t.Fatal(out.String())
	}
	target, err := workReplyTarget(ctx, client, snapshot.Tasks[0].ID)
	if err != nil || target != "worker" {
		t.Fatalf("reply target: %q %v", target, err)
	}
	if code := runWorkSession("reply", []string{snapshot.Tasks[0].ID, "follow-up", "--state", state}, strings.NewReader(""), &out, &diagnostics); code != 0 {
		t.Fatalf("reply: %d %s", code, diagnostics.String())
	}
	if code := runWorkSession("board", []string{"team note", "--state", state}, strings.NewReader(""), &out, &diagnostics); code != 0 {
		t.Fatal(diagnostics.String())
	}
}

func TestWorkPathsAndFlagsAreExplicit(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	o, err := parseWorkOptions([]string{"claude", "quoted message", "--session", "my-work"})
	if err != nil || len(o.args) != 2 || !strings.HasSuffix(o.state, "agentmux/my-work") {
		t.Fatalf("options: %+v %v", o, err)
	}
	for _, args := range [][]string{{"--session", "../escape"}, {"--state", ""}, {"--session", "one", "--session", "two"}, {"--unknown"}} {
		if _, err := parseWorkOptions(args); err == nil {
			t.Fatalf("accepted invalid flags: %q", args)
		}
	}
	if !filepath.IsAbs(o.state) {
		t.Fatal("state path must be absolute")
	}
	if _, err := os.Stat(o.state); !os.IsNotExist(err) {
		t.Fatal("parsing created state")
	}
}

func TestSavedWorkSessionRejectsDifferentProject(t *testing.T) {
	project := t.TempDir()
	config := filepath.Join(t.TempDir(), "team.yaml")
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "human", Agents: map[string]managedconfig.Agent{"alice": {Harness: "command", Command: []string{"/bin/cat"}, Workdir: project}}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = checkSavedWorkProject(config, project); err != nil {
		t.Fatal(err)
	}
	if err = checkSavedWorkProject(config, t.TempDir()); err == nil {
		t.Fatal("silently opened another project's saved session")
	}
}
