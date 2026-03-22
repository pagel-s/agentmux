package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileTracksUnknownTopLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agentmux.yaml")
	content := []byte(`
version: 1
session: demo
future_field: true
agents:
  coder:
    provider: command
    command: "echo hi"
layout:
  - window: main
    panes:
      - command: "echo hi"
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	doc, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile returned error: %v", err)
	}

	if len(doc.UnknownTopLevel) != 1 || doc.UnknownTopLevel[0] != "future_field" {
		t.Fatalf("UnknownTopLevel = %v, want [future_field]", doc.UnknownTopLevel)
	}
}

func TestLoadFileRejectsDuplicateAgentKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agentmux.yaml")
	content := []byte(`
version: 1
session: demo
agents:
  coder:
    provider: command
    command: "echo hi"
  coder:
    provider: command
    command: "echo bye"
layout:
  - window: main
    panes:
      - command: "echo hi"
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if _, err := LoadFile(path); err == nil {
		t.Fatal("LoadFile returned nil error, want duplicate-key parse error")
	}
}
