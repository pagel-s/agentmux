package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func TestTraceCLIAuthenticatesOwnerInsteadOfFileLabel(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	s, err := managed.Init(state, managedconfig.Config{Version: 2, Mode: "managed", Session: "trace-auth", Agents: map[string]managedconfig.Agent{"agent": {Harness: "command", Command: []string{"/usr/bin/true"}}}})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	agent, err := managed.LoadCredential(managed.CredentialPath(state, "agent"))
	if err != nil {
		t.Fatal(err)
	}
	agent.AgentID = "$owner"
	raw, _ := json.Marshal(agent)
	os.WriteFile(managed.CredentialPath(state, ""), raw, 0600)
	var out, stderr bytes.Buffer
	if code := runManagedTrace(context.Background(), []string{"list", "--state", state, "--agent", "agent"}, &out, &stderr); code == 0 {
		t.Fatal("agent token relabeled owner granted trace access")
	}
}
