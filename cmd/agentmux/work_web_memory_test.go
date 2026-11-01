package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestWebMemoryReviewExposesTombstoneCASWithoutSaving(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	call := func(operation string, args map[string]any) (int, []byte) {
		t.Helper()
		response := ownerWebRequest(t, state, client, "/api/call", map[string]any{"operation": operation, "args": args})
		return response.Code, response.Body.Bytes()
	}
	args := map[string]any{"namespace": "team", "key": "plan", "value": "Original", "version": 0}
	if code, body := call("memory.put", args); code != http.StatusOK {
		t.Fatalf("create: %d %s", code, body)
	}
	if _, err := workCall(context.Background(), client, "memory.delete", map[string]any{"namespace": "team", "key": "plan", "version": 1}); err != nil {
		t.Fatal(err)
	}
	code, body := call("memory.head", map[string]any{"namespace": "team", "key": "plan"})
	var head struct {
		Result struct {
			Version int    `json:"version"`
			State   string `json:"state"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &head); err != nil || code != http.StatusOK || head.Result.Version != 2 || head.Result.State != "deleted" {
		t.Fatalf("review: %d %s %v", code, body, err)
	}
	args["version"] = 1
	if code, body = call("memory.put", args); code != http.StatusBadRequest {
		t.Fatalf("review implicitly changed stale CAS: %d %s", code, body)
	}
	args["version"] = head.Result.Version
	args["value"] = "Reviewed draft"
	if code, body = call("memory.put", args); code != http.StatusOK {
		t.Fatalf("explicit reviewed save: %d %s", code, body)
	}
}

func TestWebMemoryValuePatchPreservesSourcesTagsAndAbsoluteExpiry(t *testing.T) {
	state, client, _ := inventoryBroker(t)
	original := map[string]any{"namespace": "team", "key": "sourced", "value": "Original value", "version": 0, "expires_at": time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano), "tags": []string{"reviewed"}, "sources": []map[string]any{{"kind": "external", "uri": "https://example.test/evidence"}}}
	created := ownerWebRequest(t, state, client, "/api/call", map[string]any{"operation": "memory.put", "args": original})
	if created.Code != http.StatusOK {
		t.Fatalf("create sourced memory: %d %s", created.Code, created.Body.String())
	}
	updated := ownerWebRequest(t, state, client, "/api/call", map[string]any{"operation": "memory.patch", "args": map[string]any{"namespace": "team", "key": "sourced", "value": "Edited value", "version": 1}})
	if updated.Code != http.StatusOK {
		t.Fatalf("patch sourced memory: %d %s", updated.Code, updated.Body.String())
	}
	var before, after struct {
		Result struct {
			Entry struct {
				Value     string          `json:"value"`
				Version   int             `json:"version"`
				ExpiresAt string          `json:"expires_at"`
				Sources   json.RawMessage `json:"sources"`
				Tags      json.RawMessage `json:"tags"`
			} `json:"entry"`
		} `json:"result"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.Result.Entry.Value != "Edited value" || after.Result.Entry.Version != 2 || before.Result.Entry.ExpiresAt != after.Result.Entry.ExpiresAt || string(before.Result.Entry.Sources) != string(after.Result.Entry.Sources) || string(before.Result.Entry.Tags) != string(after.Result.Entry.Tags) {
		t.Fatalf("value patch changed saved metadata: before=%s after=%s", created.Body.String(), updated.Body.String())
	}
}
