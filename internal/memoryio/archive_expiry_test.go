package memoryio

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"agentmux/internal/managed"
)

type delayedImportCaller struct {
	Caller
	until     time.Time
	operation string
	key       string
	delayed   bool
}

func (c *delayedImportCaller) Call(ctx context.Context, r managed.Request) (json.RawMessage, *managed.Fault) {
	var in struct {
		Key string `json:"key"`
	}
	json.Unmarshal(r.Args, &in)
	if !c.delayed && r.Operation == c.operation && in.Key == c.key {
		c.delayed = true
		if remaining := time.Until(c.until); remaining > 0 {
			time.Sleep(remaining)
		}
	}
	return c.Caller.Call(ctx, r)
}

func expiringArchive(at time.Time, second bool) Archive {
	a := Archive{Version: 1, SourceSession: "source", SourceAgent: "alice", Namespace: "private", ExportedAt: time.Now().UTC().Format(time.RFC3339Nano), Entries: []managed.Memory{{Key: "short", Value: "bounded lifetime", Author: "alice", Version: 1, ExpiresAt: at.UTC().Format(time.RFC3339Nano)}}}
	if second {
		a.Entries = append(a.Entries, managed.Memory{Key: "later", Value: "permanent", Author: "alice", Version: 1})
	}
	a.SHA256 = archiveDigest(a)
	return a
}

func TestImportPreservesExactAbsoluteExpiry(t *testing.T) {
	dest := fixture(t, "absolute-expiry")
	a := expiringArchive(time.Now().Add(time.Hour+123*time.Nanosecond), false)
	result, err := Import(context.Background(), dest, a, "private", false)
	if err != nil || result.Imported != 1 {
		t.Fatalf("import: %+v %v", result, err)
	}
	raw, err := Call(context.Background(), dest, "memory.get", map[string]any{"namespace": "private", "key": "short"}, "")
	var out struct {
		Entry managed.Memory `json:"entry"`
	}
	json.Unmarshal(raw, &out)
	if err != nil || out.Entry.ExpiresAt != a.Entries[0].ExpiresAt {
		t.Fatalf("source deadline was extended or rounded: %s %s %v", a.Entries[0].ExpiresAt, out.Entry.ExpiresAt, err)
	}
}

func TestImportRechecksExpiryAfterAllDestinationPreflight(t *testing.T) {
	dest := fixture(t, "delayed-preflight")
	deadline := time.Now().Add(75 * time.Millisecond)
	a := expiringArchive(deadline, true)
	c := &delayedImportCaller{Caller: dest, until: deadline.Add(5 * time.Millisecond), operation: "memory.head", key: "later"}
	result, err := Import(context.Background(), c, a, "private", false)
	if err != nil || result.Imported != 1 || result.Expired != 1 {
		t.Fatalf("expired entry admitted after slow preflight: %+v %v", result, err)
	}
	assertMissingImportedKey(t, dest, "short")
}

func TestImportBrokerRejectsExpiryWhileWriteIsInTransit(t *testing.T) {
	dest := fixture(t, "delayed-write")
	deadline := time.Now().Add(75 * time.Millisecond)
	a := expiringArchive(deadline, false)
	c := &delayedImportCaller{Caller: dest, until: deadline.Add(5 * time.Millisecond), operation: "memory.put", key: "short"}
	result, err := Import(context.Background(), c, a, "private", false)
	if err != nil || result.Imported != 0 || result.Expired != 1 {
		t.Fatalf("write revived archive after absolute deadline: %+v %v", result, err)
	}
	assertMissingImportedKey(t, dest, "short")
}

func assertMissingImportedKey(t *testing.T, c Caller, key string) {
	t.Helper()
	raw, err := Call(context.Background(), c, "memory.head", map[string]any{"namespace": "private", "key": key}, "")
	var head struct {
		Version int    `json:"version"`
		State   string `json:"state"`
	}
	json.Unmarshal(raw, &head)
	if err != nil || head.Version != 0 || head.State != "missing" {
		t.Fatalf("expired archive entry was written: %s %v", raw, err)
	}
}
