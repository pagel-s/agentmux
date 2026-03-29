package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/resolve"
	"agentmux/internal/tmux"
)

func TestVersionString(t *testing.T) {
	original := version
	version = "test"
	t.Cleanup(func() {
		version = original
	})

	if got, want := versionString(), "agentmux test"; got != want {
		t.Fatalf("versionString() = %q, want %q", got, want)
	}
}

func TestRunVersionReturnsSuccess(t *testing.T) {
	if got, want := run([]string{"version"}), exitSuccess; got != want {
		t.Fatalf("run(version) = %d, want %d", got, want)
	}
	if got, want := run([]string{"--version"}), exitSuccess; got != want {
		t.Fatalf("run(--version) = %d, want %d", got, want)
	}
}

func TestRunWithoutArgsReturnsValidationExitCode(t *testing.T) {
	if got, want := run(nil), exitValidation; got != want {
		t.Fatalf("run(nil) = %d, want %d", got, want)
	}
}

func TestRunValidateMissingFileReturnsConfigParseExitCode(t *testing.T) {
	if got, want := run([]string{"validate", "does-not-exist.yaml"}), exitConfigParse; got != want {
		t.Fatalf("run(validate missing-file) = %d, want %d", got, want)
	}
}

func TestRenderStatusJSON(t *testing.T) {
	out, err := renderStatusJSON("dogfood", "resolve", []tmux.PaneStatus{
		{
			WindowIndex:  1,
			WindowName:   "grid",
			WindowID:     "@1",
			WindowActive: true,
			PaneIndex:    4,
			PaneID:       "%9",
			PaneActive:   true,
			Dead:         true,
			Name:         "resolve",
			Title:        "Resolve",
			SourceType:   "agent",
			AgentName:    "reviewer",
			OnExit:       "restart",
			CurrentCmd:   "bash",
		},
	})
	if err != nil {
		t.Fatalf("renderStatusJSON returned error: %v", err)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("renderStatusJSON output %q is missing trailing newline", out)
	}

	var decoded struct {
		SchemaVersion int               `json:"schema_version"`
		Kind          string            `json:"kind"`
		Session       string            `json:"session"`
		Pane          string            `json:"pane"`
		Panes         []tmux.PaneStatus `json:"panes"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded.SchemaVersion != jsonSchemaV1 {
		t.Fatalf("SchemaVersion = %d, want %d", decoded.SchemaVersion, jsonSchemaV1)
	}
	if decoded.Kind != "status" {
		t.Fatalf("Kind = %q, want %q", decoded.Kind, "status")
	}
	if decoded.Session != "dogfood" {
		t.Fatalf("Session = %q, want %q", decoded.Session, "dogfood")
	}
	if decoded.Pane != "resolve" {
		t.Fatalf("Pane = %q, want %q", decoded.Pane, "resolve")
	}
	if len(decoded.Panes) != 1 || decoded.Panes[0].Name != "resolve" {
		t.Fatalf("decoded panes = %#v, want resolve pane", decoded.Panes)
	}
	if decoded.Panes[0].SourceType != "agent" || decoded.Panes[0].AgentName != "reviewer" || decoded.Panes[0].OnExit != "restart" || decoded.Panes[0].CurrentCmd != "bash" || !decoded.Panes[0].Dead {
		t.Fatalf("decoded pane metadata = %#v, want agent reviewer restart bash dead=true", decoded.Panes[0])
	}
}

func TestRenderCaptureJSON(t *testing.T) {
	out, err := renderCaptureJSON("dogfood", "review", 5, true, "hello\nworld\n")
	if err != nil {
		t.Fatalf("renderCaptureJSON returned error: %v", err)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("renderCaptureJSON output %q is missing trailing newline", out)
	}

	var decoded struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Session       string `json:"session"`
		Pane          string `json:"pane"`
		TailLines     int    `json:"tail_lines"`
		StripEmpty    bool   `json:"strip_empty"`
		Content       string `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded.SchemaVersion != jsonSchemaV1 || decoded.Kind != "capture" || decoded.Session != "dogfood" || decoded.Pane != "review" || decoded.TailLines != 5 || !decoded.StripEmpty || decoded.Content != "hello\nworld\n" {
		t.Fatalf("decoded = %#v, want session dogfood pane review content preserved", decoded)
	}
}

func TestRenderErrorJSON(t *testing.T) {
	out, err := renderErrorJSON("status", exitRuntime, `session "missing" not found`, "missing", "")
	if err != nil {
		t.Fatalf("renderErrorJSON returned error: %v", err)
	}

	var decoded struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Command       string `json:"command"`
		ErrorCode     string `json:"error_code"`
		ExitCode      int    `json:"exit_code"`
		Message       string `json:"message"`
		Session       string `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded.SchemaVersion != jsonSchemaV1 || decoded.Kind != "error" || decoded.Command != "status" || decoded.ErrorCode != "runtime" || decoded.ExitCode != exitRuntime || decoded.Session != "missing" {
		t.Fatalf("decoded error = %#v, want stable runtime error envelope", decoded)
	}
}

func TestRenderStatusIncludesRuntimeMetadata(t *testing.T) {
	out := renderStatus("dogfood", []tmux.PaneStatus{
		{
			WindowIndex: 1,
			WindowName:  "grid",
			PaneIndex:   5,
			PaneID:      "%10",
			Name:        "resolve",
			Title:       "Resolve",
			SourceType:  "agent",
			AgentName:   "reviewer",
			OnExit:      "restart",
			CurrentCmd:  "bash",
			Dead:        true,
			PaneActive:  true,
		},
	})

	for _, want := range []string{
		"pane 5 resolve",
		"id=%10",
		"source: agent",
		"(reviewer)",
		"on_exit: restart",
		"command: bash",
		"DEAD (status 0)",
		"[active]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("renderStatus output %q does not include %q", out, want)
		}
	}
}

func TestRunStatusJSONErrorForMissingSession(t *testing.T) {
	if _, err := exec.LookPath(tmux.Binary()); err != nil {
		t.Skip("tmux not installed")
	}

	code, stdout, stderr := captureRunOutput(t, []string{"status", "missing-session", "--json"})
	if code != exitRuntime {
		t.Fatalf("code = %d, want %d", code, exitRuntime)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("stderr = %q, want empty stderr for json error", stderr)
	}

	var decoded struct {
		Kind      string `json:"kind"`
		Command   string `json:"command"`
		ErrorCode string `json:"error_code"`
		ExitCode  int    `json:"exit_code"`
		Message   string `json:"message"`
		Session   string `json:"session"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded.Kind != "error" || decoded.Command != "status" || decoded.ErrorCode != "runtime" || decoded.ExitCode != exitRuntime || decoded.Session != "missing-session" {
		t.Fatalf("decoded error = %#v, want status runtime error", decoded)
	}
	if !strings.Contains(decoded.Message, `session "missing-session" not found`) {
		t.Fatalf("message = %q, want missing-session text", decoded.Message)
	}
}

func TestRunStatusJSONForSinglePane(t *testing.T) {
	if _, err := exec.LookPath(tmux.Binary()); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "cmd-json-status-pane.sock")
	t.Setenv("AGENTMUX_TMUX_SOCKET", socket)

	cfg := resolve.Config{
		Session: "cmd-json-status-pane",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{Name: "runner", SourceType: "command", Command: "printf ready", Title: "Runner"},
					{Name: "review", SourceType: "agent", AgentName: "reviewer", Command: "printf review", Title: "Review"},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = exec.Command(tmux.Binary(), "-S", socket, "kill-server").Run()
	})

	if err := tmux.Execute(cfg, tmux.Options{Replace: true}); err != nil {
		if isTMUXPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	code, stdout, stderr := captureRunOutput(t, []string{"status", "cmd-json-status-pane", "--pane", "runner", "--json"})
	if code != exitSuccess {
		t.Fatalf("code = %d, want %d", code, exitSuccess)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	var decoded struct {
		Kind    string            `json:"kind"`
		Session string            `json:"session"`
		Pane    string            `json:"pane"`
		Panes   []tmux.PaneStatus `json:"panes"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded.Kind != "status" || decoded.Session != "cmd-json-status-pane" || decoded.Pane != "runner" {
		t.Fatalf("decoded status = %#v, want filtered runner status", decoded)
	}
	if len(decoded.Panes) != 1 || decoded.Panes[0].Name != "runner" {
		t.Fatalf("decoded panes = %#v, want only runner pane", decoded.Panes)
	}
}

func TestRunFocusJSONErrorForMissingPane(t *testing.T) {
	if _, err := exec.LookPath(tmux.Binary()); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "cmd-json-focus.sock")
	t.Setenv("AGENTMUX_TMUX_SOCKET", socket)

	cfg := resolve.Config{
		Session: "cmd-json-focus",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{Name: "runner", SourceType: "command", Command: "printf ready", Title: "Runner"},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = exec.Command(tmux.Binary(), "-S", socket, "kill-server").Run()
	})

	if err := tmux.Execute(cfg, tmux.Options{Replace: true}); err != nil {
		if isTMUXPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	code, stdout, stderr := captureRunOutput(t, []string{"focus", "cmd-json-focus", "missing-pane", "--json"})
	if code != exitRuntime {
		t.Fatalf("code = %d, want %d", code, exitRuntime)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("stderr = %q, want empty stderr for json error", stderr)
	}

	var decoded struct {
		Kind      string `json:"kind"`
		Command   string `json:"command"`
		ErrorCode string `json:"error_code"`
		ExitCode  int    `json:"exit_code"`
		Message   string `json:"message"`
		Session   string `json:"session"`
		Pane      string `json:"pane"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded.Kind != "error" || decoded.Command != "focus" || decoded.ErrorCode != "runtime" || decoded.ExitCode != exitRuntime || decoded.Session != "cmd-json-focus" || decoded.Pane != "missing-pane" {
		t.Fatalf("decoded error = %#v, want focus runtime error", decoded)
	}
	if !strings.Contains(decoded.Message, `available named panes: runner`) {
		t.Fatalf("message = %q, want available pane hint", decoded.Message)
	}
}

func TestRunStatusJSONErrorForMissingDependency(t *testing.T) {
	t.Setenv("AGENTMUX_TMUX_BIN", filepath.Join(t.TempDir(), "does-not-exist-tmux"))

	code, stdout, stderr := captureRunOutput(t, []string{"status", "dogfood", "--json"})
	if code != exitDependency {
		t.Fatalf("code = %d, want %d", code, exitDependency)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("stderr = %q, want empty stderr for json dependency error", stderr)
	}

	var decoded struct {
		Kind      string `json:"kind"`
		Command   string `json:"command"`
		ErrorCode string `json:"error_code"`
		ExitCode  int    `json:"exit_code"`
		Message   string `json:"message"`
		Session   string `json:"session"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded.Kind != "error" || decoded.Command != "status" || decoded.ErrorCode != "dependency" || decoded.ExitCode != exitDependency || decoded.Session != "dogfood" {
		t.Fatalf("decoded error = %#v, want status dependency error", decoded)
	}
	if !strings.Contains(decoded.Message, "is not installed or not in PATH") {
		t.Fatalf("message = %q, want dependency text", decoded.Message)
	}
}

func TestResolveEditTargetReturnsAbsPathForYamlPath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "new-config.yaml")
	got, err := resolveEditTarget(target)
	if err != nil {
		t.Fatalf("resolveEditTarget returned error: %v", err)
	}
	want, err := filepath.Abs(target)
	if err != nil {
		t.Fatalf("filepath.Abs returned error: %v", err)
	}
	if got != want {
		t.Fatalf("resolveEditTarget = %q, want %q", got, want)
	}
}

func TestEditorInvocationUsesConfiguredEditor(t *testing.T) {
	t.Setenv("EDITOR", "vi")
	shell, command, err := editorInvocation("/tmp/config.yaml")
	if err != nil {
		t.Fatalf("editorInvocation returned error: %v", err)
	}
	if shell == "" {
		t.Fatal("shell = empty, want a shell path")
	}
	if !strings.Contains(command, "vi ") || !strings.Contains(command, "/tmp/config.yaml") {
		t.Fatalf("command = %q, want editor plus target path", command)
	}
}

func TestResolveEditTargetFromSessionMetadata(t *testing.T) {
	if _, err := exec.LookPath(tmux.Binary()); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "cmd-edit-session.sock")
	t.Setenv("AGENTMUX_TMUX_SOCKET", socket)

	cfg := resolve.Config{
		Session: "cmd-edit-session",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{Name: "runner", SourceType: "command", Command: "printf ready", Title: "Runner"},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = exec.Command(tmux.Binary(), "-S", socket, "kill-server").Run()
	})

	if err := tmux.Execute(cfg, tmux.Options{Replace: true}); err != nil {
		if isTMUXPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	wantPath := filepath.Join(t.TempDir(), "dogfood.yaml")
	if err := tmux.SetSessionConfigPath(cfg.Session, wantPath, false); err != nil {
		t.Fatalf("SetSessionConfigPath returned error: %v", err)
	}

	gotPath, err := resolveEditTarget(cfg.Session)
	if err != nil {
		t.Fatalf("resolveEditTarget returned error: %v", err)
	}
	if gotPath != wantPath {
		t.Fatalf("resolveEditTarget = %q, want %q", gotPath, wantPath)
	}
}

func TestResolveExistingConfigPathRequiresExistingFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(target, []byte("version: 1\nsession: demo\nlayout:\n  - window: main\n    panes:\n      - command: \"printf ready\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	got, err := resolveExistingConfigPath(target)
	if err != nil {
		t.Fatalf("resolveExistingConfigPath returned error: %v", err)
	}
	want, err := filepath.Abs(target)
	if err != nil {
		t.Fatalf("filepath.Abs returned error: %v", err)
	}
	if got != want {
		t.Fatalf("resolveExistingConfigPath = %q, want %q", got, want)
	}
}

func TestRunRememberStoresSessionConfigPath(t *testing.T) {
	if _, err := exec.LookPath(tmux.Binary()); err != nil {
		t.Skip("tmux not installed")
	}

	socket := filepath.Join(t.TempDir(), "cmd-remember.sock")
	t.Setenv("AGENTMUX_TMUX_SOCKET", socket)

	cfg := resolve.Config{
		Session: "cmd-remember",
		Windows: []resolve.Window{
			{
				Name: "main",
				Panes: []resolve.Pane{
					{Name: "runner", SourceType: "command", Command: "printf ready", Title: "Runner"},
				},
			},
		},
	}

	t.Cleanup(func() {
		_ = exec.Command(tmux.Binary(), "-S", socket, "kill-server").Run()
	})

	if err := tmux.Execute(cfg, tmux.Options{Replace: true}); err != nil {
		if isTMUXPermissionError(err) {
			t.Skipf("tmux integration unavailable in this environment: %v", err)
		}
		t.Fatalf("Execute returned error: %v", err)
	}

	configPath := filepath.Join(t.TempDir(), "remember.yaml")
	if err := os.WriteFile(configPath, []byte("version: 1\nsession: remember\nlayout:\n  - window: main\n    panes:\n      - command: \"printf ready\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	code, stdout, stderr := captureRunOutput(t, []string{"remember", "cmd-remember", configPath})
	if code != exitSuccess {
		t.Fatalf("code = %d, want %d", code, exitSuccess)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `remembered config path`) {
		t.Fatalf("stdout = %q, want remember success message", stdout)
	}

	gotPath, err := tmux.SessionConfigPath(cfg.Session, false)
	if err != nil {
		t.Fatalf("SessionConfigPath returned error: %v", err)
	}
	wantPath, err := filepath.Abs(configPath)
	if err != nil {
		t.Fatalf("filepath.Abs returned error: %v", err)
	}
	if gotPath != wantPath {
		t.Fatalf("SessionConfigPath = %q, want %q", gotPath, wantPath)
	}
}

func captureRunOutput(t *testing.T, args []string) (code int, stdout string, stderr string) {
	t.Helper()

	originalStdout := os.Stdout
	originalStderr := os.Stderr

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdout returned error: %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stderr returned error: %v", err)
	}

	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	t.Cleanup(func() {
		os.Stdout = originalStdout
		os.Stderr = originalStderr
	})

	code = run(args)

	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	os.Stdout = originalStdout
	os.Stderr = originalStderr

	stdoutBytes, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatalf("ReadAll stdout returned error: %v", err)
	}
	stderrBytes, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatalf("ReadAll stderr returned error: %v", err)
	}
	_ = stdoutReader.Close()
	_ = stderrReader.Close()

	return code, string(stdoutBytes), string(stderrBytes)
}

func isTMUXPermissionError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "Operation not permitted") || strings.Contains(message, "permission denied")
}
