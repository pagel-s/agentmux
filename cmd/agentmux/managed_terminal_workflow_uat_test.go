package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeTerminalLayoutsScratchAndLastAttachment(t *testing.T) {
	if os.Getenv("AGENTMUX_MANAGED_INTEGRATION") != "1" {
		t.Skip("requires explicit disposable tmux integration")
	}
	binary := os.Getenv("AGENTMUX_TEST_BINARY")
	if binary == "" {
		binary = "../../bin/agentmux"
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("python3", "../../scripts/smoke-managed-terminal.py", "--binary", binary).CombinedOutput()
	if err != nil {
		t.Fatalf("native terminal workflow: %v\n%s", err, output)
	}
	t.Log(string(output))
}
