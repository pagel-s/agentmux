package managedcheck

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/managedconfig"
)

func codexPackageFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"bin/codex", "bin/codex-code-mode-host", "codex-resources/bwrap", "codex-path/rg"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		// These fixtures must never execute: runtime inspection is filesystem-only.
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"layoutVersion":1,"version":"0.165.0","variant":"codex","entrypoint":"bin/codex","resourcesDir":"codex-resources","pathDir":"codex-path"}`
	if err := os.WriteFile(filepath.Join(root, "codex-package.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(root, "bin", "codex")
}

func checkCodexRuntime(t *testing.T, agent managedconfig.Agent, binary string) CheckResult {
	t.Helper()
	c := checker{ctx: context.Background()}
	c.codexRuntime("worker", agent, binary, t.TempDir(), nil)
	return findCheck(c.report, "worker", "harness.runtime")
}

func TestCodexRuntimeRequiresKnownCompanionsAndVisibleMounts(t *testing.T) {
	root, binary := codexPackageFixture(t)
	agent := managedconfig.Agent{Harness: "codex", Sandbox: "bubblewrap"}
	if got := checkCodexRuntime(t, agent, binary); got.Status != StatusError {
		t.Fatalf("unmounted standalone companions passed: %+v", got)
	}
	agent.ReadOnlyPaths = []string{root}
	if got := checkCodexRuntime(t, agent, binary); got.Status != StatusOK {
		t.Fatalf("complete explicit package mount failed: %+v", got)
	}
	agent.Command = []string{"/usr/bin/env", binary}
	if got := checkCodexRuntime(t, agent, "/usr/bin/env"); got.Status != StatusOK {
		t.Fatalf("executable in owner prefix was not recognized: %+v", got)
	}
	if err := os.Remove(filepath.Join(root, "bin", "codex-code-mode-host")); err != nil {
		t.Fatal(err)
	}
	if got := checkCodexRuntime(t, agent, binary); got.Status != StatusError {
		t.Fatalf("missing companion passed runtime inspection: %+v", got)
	}
}

func TestCodexRuntimeUnknownOrOlderLayoutsWarn(t *testing.T) {
	root, binary := codexPackageFixture(t)
	agent := managedconfig.Agent{Harness: "codex", Sandbox: "bubblewrap"}
	for _, manifest := range []string{`{}`, `{"layoutVersion":1,"version":"0.164.0","variant":"codex","entrypoint":"bin/codex","resourcesDir":"codex-resources","pathDir":"codex-path"}`} {
		if err := os.WriteFile(filepath.Join(root, "codex-package.json"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
		if got := checkCodexRuntime(t, agent, binary); got.Status != StatusWarning {
			t.Fatalf("unknown/older layout was incorrectly required to contain new companions: %+v", got)
		}
	}
}
