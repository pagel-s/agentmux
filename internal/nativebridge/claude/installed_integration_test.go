//go:build unix

package claude

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/nativebridge"
)

// Opt in explicitly: this starts the real installed native TUI in a temporary
// home, but all model requests go to a synthetic loopback server with a fake
// API key. It never reads the user's Claude profile or calls a paid provider.
func TestInstalledClaudeNativeInboxPreservesDraftAndApprovals(t *testing.T) {
	if os.Getenv("AGENTMUX_CLAUDE_NATIVE_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_CLAUDE_NATIVE_INTEGRATION=1 for installed Claude native TUI contract")
	}
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("testdata/installed_probe.py")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-u", script, helper, binary, t.TempDir())
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installed Claude contract failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}

// Test-only subprocess entry points exercise the production Run / RunHook
// integration. The hook's socket/token capture stays in its private fixture
// directory and is never part of a broker registration, log or test output.
func TestInstalledClaudePTYHelper(t *testing.T) {
	if os.Getenv("AGENTMUX_CLAUDE_FIXTURE_RUN") != "1" {
		t.Skip("subprocess helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	err := Run(ctx, []string{os.Getenv("AGENTMUX_CLAUDE_FIXTURE_BINARY"), "--settings", os.Getenv("AGENTMUX_CLAUDE_FIXTURE_SETTINGS"), "--model", "sonnet", "--permission-mode", "default"}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstalledClaudeHookHelper(t *testing.T) {
	if os.Getenv("AGENTMUX_CLAUDE_FIXTURE_HOOK") != "1" {
		t.Skip("subprocess helper")
	}
	dir := os.Getenv("AGENTMUX_CLAUDE_FIXTURE_DIR")
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	var metadata map[string]any
	if json.Unmarshal(input, &metadata) != nil {
		os.Exit(2)
	}
	metadata["socket"] = os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	metadata["token"] = os.Getenv("CLAUDE_CODE_MESSAGING_TOKEN")
	metadata["bridge_dir"] = os.Getenv(bridgeDirEnv)
	data, _ := json.Marshal(metadata)
	if os.WriteFile(filepath.Join(dir, "hook-private.json"), data, 0600) != nil {
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	if err := RunHook(ctx, strings.NewReader(string(input)), &installedClaudeConnection{dir: dir}); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

type installedClaudeConnection struct {
	dir  string
	sent bool
}

func (f *installedClaudeConnection) record(value any) error {
	file, err := os.OpenFile(filepath.Join(f.dir, "bridge-events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(value)
}
func (f *installedClaudeConnection) Register(_ context.Context, r nativebridge.Registration) error {
	return f.record(r)
}
func (f *installedClaudeConnection) Poll(_ context.Context, r nativebridge.Readiness) (*nativebridge.Delivery, error) {
	if f.sent || r.State != "ready" {
		return nil, nil
	}
	if _, err := os.Stat(filepath.Join(f.dir, "allow-delivery")); err != nil {
		return nil, nil
	}
	f.sent = true
	return &nativebridge.Delivery{ID: "fixture-peer-delivery", TaskID: "fixture-task", From: "codex", Kind: "instruction", Text: "AGENTMUX_INBOUND_FIXTURE_1"}, nil
}
func (f *installedClaudeConnection) Report(_ context.Context, e nativebridge.Event) error {
	return f.record(e)
}
func (f *installedClaudeConnection) Status(context.Context) (nativebridge.Status, error) {
	id := ""
	if f.sent {
		id = "fixture-peer-delivery"
	}
	return nativebridge.Status{Mode: "auto", ActiveID: id}, nil
}
