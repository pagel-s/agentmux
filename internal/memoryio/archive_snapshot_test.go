package memoryio

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
)

type patchBeforeImportGet struct {
	Caller
	patched bool
}

func (c *patchBeforeImportGet) Call(ctx context.Context, r managed.Request) (json.RawMessage, *managed.Fault) {
	if r.Operation == "memory.get" && !c.patched {
		c.patched = true
		args, _ := json.Marshal(map[string]any{"namespace": "private", "key": "design", "value": "concurrent local change", "version": 1})
		if _, p := c.Caller.Call(ctx, managed.Request{Operation: "memory.patch", RequestID: "concurrent-owner-patch", Args: args}); p != nil {
			return nil, p
		}
	}
	return c.Caller.Call(ctx, r)
}

func TestImportNeverSkipsChangedSnapshotWithPreservedImportClaims(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep", true: "replace"}[replace], func(t *testing.T) {
			ctx := context.Background()
			source, dest := fixture(t, "snapshot-source"), fixture(t, "snapshot-dest")
			expiry := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
			if _, err := Call(ctx, source, "memory.put", map[string]any{"namespace": "private", "key": "design", "value": "archive value", "expires_at": expiry, "tags": []string{"retained-tag"}}, "source-write"); err != nil {
				t.Fatal(err)
			}
			a, err := Export(ctx, source, "private")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Import(ctx, dest, a, "private", false); err != nil {
				t.Fatal(err)
			}
			var before, after struct {
				Entry managed.Memory `json:"entry"`
			}
			raw, err := Call(ctx, dest, "memory.get", map[string]any{"namespace": "private", "key": "design"}, "")
			if err != nil {
				t.Fatal(err)
			}
			json.Unmarshal(raw, &before)
			result, err := Import(ctx, &patchBeforeImportGet{Caller: dest}, a, "private", replace)
			if err == nil || !strings.Contains(err.Error(), "changed during import preflight") || result.Imported != 0 || result.Skipped != 0 {
				t.Fatalf("changed value falsely reported already imported: %+v %v", result, err)
			}
			raw, err = Call(ctx, dest, "memory.get", map[string]any{"namespace": "private", "key": "design"}, "")
			if err != nil {
				t.Fatal(err)
			}
			json.Unmarshal(raw, &after)
			if after.Entry.Version != 2 || after.Entry.Value != "concurrent local change" || after.Entry.ExpiresAt != expiry || !reflect.DeepEqual(before.Entry.Tags, after.Entry.Tags) || !reflect.DeepEqual(before.Entry.Sources, after.Entry.Sources) {
				t.Fatalf("import race overwrote local value, metadata or exact expiry: %+v", after.Entry)
			}
		})
	}
}
