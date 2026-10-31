package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
)

func dynamicConfig() managedconfig.Config {
	c := testConfig()
	c.Spawn = managedconfig.Spawn{Enabled: true, Templates: map[string]managedconfig.Agent{"echo": {Harness: "command", Command: []string{"/bin/cat"}, Sandbox: "bubblewrap", Network: "none", Env: map[string]string{"API_KEY": "template-secret"}}}, Allow: map[string][]string{"alice": {"echo"}}}
	c.Teams = managedconfig.Teams{Enabled: true, Creators: []string{"alice"}}
	return c
}
func spawned(t *testing.T, s *Store, actor string, caps Capabilities) AgentInfo {
	t.Helper()
	raw := call(t, s, actor, "agent.spawn", map[string]any{"template": "echo", "capabilities": caps})
	var v struct {
		Agent AgentInfo `json:"agent"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Agent.ID == "" {
		t.Fatalf("spawn: %s %v", raw, err)
	}
	return v.Agent
}
func teamIn(t *testing.T, raw []byte) Team {
	t.Helper()
	var v struct {
		Team Team `json:"team"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Team.ID == "" {
		t.Fatalf("team: %s %v", raw, err)
	}
	return v.Team
}

func TestSpawnAttenuatesPermissionsAndKeepsOwnCredentials(t *testing.T) {
	s := testStore(t, dynamicConfig())
	expectFault(t, s, "bob", "agent.spawn", "forbidden", map[string]any{"template": "echo"})
	expectFault(t, s, "alice", "agent.spawn", "invalid", map[string]any{"template": "echo", "command": []string{"arbitrary"}})
	expectFault(t, s, "alice", "agent.spawn", "forbidden", map[string]any{"template": "echo", "capabilities": map[string]any{"targets": []TargetGrant{{To: "eve", Actions: []string{"instruct"}}}}})
	call(t, s, "eve", "board.post", map[string]any{"board": "public", "text": "not inherited"})
	child := spawned(t, s, "alice", Capabilities{})
	if child.Parent != "alice" || child.Depth != 1 || child.Config.Sandbox != "bubblewrap" || child.Config.Env["API_KEY"] != "[redacted]" {
		t.Fatal(child)
	}
	actual, err := s.Agent(child.ID)
	if err != nil || actual.Env["API_KEY"] != "template-secret" {
		t.Fatal("registry redaction modified runtime environment", actual, err)
	}
	if s.Config().Spawn.Templates["echo"].Env["API_KEY"] != "template-secret" {
		t.Fatal("spawn mutated frozen template")
	}
	cred := credential(t, s, child.ID)
	if cred.AgentID != child.ID || cred.Token == credential(t, s, "alice").Token {
		t.Fatal("child reused parent identity")
	}
	if raw := call(t, s, child.ID, "message.list", map[string]any{}); strings.Contains(string(raw), "not inherited") {
		t.Fatal("wildcard board permission leaked to child")
	}
	expectFault(t, s, child.ID, "board.post", "forbidden", map[string]any{"board": "public", "text": "no ambient ACL"})
	expectFault(t, s, child.ID, "instruct", "forbidden", map[string]any{"to": []string{"alice"}, "text": "reverse command"})
	call(t, s, child.ID, "message.send", map[string]any{"to": []string{"alice"}, "text": "report to parent"})
	call(t, s, "alice", "instruct", map[string]any{"to": []string{child.ID}, "text": "bounded child task"})
	allowed := spawned(t, s, "alice", Capabilities{BoardsRead: []string{"team"}, Targets: []TargetGrant{{To: "bob", Actions: []string{"message.send"}}}})
	call(t, s, allowed.ID, "message.send", map[string]any{"to": []string{"bob"}, "text": "delegated note"})
	expectFault(t, s, allowed.ID, "instruct", "forbidden", map[string]any{"to": []string{"bob"}, "text": "not delegated"})
	expectFault(t, s, allowed.ID, "board.post", "forbidden", map[string]any{"board": "team", "text": "read does not grant write"})
	call(t, s, child.ID, "memory.put", map[string]any{"namespace": "private", "key": "local", "value": "child-only", "version": 0})
	expectFault(t, s, "alice", "memory.get", "not_found", map[string]any{"namespace": "private", "key": "local"})
}

func TestSpawnRetryQuotaRollbackAndRevocationCascade(t *testing.T) {
	cfg := dynamicConfig()
	cfg.Limits.MaxSpawns = 2
	s := testStore(t, cfg)
	c := credential(t, s, "alice")
	args := map[string]any{"template": "echo", "capabilities": Capabilities{SpawnTemplates: []string{"echo"}}}
	first, f := invoke(s, c, "agent.spawn", args, "stable-spawn")
	if f != nil {
		t.Fatal(f)
	}
	again, f := invoke(s, c, "agent.spawn", args, "stable-spawn")
	if f != nil || !bytes.Equal(first, again) {
		t.Fatal("spawn retried into different identity", f)
	}
	var v struct {
		Agent AgentInfo `json:"agent"`
	}
	_ = json.Unmarshal(first, &v)
	child := v.Agent
	grandchild := spawned(t, s, child.ID, Capabilities{SpawnTemplates: []string{"echo"}})
	expectFault(t, s, grandchild.ID, "agent.spawn", "limit", map[string]any{"template": "echo"})
	old := credential(t, s, grandchild.ID)
	call(t, s, "alice", "agent.stop", map[string]any{"id": child.ID})
	if _, f := invoke(s, old, "identity", map[string]any{}, ""); f == nil || f.Code != "unauthorized" {
		t.Fatal("descendant credential survived parent revocation")
	}
	if _, err := s.Agent(grandchild.ID); err == nil {
		t.Fatal("revoked grandchild still launchable")
	}
	expectFault(t, s, "alice", "agent.spawn", "limit", map[string]any{"template": "echo"})
	// A quota failure removes staged workspace/token files and rolls back the
	// registry and credential hash together.
	limited := dynamicConfig()
	limited.Limits.MaxRecords = 6
	small := testStore(t, limited)
	expectFault(t, small, "alice", "agent.spawn", "limit", map[string]any{"template": "echo"})
	entries, err := os.ReadDir(filepath.Join(small.dir, "credentials"))
	if err != nil || len(entries) != 3 {
		t.Fatal("rejected spawn left credential files", entries, err)
	}
	var hashes int
	_ = small.db.QueryRow("SELECT count(*) FROM credentials").Scan(&hashes)
	if hashes != 4 {
		t.Fatal("rejected spawn left credential hash")
	}
	children, _ := count(context.Background(), small.db, "agent")
	if children != 0 {
		t.Fatal("rejected spawn persisted")
	}
}

func TestTeamConsentRevocationAndNoCommandAuthority(t *testing.T) {
	s := testStore(t, dynamicConfig())
	expectFault(t, s, "bob", "team.create", "forbidden", map[string]any{"name": "not allowed"})
	team := teamIn(t, call(t, s, "alice", "team.create", map[string]any{"name": "Review team"}))
	expectFault(t, s, "bob", "team.join", "not_found", map[string]any{"id": team.ID, "version": team.Version})
	team = teamIn(t, call(t, s, "alice", "team.invite", map[string]any{"id": team.ID, "agent": "bob", "version": team.Version}))
	expectFault(t, s, "bob", "board.post", "forbidden", map[string]any{"board": team.Board, "text": "not yet joined"})
	team = teamIn(t, call(t, s, "bob", "team.join", map[string]any{"id": team.ID, "version": team.Version}))
	call(t, s, "alice", "board.post", map[string]any{"board": team.Board, "text": "shared after consent"})
	call(t, s, "bob", "memory.put", map[string]any{"namespace": team.Memory, "key": "finding", "value": "team-only", "version": 0})
	if raw := call(t, s, "bob", "message.list", map[string]any{"board": team.Board}); !strings.Contains(string(raw), "shared after consent") {
		t.Fatal(string(raw))
	}
	expectFault(t, s, "bob", "instruct", "forbidden", map[string]any{"to": []string{"alice"}, "text": "membership is not authority"})
	expectFault(t, s, "eve", "message.list", "forbidden", map[string]any{"board": team.Board})
	team = teamIn(t, call(t, s, "bob", "team.leave", map[string]any{"id": team.ID, "version": team.Version}))
	expectFault(t, s, "bob", "memory.get", "forbidden", map[string]any{"namespace": team.Memory, "key": "finding"})
	expectFault(t, s, "bob", "message.list", "forbidden", map[string]any{"board": team.Board})
	call(t, s, "alice", "team.dissolve", map[string]any{"id": team.ID, "version": team.Version})
	expectFault(t, s, "alice", "board.post", "forbidden", map[string]any{"board": team.Board, "text": "after dissolution"})
}

func TestDelegatedTeamAccessTracksParentMembership(t *testing.T) {
	s := testStore(t, dynamicConfig())
	team := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "Temporary access"}))
	team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": "alice", "version": team.Version}))
	team = teamIn(t, call(t, s, "alice", "team.join", map[string]any{"id": team.ID, "version": team.Version}))
	child := spawned(t, s, "alice", Capabilities{BoardsRead: []string{team.Board}, JoinTeams: true})
	expectFault(t, s, child.ID, "message.list", "forbidden", map[string]any{"board": team.Board})
	team = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": team.ID, "agent": child.ID, "version": team.Version}))
	team = teamIn(t, call(t, s, child.ID, "team.join", map[string]any{"id": team.ID, "version": team.Version}))
	call(t, s, child.ID, "message.list", map[string]any{"board": team.Board})
	call(t, s, "alice", "team.leave", map[string]any{"id": team.ID, "version": team.Version})
	expectFault(t, s, child.ID, "message.list", "forbidden", map[string]any{"board": team.Board})
}

