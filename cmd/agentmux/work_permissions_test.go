package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func permissionCLIBroker(t *testing.T) (string, *managed.Client) {
	t.Helper()
	root, err := os.MkdirTemp("", "amx-permission-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	state := filepath.Join(root, "s")
	store, err := managed.Init(state, managedconfig.Config{
		Version: 2, Mode: "managed", Session: "permissions",
		Agents: map[string]managedconfig.Agent{
			"alice": {Harness: "pi", Interaction: "interactive"},
			"bob":   {Harness: "pi", Interaction: "interactive"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- managed.Serve(ctx, store) }()
	t.Cleanup(func() { cancel(); <-done; _ = store.Close() })
	client := permissionCLIClient(t, state, "")
	until := time.Now().Add(3 * time.Second)
	for {
		if _, err := workCall(ctx, client, "identity", map[string]any{}); err == nil {
			return state, client
		}
		select {
		case err := <-done:
			done <- err
			t.Fatal(err)
		default:
		}
		if time.Now().After(until) {
			t.Fatal("permission fixture broker unavailable")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func permissionCLIClient(t *testing.T, state, id string) *managed.Client {
	t.Helper()
	credential, err := managed.LoadCredential(managed.CredentialPath(state, id))
	if err != nil {
		t.Fatal(err)
	}
	return managed.NewClient(credential)
}

func permissionCLIControls(t *testing.T, client *managed.Client) managed.ControlState {
	t.Helper()
	raw, err := workCall(context.Background(), client, "control.get", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var control managed.ControlState
	if err := json.Unmarshal(raw, &control); err != nil {
		t.Fatal(err)
	}
	return control
}

func permissionCLIBoundary(t *testing.T, client *managed.Client, from, to, action string) bool {
	t.Helper()
	raw, err := workCall(context.Background(), client, "boundaries.get", map[string]any{"agent": from})
	if err != nil {
		t.Fatal(err)
	}
	var boundary managed.Boundaries
	if err := json.Unmarshal(raw, &boundary); err != nil {
		t.Fatal(err)
	}
	for _, target := range boundary.Targets {
		if target.To == to {
			for _, candidate := range target.Actions {
				if candidate == action {
					return true
				}
			}
		}
	}
	return false
}

func TestPermissionCLIDirectionalCommandsKeepDeliveryAndActorIndependent(t *testing.T) {
	state, owner := permissionCLIBroker(t)
	ctx := context.Background()
	policy := func() managed.NativePolicy {
		raw, err := workCall(ctx, owner, "native.status", map[string]any{"agent": "bob"})
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Status managed.NativeStatus `json:"status"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		return response.Status.Policy
	}
	before := policy()
	var out, diagnostics bytes.Buffer
	if code := runWorkPermission("allow", []string{"alice", "bob", "commands", "--state", state}, &out, &diagnostics); code != exitSuccess {
		t.Fatalf("allow: %d %s", code, &diagnostics)
	}
	if !strings.Contains(out.String(), "alice → bob · commands (instruct)") || !strings.Contains(out.String(), "Native delivery is separate") {
		t.Fatalf("missing exact edge or delivery distinction: %s", &out)
	}
	if !reflect.DeepEqual(before, policy()) {
		t.Fatal("command permission changed native delivery policy")
	}
	if !permissionCLIBoundary(t, owner, "alice", "bob", "instruct") || permissionCLIBoundary(t, owner, "bob", "alice", "instruct") || permissionCLIBoundary(t, owner, "alice", "bob", "message.send") {
		t.Fatal("direction or action was broadened")
	}
	alice := permissionCLIClient(t, state, "alice")
	bob := permissionCLIClient(t, state, "bob")
	if _, err := workCall(ctx, alice, "instruct", map[string]any{"to": []string{"bob"}, "text": "A bounded peer instruction"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workCall(ctx, bob, "instruct", map[string]any{"to": []string{"alice"}, "text": "The reverse direction is not granted"}); err == nil {
		t.Fatal("reverse instruction was allowed")
	}
	control := permissionCLIControls(t, owner)
	for _, operation := range []string{"control.get", "control.update"} {
		args := map[string]any{}
		if operation == "control.update" {
			args = map[string]any{"version": control.Version, "kind": "edge", "from": "bob", "to": "alice", "action": "instruct", "allow": true}
		}
		_, err := workCall(ctx, alice, operation, args)
		var problem *managed.Fault
		if !errors.As(err, &problem) || problem.Code != "forbidden" {
			t.Fatalf("sender gained owner %s authority: %v", operation, err)
		}
	}
	if permissionCLIControls(t, owner).Version != control.Version {
		t.Fatal("forbidden sender operation changed permissions")
	}
}

func TestPermissionCLIDenyOverridesInheritedGrantOnlyForSelectedAction(t *testing.T) {
	state, owner := permissionCLIBroker(t)
	ctx := context.Background()
	if _, err := workCollaboration(ctx, owner, []string{"alice", "bob"}, false); err != nil {
		t.Fatal(err)
	}
	if !permissionCLIBoundary(t, owner, "alice", "bob", "message.send") {
		t.Fatal("fixture did not inherit collaboration messages")
	}
	var out, diagnostics bytes.Buffer
	if code := runWorkPermission("deny", []string{"--state", state, "alice", "bob", "message.send"}, &out, &diagnostics); code != exitSuccess {
		t.Fatalf("deny: %d %s", code, &diagnostics)
	}
	if permissionCLIBoundary(t, owner, "alice", "bob", "message.send") || !permissionCLIBoundary(t, owner, "bob", "alice", "message.send") || !permissionCLIBoundary(t, owner, "alice", "bob", "ping.send") {
		t.Fatal("deny did not override just the selected inherited action")
	}
	if _, err := workCall(ctx, permissionCLIClient(t, state, "alice"), "message.send", map[string]any{"to": []string{"bob"}, "text": "Denied despite collaboration"}); err == nil {
		t.Fatal("actual inherited message send was not denied")
	}
	if !strings.Contains(out.String(), "cancel accepted tasks separately") {
		t.Fatal("deny omitted accepted-task distinction")
	}
	// The alias grants the exact same action back, preserving the group.
	if code := runWorkPermission("allow", []string{"alice", "bob", "messages", "--state=" + state}, &out, &diagnostics); code != exitSuccess || !permissionCLIBoundary(t, owner, "alice", "bob", "message.send") {
		t.Fatalf("explicit regrant failed: %d %s", code, &diagnostics)
	}
	if len(permissionCLIControls(t, owner).Groups) != 1 {
		t.Fatal("edge edit replaced collaboration group")
	}
}

func TestPermissionCLIStaleCASDoesNotRetryOrOverwrite(t *testing.T) {
	_, owner := permissionCLIBroker(t)
	ctx := context.Background()
	updates := 0
	var concurrent managed.ControlState
	call := func(ctx context.Context, operation string, args any) (json.RawMessage, error) {
		raw, err := workCall(ctx, owner, operation, args)
		if operation == "control.get" && err == nil {
			var snapshot managed.ControlState
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				t.Fatal(err)
			}
			if _, err := workCall(ctx, owner, "control.update", map[string]any{"version": snapshot.Version, "kind": "edge", "from": "bob", "to": "alice", "action": "ping.send", "allow": true}); err != nil {
				t.Fatal(err)
			}
			concurrent = permissionCLIControls(t, owner)
		}
		if operation == "control.update" {
			updates++
		}
		return raw, err
	}
	err := changeWorkPermission(ctx, call, "alice", "bob", "instruct", true)
	var problem *managed.Fault
	if !errors.As(err, &problem) || problem.Code != "conflict" || updates != 1 {
		t.Fatalf("stale edit was retried or not rejected: updates=%d err=%v", updates, err)
	}
	if current := permissionCLIControls(t, owner); !reflect.DeepEqual(current, concurrent) {
		t.Fatal("stale edit changed another owner's permissions")
	}
}

func TestPermissionCLIHelpAndStrictValidationBeforeIO(t *testing.T) {
	for _, operation := range []string{"allow", "deny"} {
		var out, diagnostics bytes.Buffer
		if code := runWorkPermission(operation, []string{"--help"}, &out, &diagnostics); code != exitSuccess || !strings.Contains(out.String(), "FROM TO") || !strings.Contains(out.String(), "Native delivery is separate") {
			t.Fatalf("missing help: %d %s %s", code, &out, &diagnostics)
		}
	}
	for _, args := range [][]string{
		{}, {"alice", "bob"}, {"alice", "bob", "commands", "extra"},
		{"alice", "alice", "commands"}, {"Alice label", "bob", "commands"},
		{"alice", "bob", "all"}, {"alice", "bob", "command"}, {"alice", "bob", "Commands"},
		{"alice", "bob", "commands", "--mode", "auto"},
		{"alice", "bob", "commands", "--session", "other", "--state", "/missing"},
		{"alice", "bob", "commands", "--state", ""},
		{"alice", "bob", "commands", "--session", "one", "--session", "two"},
	} {
		var out, diagnostics bytes.Buffer
		if code := runWorkPermission("allow", args, &out, &diagnostics); code == exitSuccess || diagnostics.Len() == 0 || strings.Contains(diagnostics.String(), "workspace not found") {
			t.Fatalf("invalid input reached credential IO: %v -> %d %s", args, code, &diagnostics)
		}
	}
}

func TestPermissionCLIInactiveAgentAndExactPingAliases(t *testing.T) {
	state, owner := permissionCLIBroker(t)
	before := permissionCLIControls(t, owner)
	var out, diagnostics bytes.Buffer
	if code := runWorkPermission("allow", []string{"alice", "missing", "commands", "--state", state}, &out, &diagnostics); code == exitSuccess {
		t.Fatal("unregistered agent was accepted")
	}
	if current := permissionCLIControls(t, owner); !reflect.DeepEqual(before, current) {
		t.Fatal("invalid agent changed permission state")
	}
	for _, action := range []string{"pings", "ping.send", "instruct"} {
		out.Reset()
		diagnostics.Reset()
		if code := runWorkPermission("allow", []string{"alice", "bob", action, "--state", state}, &out, &diagnostics); code != exitSuccess {
			t.Fatalf("supported exact alias %s: %d %s", action, code, &diagnostics)
		}
	}
	if !permissionCLIBoundary(t, owner, "alice", "bob", "ping.send") || !permissionCLIBoundary(t, owner, "alice", "bob", "instruct") {
		t.Fatal("exact action aliases did not grant their selected action")
	}
}
