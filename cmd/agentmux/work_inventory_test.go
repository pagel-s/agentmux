package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
	"agentmux/internal/sessioninventory"
)

// The subprocess is the actual CLI implementation from this test build, not a
// mock of its credential selection. No provider, native terminal or tmux server
// is started by these tests.
func init() {
	if os.Getenv("AGENTMUX_BRIDGE_TEST_CONTROLLER") == "1" && len(os.Args) > 2 && os.Args[1] == "managed" {
		os.Exit(runManagedIO(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
}

func inventoryBroker(t *testing.T) (string, *managed.Client, sessioninventory.Session) {
	t.Helper()
	root, err := os.MkdirTemp("", "amx-bridge 'quoted'-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	state := filepath.Join(root, "state 'one'")
	project := filepath.Join(root, "project 'one'")
	if err = os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "human", Agents: map[string]managedconfig.Agent{
		"peer": {Harness: "command", Command: []string{"/bin/cat"}, Sandbox: "local", Workdir: project},
	}, Boards: map[string]managedconfig.Access{"team": {Readers: []string{"*"}, Writers: []string{"*"}}}, Memory: map[string]managedconfig.Access{"team": {Readers: []string{"*"}, Writers: []string{"*"}}}}
	store, err := managed.Init(state, cfg)
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
	until := time.Now().Add(3 * time.Second)
	for {
		if _, err = workCall(ctx, client, "identity", map[string]any{}); err == nil {
			break
		}
		select {
		case serveErr := <-done:
			done <- serveErr
			t.Fatalf("broker failed: %v", serveErr)
		default:
		}
		if time.Now().After(until) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	controller := filepath.Join(root, "controller 'quoted'")
	if err = os.Symlink(executable, controller); err != nil {
		t.Fatal(err)
	}
	runtime := managedlaunch.Runtime{Version: 1, AttemptID: "fixture-attempt", Session: "fixture", Controller: controller}
	raw, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(state, "runtime.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	observed := sessioninventory.Session{ID: "process:123:stable-start", Harness: "claude", Project: project, Source: "external-terminal", PID: 123, TTY: "pts/9"}
	previous := discoverWorkSessions
	discoverWorkSessions = func(context.Context, string) []sessioninventory.Session { return []sessioninventory.Session{observed} }
	t.Cleanup(func() { discoverWorkSessions = previous })
	return state, client, observed
}

func inventoryRegistry(t *testing.T, client *managed.Client) []managed.AgentInfo {
	t.Helper()
	raw, err := workCall(context.Background(), client, "agent.list", map[string]any{"limit": 200})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Agents []managed.AgentInfo `json:"agents"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result.Agents
}

func TestExternalBridgeRetainsObservationAndUsesAssignedIdentity(t *testing.T) {
	state, client, observed := inventoryBroker(t)
	instructions, err := connectObservedSession(context.Background(), state, observed.ID, "native-one")
	if err != nil {
		t.Fatal(err)
	}
	var bridge managed.AgentInfo
	for _, agent := range inventoryRegistry(t, client) {
		if agent.ID == "native-one" {
			bridge = agent
		}
	}
	if bridge.ID != "native-one" || !bridge.External || bridge.ExternalRef != observed.ID || bridge.State != "active" || bridge.Config.Interaction != "interactive" || bridge.Config.Workdir != observed.Project {
		t.Fatalf("wrong identity/linkage: %+v", bridge)
	}
	if !reflect.DeepEqual(bridge.Capabilities, managed.Capabilities{}) || bridge.Config.Autonomy.Enabled {
		t.Fatalf("bridge acquired automation capabilities: %+v", bridge)
	}
	helper := filepath.Join(state+".bridge", "native-one")
	info, err := os.Stat(helper)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("helper permissions: %v %v", info, err)
	}
	helperText, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := managed.LoadCredential(managed.CredentialPath(state, ""))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(helperText), owner.Token) || strings.Contains(instructions, owner.Token) || strings.Contains(string(helperText), managed.CredentialPath(state, "")) {
		t.Fatal("bridge exposed or selected owner credentials")
	}
	if !strings.Contains(instructions, "Keep the current native conversation, tools, skills, MCPs") || !strings.Contains(instructions, "Collaboration is off") || !strings.Contains(instructions, "Broker notes are observations") {
		t.Fatal("setup lost native-workflow/permission instructions")
	}
	// Inherited credentials and a different workspace's socket must never alter
	// this helper's assigned identity. This also exercises executable/helper/
	// credential paths containing both whitespace and single quotes.
	cmd := exec.Command(helper, "identity")
	cmd.Env = append(os.Environ(), "AGENTMUX_BRIDGE_TEST_CONTROLLER=1", "AGENTMUX_CREDENTIAL_FILE="+managed.CredentialPath(state, ""), "AGENTMUX_SOCKET="+filepath.Join(state, "wrong.sock"))
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper identity failed: %v %s", err, data)
	}
	var envelope struct {
		OK     bool `json:"ok"`
		Result struct {
			AgentID string `json:"agent_id"`
			Owner   bool   `json:"owner"`
		} `json:"result"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil || !envelope.OK || envelope.Result.AgentID != "native-one" || envelope.Result.Owner {
		t.Fatalf("helper used wrong principal: %s (%v)", data, err)
	}

	for _, args := range [][]string{{"control.get"}, {"identity", "--credential-file", managed.CredentialPath(state, "")}, {"identity", "--socket", managed.SocketPath(state)}} {
		cmd := exec.Command(helper, args...)
		cmd.Env = append(os.Environ(), "AGENTMUX_BRIDGE_TEST_CONTROLLER=1")
		data, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("bridge accepted owner operation or override %q: %s", args, data)
		}
		if strings.Contains(string(data), owner.Token) {
			t.Fatal("rejected override exposed credentials")
		}
	}
	raw, err := workCall(context.Background(), client, "activity", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var activity managedActivitySnapshot
	if err = json.Unmarshal(raw, &activity); err != nil {
		t.Fatal(err)
	}
	if len(activity.Tasks) != 0 || len(activity.Messages) != 0 {
		t.Fatalf("connecting created unsolicited instructions/messages: %s", raw)
	}
	beforeCredential, err := os.ReadFile(managed.CredentialPath(state, "native-one"))
	if err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := runWorkInventory([]string{"--state", state}, &out, &diagnostics); code != 0 {
		t.Fatalf("inventory: %d %s", code, diagnostics.String())
	}
	if !strings.Contains(out.String(), "native-one") || !strings.Contains(out.String(), "external bridge") {
		t.Fatalf("connected session not listed: %s", out.String())
	}
	afterCredential, err := os.ReadFile(managed.CredentialPath(state, "native-one"))
	if err != nil || !bytes.Equal(beforeCredential, afterCredential) {
		t.Fatal("observation rotated the connected identity")
	}
}

func TestExternalBridgeRejectsStaleObservationAndPreservesExistingHelper(t *testing.T) {
	state, client, observed := inventoryBroker(t)
	discoverWorkSessions = func(context.Context, string) []sessioninventory.Session { return nil }
	if _, err := connectObservedSession(context.Background(), state, observed.ID, "stale"); err == nil || !strings.Contains(err.Error(), "no longer live") {
		t.Fatalf("stale observation accepted: %v", err)
	}
	if len(inventoryRegistry(t, client)) != 1 {
		t.Fatal("stale observation registered an agent")
	}
	if _, err := os.Stat(state + ".bridge"); !os.IsNotExist(err) {
		t.Fatal("stale observation created bridge files")
	}
	discoverWorkSessions = func(context.Context, string) []sessioninventory.Session { return []sessioninventory.Session{observed} }
	if _, err := connectObservedSession(context.Background(), state, observed.ID, "native-one"); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(state+".bridge", "native-one")
	before, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connectObservedSession(context.Background(), state, observed.ID, "native-one"); err != nil {
		t.Fatalf("repeated connect did not recover existing identity: %v", err)
	}
	after, err := os.ReadFile(helper)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("duplicate registration changed existing helper")
	}
	if _, err := connectObservedSession(context.Background(), state, observed.ID, "peer"); err == nil {
		t.Fatal("bridge replaced configured peer")
	}
	if _, err := os.Stat(filepath.Join(state+".bridge", "peer")); !os.IsNotExist(err) {
		t.Fatal("failed registration retained helper")
	}
	if len(inventoryRegistry(t, client)) != 2 {
		t.Fatal("failed registration changed registry")
	}
}

func TestExternalBridgeRejectsUnknownProjectAndUnsafeDirectory(t *testing.T) {
	state, client, observed := inventoryBroker(t)
	observed.Project = ""
	discoverWorkSessions = func(context.Context, string) []sessioninventory.Session { return []sessioninventory.Session{observed} }
	if _, err := connectObservedSession(context.Background(), state, observed.ID, "no-project"); err == nil {
		t.Fatal("bridge accepted unknown project")
	}
	if len(inventoryRegistry(t, client)) != 1 {
		t.Fatal("unknown project registered an agent")
	}
	observed.Project = t.TempDir()
	if err := os.Mkdir(state+".bridge", 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := connectObservedSession(context.Background(), state, observed.ID, "insecure"); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("insecure directory accepted: %v", err)
	}
	if len(inventoryRegistry(t, client)) != 1 {
		t.Fatal("insecure helper registered an agent")
	}
}

func TestExternalBridgeReferenceIsUniqueAndOnlyExternal(t *testing.T) {
	state, client, observed := inventoryBroker(t)
	if _, err := connectObservedSession(context.Background(), state, observed.ID, "native-one"); err != nil {
		t.Fatal(err)
	}
	if _, err := connectObservedSession(context.Background(), state, observed.ID, "alias"); err == nil || !strings.Contains(err.Error(), "native-one") {
		t.Fatalf("duplicate terminal alias accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(state+".bridge", "alias")); !os.IsNotExist(err) {
		t.Fatal("duplicate observation created another helper")
	}
	definition := managedconfig.Agent{Harness: "claude", Interaction: "interactive", Sandbox: "local", Workdir: observed.Project}
	for _, args := range []map[string]any{
		{"id": "api-alias", "config": definition, "external": true, "external_ref": observed.ID},
		{"id": "invalid-native", "config": definition, "external": false, "external_ref": "other-observation"},
	} {
		if _, err := workCall(context.Background(), client, "agent.add", args); err == nil {
			t.Fatalf("broker accepted invalid linkage: %+v", args)
		}
		if _, err := os.Stat(managed.CredentialPath(state, args["id"].(string))); !os.IsNotExist(err) {
			t.Fatal("rejected linkage created credentials")
		}
	}
	if got := inventoryRegistry(t, client); len(got) != 2 {
		t.Fatalf("duplicate observation changed registry: %+v", got)
	}
}

func TestExternalBridgeRetainsHelperAfterResponseLoss(t *testing.T) {
	for _, committed := range []bool{true, false} {
		t.Run(fmt.Sprintf("committed-%v", committed), func(t *testing.T) {
			state, client, observed := inventoryBroker(t)
			path := managed.CredentialPath(state, "")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var credential managed.Credential
			if err = json.Unmarshal(original, &credential); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", filepath.Join(state, "proxy.sock"))
			if err != nil {
				t.Fatal(err)
			}
			var addCalls atomic.Int32
			proxy := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request managed.Request
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					http.Error(w, "invalid", 400)
					return
				}
				var result json.RawMessage
				var problem *managed.Fault
				if request.Operation != "agent.add" || committed {
					result, problem = client.Call(r.Context(), request)
				}
				if request.Operation == "agent.add" && problem == nil {
					addCalls.Add(1)
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close()
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": problem})
			})}
			done := make(chan error, 1)
			go func() { done <- proxy.Serve(listener) }()
			t.Cleanup(func() { _ = proxy.Close(); <-done })
			credential.Socket = listener.Addr().String()
			raw, err := json.Marshal(credential)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			_, err = connectObservedSession(context.Background(), state, observed.ID, "uncertain-native")
			if err == nil || !strings.Contains(err.Error(), "outcome is uncertain") {
				t.Fatalf("interrupted registration hid uncertainty: %v", err)
			}
			helper := filepath.Join(state+".bridge", "uncertain-native")
			before, err := os.ReadFile(helper)
			if err != nil {
				t.Fatalf("uncertain registration removed its helper: %v", err)
			}
			beforeIdentity, err := os.ReadFile(managed.CredentialPath(state, "uncertain-native"))
			if committed && err != nil {
				t.Fatal(err)
			}
			if !committed && !os.IsNotExist(err) {
				t.Fatal("pre-commit failure created credentials")
			}
			if err = os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = connectObservedSession(context.Background(), state, observed.ID, "uncertain-native"); err != nil {
				t.Fatalf("recovery of committed bridge failed: %v", err)
			}
			after, err := os.ReadFile(helper)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("recovery rewrote its helper")
			}
			afterIdentity, err := os.ReadFile(managed.CredentialPath(state, "uncertain-native"))
			if err != nil || committed && !bytes.Equal(beforeIdentity, afterIdentity) {
				t.Fatal("recovery lost or changed its identity")
			}
			if addCalls.Load() != 1 || len(inventoryRegistry(t, client)) != 2 {
				t.Fatal("recovery retried registration")
			}
		})
	}
}

func TestExternalBridgeRejectsUntrustedRecoveredFiles(t *testing.T) {
	for _, kind := range []string{"public-helper", "changed-helper", "symlink-helper", "oversized-helper", "owner-credential"} {
		t.Run(kind, func(t *testing.T) {
			state, client, observed := inventoryBroker(t)
			if _, err := connectObservedSession(context.Background(), state, observed.ID, "native-one"); err != nil {
				t.Fatal(err)
			}
			helper := filepath.Join(state+".bridge", "native-one")
			switch kind {
			case "public-helper":
				if err := os.Chmod(helper, 0755); err != nil {
					t.Fatal(err)
				}
			case "changed-helper":
				if err := os.WriteFile(helper, []byte("#!/bin/sh\necho changed\n"), 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink-helper":
				original := helper + ".original"
				if err := os.Rename(helper, original); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(original, helper); err != nil {
					t.Fatal(err)
				}
			case "oversized-helper":
				if err := os.WriteFile(helper, []byte(strings.Repeat("x", 1<<20)), 0700); err != nil {
					t.Fatal(err)
				}
			case "owner-credential":
				owner, err := os.ReadFile(managed.CredentialPath(state, ""))
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(managed.CredentialPath(state, "native-one"), owner, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := connectObservedSession(context.Background(), state, observed.ID, "native-one"); err == nil {
				t.Fatalf("recovered untrusted %s", kind)
			}
			if len(inventoryRegistry(t, client)) != 2 {
				t.Fatal("invalid helper changed registry")
			}
		})
	}
}