func TestSharedConcurrencyAndDeadlineCannotReset(t *testing.T) {
	cfg := dynamicConfig()
	cfg.Limits.MaxConcurrentTasks = 1
	s := testStore(t, cfg)
	instruction(t, s)
	task, lease := claim(t, s)
	call(t, s, "", "instruct", map[string]any{"to": []string{"alice"}, "text": "wait for shared slot"})
	expectFault(t, s, "alice", "task.claim", "busy", map[string]any{})
	call(t, s, "bob", "task.complete", map[string]any{"id": task.ID, "version": task.Version, "lease": lease, "status": "submitted", "result": "done", "usage": map[string]int{"input_tokens": 3}})
	call(t, s, "alice", "task.claim", map[string]any{})
	if err := s.startRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline, err := s.RuntimeDeadline()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.startRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	again, _ := s.RuntimeDeadline()
	if !again.Equal(deadline) {
		t.Fatal("restart reset deadline")
	}
	c := credential(t, s, "alice")
	prior, f := invoke(s, c, "board.post", map[string]any{"board": "team", "text": "cached before expiry"}, "expiry-retry")
	if f != nil {
		t.Fatal(f)
	}
	_, err = s.db.Exec("UPDATE metadata SET value=? WHERE key='deadline'", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	got, f := invoke(s, c, "board.post", map[string]any{"board": "team", "text": "cached before expiry"}, "expiry-retry")
	if f != nil || !bytes.Equal(prior, got) {
		t.Fatal("expired budget hid committed receipt", f)
	}
	expectFault(t, s, "alice", "agent.spawn", "limit", map[string]any{"template": "echo"})
	expectFault(t, s, "alice", "board.post", "limit", map[string]any{"board": "team", "text": "new after expiry"})
}

func TestOwnerExportRemovesLeasesAndIncludesEveryPage(t *testing.T) {
	s := testStore(t, dynamicConfig())
	instruction(t, s)
	_, lease := claim(t, s)
	spawned(t, s, "alice", Capabilities{})
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := s.Export(context.Background(), cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Records) == 0 {
			break
		}
		for _, v := range page.Records {
			if seen[v.ID] {
				t.Fatal("duplicate export record")
			}
			seen[v.ID] = true
			if strings.Contains(string(v.Data), lease) || strings.Contains(string(v.Data), "template-secret") {
				t.Fatal("export leaked private runtime material")
			}
		}
		cursor = page.Cursor
	}
	var n int
	_ = s.db.QueryRow("SELECT count(*) FROM records").Scan(&n)
	if len(seen) != n {
		t.Fatal("export missed records", len(seen), n)
	}
	expectFault(t, s, "alice", "export", "forbidden", map[string]any{})
}

