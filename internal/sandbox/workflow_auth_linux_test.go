//go:build linux

package sandbox_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/agentauth"
	"agentmux/internal/harness"
	"agentmux/internal/managedconfig"
	"agentmux/internal/sandbox"
)

func workflowBoundaryFixture(t *testing.T) (string, string, sandbox.WorkflowOptions) {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "run")
	workspace := filepath.Join(state, "worktree")
	for _, path := range []string{state, workspace} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return root, workspace, sandbox.WorkflowOptions{Workspace: workspace, StateDir: state, Network: "none"}
}

func requireWorkflowBoundary(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENTMUX_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_SANDBOX_INTEGRATION=1 for actual sealed credential namespace tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sandbox.ProbeWorkflow(ctx); err != nil {
		t.Fatal(err)
	}
}

func workflowAnonymousAuth(t *testing.T, source string) (*agentauth.Prepared, []sandbox.HomeFile) {
	t.Helper()
	prepared, err := agentauth.PrepareAnonymous("codex", managedconfig.NativeAuth{File: source})
	if err != nil {
		t.Fatal(err)
	}
	homes := []sandbox.HomeFile{}
	for _, file := range prepared.Files {
		homes = append(homes, sandbox.HomeFile{Source: file.Source, Target: file.Target, Descriptor: file.Descriptor})
	}
	return prepared, homes
}

func TestWorkflowBoundaryRealFilteredAnonymousAuthIsReadOnly(t *testing.T) {
	requireWorkflowBoundary(t)
	root, workspace, options := workflowBoundaryFixture(t)
	source := filepath.Join(root, "owner-login.json")
	if err := os.WriteFile(source, []byte(`{"OPENAI_API_KEY":"selected-native-credential-fixture","unrelated_owner_data":"must-stay-host-only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, homes := workflowAnonymousAuth(t, source)
	defer prepared.Close()
	options.HomeFiles = homes
	script := fmt.Sprintf(`import json,pathlib
p=pathlib.Path('/home/agent/.codex/auth.json')
assert json.loads(p.read_text())=={'OPENAI_API_KEY':'selected-native-credential-fixture'}
assert not pathlib.Path(%q).exists()
assert not pathlib.Path(%q).exists()
try:
    p.write_text('mutated')
    raise AssertionError('read-only credential could be modified')
except OSError: pass
print('ANONYMOUS_AUTH_OK')
`, source, homes[0].Source)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := harness.Run(ctx, harness.Request{Harness: "command", Command: []string{"/usr/bin/python3", "-c", script}, Workdir: workspace, Wrap: func(spec harness.Spec) (harness.Spec, error) { return sandbox.WrapWorkflow(spec, options) }, ReapDescendants: true})
	if err != nil || !strings.Contains(result.Text, "ANONYMOUS_AUTH_OK") {
		t.Fatalf("anonymous read-only mount failed: %v %s", err, result.Text)
	}
	data, err := os.ReadFile(source)
	if err != nil || !strings.Contains(string(data), "must-stay-host-only") {
		t.Fatal("original login changed")
	}
}

func TestWorkflowAnonymousCredentialOwner(t *testing.T) {
	if os.Getenv("AGENTMUX_ANONYMOUS_OWNER") != "1" {
		t.Skip("owner subprocess fixture")
	}
	root, workspace := os.Getenv("AGENTMUX_ANONYMOUS_STATE"), os.Getenv("AGENTMUX_ANONYMOUS_WORKSPACE")
	prepared, homes := workflowAnonymousAuth(t, os.Getenv("AGENTMUX_ANONYMOUS_SOURCE"))
	defer prepared.Close()
	if err := os.WriteFile(os.Getenv("AGENTMUX_ANONYMOUS_REFERENCE"), []byte(homes[0].Source), 0600); err != nil {
		t.Fatal(err)
	}
	script := `import json,pathlib,time
assert json.loads(pathlib.Path('/home/agent/.codex/auth.json').read_text())['OPENAI_API_KEY']=='selected-native-credential-fixture'
pathlib.Path('ready').write_text('ready')
time.sleep(0.8)
pathlib.Path('must-not-survive').write_text('escaped')
time.sleep(30)
`
	options := sandbox.WorkflowOptions{StateDir: root, Workspace: workspace, HomeFiles: homes, Network: "none"}
	_, err := harness.Run(context.Background(), harness.Request{Harness: "command", Command: []string{"/usr/bin/python3", "-c", script}, Workdir: workspace, Wrap: func(spec harness.Spec) (harness.Spec, error) { return sandbox.WrapWorkflow(spec, options) }, ReapDescendants: true})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowBoundaryRealOwnerSIGKILLStrandsNoCredentialCopy(t *testing.T) {
	requireWorkflowBoundary(t)
	root, workspace, options := workflowBoundaryFixture(t)
	source := filepath.Join(root, "owner-login.json")
	original := []byte(`{"OPENAI_API_KEY":"selected-native-credential-fixture","unrelated_owner_data":"host-only"}`)
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(root, "owner-temporary")
	if err := os.Mkdir(temporary, 0700); err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(root, "anonymous-ref")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestWorkflowAnonymousCredentialOwner$")
	cmd.Env = append(os.Environ(), "AGENTMUX_ANONYMOUS_OWNER=1", "AGENTMUX_ANONYMOUS_STATE="+options.StateDir, "AGENTMUX_ANONYMOUS_WORKSPACE="+workspace, "AGENTMUX_ANONYMOUS_SOURCE="+source, "AGENTMUX_ANONYMOUS_REFERENCE="+reference, "TMPDIR="+temporary)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(workspace, "ready")); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(workspace, "ready")); err != nil {
		t.Fatal("native process did not read the selected anonymous credential")
	}
	data, err := os.ReadFile(reference)
	if err != nil {
		t.Fatal(err)
	}
	var filtered map[string]any
	before, err := os.ReadFile(string(data))
	if err != nil || json.Unmarshal(before, &filtered) != nil || len(filtered) != 1 {
		t.Fatal("anonymous reference did not identify a filtered object")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	time.Sleep(time.Second)
	if _, err := os.Stat(string(data)); !os.IsNotExist(err) {
		t.Fatal("owner death left the credential descriptor addressable")
	}
	if _, err := os.Stat(filepath.Join(workspace, "must-not-survive")); !os.IsNotExist(err) {
		t.Fatal("native process survived controller SIGKILL")
	}
	files, err := os.ReadDir(temporary)
	if err != nil || len(files) != 0 {
		t.Fatal("controller SIGKILL stranded a pathname-backed credential copy")
	}
	after, err := os.ReadFile(source)
	if err != nil || string(after) != string(original) {
		t.Fatal("original host login was modified")
	}
}
