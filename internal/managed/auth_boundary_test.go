package managed

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/agentauth"
	"agentmux/internal/managedconfig"
)

func TestWorkspaceAdmissionChecksAllAgentsAuthBoundariesInEitherOrder(t *testing.T) {
	for _, authFirst := range []bool{false, true} {
		name := "project-first"
		if authFirst {
			name = "auth-first"
		}
		t.Run(name, func(t *testing.T) {
			s := testStore(t, testConfig())
			private := t.TempDir()
			auth := filepath.Join(private, "native.json")
			if err := os.WriteFile(auth, []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			login := managedconfig.Agent{Harness: "claude", Interaction: "interactive", Sandbox: "bubblewrap", Workdir: t.TempDir(), Auth: managedconfig.NativeAuth{File: auth}}
			project := managedconfig.Agent{Harness: "command", Interaction: "interactive", Command: []string{"/bin/cat"}, Workdir: private}
			first, second := login, project
			if !authFirst {
				first, second = second, first
			}
			call(t, s, "", "agent.add", map[string]any{"id": "first", "config": first})
			expectFault(t, s, "", "agent.add", "invalid", map[string]any{"id": "second", "config": second})
			if _, err := os.Stat(CredentialPath(s.dir, "second")); !os.IsNotExist(err) {
				t.Fatalf("rejected definition left a credential: %v", err)
			}
			if s.knownAgent(context.Background(), s.db, "second") {
				t.Fatal("rejected definition entered the registry")
			}
		})
	}
}

func TestWorkspaceAdmissionRejectsUninspectableAuthAndStateAliases(t *testing.T) {
	s := testStore(t, testConfig())
	a := managedconfig.Agent{Harness: "claude", Interaction: "interactive", Sandbox: "bubblewrap", Workdir: t.TempDir(), Auth: managedconfig.NativeAuth{File: filepath.Join(t.TempDir(), "missing.json")}}
	expectFault(t, s, "", "agent.add", "invalid", map[string]any{"id": "hidden", "config": a})
	alias := filepath.Join(t.TempDir(), "project")
	if err := os.Symlink(filepath.Dir(s.dir), alias); err != nil {
		t.Fatal(err)
	}
	a = managedconfig.Agent{Harness: "command", Interaction: "interactive", Command: []string{"/bin/cat"}, Workdir: alias}
	expectFault(t, s, "", "agent.add", "invalid", map[string]any{"id": "alias", "config": a})
}

func TestWorkerAuthBoundaryIncludesPersistedAdditionsAndTemplates(t *testing.T) {
	s := testStore(t, testConfig())
	a := managedconfig.Agent{Harness: "claude", Interaction: "interactive", Sandbox: "bubblewrap", Workdir: t.TempDir()}
	call(t, s, "", "agent.add", map[string]any{"id": "addition", "config": a, "external": true})
	a, err := s.Agent("addition")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := workerAuthBoundaryConfig(context.Background(), WorkerOptions{AgentID: "alice"}, s.Config(), s.cfg.Agents["alice"], s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents["addition"].Workdir != a.Workdir || len(cfg.Agents) != 4 {
		t.Fatalf("worker lost persisted peer mounts: %+v", cfg.Agents)
	}
	// A subsequently selected auth original must be checked against that peer,
	// even when the immutable initial configuration has no such directory.
	path := filepath.Join(a.Workdir, "login.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	current := cfg.Agents["alice"]
	current.Auth.File = path
	cfg.Agents["alice"] = current
	if err := agentauth.CheckBoundary(cfg); err == nil {
		t.Fatal("worker boundary ignored persisted peer workspace")
	}
	// Spawn template mounts remain part of admission, too.
	cfg.Agents["addition"] = managedconfig.Agent{}
	cfg.Spawn.Enabled = true
	cfg.Spawn.Templates = map[string]managedconfig.Agent{"template": {Workdir: a.Workdir}}
	if err := checkWorkspaceAuthBoundary(cfg); err == nil {
		t.Fatal("boundary ignored template workspace")
	}
}
