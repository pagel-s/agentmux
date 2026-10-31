package managed

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func memoryIn(t *testing.T, raw []byte) Memory {
	t.Helper()
	var out struct {
		Entry Memory `json:"entry"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out.Entry
}
func expireMemory(t *testing.T, s *Store, scope, key string) {
	t.Helper()
	id := "memory_" + hash(scope+"\x00"+key)
	var m Memory
	if err := get(context.Background(), s.db, "memory", id, &m); err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(-time.Second)
	m.ExpiresAt = expiry.UTC().Format(time.RFC3339Nano)
	m.ExpiresUnixNano = expiry.UnixNano()
	if err := save(context.Background(), s.db, "memory", id, m.Author, "", scope, m); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryExpiryPruneTombstonesAndReplay(t *testing.T) {
	s := testStore(t, testConfig())
	alice := credential(t, s, "alice")
	args := map[string]any{"namespace": "private", "key": "plan", "value": "private secret", "ttl_seconds": 10, "tags": []string{"plan"}}
	raw, p := invoke(s, alice, "memory.put", args, "stable-memory-write")
	if p != nil {
		t.Fatal(p)
	}
	m := memoryIn(t, raw)
	if m.Version != 1 || m.SHA256 != hash("private secret") {
		t.Fatal("missing provenance digest")
	}
	expireMemory(t, s, "private:alice", "plan")
	expectFault(t, s, "alice", "memory.get", "not_found", map[string]any{"namespace": "private", "key": "plan"})
	if _, p := invoke(s, alice, "memory.put", args, "stable-memory-write"); p == nil || p.Code != "not_found" {
		t.Fatalf("receipt resurrected expired body: %v", p)
	}
	if strings.Contains(string(call(t, s, "alice", "memory.list", map[string]any{"namespace": "private"})), "private secret") {
		t.Fatal("expired value leaked through list")
	}
	call(t, s, "", "memory.prune", map[string]any{})
	var prior string
	if err := s.db.QueryRow("SELECT response FROM requests WHERE request_id='stable-memory-write'").Scan(&prior); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prior, "private secret") {
		t.Fatal("prune retained payload in idempotency receipt")
	}
	var head struct {
		Version int    `json:"version"`
		State   string `json:"state"`
	}
	json.Unmarshal(call(t, s, "alice", "memory.head", map[string]any{"namespace": "private", "key": "plan"}), &head)
	if head.Version != 2 || head.State != "deleted" {
		t.Fatalf("CAS tombstone lost: %+v", head)
	}
	expectFault(t, s, "alice", "memory.put", "conflict", map[string]any{"namespace": "private", "key": "plan", "value": "stale", "version": 1})
	newEntry := memoryIn(t, call(t, s, "alice", "memory.put", map[string]any{"namespace": "private", "key": "plan", "value": "renewed", "version": 2}))
	if newEntry.Version != 3 {
		t.Fatal("version reused after deletion")
	}
	if strings.Contains(string(call(t, s, "bob", "memory.list", map[string]any{"namespace": "private"})), "renewed") {
		t.Fatal("private namespace leaked")
	}
}

func TestMemorySearchACLProvenanceAndIndexInvalidation(t *testing.T) {
	s := testStore(t, testConfig())
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "cache", "value": "cache invalidation design", "tags": []string{"architecture"}})
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "parser", "value": "parser implementation"})
	call(t, s, "alice", "memory.index", map[string]any{"namespace": "team", "key": "cache", "version": 1, "embedding": []float64{1, 0}, "embedding_model": "local-embedding"})
	call(t, s, "alice", "memory.index", map[string]any{"namespace": "team", "key": "parser", "version": 1, "embedding": []float64{0, 1}, "embedding_model": "local-embedding"})
	var out struct {
		Hits []memoryHit `json:"hits"`
	}
	json.Unmarshal(call(t, s, "bob", "memory.search", map[string]any{"namespace": "team", "embedding": []float64{1, 0}, "embedding_model": "local-embedding"}), &out)
	if len(out.Hits) != 2 || out.Hits[0].Entry.Key != "cache" || out.Hits[0].Score != 1 {
		t.Fatalf("wrong cosine ranking: %+v", out.Hits)
	}
	expectFault(t, s, "eve", "memory.search", "forbidden", map[string]any{"namespace": "team", "query": "cache"})
	expectFault(t, s, "bob", "memory.index", "forbidden", map[string]any{"namespace": "team", "key": "cache", "version": 1, "embedding": []float64{1, 0}, "embedding_model": "local-embedding"})
	expectFault(t, s, "alice", "memory.put", "not_found", map[string]any{"namespace": "team", "key": "forged", "value": "x", "sources": []MemorySource{{Kind: "message", ID: "hidden-id"}}})
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "cache", "value": "changed", "version": 1, "sources": []MemorySource{{Kind: "external", URI: "https://example.test/design", SHA256: hash("evidence")}}})
	json.Unmarshal(call(t, s, "bob", "memory.search", map[string]any{"namespace": "team", "embedding": []float64{1, 0}, "embedding_model": "local-embedding"}), &out)
	if len(out.Hits) != 1 || out.Hits[0].Entry.Key != "parser" {
		t.Fatal("stale vector remained attached to changed content")
	}
}

func TestMemoryByteAllocationAndTTLPolicy(t *testing.T) {
	cfg := testConfig()
	cfg.MemoryPolicy.MaxTotalBytes = 1024
	cfg.MemoryPolicy.MaxTTLSeconds = 5
	s := testStore(t, cfg)
	expectFault(t, s, "alice", "memory.put", "invalid", map[string]any{"namespace": "private", "key": "a", "value": "x", "ttl_seconds": 6})
	expectFault(t, s, "alice", "memory.put", "limit", map[string]any{"namespace": "private", "key": "a", "value": strings.Repeat("x", 1024)})
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "private", "key": "a", "value": "small", "ttl_seconds": 5})
	call(t, s, "alice", "memory.delete", map[string]any{"namespace": "private", "key": "a", "version": 1})
	expectFault(t, s, "alice", "memory.get", "not_found", map[string]any{"namespace": "private", "key": "a"})
}
