package managed

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestLegacyWorkspaceGroupWithoutOverrideDoesNotExplainADeniedEdgeAsGranted(t *testing.T) {
	s, _, c := legacyGroupState(t)
	call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "edge", "from": "alice", "to": "bob", "action": "message.send", "reset": true})
	if s.can(context.Background(), s.db, "alice", "message.send", "bob") {
		t.Fatal("unreviewed group contributed a new grant")
	}
	reason := s.boundaryEdgeReason(context.Background(), s.db, "alice", "message.send", "bob")
	if strings.HasPrefix(reason, "collaboration group ") {
		t.Fatal("denied legacy edge explained as a group grant: " + reason)
	}
}

func legacyGroupState(t *testing.T) (*Store, WorkspaceGroup, ControlState) {
	t.Helper()
	s := workspaceGroupStore(t)
	c := connectWorkspaceGroup(t, s, 1, "alice", "bob")
	g := *c.Group
	g.ContributionsVersion = 0
	if err := save(context.Background(), s.db, "workspace_group", workspaceGroupKey(g.Agents), ownerID, "", "owner", g); err != nil {
		t.Fatal(err)
	}
	c.Group = nil
	for _, pair := range [][2]string{{"alice", "bob"}, {"bob", "alice"}} {
		for _, action := range []string{"message.send", "ping.send"} {
			c.Edges = append(c.Edges, EdgeControl{From: pair[0], To: pair[1], Action: action, Allow: true})
		}
	}
	c.Edges = append(c.Edges, EdgeControl{From: "alice", To: "bob", Action: "instruct", Allow: false}, EdgeControl{From: "alice", To: "eve", Action: "message.send", Allow: true})
	c.Scopes = append(c.Scopes, ScopeControl{Agent: "alice", Kind: "memory", Namespace: g.Memory, Read: true, Write: true})
	if err := saveControls(context.Background(), s.db, c); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	return reopened, g, c
}
func TestLegacyWorkspaceGroupEditRequiresExplicitAtomicOwnerReview(t *testing.T) {
	s, g, c := legacyGroupState(t)
	beforeEvents, _ := count(context.Background(), s.db, "event")
	for _, policy := range []string{"", "guess"} {
		code := "conflict"
		if policy != "" {
			code = "invalid"
		}
		expectFault(t, s, "", "control.update", code, map[string]any{"version": c.Version, "kind": "collaboration", "agents": g.Agents, "enabled": false, "legacy_policy": policy})
	}
	current, err := readControls(context.Background(), s.db)
	if err != nil || !reflect.DeepEqual(c, current) {
		t.Fatal("unreviewed legacy edit changed controls")
	}
	var saved WorkspaceGroup
	if err = get(context.Background(), s.db, "workspace_group", workspaceGroupKey(g.Agents), &saved); err != nil || !reflect.DeepEqual(g, saved) {
		t.Fatal("unreviewed legacy edit changed membership")
	}
	if after, _ := count(context.Background(), s.db, "event"); after != beforeEvents {
		t.Fatal("rejected legacy edit wrote events")
	}
	if !s.can(context.Background(), s.db, "alice", "message.send", "bob") {
		t.Fatal("read-only old state lost existing rights")
	}
	expectFault(t, s, "alice", "control.update", "forbidden", map[string]any{"version": c.Version, "kind": "collaboration", "agents": g.Agents, "enabled": false, "legacy_policy": "group"})
}
func TestLegacyWorkspaceGroupMigrationRemovesOnlyReviewedPairAllows(t *testing.T) {
	s, g, c := legacyGroupState(t)
	raw := call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "collaboration", "agents": g.Agents, "enabled": false, "legacy_policy": "group"})
	var result ControlState
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Group == nil || result.Group.ContributionsVersion != 1 || result.Group.State != "inactive" {
		t.Fatal("migration marker/state missing")
	}
	for _, action := range []string{"message.send", "ping.send", "instruct"} {
		if s.can(context.Background(), s.db, "alice", action, "bob") {
			t.Fatal("disabled migrated group retained pairallow")
		}
	}
	if len(result.Edges) != 2 || !reflect.DeepEqual(result.Scopes, c.Scopes) || !s.can(context.Background(), s.db, "alice", "message.send", "eve") {
		t.Fatal("migration changed denies,scopes or unrelated controls")
	}
	connectWorkspaceGroup(t, s, result.Version, "alice", "bob")
	expectFault(t, s, "alice", "instruct", "forbidden", map[string]any{"to": []string{"bob"}, "text": "explicitdenyremains"})
}
func TestLegacyWorkspaceGroupPreserveChoiceAndNewOverlapRemainIndependent(t *testing.T) {
	for _, policy := range []string{"preserve", "group"} {
		t.Run(policy, func(t *testing.T) {
			s, g, c := legacyGroupState(t)
			overlap := connectWorkspaceGroup(t, s, c.Version, "alice", "bob", "eve")
			raw := call(t, s, "", "control.update", map[string]any{"version": overlap.Version, "kind": "collaboration", "agents": g.Agents, "enabled": false, "legacy_policy": policy})
			var result ControlState
			json.Unmarshal(raw, &result)
			if !s.can(context.Background(), s.db, "alice", "message.send", "bob") {
				t.Fatal("migration disabled othernewgroupgrant")
			}
			call(t, s, "", "control.update", map[string]any{"version": result.Version, "kind": "collaboration", "agents": []string{"alice", "bob", "eve"}, "enabled": false})
			if got := s.can(context.Background(), s.db, "alice", "message.send", "bob"); got != (policy == "preserve") {
				t.Fatalf("policy%s wrongdirectoverride%s", policy, string(raw))
			}
		})
	}
}
func TestLegacyWorkspaceGroupMigrationRollbackAndReceipt(t *testing.T) {
	s, g, c := legacyGroupState(t)
	s.cfg.Limits.MaxRecords = 1
	expectFault(t, s, "", "control.update", "limit", map[string]any{"version": c.Version, "kind": "collaboration", "agents": g.Agents, "enabled": false, "legacy_policy": "group"})
	current, _ := readControls(context.Background(), s.db)
	if !reflect.DeepEqual(c, current) {
		t.Fatal("failed migration changed controlintent")
	}
	var saved WorkspaceGroup
	get(context.Background(), s.db, "workspace_group", workspaceGroupKey(g.Agents), &saved)
	if saved.ContributionsVersion != 0 || saved.State != "active" {
		t.Fatal("failedmigration markedreviewed")
	}
	s.cfg.Limits.MaxRecords = 10000
	args := map[string]any{"version": c.Version, "kind": "collaboration", "agents": g.Agents, "enabled": false, "legacy_policy": "group"}
	credential := credential(t, s, "")
	first, p := invoke(s, credential, "control.update", args, "explicit-legacy-choice")
	if p != nil {
		t.Fatal(p)
	}
	again, p := invoke(s, credential, "control.update", args, "explicit-legacy-choice")
	if p != nil || string(first) != string(again) {
		t.Fatal("migrationreceiptunstable")
	}
}
