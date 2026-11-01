package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func TestDeliveryCLIPreservesPolicyAndApprovesExactCandidate(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	store, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: "delivery", Agents: map[string]managedconfig.Agent{"receiver": {Harness: "pi", Interaction: "interactive"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- managed.Serve(ctx, store) }()
	defer func() { cancel(); <-done }()
	cred, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		t.Fatal(err)
	}
	client := managed.NewClient(cred)
	for {
		if _, problem := client.Call(ctx, managed.Request{Operation: "identity"}); problem == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("broker unavailable")
		}
		time.Sleep(time.Millisecond * 10)
	}
	run := func(args ...string) (int, string) {
		t.Helper()
		var out, diagnostics bytes.Buffer
		code := runWorkDelivery(append(args, "--state", state), &out, &diagnostics)
		return code, out.String() + diagnostics.String()
	}
	if code, out := run("receiver", "review", "--max-turns", "7", "--notes=true"); code != 0 || !strings.Contains(out, "review") {
		t.Fatalf("configure: %d %s", code, out)
	}
	raw, err := workCall(ctx, client, "instruct", map[string]any{"to": []string{"receiver"}, "text": "An exact owner request"})
	if err != nil {
		t.Fatal(err)
	}
	var task struct {
		Task managed.Task `json:"task"`
	}
	_ = json.Unmarshal(raw, &task)
	if code, out := run("receiver", "--approve", task.Task.ID); code != 0 || !strings.Contains(out, "Approved "+task.Task.ID) {
		t.Fatalf("approve: %d %s", code, out)
	}
	if code, out := run("receiver", "--approve", task.Task.ID, "--json"); code != 0 || !json.Valid([]byte(out)) {
		t.Fatalf("JSON approval: %d %s", code, out)
	}
	if code, _ := run("receiver", "auto", "--max-turns", "-1"); code == 0 {
		t.Fatal("invalid limit was accepted")
	}
	code, out := run("receiver", "--json")
	if code != 0 {
		t.Fatal(out)
	}
	var status managed.NativeStatus
	if err = json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatal(err)
	}
	if status.Mode != "review" || status.Policy.MaxTurns != 7 || !status.Policy.Notes {
		t.Fatalf("failed edit changed prior policy: %+v", status.Policy)
	}
}

func TestDeliveryCLIRejectsAmbiguousWorkspaceBeforeIO(t *testing.T) {
	var out, diagnostics bytes.Buffer
	if code := runWorkDelivery([]string{"receiver", "auto", "--session", "another", "--state", "/missing/state"}, &out, &diagnostics); code == 0 || !strings.Contains(diagnostics.String(), "either") {
		t.Fatalf("selector validation %d %s", code, diagnostics.String())
	}
}
