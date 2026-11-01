package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/managedlaunch"
	tea "github.com/charmbracelet/bubbletea"
)

func TestWorkspaceInventoryFindsNamedAndCustomAndDeduplicatesAliases(t *testing.T) {
	root, err := os.MkdirTemp("", "amx-ws-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	t.Setenv("XDG_STATE_HOME", root)
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nprintf \"can't find session\\n\" >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	named, _ := defaultWorkState("daily")
	custom := filepath.Join(root, "custom")
	for _, state := range []string{named, custom} {
		if err := os.MkdirAll(state, 0700); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(managedlaunch.Runtime{Version: 1, AttemptID: "saved", Session: "same-title", State: "stopped", Agents: map[string]managedlaunch.AgentPane{"a": {State: "stopped"}}})
		if err := os.WriteFile(filepath.Join(state, "runtime.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := rememberWorkState(custom); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(custom, alias); err != nil {
		t.Fatal(err)
	}
	if err := rememberWorkState(alias); err != nil {
		t.Fatal(err)
	}
	rows, err := workspaceInventory("")
	if err != nil || len(rows) != 2 {
		t.Fatalf("workspace inventory: %+v %v", rows, err)
	}
	if rows[0].Session != rows[1].Session || rows[0].State == rows[1].State {
		t.Fatal("identity conflated by display name")
	}
	for _, row := range rows {
		if row.Status != "stopped" {
			t.Fatalf("invented liveness: %+v", row)
		}
	}
	m := console{state: custom, workspacePicker: &consoleWorkspacePicker{rows: rows}}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(console)
	if m.workspacePicker == nil {
		t.Fatal("stopped workspace was opened automatically")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(console).workspacePicker != nil {
		t.Fatal("workspace chooser cannot cancel")
	}
}
