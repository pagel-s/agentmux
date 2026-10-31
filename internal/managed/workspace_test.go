package managed

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentmux/internal/managedconfig"
)

func workspaceAgent(t *testing.T, harness string) managedconfig.Agent {
	t.Helper()
	return managedconfig.Agent{Harness: harness, Interaction: "interactive", Sandbox: "local", Workdir: t.TempDir(), Role: "Help the human with this project", Env: map[string]string{"PROJECT_LABEL": "private-project-value"}}
}

func addedWorkspaceAgent(t *testing.T, raw json.RawMessage) AgentInfo {
	t.Helper()
	var result struct {
		Agent AgentInfo `json:"agent"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result.Agent
}

func TestWorkspaceAdditionIsOwnerOnlyDurableAndIdempotent(t *testing.T) {
	s := testStore(t, testConfig())
	before := s.Config()
	definition := workspaceAgent(t, "claude")
	args := map[string]any{"id": "project-one", "config": definition}
	expectFault(t, s, "alice", "agent.add", "forbidden", args)
	if _, err := os.Stat(CredentialPath(s.dir, "project-one")); !os.IsNotExist(err) {
		t.Fatal("unauthorized addition created a credential")
	}
	owner := credential(t, s, "")
	one, problem := invoke(s, owner, "agent.add", args, "add-project-once")
	if problem != nil {
		t.Fatal(problem)
	}
	first := addedWorkspaceAgent(t, one)
	identity := credential(t, s, first.ID)
	two, problem := invoke(s, owner, "agent.add", args, "add-project-once")
	if problem != nil || string(one) != string(two) {
		t.Fatalf("addition replay changed its receipt: %s %s %v", one, two, problem)
	}
	if first.ID != "project-one" || first.Parent != ownerID || !first.OwnerManaged || first.State != "active" || first.External {
		t.Fatalf("unexpected workspace identity: %+v", first)
	}
	if identity.AgentID != first.ID || identity.InstanceID == "" || identity.Generation != 1 || identity.Token == "" {
		t.Fatalf("missing stable credential identity: %+v", identity)
	}
	if strings.Contains(string(one), "private-project-value") {
		t.Fatal("addition receipt exposed environment value")
	}
	loaded, err := s.Agent(first.ID)
	if err != nil || loaded.Env["PROJECT_LABEL"] != "private-project-value" || loaded.Workdir != definition.Workdir {
		t.Fatalf("owner definition lost private settings: %+v %v", loaded, err)
	}
	expectFault(t, s, "", "agent.add", "conflict", args)
	expectFault(t, s, "", "agent.add", "conflict", map[string]any{"id": "alice", "config": definition})
	secondDefinition := workspaceAgent(t, "codex")
	second := addedWorkspaceAgent(t, call(t, s, "", "agent.add", map[string]any{"id": "project-two", "config": secondDefinition}))
	if second.ID == first.ID || second.Config.Workdir == first.Config.Workdir {
		t.Fatal("multi-project additions shared an identity or project")
	}
	if !reflect.DeepEqual(before, s.Config()) {
		t.Fatal("workspace additions changed the saved launch configuration")
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayed, problem := invoke(reopened, owner, "agent.add", args, "add-project-once")
	if problem != nil || string(replayed) != string(one) {
		t.Fatalf("reopening lost the original addition receipt: %s %v", replayed, problem)
	}
	if got := credential(t, reopened, first.ID); !reflect.DeepEqual(got, identity) {
		t.Fatal("reopening the workspace rotated the native session identity")
	}
	for id, project := range map[string]string{first.ID: definition.Workdir, second.ID: secondDefinition.Workdir} {
		got, err := reopened.Agent(id)
		if err != nil || got.Workdir != project || got.Interaction != "interactive" {
			t.Fatalf("addition did not survive reopen: %s %+v %v", id, got, err)
		}
	}
	if !reflect.DeepEqual(before, reopened.Config()) {
		t.Fatal("reopening persisted additions into immutable launch configuration")
	}
}

func TestWorkspaceAdditionStartsIndependentAndExternalRegistersWithoutExecuting(t *testing.T) {
	s := testStore(t, testConfig())
	definition := workspaceAgent(t, "command")
	sentinel := filepath.Join(definition.Workdir, "must-not-execute")
	definition.Command = []string{"/bin/sh", "-c", "touch \"$1\"", "fixture", sentinel}
	result := addedWorkspaceAgent(t, call(t, s, "", "agent.add", map[string]any{"id": "existing-terminal", "config": definition, "external": true}))
	if !result.External || !result.OwnerManaged || !reflect.DeepEqual(result.Capabilities, Capabilities{}) {
		t.Fatalf("external registration acquired capabilities: %+v", result)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("broker registration executed the external process definition")
	}
	for _, op := range []string{"message.send", "ping.send", "instruct"} {
		expectFault(t, s, result.ID, op, "forbidden", map[string]any{"to": []string{"alice"}, "text": "not authorized"})
		expectFault(t, s, "alice", op, "forbidden", map[string]any{"to": []string{result.ID}, "text": "not authorized"})
	}
	// A wildcard in the original launch config must not enroll a new session.
	for _, board := range []string{"team", "public"} {
		expectFault(t, s, result.ID, "board.post", "forbidden", map[string]any{"board": board, "text": "not enrolled"})
		expectFault(t, s, result.ID, "message.list", "forbidden", map[string]any{"board": board})
	}
	expectFault(t, s, result.ID, "memory.put", "forbidden", map[string]any{"namespace": "team", "key": "note", "value": "not shared", "version": 0})
	call(t, s, result.ID, "memory.put", map[string]any{"namespace": "private", "key": "note", "value": "native session private note", "version": 0})
	call(t, s, result.ID, "identity", map[string]any{})
}

func TestWorkspaceCollaborationCanBeEnabledAndEndedWithoutStoppingSessions(t *testing.T) {
	s := testStore(t, testConfig())
	before := s.Config()
	for _, id := range []string{"native-a", "native-b"} {
		call(t, s, "", "agent.add", map[string]any{"id": id, "config": workspaceAgent(t, "claude")})
	}
	identities := map[string]Credential{"native-a": credential(t, s, "native-a"), "native-b": credential(t, s, "native-b")}
	args := map[string]any{"version": 1, "kind": "collaboration", "agents": []string{"native-a", "native-b"}, "enabled": true, "board": "team", "memory": "team"}
	expectFault(t, s, "native-a", "control.update", "forbidden", args)
	call(t, s, "", "control.update", args)
	for from, to := range map[string]string{"native-a": "native-b", "native-b": "native-a"} {
		for _, op := range []string{"message.send", "ping.send"} {
			call(t, s, from, op, map[string]any{"to": []string{to}, "text": "cooperate with the human"})
		}
		expectFault(t, s, from, "instruct", "forbidden", map[string]any{"to": []string{to}, "text": "no command delegation"})
		call(t, s, from, "board.post", map[string]any{"board": "team", "text": "shared project context"})
		call(t, s, from, "memory.put", map[string]any{"namespace": "team", "key": from, "value": "shared finding", "version": 0})
		expectFault(t, s, from, "message.send", "forbidden", map[string]any{"to": []string{"eve"}, "text": "outside the selected pair"})
	}
	call(t, s, "native-b", "memory.get", map[string]any{"namespace": "team", "key": "native-a"})
	// Include an independently granted command and an incoming edge from an
	// unselected session: ending cooperation must revoke both directions.
	call(t, s, "", "control.update", map[string]any{"version": 2, "kind": "edge", "from": "eve", "to": "native-a", "action": "instruct", "allow": true})
	call(t, s, "", "control.update", map[string]any{"version": 3, "kind": "edge", "from": "native-a", "to": "native-b", "action": "instruct", "allow": true})
	call(t, s, "", "control.update", map[string]any{"version": 4, "kind": "independent", "agents": []string{"native-a"}})
	for from, to := range map[string]string{"native-a": "native-b", "native-b": "native-a", "eve": "native-a"} {
		for _, op := range []string{"message.send", "ping.send", "instruct"} {
			expectFault(t, s, from, op, "forbidden", map[string]any{"to": []string{to}, "text": "cooperation ended"})
		}
	}
	expectFault(t, s, "native-a", "message.list", "forbidden", map[string]any{"board": "team"})
	expectFault(t, s, "native-a", "board.post", "forbidden", map[string]any{"board": "team", "text": "revoked"})
	expectFault(t, s, "native-a", "memory.get", "forbidden", map[string]any{"namespace": "team", "key": "native-b"})
	expectFault(t, s, "native-a", "memory.put", "forbidden", map[string]any{"namespace": "team", "key": "new", "value": "revoked", "version": 0})
	// The other member keeps its explicitly allowed scope, and stored work is
	// retained. Native workers keep their identities and active definitions.
	call(t, s, "native-b", "memory.get", map[string]any{"namespace": "team", "key": "native-a"})
	for id, identity := range identities {
		if !reflect.DeepEqual(credential(t, s, id), identity) {
			t.Fatalf("making independent rotated %s", id)
		}
		info, err := s.agent(context.Background(), s.db, id)
		if err != nil || info.State != "active" || info.Config.Interaction != "interactive" {
			t.Fatalf("permission change stopped native session %s: %+v %v", id, info, err)
		}
	}
	if !reflect.DeepEqual(before, s.Config()) {
		t.Fatal("collaboration controls changed saved launch configuration")
	}
}

func TestWorkspaceRejectsInvalidAdditionsWithoutLeavingCredentialsOrRegistryEntries(t *testing.T) {
	s := testStore(t, testConfig())
	for _, tc := range []struct {
		name   string
		change func(*managedconfig.Agent)
	}{
		{"managed-worker", func(a *managedconfig.Agent) { a.Interaction = "managed" }},
		{"relative-project", func(a *managedconfig.Agent) { a.Workdir = "relative/project" }},
		{"broker-in-project", func(a *managedconfig.Agent) { a.Workdir = filepath.Dir(s.dir) }},
		{"controller-in-project", func(a *managedconfig.Agent) { a.Workdir = s.dir + ".bin" }},
		{"unknown-harness", func(a *managedconfig.Agent) { a.Harness = "not-a-harness" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			definition := workspaceAgent(t, "claude")
			tc.change(&definition)
			expectFault(t, s, "", "agent.add", "invalid", map[string]any{"id": tc.name, "config": definition})
			if _, err := os.Stat(CredentialPath(s.dir, tc.name)); !os.IsNotExist(err) {
				t.Fatal("failed registration left a usable credential")
			}
			if _, err := s.Agent(tc.name); err == nil {
				t.Fatal("failed registration left an active definition")
			}
		})
	}
}

func TestWorkspaceCollaborationValidationIsAtomicAndCommandsAreExplicit(t *testing.T) {
	s := testStore(t, testConfig())
	for _, ids := range [][]string{nil, {"alice"}, {"alice", "alice"}, {"alice", "missing"}} {
		expectFault(t, s, "", "control.update", "invalid", map[string]any{"version": 1, "kind": "collaboration", "agents": ids, "enabled": true, "board": "team"})
	}
	for _, scope := range []map[string]any{{"board": "missing"}, {"memory": "private"}} {
		args := map[string]any{"version": 1, "kind": "collaboration", "agents": []string{"alice", "bob"}, "enabled": true}
		for key, value := range scope {
			args[key] = value
		}
		expectFault(t, s, "", "control.update", "invalid", args)
	}
	var initial ControlState
	if err := json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &initial); err != nil || initial.Version != 1 || len(initial.Edges) != 0 || len(initial.Scopes) != 0 {
		t.Fatalf("invalid group edit partially changed permissions: %+v %v", initial, err)
	}
	args := map[string]any{"version": 1, "kind": "collaboration", "agents": []string{"alice", "bob"}, "enabled": true, "commands": false}
	call(t, s, "", "control.update", args)
	// Even a pre-existing command grant is removed by the notes-only preset.
	expectFault(t, s, "alice", "instruct", "forbidden", map[string]any{"to": []string{"bob"}, "text": "notes only"})
	expectFault(t, s, "bob", "instruct", "forbidden", map[string]any{"to": []string{"alice"}, "text": "notes only"})
	args["version"], args["commands"] = 2, true
	call(t, s, "", "control.update", args)
	for from, to := range map[string]string{"alice": "bob", "bob": "alice"} {
		call(t, s, from, "instruct", map[string]any{"to": []string{to}, "text": "explicit command delegation"})
	}
	args["version"], args["enabled"] = 3, false
	call(t, s, "", "control.update", args)
	for from, to := range map[string]string{"alice": "bob", "bob": "alice"} {
		for _, op := range []string{"message.send", "ping.send", "instruct"} {
			expectFault(t, s, from, op, "forbidden", map[string]any{"to": []string{to}, "text": "collaboration disabled"})
		}
	}
}

func TestWorkspaceAdditionRejectsProjectAliasContainingPrivateState(t *testing.T) {
	s := testStore(t, testConfig())
	alias := filepath.Join(t.TempDir(), "project-alias")
	if err := os.Symlink(filepath.Dir(s.dir), alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	definition := workspaceAgent(t, "claude")
	definition.Workdir = alias
	definition.Sandbox = "bubblewrap"
	expectFault(t, s, "", "agent.add", "invalid", map[string]any{"id": "private-state-alias", "config": definition})
	if _, err := os.Stat(CredentialPath(s.dir, "private-state-alias")); !os.IsNotExist(err) {
		t.Fatal("unsafe aliased workspace left a credential")
	}
}