func TestTeamHistoryStartsAtJoinAndChildJoiningIsExplicit(t *testing.T) {
	for _, history := range []string{"since-join", "all"} {
		t.Run(history, func(t *testing.T) {
			cfg := dynamicConfig()
			cfg.Teams.History = history
			s := testStore(t, cfg)
			team := teamIn(t, call(t, s, "alice", "team.create", map[string]any{"name": "History"}))
			old := call(t, s, "alice", "board.post", map[string]any{"board": team.Board, "text": "before membership"})
			var sent struct {
				Message Message `json:"message"`
			}
			_ = json.Unmarshal(old, &sent)
			team = teamIn(t, call(t, s, "alice", "team.invite", map[string]any{"id": team.ID, "agent": "bob", "version": team.Version}))
			team = teamIn(t, call(t, s, "bob", "team.join", map[string]any{"id": team.ID, "version": team.Version}))
			call(t, s, "alice", "board.post", map[string]any{"board": team.Board, "text": "after membership"})
			raw := call(t, s, "bob", "message.list", map[string]any{"board": team.Board})
			if !strings.Contains(string(raw), "after membership") {
				t.Fatal("new posts hidden")
			}
			if strings.Contains(string(raw), "before membership") != (history == "all") {
				t.Fatal("history policy violated", string(raw))
			}
			if history == "since-join" {
				expectFault(t, s, "bob", "message.list", "invalid", map[string]any{"after": sent.Message.ID})
			}
			if raw := call(t, s, "bob", "team.list", map[string]any{}); strings.Contains(string(raw), "joined_after") {
				t.Fatal("global sequence leaked to member")
			}
			child := spawned(t, s, "alice", Capabilities{})
			team = teamIn(t, call(t, s, "alice", "team.invite", map[string]any{"id": team.ID, "agent": child.ID, "version": team.Version}))
			expectFault(t, s, child.ID, "team.join", "forbidden", map[string]any{"id": team.ID, "version": team.Version})
			allowed := spawned(t, s, "alice", Capabilities{JoinTeams: true})
			team = teamIn(t, call(t, s, "alice", "team.invite", map[string]any{"id": team.ID, "agent": allowed.ID, "version": team.Version}))
			team = teamIn(t, call(t, s, allowed.ID, "team.join", map[string]any{"id": team.ID, "version": team.Version}))
			call(t, s, allowed.ID, "memory.put", map[string]any{"namespace": team.Memory, "key": "current", "value": "accepted", "version": 0})
			team = teamIn(t, call(t, s, "alice", "team.remove", map[string]any{"id": team.ID, "agent": allowed.ID, "version": team.Version}))
			expectFault(t, s, allowed.ID, "memory.get", "forbidden", map[string]any{"namespace": team.Memory, "key": "current"})
		})
	}
}

