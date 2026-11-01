package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	tea "github.com/charmbracelet/bubbletea"
)

func TestConsoleMemoryReviewReadsRealTombstoneExpiredAndMissingCAS(t *testing.T) {
	state, owner, _ := inventoryBroker(t)
	credential, err := managed.LoadCredential(managed.CredentialPath(state, "peer"))
	if err != nil {
		t.Fatal(err)
	}
	client := managed.NewClient(credential)
	call := func(operation string, args map[string]any) json.RawMessage {
		t.Helper()
		raw, err := workCall(context.Background(), owner, operation, args)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	call("memory.put", map[string]any{"namespace": "team", "key": "deleted", "value": "prior body", "version": 0})
	call("memory.delete", map[string]any{"namespace": "team", "key": "deleted", "version": 1})
	call("memory.put", map[string]any{"namespace": "team", "key": "expired", "value": "expired body", "version": 0, "ttl_seconds": 1})
	time.Sleep(1100 * time.Millisecond)
	for _, tc := range []struct {
		key, state string
		version    int
	}{{"deleted", "deleted", 2}, {"expired", "expired", 1}, {"missing", "missing", 0}} {
		t.Run(tc.state, func(t *testing.T) {
			m := console{client: client, width: 80, height: 24, composer: "memory-value", composeTarget: "team/" + tc.key, memoryNamespace: "team", memoryKey: tc.key, memoryVersion: 0, text: []rune("retained draft")}
			review := m.reviewMemoryVersion()().(consoleMemoryVersionReview)
			if review.err != nil || review.state != tc.state || review.version != tc.version {
				t.Fatalf("review: %+v", review)
			}
			m.receiveMemoryReview(review)
			if m.reader == nil || !strings.Contains(m.reader.body, "No live value ("+tc.state+")") || m.memoryVersion != 0 {
				t.Fatal("review lost state or implicitly adopted")
			}
			m, cmd := consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
			if cmd != nil || m.memoryVersion != tc.version || string(m.text) != "retained draft" {
				t.Fatal("adoption wrote or changed retained draft")
			}
			var head struct {
				Version int    `json:"version"`
				State   string `json:"state"`
			}
			if err := json.Unmarshal(call("memory.head", map[string]any{"namespace": "team", "key": tc.key}), &head); err != nil || head.State != tc.state || head.Version != tc.version {
				t.Fatalf("adoption mutated memory: %+v %v", head, err)
			}
			m, cmd = consolePress(m, tea.KeyMsg{Type: tea.KeyEnter})
			if cmd == nil || !m.busy {
				t.Fatal("editor did not submit the adopted replacement")
			}
			result := cmd().(consoleAction)
			if result.err != nil {
				t.Fatalf("editor save after %s review: %v", tc.state, result.err)
			}
			var saved struct {
				Entry managed.Memory `json:"entry"`
			}
			if err := json.Unmarshal(call("memory.get", map[string]any{"namespace": "team", "key": tc.key}), &saved); err != nil || saved.Entry.Value != "retained draft" || saved.Entry.Version != tc.version+1 {
				t.Fatalf("saved replacement: %+v %v", saved, err)
			}
		})
	}
}

func TestConsoleMemoryLoadsEveryBrokerPageWithoutChangingNamespace(t *testing.T) {
	state, owner, _ := inventoryBroker(t)
	for i := range 107 {
		_, err := workCall(context.Background(), owner, "memory.put", map[string]any{"namespace": "team", "key": fmt.Sprintf("entry-%03d", i), "value": fmt.Sprintf("knowledge %d", i), "version": 0})
		if err != nil {
			t.Fatal(err)
		}
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(state, "peer"))
	if err != nil {
		t.Fatal(err)
	}
	m := console{client: managed.NewClient(credential), agents: []managed.AgentInfo{{ID: "peer"}}}
	result := m.loadMemory("team")().(consoleMemory)
	if result.err != nil || result.namespace != "team" || len(result.entries) != 107 || result.entries[106].Key != "entry-106" {
		t.Fatalf("truncated paginated memory: %d %v", len(result.entries), result.err)
	}
}
