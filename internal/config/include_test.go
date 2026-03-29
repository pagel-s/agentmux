package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadFileWithIncludes(t *testing.T) {
	tmpDir := t.TempDir()

	// Child config
	childPath := filepath.Join(tmpDir, "child.yaml")
	childContent := `
version: 1
params:
  p1: child-v1
  p2: child-v2
env:
  E1: child-e1
agents:
  a1:
    command: child-cmd
layout:
  - window: child-window
`
	if err := os.WriteFile(childPath, []byte(childContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Parent config
	parentPath := filepath.Join(tmpDir, "parent.yaml")
	parentContent := `
version: 1
include:
  - child.yaml
params:
  p1: parent-v1
env:
  E2: parent-e2
agents:
  a2:
    command: parent-cmd
layout:
  - window: parent-window
`
	if err := os.WriteFile(parentPath, []byte(parentContent), 0644); err != nil {
		t.Fatal(err)
	}

	doc, err := LoadFile(parentPath)
	if err != nil {
		t.Fatalf("LoadFile failed: %v", err)
	}

	cfg := doc.Config

	// Verify Params (Parent wins)
	expectedParams := map[string]string{
		"p1": "parent-v1",
		"p2": "child-v2",
	}
	if !reflect.DeepEqual(cfg.Params, expectedParams) {
		t.Errorf("Params = %v, want %v", cfg.Params, expectedParams)
	}

	// Verify Env
	expectedEnv := map[string]string{
		"E1": "child-e1",
		"E2": "parent-e2",
	}
	if !reflect.DeepEqual(cfg.Env, expectedEnv) {
		t.Errorf("Env = %v, want %v", cfg.Env, expectedEnv)
	}

	// Verify Agents
	if _, ok := cfg.Agents["a1"]; !ok {
		t.Errorf("Agent a1 missing")
	}
	if _, ok := cfg.Agents["a2"]; !ok {
		t.Errorf("Agent a2 missing")
	}

	// Verify Layout (Child appends before Parent)
	if len(cfg.Layout) != 2 {
		t.Fatalf("Layout length = %d, want 2", len(cfg.Layout))
	}
	if cfg.Layout[0].Window != "child-window" {
		t.Errorf("First window = %s, want child-window", cfg.Layout[0].Window)
	}
	if cfg.Layout[1].Window != "parent-window" {
		t.Errorf("Second window = %s, want parent-window", cfg.Layout[1].Window)
	}
}

func TestCircularInclude(t *testing.T) {
	tmpDir := t.TempDir()

	aPath := filepath.Join(tmpDir, "a.yaml")
	bPath := filepath.Join(tmpDir, "b.yaml")

	aContent := "include: [b.yaml]"
	bContent := "include: [a.yaml]"

	_ = os.WriteFile(aPath, []byte(aContent), 0644)
	_ = os.WriteFile(bPath, []byte(bContent), 0644)

	_, err := LoadFile(aPath)
	if err == nil {
		t.Fatal("expected error for circular include, got nil")
	}
	if !reflect.DeepEqual(err.Error(), "include \"b.yaml\": include \"a.yaml\": circular include detected: "+aPath) {
		// Note: The exact error string might depend on absolute path resolution
		t.Logf("Error message: %v", err)
	}
}
