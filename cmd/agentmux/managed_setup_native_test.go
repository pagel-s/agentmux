package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestCodingPresetCreatesNativeWorkspaceWithoutLifetimeExpiry(t *testing.T) {
	root := t.TempDir()
	binaries := filepath.Join(root, "bin")
	if err := os.Mkdir(binaries, 0700); err != nil {
		t.Fatal(err)
	}
	// Metadata only. Any attempt to start a native conversation fails this test.
	metadata := `#!/bin/sh
for arg do
 case "$arg" in
 --help) echo '--append-system-prompt --no-daemon --no-alt-screen --sandbox --model --config -c'; exit 0;;
 --version) echo '2.1.316'; exit 0;;
 status) echo '{"loggedIn":true}'; exit 0;;
 esac
done
exit 91
`
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(binaries, name), []byte(metadata), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binaries+string(os.PathListSeparator)+os.Getenv("PATH"))
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "coding.yaml")
	var out, diagnostics bytes.Buffer
	if code := runManagedSetupIO(context.Background(), []string{"--preset", "coding", "--sandbox", "local", "--project", project, "--output", output}, bytes.NewReader(nil), &out, &diagnostics); code != 0 {
		t.Fatalf("coding setup failed: %d %s %s", code, out.String(), diagnostics.String())
	}
	cfg, err := managedconfig.Load(output)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.InteractiveWorkspace || cfg.Spawn.Enabled {
		t.Fatalf("coding preset retains an arbitrary lifetime or enables spawning: %+v", cfg)
	}
	for id, agent := range cfg.Agents {
		if agent.Interaction != "interactive" || agent.Autonomy.Enabled {
			t.Fatalf("%s lost human-led native mode", id)
		}
	}
	if cfg.Limits.MaxTasks <= 0 || cfg.Limits.MaxRecords <= 0 || cfg.Limits.MaxRuntimeSeconds <= 0 {
		t.Fatal("coding preset removed finite resource/lease limits")
	}
}
