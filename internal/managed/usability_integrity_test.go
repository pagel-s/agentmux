package managed

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
)

func TestMemoryPatchPreservesMetadataExactExpiryAndFencesReview(t *testing.T) {
	s := testStore(t, testConfig())
	expires := time.Now().Add(75*time.Second + 123*time.Nanosecond).UTC().Format(time.RFC3339Nano)
	source := MemorySource{Kind: "external", URI: "https://example.test/asserted", SHA256: hash("source")}
	first := memoryIn(t, call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "review", "value": "first", "expires_at": expires, "tags": []string{"reviewed"}, "sources": []MemorySource{source}}))
	call(t, s, "alice", "memory.index", map[string]any{"namespace": "team", "key": "review", "version": first.Version, "embedding": []float64{1, 0}, "embedding_model": "test"})
	args := map[string]any{"namespace": "team", "key": "review", "value": "edited", "version": first.Version}
	updated := memoryIn(t, call(t, s, "alice", "memory.patch", args))
	if updated.ExpiresAt != expires || updated.ExpiresUnixNano != first.ExpiresUnixNano || !reflect.DeepEqual(updated.Tags, first.Tags) || !reflect.DeepEqual(updated.Sources, first.Sources) || updated.PreviousSHA256 != first.SHA256 || updated.CreatedAt != first.CreatedAt || updated.Version != 2 || updated.SHA256 != hash("edited") {
		t.Fatalf("value edit changed reviewed metadata: %+v", updated)
	}
	var stored Memory
	if err := get(context.Background(), s.db, "memory", "memory_"+hash("team\x00review"), &stored); err != nil || len(stored.Embedding) != 0 {
		t.Fatalf("value edit retained stale index: %+v %v", stored, err)
	}
	expectFault(t, s, "alice", "memory.patch", "conflict", args)
	expectFault(t, s, "bob", "memory.patch", "forbidden", map[string]any{"namespace": "team", "key": "review", "version": 2, "value": "unauthorized"})
	expectFault(t, s, "alice", "memory.patch", "invalid", map[string]any{"namespace": "team", "key": "review", "version": 2, "value": "x", "tags": []string{}})
	// A metadata edit after preview must invalidate the same content CAS fence.
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "review", "version": 2, "value": "edited", "tags": []string{"new-owner-metadata"}})
	expectFault(t, s, "alice", "memory.patch", "conflict", map[string]any{"namespace": "team", "key": "review", "version": 2, "value": "stale-preview"})
	expectFault(t, s, "alice", "memory.put", "expired", map[string]any{"namespace": "private", "key": "late", "value": "no resurrection", "expires_at": time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)})
	expireMemory(t, s, "team", "review")
	expectFault(t, s, "alice", "memory.patch", "not_found", map[string]any{"namespace": "team", "key": "review", "version": 3, "value": "cannot renew"})
}

