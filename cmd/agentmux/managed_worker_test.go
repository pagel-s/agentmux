package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func TestManagedWorkerLauncherFlagIsExplicitAndWorkerOnly(t *testing.T) {
	options, err := parseManagedCLI("worker", []string{"--state", "state", "--agent", "native", "--native-launcher", "/selected/bin/codex", "--native-resume", "last"})
	if err != nil || options.values["native-launcher"] != "/selected/bin/codex" || options.values["native-resume"] != "last" {
		t.Fatalf("explicit launcher/resume rejected: %+v err=%v", options, err)
	}
	for _, args := range [][]string{
		{"worker", "--state", "state", "--agent", "native", "--native-launcher", "relative/codex"},
		{"worker", "--state", "state", "--agent", "native", "--native-launcher"},
		{"worker", "--state", "state", "--agent", "native", "--native-launcher", "/first", "--native-launcher", "/second"},
		{"serve", "--state", "state", "--native-launcher", "/selected/bin/codex"},
	} {
		if _, err := parseManagedCLI(args[0], args[1:]); err == nil {
			t.Fatalf("ambiguous/unsupported launcher arguments accepted: %q", args)
		}
	}
	var help bytes.Buffer
	printManagedUsage(&help)
	for _, text := range []string{"--native-launcher ABSOLUTE_PATH", "this invocation", "trailing command arguments", "never rewrites"} {
		if !strings.Contains(help.String(), text) {
			t.Fatalf("help omits recovery boundary %q", text)
		}
	}
}

func TestManagedWorkerMachineResultKeepsEnvelopeAndExitCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code int
		ok   bool
	}{
		{"normal", nil, exitSuccess, true},
		{"cancelled", context.Canceled, exitSuccess, true},
		{"failed", errors.New("selected executable is unavailable"), exitRuntime, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			code := writeManagedWorkerResult(&out, "native", true, tc.err)
			var result managedTestEnvelope
			if code != tc.code || json.Unmarshal(out.Bytes(), &result) != nil || result.Kind != "managed" || result.Operation != "worker" || result.OK != tc.ok {
				t.Fatalf("machine worker protocol changed: code=%d output=%q", code, out.String())
			}
			if tc.ok && !bytes.Contains(result.Result, []byte(`"stopped": true`)) {
				t.Fatalf("machine completion result changed: %s", result.Result)
			}
			if !tc.ok && (result.Error == nil || result.Error.Message != tc.err.Error()) {
				t.Fatalf("machine failure detail changed: %+v", result)
			}
		})
	}
	file, err := os.CreateTemp(t.TempDir(), "redirected-result")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if managedWorkerTerminal(file) {
		t.Fatal("ordinary output file was treated as an interactive terminal")
	}
}

func TestManagedWorkerPresentationReadsCurrentPersistedAgent(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	store, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: "native-exit", Agents: map[string]managedconfig.Agent{
		"native": {Harness: "command", Command: []string{"/bin/true"}, Interaction: "interactive"},
		"queued": {Harness: "command", Command: []string{"/bin/true"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if !managedWorkerIsInteractive(managed.WorkerOptions{StateDir: state, AgentID: "native"}) || managedWorkerIsInteractive(managed.WorkerOptions{StateDir: state, AgentID: "queued"}) || managedWorkerIsInteractive(managed.WorkerOptions{StateDir: state, AgentID: "missing"}) {
		t.Fatal("exit presentation confused a native terminal with a managed/missing worker")
	}
}
