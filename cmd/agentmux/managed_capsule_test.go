package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/capsule"
	"agentmux/internal/harness"
	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/sandbox"
)

func TestManagedCapsuleOwnerOfflineInspectAndDiscard(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "capsule-cli", Agents: map[string]managedconfig.Agent{"agent": {Harness: "command", Command: []string{"/usr/bin/true"}, Sandbox: "bubblewrap", Network: "none", Disk: managedconfig.Disk{WorkspaceBytes: 1 << 20, HistoryBytes: 1 << 20, TempBytes: 1 << 20, MaxEntries: 10}}}}
	s, err := managed.Init(state, cfg)
	if err != nil {
		t.Fatal(err)
	}
	workspace := s.Config().Agents["agent"].Workdir
	s.Close()
	c, err := capsule.Prepare(context.Background(), capsule.Options{StateDir: state, AgentID: "agent", Workspace: workspace, WorkspaceBytes: 1 << 20, HistoryBytes: 1 << 20, TempBytes: 1 << 20, MaxEntries: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Wrap(harness.Spec{}, sandbox.Options{Mode: "local"}); err == nil {
		t.Fatal("invalid wrap accepted")
	}
	c.Close()
	var out, stderr bytes.Buffer
	if code := runManagedCapsule(context.Background(), []string{"inspect", "--state", state, "--agent", "agent"}, &out, &stderr); code != 0 {
		t.Fatalf("offline owner inspect failed %d: %s %s", code, out.String(), stderr.String())
	}
	var envelope struct {
		Result capsule.RecoveryReport `json:"result"`
	}
	if err = json.Unmarshal(out.Bytes(), &envelope); err != nil || envelope.Result.Digest == "" {
		t.Fatal("inspect digest missing")
	}
	out.Reset()
	stderr.Reset()
	if code := runManagedCapsule(context.Background(), []string{"recover", "--state", state, "--agent", "agent", "--action", "discard", "--expect", envelope.Result.Digest}, &out, &stderr); code != 0 {
		t.Fatalf("owner discard failed %d: %s", code, out.String())
	}
	if _, err = os.Stat(filepath.Join(state, "capsules", "agent")); !os.IsNotExist(err) {
		t.Fatal("recovery did not remove safe preparation")
	}
}

func TestManagedCapsuleRejectsAgentTokenRelabeledAsOwner(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	s, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: "owner", Agents: map[string]managedconfig.Agent{"agent": {Harness: "command", Command: []string{"/usr/bin/true"}}}})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	agent, err := managed.LoadCredential(managed.CredentialPath(state, "agent"))
	if err != nil {
		t.Fatal(err)
	}
	agent.AgentID = "$owner"
	data, _ := json.Marshal(agent)
	if err = os.WriteFile(managed.CredentialPath(state, ""), data, 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := runManagedCapsule(context.Background(), []string{"inspect", "--state", state, "--agent", "agent"}, &out, &stderr); code == 0 {
		t.Fatal("agent token relabeled owner passed actual broker identity")
	}
}