func TestMemoryPatchConcurrentReviewOnlyOneWriterWins(t *testing.T) {
	s := testStore(t, testConfig())
	c := credential(t, s, "alice")
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "private", "key": "race", "value": "first", "tags": []string{"keep"}, "ttl_seconds": 100})
	var wg sync.WaitGroup
	results := make(chan *Fault, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, p := invoke(s, c, "memory.patch", map[string]any{"namespace": "private", "key": "race", "value": fmt.Sprint(i), "version": 1}, fmt.Sprintf("patch-race-%d", i))
			results <- p
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for p := range results {
		if p == nil {
			successes++
		} else if p.Code == "conflict" {
			conflicts++
		} else {
			t.Fatal(p)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS admitted %d successes and %d conflicts", successes, conflicts)
	}
	m := memoryIn(t, call(t, s, "alice", "memory.get", map[string]any{"namespace": "private", "key": "race"}))
	if m.Version != 2 || !reflect.DeepEqual(m.Tags, []string{"keep"}) {
		t.Fatal(m)
	}
}

func TestMemoryPatchRequiresReadAndWriteIncludingReceiptReplay(t *testing.T) {
	s := testStore(t, testConfig())
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "known-key", "value": "original-value", "tags": []string{"private-project-secret"}, "sources": []MemorySource{{Kind: "external", URI: "https://example.test/confidential-project"}}})
	call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "memory", "agent": "bob", "namespace": "team", "read": false, "write": true})
	bob := credential(t, s, "bob")
	args := map[string]any{"namespace": "team", "key": "known-key", "version": 1, "value": "replacement"}
	if raw, p := invoke(s, bob, "memory.patch", args, "write-only-patch"); p == nil || p.Code != "forbidden" || len(raw) != 0 {
		t.Fatalf("write-only patch disclosed inherited metadata: %s %v", raw, p)
	}
	unchanged := memoryIn(t, call(t, s, "alice", "memory.get", map[string]any{"namespace": "team", "key": "known-key"}))
	if unchanged.Version != 1 || unchanged.Value != "original-value" {
		t.Fatalf("forbidden patch mutated memory: %+v", unchanged)
	}
	call(t, s, "", "control.update", map[string]any{"version": 2, "kind": "memory", "agent": "bob", "namespace": "team", "read": true, "write": true})
	if _, p := invoke(s, bob, "memory.patch", args, "authorized-patch"); p != nil {
		t.Fatal(p)
	}
	call(t, s, "", "control.update", map[string]any{"version": 3, "kind": "memory", "agent": "bob", "namespace": "team", "read": false, "write": true})
	if raw, p := invoke(s, bob, "memory.patch", args, "authorized-patch"); p == nil || p.Code != "forbidden" || len(raw) != 0 {
		t.Fatalf("receipt restored withdrawn metadata visibility: %s %v", raw, p)
	}
}

func TestWorkspaceReconnectPreservesSavedCommandsAndExplicitDenies(t *testing.T) {
	s := workspaceGroupStore(t)
	call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "collaboration", "agents": []string{"alice", "bob"}, "enabled": true, "commands": true})
	call(t, s, "alice", "instruct", map[string]any{"to": []string{"bob"}, "text": "command enabled"})
	call(t, s, "", "control.update", map[string]any{"version": 2, "kind": "independent", "agents": []string{"alice"}})
	group := connectWorkspaceGroup(t, s, 3, "alice", "bob")
	if !group.Group.Commands {
		t.Fatal("omitted commands erased the saved preference")
	}
	call(t, s, "alice", "instruct", map[string]any{"to": []string{"bob"}, "text": "command restored"})
	call(t, s, "", "control.update", map[string]any{"version": 4, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": false})
	connectWorkspaceGroup(t, s, 5, "alice", "bob")
	expectFault(t, s, "alice", "instruct", "forbidden", map[string]any{"to": []string{"bob"}, "text": "explicit owner deny remains"})
	call(t, s, "", "control.update", map[string]any{"version": 6, "kind": "collaboration", "agents": []string{"alice", "bob"}, "enabled": true, "commands": false})
	group = connectWorkspaceGroup(t, s, 7, "alice", "bob")
	if group.Group.Commands {
		t.Fatal("explicit false did not remain notes-only")
	}
	expectFault(t, s, "bob", "instruct", "forbidden", map[string]any{"to": []string{"alice"}, "text": "notes only"})
	call(t, s, "bob", "message.send", map[string]any{"to": []string{"alice"}, "text": "note allowed"})
}

func TestAgentRetirePreservesIdentityHistoryAndRequiresStoppedProcess(t *testing.T) {
	s := testStore(t, dynamicConfig())
	child := spawned(t, s, "alice", Capabilities{})
	for _, id := range []string{"alice", child.ID} {
		expectFault(t, s, "bob", "agent.retire", "forbidden", map[string]any{"agent": id})
		expectFault(t, s, "", "agent.retire", "conflict", map[string]any{"agent": id})
	}
	message := call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "permanent identity history"})
	unlock, err := acquireLock(filepath.Join(s.dir, "worker-alice.lock"))
	if err != nil {
		t.Fatal(err)
	}
	call(t, s, "", "agent.stop", map[string]any{"id": "alice", "expected_agents": []string{"alice", child.ID}})
	expectFault(t, s, "", "agent.retire", "conflict", map[string]any{"agent": "alice"})
	unlock()
	for _, id := range []string{"alice", child.ID} {
		oldCredential := credential(t, s, id)
		call(t, s, "", "agent.retire", map[string]any{"agent": id})
		if _, p := invoke(s, oldCredential, "identity", map[string]any{}, ""); p == nil || p.Code != "unauthorized" {
			t.Fatalf("retired credential active: %v", p)
		}
		if _, err := s.Rotate(id); err == nil {
			t.Fatal("retired identity rotated into an active credential")
		}
		if _, err := s.Agent(id); err == nil {
			t.Fatal("retired definition can launch")
		}
		expectFault(t, s, "", "agent.label", "conflict", map[string]any{"id": id, "title": "alter archive", "version": 0})
		expectFault(t, s, "", "agent.add", "conflict", map[string]any{"id": id, "config": workspaceAgent(t, "claude")})
	}
	var active, archive struct {
		Agents []AgentInfo `json:"agents"`
	}
	json.Unmarshal(call(t, s, "", "agent.list", map[string]any{}), &active)
	json.Unmarshal(call(t, s, "", "agent.list", map[string]any{"include_archived": true}), &archive)
	if len(active.Agents) != 2 || len(archive.Agents) != 4 {
		t.Fatalf("archive inclusion incorrect: %d %d", len(active.Agents), len(archive.Agents))
	}
	for _, a := range archive.Agents {
		if a.ID == "alice" || a.ID == child.ID {
			if a.State != "retired" || a.RetiredAt == "" {
				t.Fatal(a)
			}
		}
	}
	var sent struct {
		Message Message `json:"message"`
	}
	json.Unmarshal(message, &sent)
	if raw := call(t, s, "", "message.get", map[string]any{"id": sent.Message.ID}); !strings.Contains(string(raw), "permanent identity history") {
		t.Fatal("retirement erased history")
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.knownAgent(context.Background(), reopened.db, "alice") {
		t.Fatal("configured retired agent resurrected on reopen")
	}
}