func TestStaticRevocationAndTeamExitRetainBudgetCapacity(t *testing.T) {
	cfg := dynamicConfig()
	s := testStore(t, cfg)
	team := teamIn(t, call(t, s, "alice", "team.create", map[string]any{"name": "Bounded"}))
	team = teamIn(t, call(t, s, "alice", "team.invite", map[string]any{"id": team.ID, "agent": "bob", "version": team.Version}))
	team = teamIn(t, call(t, s, "bob", "team.join", map[string]any{"id": team.ID, "version": team.Version}))
	// Pin the live budgets to the exact accounted usage, including reserved
	// revocation and exit slots. Ordinary writes now fail, while exits fit.
	reserved, err := lifecycleReserve(context.Background(), s.db)
	if err != nil {
		t.Fatal(err)
	}
	var records, requests int
	_ = s.db.QueryRow("SELECT count(*) FROM records").Scan(&records)
	_ = s.db.QueryRow("SELECT count(*) FROM requests").Scan(&requests)
	s.cfg.Limits.MaxRecords = records + reserved
	s.cfg.Limits.MaxRequests = requests + reserved
	expectFault(t, s, "alice", "board.post", "limit", map[string]any{"board": "team", "text": "ordinary write blocked"})
	team = teamIn(t, call(t, s, "bob", "team.leave", map[string]any{"id": team.ID, "version": team.Version}))
	call(t, s, "alice", "team.dissolve", map[string]any{"id": team.ID, "version": team.Version})
	old := credential(t, s, "alice")
	call(t, s, "", "agent.stop", map[string]any{"id": "alice"})
	_, f := invoke(s, old, "identity", map[string]any{}, "")
	if f == nil || f.Code != "unauthorized" {
		t.Fatal("static revocation failed at budget")
	}
}
