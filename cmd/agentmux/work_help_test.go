package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/sessioninventory"
)

func TestOnboardingHelpDoesNotReadWorkspaceOrDiscoverSessions(t *testing.T) {
	previous := discoverWorkSessions
	discoverWorkSessions = func(context.Context, string) []sessioninventory.Session {
		t.Fatal("help performed session discovery")
		return nil
	}
	t.Cleanup(func() { discoverWorkSessions = previous })
	for _, help := range []string{"--help", "-h"} {
		for _, command := range []string{"sessions", "connect", "research plan", "research run", "research status", "research watch", "research step", "experiment run"} {
			t.Run(command+help, func(t *testing.T) {
				var out, diagnostics bytes.Buffer
				code := -1
				switch command {
				case "sessions":
					code = runWorkInventory([]string{help}, &out, &diagnostics)
				case "connect":
					code = runWorkConnect([]string{help}, &out, &diagnostics)
				case "experiment run":
					code = runExperimentIO(context.Background(), []string{"run", help}, &out, &diagnostics)
				default:
					code = runResearchIO(context.Background(), []string{strings.TrimPrefix(command, "research "), help}, &out, &diagnostics)
				}
				if code != exitSuccess || !strings.Contains(out.String()+diagnostics.String(), "Usage:") {
					t.Fatalf("help failed: code=%d out=%s diagnostics=%s", code, &out, &diagnostics)
				}
			})
		}
	}
}

func TestWorkHelpPreservesLiteralMessagesAndOptionValues(t *testing.T) {
	for _, args := range [][]string{{"board", "--", "--help"}, {"--state", "--help"}, {"--project=--help"}, {"--", "-h"}} {
		if workHelpRequested(args) {
			t.Fatalf("literal argument became help: %q", args)
		}
	}
	if !workHelpRequested([]string{"--state", "/tmp/work", "--help"}) {
		t.Fatal("help after a workspace option was ignored")
	}
}

func TestWorkSendRoutingPreservesLiteralAndLegacySeparators(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"agent", "message"}, {"agent", "--", "--help"}, {"agent", "--", "--state=literal-message"}, {"agent", "message", "--state", "/tmp/work"}} {
		if !workSendRequested(args) {
			t.Fatalf("human message routed to raw pane input: %q", args)
		}
	}
	for _, args := range [][]string{{"workspace", "pane", "--", "--help"}, {"workspace", "pane", "--", "command", "--state=literal"}, {"workspace", "pane", "--no-enter", "literal"}} {
		if workSendRequested(args) {
			t.Fatalf("raw pane input reinterpreted as coordination: %q", args)
		}
	}
}

func TestInventorySeparatesMissingWorkspaceFromEmptyObservations(t *testing.T) {
	previous := discoverWorkSessions
	discoverWorkSessions = func(context.Context, string) []sessioninventory.Session { return nil }
	t.Cleanup(func() { discoverWorkSessions = previous })
	var out, diagnostics bytes.Buffer
	state := filepath.Join(t.TempDir(), "work with spaces")
	if code := runWorkInventory([]string{"--state", state}, &out, &diagnostics); code != exitSuccess {
		t.Fatalf("observational inventory failed: %d %s", code, &diagnostics)
	}
	if !strings.Contains(diagnostics.String(), "registry not loaded") || !strings.Contains(out.String(), "No additional recognizable") || !strings.Contains(out.String(), "'--state'") || !strings.Contains(out.String(), state) {
		t.Fatalf("missing workspace looked empty or lost context: %s %s", &out, &diagnostics)
	}
}

func TestInventoryBrokerOutageDoesNotClaimNoRegisteredSessions(t *testing.T) {
	previous := discoverWorkSessions
	discoverWorkSessions = func(context.Context, string) []sessioninventory.Session { return nil }
	t.Cleanup(func() { discoverWorkSessions = previous })
	state := filepath.Join(t.TempDir(), "offline")
	s, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: "offline", Agents: map[string]managedconfig.Agent{"worker": {Harness: "command", Command: []string{"/bin/true"}, Sandbox: "local"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var out, diagnostics bytes.Buffer
	if code := runWorkInventory([]string{"--state", state}, &out, &diagnostics); code != exitSuccess {
		t.Fatalf("observational inventory failed: %d %s", code, &diagnostics)
	}
	if !strings.Contains(diagnostics.String(), "registry unavailable") || !strings.Contains(diagnostics.String(), "status are unknown") || strings.Contains(out.String(), "No registered Agentmux") {
		t.Fatalf("unavailable registry presented as empty: %s %s", &out, &diagnostics)
	}
}

func TestWorkBoardLiteralHelpAfterSeparatorIsDelivered(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	var out, diagnostics bytes.Buffer
	if code := runWorkSession("board", []string{"--state", state, "--board", "team", "--", "--help"}, strings.NewReader(""), &out, &diagnostics); code != exitSuccess {
		t.Fatalf("literal help text was not delivered: %d %s %s", code, &out, &diagnostics)
	}
	raw, err := workCall(context.Background(), client, "activity", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var activity managedActivitySnapshot
	if err := json.Unmarshal(raw, &activity); err != nil || len(activity.Messages) != 1 || activity.Messages[0].Text != "--help" {
		t.Fatalf("literal message became a CLI option: %s (%v)", raw, err)
	}
}

func TestWorkRemoveReportsRevocationScopeWithoutClaimingProcessExit(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	var out, diagnostics bytes.Buffer
	if code := runWorkSession("remove", []string{"peer", "--state", state}, strings.NewReader(""), &out, &diagnostics); code != exitSuccess {
		t.Fatalf("remove failed: %d %s", code, &diagnostics)
	}
	if !strings.Contains(out.String(), "active descendants (1 session(s))") || !strings.Contains(out.String(), "not yet confirmed") || strings.Contains(out.String(), "Other terminals keep running") {
		t.Fatalf("remove hid descendant scope or claimed shutdown: %s", &out)
	}
	for _, agent := range inventoryRegistry(t, client) {
		if agent.ID == "peer" && agent.State != "revoked" {
			t.Fatal("successful remove did not revoke coordination")
		}
	}
}
