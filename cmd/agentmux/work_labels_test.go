package main

import (
	"reflect"
	"strings"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestWorkAddNameIsOptionalAndKeepsExistingFlags(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		id    string
		flags []string
	}{
		{[]string{"reviewer", "--harness", "claude", "--title", "Review login"}, "reviewer", []string{"--harness", "claude", "--title", "Review login"}},
		{[]string{"--harness", "codex", "--title", "Fix login"}, "", []string{"--harness", "codex", "--title", "Fix login"}},
		{nil, "", nil},
	} {
		id, flags := workAddName(tc.args)
		if id != tc.id || !reflect.DeepEqual(flags, tc.flags) {
			t.Fatalf("name/flag parsing changed: %q %v", id, flags)
		}
	}
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newWorkAgentID("claude")
		if !managedconfig.ValidID(id) || !strings.HasPrefix(id, "claude_") || seen[id] {
			t.Fatalf("generated ID invalid or duplicated: %s", id)
		}
		seen[id] = true
	}
	o, err := parseWorkOptions([]string{"alice", "", "--state", t.TempDir()})
	if err != nil || len(o.args) != 2 || o.args[1] != "" {
		t.Fatalf("CLI cannot clear display title: %+v %v", o, err)
	}
}
