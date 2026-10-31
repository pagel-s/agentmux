package managed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteDefinitionOnlyOwnProcessAndPaths(t *testing.T) {
	cfg := testConfig()
	a := cfg.Agents["alice"]
	a.Sandbox = "bubblewrap"
	a.Network = "none"
	cfg.Agents["alice"] = a
	s := testStore(t, cfg)
	if err := s.startRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	d, err := s.WorkerDefinition("alice", RemoteLayout{Workdir: "/tmp/remote/state/workspaces/alice"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Config.Agents) != 1 || strings.Contains(string(raw), `"bob":`) || strings.Contains(string(raw), `"eve":`) || strings.Contains(string(raw), "token") || strings.Contains(string(raw), "owner.json") {
		t.Fatal("deployment contains peer or owner state")
	}
	path := filepath.Join(t.TempDir(), "worker.json")
	os.WriteFile(path, raw, 0600)
	parsed, err := ReadWorkerDefinition(path)
	if err != nil || parsed.AgentID != "alice" {
		t.Fatal(err)
	}
	os.Chmod(path, 0644)
	if _, err := ReadWorkerDefinition(path); err == nil {
		t.Fatal("public process definition accepted")
	}
	if _, err := s.WorkerDefinition("alice", RemoteLayout{Workdir: "relative"}); err == nil {
		t.Fatal("relative remote workspace accepted")
	}
}

func TestRemoteRefusesFalseTraceAnchoringAndHostInjection(t *testing.T) {
	cfg := testConfig()
	a := cfg.Agents["alice"]
	a.Sandbox = "bubblewrap"
	a.Network = "none"
	a.Trace.Enabled = true
	cfg.Agents["alice"] = a
	s := testStore(t, cfg)
	if _, err := s.WorkerDefinition("alice", RemoteLayout{Workdir: "/tmp/work"}); err == nil {
		t.Fatal("remote trace incorrectly claims central verification")
	}
	for _, host := range []string{"-oProxyCommand=evil", "host;touch /tmp/x", "host\ncommand", "$(evil)"} {
		if sshHostPattern.MatchString(host) {
			t.Fatal("SSH option/shell injection permitted")
		}
	}
	for _, word := range []string{"abc", "a'b", "a;$(evil)"} {
		quoted := shellWord(word)
		if !strings.HasPrefix(quoted, "'") || !strings.HasSuffix(quoted, "'") {
			t.Fatal("remote word not single quoted")
		}
	}
}
