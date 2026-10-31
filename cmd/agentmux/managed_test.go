package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
)

type managedTestEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	Kind          string          `json:"kind"`
	Operation     string          `json:"operation"`
	OK            bool            `json:"ok"`
	RequestID     string          `json:"request_id"`
	Result        json.RawMessage `json:"result"`
	Error         *managed.Fault  `json:"error"`
}

func managedTestCall(t *testing.T, stdin string, args ...string) (managedTestEnvelope, int) {
	t.Helper()
	var out, diagnostics bytes.Buffer
	code := runManagedIO(args, strings.NewReader(stdin), &out, &diagnostics)
	var envelope managedTestEnvelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("managed output is not JSON: %v; output %q; diagnostics %q", err, out.String(), diagnostics.String())
	}
	if envelope.SchemaVersion != 1 || envelope.Kind != "managed" {
		t.Fatalf("unexpected managed envelope: %+v", envelope)
	}
	return envelope, code
}

func TestManagedCLIRejectsInvalidFlagsAndArguments(t *testing.T) {
	for _, args := range [][]string{
		{"init", "config.yaml"},
		{"serve", "--state", "x", "unexpected"},
		{"worker", "--state", "x"},
		{"rotate", "--state", "x"},
		{"call", "identity", "--json", "{}", "--stdin"},
		{"call", "identity", "--credential-file", "x", "--credential-file", "y"},
		{"serve", "--state", "--agent", "x"},
		{"call", "identity", "--stdin=false"},
		{"serve", "--state", "x", "--sandbox", "magic"},
		{"unknown"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := parseManagedCLI(args[0], args[1:]); err == nil {
				t.Fatalf("accepted invalid arguments: %q", args)
			}
		})
	}
	options, err := parseManagedCLI("init", []string{"config.yaml", "--state=state"})
	if err != nil || options.values["state"] != "state" || options.positionals[0] != "config.yaml" {
		t.Fatalf("interspersed arguments: %+v, %v", options, err)
	}
}

func TestManagedCLIRejectsJSONWithoutEchoingInput(t *testing.T) {
	const sensitiveInput = `{"secret":"do-not-copy-this-value"} {}`
	var out bytes.Buffer
	code := runManagedIO([]string{"call", "identity", "--credential-file", "unused", "--json", sensitiveInput}, strings.NewReader(""), &out, io.Discard)
	if code != exitValidation || strings.Contains(out.String(), "do-not-copy-this-value") {
		t.Fatalf("invalid JSON failure exposed input: code %d, %s", code, out.String())
	}
}

func TestManagedUpPreflightDoesNotCreateStateOnFailure(t *testing.T) {
	dir := t.TempDir()
	path, state := filepath.Join(dir, "invalid-tool.yaml"), filepath.Join(dir, "state")
	config := "version: 2\nmode: managed\nsession: preflight\nagents:\n  worker:\n    harness: command\n    command: [/definitely/missing/agentmux-test-command]\n"
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	response, code := managedTestCall(t, "", "up", path, "--state", state)
	if code != exitValidation || response.OK || response.Error == nil || response.Error.Code != "preflight" {
		t.Fatalf("expected blocking preflight: %+v, code %d", response, code)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("failed preflight created state: %v", err)
	}
	if _, err := os.Stat(state + ".launch.lock"); !os.IsNotExist(err) {
		t.Fatalf("failed preflight created lifecycle lock: %v", err)
	}
}