func TestRetiredDefinitionsReleaseAdmissionAndFrozenRevokeRejectsNewChild(t *testing.T) {
	s := testStore(t, dynamicConfig())
	cfg := managedconfig.Agent{Harness: "command", Command: []string{"/bin/cat"}}
	for i := 0; i < 197; i++ {
		id := fmt.Sprintf("retire-cap-%03d", i)
		info := AgentInfo{ID: id, State: "revoked", Config: cfg}
		if err := save(context.Background(), s.db, "agent", "agent_"+id, ownerID, id, "", info); err != nil {
			t.Fatal(err)
		}
	}
	expectFault(t, s, "", "agent.add", "limit", map[string]any{"id": "replacement", "config": workspaceAgent(t, "claude")})
	call(t, s, "", "agent.retire", map[string]any{"agent": "retire-cap-000"})
	call(t, s, "", "agent.add", map[string]any{"id": "replacement", "config": workspaceAgent(t, "claude")})
	child := spawned(t, s, "alice", Capabilities{})
	expectFault(t, s, "", "agent.stop", "conflict", map[string]any{"id": "alice", "expected_agents": []string{"alice"}})
	if !s.knownAgent(context.Background(), s.db, "alice") || !s.knownAgent(context.Background(), s.db, child.ID) {
		t.Fatal("stale revoke partially revoked subtree")
	}
}

func TestMoreThanTwoHundredLifetimeWorkspaceDefinitionsCanBeRetired(t *testing.T) {
	s := testStore(t, testConfig())
	definition := workspaceAgent(t, "claude")
	for i := 0; i < 205; i++ {
		id := fmt.Sprintf("lifetime-%03d", i)
		call(t, s, "", "agent.add", map[string]any{"id": id, "config": definition})
		call(t, s, "", "agent.stop", map[string]any{"id": id})
		call(t, s, "", "agent.retire", map[string]any{"agent": id})
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM records WHERE kind='agent' AND json_extract(data,'$.state')='retired'").Scan(&n); err != nil || n != 205 {
		t.Fatalf("lost lifecycle history: %d %v", n, err)
	}
	call(t, s, "", "agent.add", map[string]any{"id": "still-admitted", "config": definition})
	expectFault(t, s, "", "agent.add", "conflict", map[string]any{"id": "lifetime-000", "config": definition})
	boundary, err := s.authBoundaryConfig(context.Background(), s.db)
	if err != nil || len(boundary.Agents) != 4 {
		t.Fatalf("retired definitions leaked into live boundaries: %d %v", len(boundary.Agents), err)
	}
}
