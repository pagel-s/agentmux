package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
)

func TestManagedShutdownFailureKeepsJSONAndAddsExactStateRecovery(t *testing.T) {
	for _, operation := range []string{"down", "restart"} {
		t.Run(operation, func(t *testing.T) {
			if operation == "restart" {
				if _, err := exec.LookPath("tmux"); err != nil {
					t.Skip("restart preflight requires tmux on PATH")
				}
			}
			// Keep the dedicated socket path short, and exercise literal shell
			// quoting without launching a broker, terminal or provider.
			dir, err := os.MkdirTemp("/tmp", "amx-stop-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			state := filepath.Join(dir, "owner's $(literal) `state`")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			store, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: "shutdown-guidance", Agents: map[string]managedconfig.Agent{
				"fixture": {Harness: "command", Command: []string{executable}, Sandbox: "local", Network: "host"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(state, "runtime.json")
			if err := os.WriteFile(manifest, []byte("{"), 0600); err != nil {
				t.Fatal(err)
			}
			var out, diagnostics bytes.Buffer
			code := runManagedIO([]string{operation, "--state", state}, strings.NewReader(""), &out, &diagnostics)
			var envelope managedTestEnvelope
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatalf("failure output stopped being JSON: %v; %s", err, out.String())
			}
			if code != 1 || envelope.OK || envelope.Operation != operation || envelope.Error == nil || envelope.Error.Code != "runtime" || envelope.Error.Message != "invalid managed runtime manifest" {
				t.Fatalf("shutdown error contract changed: exit=%d; envelope=%+v; diagnostics=%s", code, envelope, diagnostics.String())
			}
			for _, args := range [][]string{{"managed", "status", "--state", state}, {"attach", "--state", state}, {"managed", "down", "--state", state}} {
				command := managedlaunch.ShellCommand(append([]string{executable}, args...))
				if !strings.Contains(diagnostics.String(), command) {
					t.Fatalf("recovery omitted exact shell-safe command %q: %s", command, diagnostics.String())
				}
			}
			for _, text := range []string{"availability are unconfirmed", "owned terminal", "exit its native CLI normally", "detaching does not exit"} {
				if !strings.Contains(diagnostics.String(), text) {
					t.Fatalf("recovery omitted meaningful guidance %q: %s", text, diagnostics.String())
				}
			}
			restart := managedlaunch.ShellCommand([]string{executable, "managed", "restart", "--state", state})
			if strings.Contains(diagnostics.String(), restart) != (operation == "restart") || operation == "restart" && !strings.Contains(diagnostics.String(), "Only after shutdown succeeds") {
				t.Fatalf("restart guidance did not require confirmed shutdown: %s", diagnostics.String())
			}
			if strings.Contains(diagnostics.String(), "broker remains available") || strings.Contains(diagnostics.String(), "kill-server") || strings.Contains(out.String(), "Then retry") {
				t.Fatal("recovery assumed broker liveness, suggested unowned shutdown or contaminated JSON")
			}
			if data, err := os.ReadFile(manifest); err != nil || string(data) != "{" {
				t.Fatalf("failure guidance modified runtime metadata: %q, %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(state, "runtime-stop.json")); !os.IsNotExist(err) {
				t.Fatalf("invalid metadata path attempted to stop processes: %v", err)
			}
		})
	}
}

func TestManagedDownAlreadyStoppedDoesNotPrintRecovery(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-stopped-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	var out, diagnostics bytes.Buffer
	code := runManagedIO([]string{"down", "--state", filepath.Join(dir, "absent")}, strings.NewReader(""), &out, &diagnostics)
	if code != exitSuccess || diagnostics.Len() != 0 || !strings.Contains(out.String(), `"state": "stopped"`) {
		t.Fatalf("idempotent down changed: exit=%d; out=%s; diagnostics=%s", code, out.String(), diagnostics.String())
	}
}