func TestManagedUpMissingTMuxDoesNotCreateState(t *testing.T) {
	dir := t.TempDir()
	path, state := filepath.Join(dir, "command.yaml"), filepath.Join(dir, "state")
	if err := os.WriteFile(path, []byte("version: 2\nmode: managed\nsession: no-tmux\nagents:\n  worker:\n    harness: command\n    command: [/bin/cat]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	response, code := managedTestCall(t, "", "up", path, "--state", state)
	if code != exitValidation || response.OK || !bytes.Contains(response.Result, []byte(`"tmux.binary"`)) {
		t.Fatalf("missing tmux did not fail preflight: %+v, code %d", response, code)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("missing tmux created state: %v", err)
	}
}

func TestManagedCLICommandRoundTrip(t *testing.T) {
	if _, err := os.Stat("/bin/cat"); err != nil {
		t.Skip("command demo requires /bin/cat")
	}
	dir := t.TempDir()
	configPath, state := filepath.Join(dir, "demo.yaml"), filepath.Join(dir, "state")
	config := `version: 2
mode: managed
session: cli-test
agents:
  lead:
    harness: command
    command: [/bin/cat]
  worker:
    harness: command
    command: [/bin/cat]
    workdir: relative-work
    read_only_paths: [assets]
  owner:
    harness: command
    command: [/bin/cat]
policy:
  default: deny
  grants:
    - from: [lead]
      to: [worker]
      actions: [instruct]
limits:
  max_runtime_seconds: 10
`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	initialized, code := managedTestCall(t, "", "init", configPath, "--state", state)
	if code != exitSuccess || !initialized.OK {
		t.Fatalf("init failed: %+v", initialized)
	}
	store, err := managed.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if got := store.Config().Agents["worker"].Workdir; got != filepath.Join(dir, "relative-work") {
		t.Fatalf("workdir = %q, want path relative to config", got)
	}
	if got := store.Config().Agents["worker"].ReadOnlyPaths; len(got) != 1 || got[0] != filepath.Join(dir, "assets") {
		t.Fatalf("read_only_paths = %q, want path relative to config", got)
	}
	// The normal agent named owner must remain distinct from the reserved owner.
	rotated, code := managedTestCall(t, "", "rotate", "--state", state, "--agent", "owner")
	if code != exitSuccess || !rotated.OK {
		t.Fatalf("rotate configured owner agent: %+v", rotated)
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(state, "owner"))
	if err != nil || credential.AgentID != "owner" || credential.Generation != 2 {
		t.Fatalf("wrong rotated identity: %q generation %d, error %v", credential.AgentID, credential.Generation, err)
	}
	for _, envelope := range []managedTestEnvelope{initialized, rotated} {
		raw, _ := json.Marshal(envelope)
		if strings.Contains(string(raw), credential.Token) || strings.Contains(string(raw), `"token"`) {
			t.Fatal("CLI output contains a credential")
		}
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- managed.Serve(ctx, store) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("managed service did not stop")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(managed.SocketPath(state)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("managed socket did not appear")
		}
		time.Sleep(10 * time.Millisecond)
	}
	leadPath, workerPath := managed.CredentialPath(state, "lead"), managed.CredentialPath(state, "worker")
	t.Setenv("AGENTMUX_CREDENTIAL_FILE", workerPath)
	t.Setenv("AGENTMUX_SOCKET", managed.SocketPath(state))
	// Explicit credentials override the ambient worker identity. Stdin passes
	// arguments without shell interpolation, and a retry creates only one task.
	instruction := `{"to":["worker"],"text":"literal task $(expression)"}`
	for attempt := 0; attempt < 2; attempt++ {
		response, code := managedTestCall(t, instruction, "call", "instruct", "--credential-file", leadPath, "--stdin", "--request-id", "cli-roundtrip")
		if code != exitSuccess || !response.OK || response.RequestID != "cli-roundtrip" {
			t.Fatalf("instruct: %+v, exit %d", response, code)
		}
	}
	denied, code := managedTestCall(t, `{"to":["lead"],"text":"not authorized"}`, "call", "instruct", "--stdin")
	if code != exitValidation || denied.Error == nil || denied.Error.Code != "forbidden" || denied.RequestID == "" {
		t.Fatalf("expected policy denial with retry identity, got %+v, exit %d", denied, code)
	}
	finished, code := managedTestCall(t, "", "worker", "--state", state, "--agent", "worker", "--once")
	if code != exitSuccess || !finished.OK {
		t.Fatalf("worker: %+v, exit %d", finished, code)
	}
	listed, code := managedTestCall(t, "", "call", "task.list", "--credential-file", leadPath)
	if code != exitSuccess || !listed.OK {
		t.Fatalf("task.list: %+v, exit %d", listed, code)
	}
	var tasks struct {
		Tasks []managed.Task `json:"tasks"`
	}
	if err := json.Unmarshal(listed.Result, &tasks); err != nil {
		t.Fatal(err)
	}
	if len(tasks.Tasks) != 1 || tasks.Tasks[0].State != "submitted" || !strings.Contains(tasks.Tasks[0].Result, "literal task $(expression)") {
		t.Fatalf("unexpected handoff result: %+v", tasks.Tasks)
	}
	activity, code := managedTestCall(t, "", "activity", "--state", state)
	var snapshot managedActivitySnapshot
	if code != exitSuccess || json.Unmarshal(activity.Result, &snapshot) != nil || len(snapshot.Tasks) != 1 || snapshot.Tasks[0].Result != tasks.Tasks[0].Result {
		t.Fatalf("owner activity omitted the task result: %+v, code %d", activity, code)
	}
	deniedActivity, code := managedTestCall(t, "", "call", "activity", "--credential-file", workerPath)
	if code != exitValidation || deniedActivity.Error == nil || deniedActivity.Error.Code != "forbidden" {
		t.Fatalf("ordinary agent could access owner activity: %+v, code %d", deniedActivity, code)
	}
}
