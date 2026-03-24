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
	out, err := renderStatusJSON("dogfood", []tmux.PaneStatus{
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
	if len(decoded.Panes) != 1 || decoded.Panes[0].Name != "resolve" {
		t.Fatalf("decoded panes = %#v, want resolve pane", decoded.Panes)
	}
	if decoded.Panes[0].SourceType != "agent" || decoded.Panes[0].AgentName != "reviewer" || decoded.Panes[0].OnExit != "restart" || decoded.Panes[0].CurrentCmd != "bash" || !decoded.Panes[0].Dead {
		t.Fatalf("decoded pane metadata = %#v, want agent reviewer restart bash dead=true", decoded.Panes[0])
	}
}

func TestRenderCaptureJSON(t *testing.T) {
	out, err := renderCaptureJSON("dogfood", "review", 5, "hello\nworld\n")
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
		Content       string `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if decoded.SchemaVersion != jsonSchemaV1 || decoded.Kind != "capture" || decoded.Session != "dogfood" || decoded.Pane != "review" || decoded.TailLines != 5 || decoded.Content != "hello\nworld\n" {
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
		`name="resolve"`,
		`source=agent`,
		`agent="reviewer"`,
		`on_exit="restart"`,
		`current="bash"`,
		`dead=true`,
		`active=true`,
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
