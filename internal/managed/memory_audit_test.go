package managed

import (
	"fmt"
	"strings"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestPrivateMemoryReceiptRedactionMatchesExactActor(t *testing.T) {
	c := testConfig()
	for _, id := range []string{"a_b", "axb"} {
		c.Agents[id] = managedconfig.Agent{Harness: "command", Command: []string{"/usr/bin/true"}}
	}
	s := testStore(t, c)
	first := map[string]any{"namespace": "private", "key": "same", "value": "first private value"}
	other := map[string]any{"namespace": "private", "key": "same", "value": "unrelated private value"}
	if _, f := invoke(s, credential(t, s, "a_b"), "memory.put", first, "first-receipt"); f != nil {
		t.Fatal(f)
	}
	if _, f := invoke(s, credential(t, s, "axb"), "memory.put", other, "other-receipt"); f != nil {
		t.Fatal(f)
	}
	call(t, s, "a_b", "memory.delete", map[string]any{"namespace": "private", "key": "same", "version": 1})
	replayed, f := invoke(s, credential(t, s, "axb"), "memory.put", other, "other-receipt")
	if f != nil || !strings.Contains(string(replayed), "unrelated private value") {
		t.Fatal("underscore actor redacted another agent's private receipt", f)
	}
	var raw string
	if err := s.db.QueryRow("SELECT response FROM requests WHERE request_id='first-receipt'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "first private value") {
		t.Fatal("deleted actor retained old receipt payload")
	}
}

func TestMemoryTombstoneGrowthReservedBeforeAdmission(t *testing.T) {
	c := testConfig()
	id := strings.Repeat("a", 48)
	c.Agents[id] = managedconfig.Agent{Harness: "command", Command: []string{"/usr/bin/true"}}
	c.Memory["team"] = managedconfig.Access{Readers: []string{id}, Writers: []string{id}}
	s := testStore(t, c)
	for i := 0; i < 8; i++ {
		call(t, s, "", "memory.put", map[string]any{"namespace": "team", "key": fmt.Sprint(i), "value": ""})
	}
	var used int64
	if err := s.db.QueryRow("SELECT sum(length(CAST(data AS BLOB))) FROM records WHERE kind='memory'").Scan(&used); err != nil {
		t.Fatal(err)
	}
	s.cfg.MemoryPolicy.MaxTotalBytes = used + 8*64
	expectFault(t, s, "", "memory.put", "limit", map[string]any{"namespace": "team", "key": "extra", "value": ""})
	s.cfg.MemoryPolicy.MaxTotalBytes = used + 8*128
	for i := 0; i < 8; i++ {
		call(t, s, id, "memory.delete", map[string]any{"namespace": "team", "key": fmt.Sprint(i), "version": 1})
	}
	var after int64
	if err := s.db.QueryRow("SELECT sum(length(CAST(data AS BLOB))) FROM records WHERE kind='memory'").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after <= used {
		t.Fatal("fixture did not exercise tombstone growth")
	}
	if after > s.cfg.MemoryPolicy.MaxTotalBytes {
		t.Fatal("deletion exceeded memory byte ceiling")
	}
}

func TestMemoryMutationsRequireReceiptsAndReplayNoPayloadAfterExpiry(t *testing.T) {
	s := testStore(t, testConfig())
	alice := credential(t, s, "alice")
	owner := credential(t, s, "")
	for _, op := range []string{"memory.delete", "memory.index", "memory.prune"} {
		credential := alice
		if op == "memory.prune" {
			credential = owner
		}
		if _, f := invoke(s, credential, op, map[string]any{}, ""); f == nil || f.Code != "invalid" {
			t.Fatalf("%s bypassed transaction/idempotency path: %v", op, f)
		}
	}
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "private", "key": "indexed", "value": "private expiring payload"})
	args := map[string]any{"namespace": "private", "key": "indexed", "version": 1, "embedding": []float64{1, 0}, "embedding_model": "exact"}
	if _, f := invoke(s, alice, "memory.index", args, "same-index"); f != nil {
		t.Fatal(f)
	}
	expireMemory(t, s, "private:alice", "indexed")
	raw, f := invoke(s, alice, "memory.index", args, "same-index")
	if f != nil || strings.Contains(string(raw), "private expiring payload") {
		t.Fatal("index receipt disclosed expired content", f)
	}
}
