package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
)

func TestConflictingWorkspaceSelectorsCannotRenameAnotherWorkspace(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	var out, diagnostics bytes.Buffer
	code := runWorkSession("label", []string{"peer", "wrong workspace", "--session", "other", "--state", state}, strings.NewReader(""), &out, &diagnostics)
	if code == exitSuccess || !strings.Contains(diagnostics.String(), "either --session or --state") {
		t.Fatalf("conflicting workspace selection accepted: %d %s", code, &diagnostics)
	}
	agents, err := workRegistry(context.Background(), client)
	if err != nil || len(agents) != 1 || agents[0].Title != "" {
		t.Fatalf("rejected selection changed title: %+v %v", agents, err)
	}
	if _, err := parseWorkOptionsWithStorage([]string{"--session", "other", "--state", state}, true); err != nil {
		t.Fatalf("start cannot name a new workspace with custom storage: %v", err)
	}
}

func uat20Activity(t *testing.T, client *managed.Client) managedActivitySnapshot {
	t.Helper()
	raw, err := workCall(context.Background(), client, "activity", map[string]any{"limit": 100})
	var snapshot managedActivitySnapshot
	if err != nil || json.Unmarshal(raw, &snapshot) != nil {
		t.Fatalf("activity: %s %v", raw, err)
	}
	return snapshot
}

func TestBlankReplyCannotQueueAnInstructionThroughItsReferencePrefix(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	if _, err := workCall(context.Background(), client, "instruct", map[string]any{"to": []string{"peer"}, "text": "Original"}); err != nil {
		t.Fatal(err)
	}
	before := uat20Activity(t, client)
	id := before.Tasks[0].ID
	var out, diagnostics bytes.Buffer
	code := runWorkSession("reply", []string{id, " \n\t", "--state", state}, strings.NewReader(""), &out, &diagnostics)
	if code == exitSuccess || !strings.Contains(diagnostics.String(), "must not be empty") {
		t.Fatalf("blank reply accepted: %d %s", code, &diagnostics)
	}
	response := ownerWebRequest(t, state, client, "/api/send", map[string]any{"target": "peer", "reply_to": id, "text": " \n"})
	after := uat20Activity(t, client)
	if response.Code != http.StatusBadRequest || len(after.Tasks) != len(before.Tasks) || len(after.Messages) != len(before.Messages) {
		t.Fatalf("blank reply created work: %d %s", response.Code, response.Body.String())
	}
}

func TestBoardReplyCannotSilentlyBecomeAPrivateInstruction(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	credential, err := managed.LoadCredential(managed.CredentialPath(state, "peer"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workCall(context.Background(), managed.NewClient(credential), "board.post", map[string]any{"board": "team", "text": "A shared proposal"}); err != nil {
		t.Fatal(err)
	}
	before := uat20Activity(t, client)
	var out, diagnostics bytes.Buffer
	code := runWorkSession("reply", []string{before.Messages[0].ID, "A shared response", "--state", state}, strings.NewReader(""), &out, &diagnostics)
	after := uat20Activity(t, client)
	if code == exitSuccess || !strings.Contains(diagnostics.String(), "explicit destination") || len(after.Tasks) != len(before.Tasks) || len(after.Messages) != len(before.Messages) {
		t.Fatalf("board reply changed scope: %d %s before=%+v after=%+v", code, &diagnostics, before, after)
	}
}

func TestConsoleRejectedTitleDiffersFromUncertainTransport(t *testing.T) {
	for _, tc := range []struct {
		code      string
		uncertain bool
	}{{"conflict", false}, {"forbidden", false}, {"transport", true}} {
		m := console{}
		next, _ := m.Update(consoleAction{labelID: "peer", labelText: "kept", err: &managed.Fault{Code: tc.code, Message: "Failure"}})
		m = next.(console)
		if strings.Contains(m.message, "delivery may be uncertain") != tc.uncertain || m.labelDrafts["peer"] != "kept" {
			t.Fatalf("wrong recovery for %s: %s", tc.code, m.message)
		}
	}
}

func TestAddAndConnectKeepBothExplicitSelectorsForValidation(t *testing.T) {
	for _, command := range []string{"add", "connect"} {
		var out, diagnostics bytes.Buffer
		var code int
		args := []string{"observed", "--session", "other", "--state", t.TempDir()}
		if command == "add" {
			code = runWorkAdd(args, &out, &diagnostics)
		} else {
			code = runWorkConnect(args, &out, &diagnostics)
		}
		if code == exitSuccess || !strings.Contains(diagnostics.String(), "either --session or --state") {
			t.Fatalf("%s silently preferred one selector: %d %s", command, code, &diagnostics)
		}
	}
}

func TestReopeningWorkspaceHonorsExplicitBrokerSandbox(t *testing.T) {
	// Substitute only the tmux observation boundary: run the real start parser,
	// saved config comparison and reopen path without creating any processes.
	root, err := os.MkdirTemp("", "amx-reopen-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	tmux := "#!/bin/sh\ncase \"$5\" in\nshow-options) printf 'fixture-attempt\\n' ;;\nlist-panes) printf '%%1\\t0\\tfixture-attempt\\tworker\\tworker\\n' ;;\n*) exit 97 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(root, "tmux"), []byte(tmux), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	for _, mode := range []string{"local", "bubblewrap"} {
		state := filepath.Join(root, mode)
		cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "reopen", Agents: map[string]managedconfig.Agent{
			"worker": {Harness: "command", Command: []string{"/bin/cat"}, Workdir: root},
		}}
		store, err := managed.Init(state, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		config := filepath.Join(root, mode+".yaml")
		definition, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config, definition, 0600); err != nil {
			t.Fatal(err)
		}
		runtimePath := filepath.Join(state, "runtime.json")
		runtime, err := json.Marshal(managedlaunch.Runtime{Version: 1, AttemptID: "fixture-attempt", Session: "reopen", State: "running", BrokerSandbox: mode, Agents: map[string]managedlaunch.AgentPane{}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(runtimePath, runtime, 0600); err != nil {
			t.Fatal(err)
		}
		for _, requested := range []string{"", "local", "bubblewrap"} {
			t.Run(mode+"/requested="+requested, func(t *testing.T) {
				args := []string{"--state", state, "--config", config, "--detach"}
				if requested != "" {
					args = append(args, "--sandbox="+requested)
				}
				var out, diagnostics bytes.Buffer
				code := runWorkSession("start", args, strings.NewReader(""), &out, &diagnostics)
				if requested != "" && requested != mode {
					if code == exitSuccess || !strings.Contains(diagnostics.String(), "requested \""+requested+"\"") || !strings.Contains(diagnostics.String(), "running workspace uses \""+mode+"\"") || strings.Contains(out.String(), "already running") {
						t.Fatalf("sandbox mismatch acknowledged as reopened: %d %s %s", code, &out, &diagnostics)
					}
				} else if code != exitSuccess || !strings.Contains(out.String(), "already running") {
					t.Fatalf("compatible reopen failed: %d %s %s", code, &out, &diagnostics)
				}
				after, err := os.ReadFile(runtimePath)
				if err != nil || !bytes.Equal(after, runtime) {
					t.Fatalf("reopen changed running metadata: %s %v", after, err)
				}
			})
		}
	}
}
