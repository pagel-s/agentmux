package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
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
