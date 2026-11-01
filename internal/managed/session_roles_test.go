package managed

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLiveRoleCASIsOwnerBoundAndFrozenAtClaim(t *testing.T) {
	cfg := testConfig()
	a := cfg.Agents["bob"]
	a.Role = "original launch role"
	cfg.Agents["bob"] = a
	s := testStore(t, cfg)
	call(t, s, "", "agent.role", map[string]any{"id": "bob", "role": "review current changes", "version": 0})
	info, err := s.Agent("bob")
	if err != nil || info.Role != "review current changes" || s.Config().Agents["bob"].Role != "original launch role" {
		t.Fatalf("live role mutated immutable definition or not applied: %+v %v", info, err)
	}
	var identity struct {
		Role        string `json:"role"`
		RoleVersion int    `json:"role_version"`
	}
	if err := json.Unmarshal(call(t, s, "bob", "identity", map[string]any{}), &identity); err != nil {
		t.Fatal(err)
	}
	if identity.Role != "review current changes" || identity.RoleVersion != 1 {
		t.Fatalf("identity stale: %+v", identity)
	}
	expectFault(t, s, "alice", "agent.role", "forbidden", map[string]any{"id": "bob", "role": "spoof", "version": 1})
	expectFault(t, s, "", "agent.role", "conflict", map[string]any{"id": "bob", "role": "stale edit", "version": 0})
	expectFault(t, s, "", "agent.role", "invalid", map[string]any{"id": "bob", "role": "escape\x1b[2J", "version": 1})
	call(t, s, "alice", "instruct", map[string]any{"to": []string{"bob"}, "text": "bounded task"})
	var claimed struct {
		Role        string `json:"role"`
		RoleVersion int    `json:"role_version"`
		Task        Task   `json:"task"`
	}
	if err := json.Unmarshal(call(t, s, "bob", "task.claim", map[string]any{}), &claimed); err != nil {
		t.Fatal(err)
	}
	call(t, s, "", "agent.role", map[string]any{"id": "bob", "role": "next task only", "version": 1})
	if claimed.Role != "review current changes" || claimed.RoleVersion != 1 || claimed.Task.Text != "bounded task" {
		t.Fatalf("claim role not frozen: %+v", claimed)
	}
	var inbox struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(call(t, s, "bob", "message.list", map[string]any{}), &inbox); err != nil {
		t.Fatal(err)
	}
	notices := 0
	for _, m := range inbox.Messages {
		if strings.Contains(m.Text, "owner updated your role") {
			notices++
			if m.Kind != "note" || m.TaskID != "" {
				t.Fatal("role notice became executable work")
			}
		}
	}
	if notices != 2 {
		t.Fatalf("role notice count %d", notices)
	}
}

func TestNativeOnlyRejectsLiveInjectedRole(t *testing.T) {
	cfg := testConfig()
	a := cfg.Agents["alice"]
	a.Interaction = "interactive"
	a.NativeOnly = true
	cfg.Agents["alice"] = a
	s := testStore(t, cfg)
	expectFault(t, s, "", "agent.role", "invalid", map[string]any{"id": "alice", "role": "inject context", "version": 0})
}
