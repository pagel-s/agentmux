package managed

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"agentmux/internal/managedconfig"
)

func taskPairReview(t *testing.T, s *Store, mode string) TaskPairRequest {
	t.Helper()
	var control ControlState
	if err := json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &control); err != nil {
		t.Fatal(err)
	}
	in := TaskPairRequest{Version: control.Version, Mode: mode}
	for _, id := range []string{"alice", "bob"} {
		status, err := s.nativeStatus(context.Background(), s.db, id)
		if err != nil {
			t.Fatal(err)
		}
		in.Agents = append(in.Agents, TaskPairAgent{ID: id, PolicyVersion: status.Policy.Version, NativeSession: status.Session})
	}
	return in
}

func taskPairStore(t *testing.T) *Store {
	cfg := testConfig()
	cfg.Policy = managedconfig.Policy{Default: "deny"}
	for _, id := range []string{"alice", "bob"} {
		a := cfg.Agents[id]
		a.Harness, a.Interaction, a.Command = "pi", "interactive", nil
		a.NativeDelivery = &managedconfig.NativeDelivery{Mode: "manual", MaxTurns: 7, MaxHops: 2, CooldownSeconds: 3, Notes: true}
		cfg.Agents[id] = a
	}
	s := testStore(t, cfg)
	nativeRegister(t, s, "alice", "alice-native")
	nativeRegister(t, s, "bob", "bob-native")
	return s
}

func TestTaskPairIsAtomicIdempotentAndTaskScoped(t *testing.T) {
	s := taskPairStore(t)
	before := s.Config()
	in := taskPairReview(t, s, "auto")
	expectFault(t, s, "alice", "control.pair", "forbidden", in)
	expectFault(t, s, "alice", "instruct", "forbidden", map[string]any{"to": []string{"bob"}, "text": "not yet"})
	one, problem := invoke(s, credential(t, s, ""), "control.pair", in, "exact-pair")
	if problem != nil {
		t.Fatal(problem)
	}
	two, problem := invoke(s, credential(t, s, ""), "control.pair", in, "exact-pair")
	if problem != nil || string(one) != string(two) {
		t.Fatalf("pair receipt did not replay exactly: %s %s %v", one, two, problem)
	}
	var pairEvents int
	if err := s.db.QueryRow("SELECT count(*) FROM records WHERE kind='event' AND json_extract(data,'$.type')='control.pair'").Scan(&pairEvents); err != nil || pairEvents != 1 {
		t.Fatalf("pair audit duplicated or omitted the owner action: %d %v", pairEvents, err)
	}
	for i, id := range []string{"alice", "bob"} {
		other := []string{"bob", "alice"}[i]
		call(t, s, id, "instruct", map[string]any{"to": []string{other}, "text": "Allowed task"})
		expectFault(t, s, id, "message.send", "forbidden", map[string]any{"to": []string{other}, "text": "No note grant"})
		expectFault(t, s, id, "ping.send", "forbidden", map[string]any{"to": []string{other}, "text": "No ping grant"})
		expectFault(t, s, id, "instruct", "forbidden", map[string]any{"to": []string{"eve"}, "text": "No third-party grant"})
		status, err := s.nativeStatus(context.Background(), s.db, id)
		if err != nil || status.Policy.Mode != "auto" || !status.Policy.Results || status.Policy.MaxTurns != 7 || status.Policy.MaxHops != 2 || status.Policy.CooldownSeconds != 3 || !status.Policy.Notes || status.Policy.Pings || status.Policy.Boards || status.Policy.MemoryUpdates || status.Turns != 0 {
			t.Fatalf("pair changed unrelated delivery state: %+v %v", status, err)
		}
	}
	control, err := controls(context.Background(), s.db)
	if err != nil || len(control.Edges) != 2 || len(control.Scopes) != 0 || len(control.Independent) != 0 || len(control.Groups) != 0 {
		t.Fatalf("pair broadened shared scopes: %+v %v", control, err)
	}
	groups, err := workspaceGroups(context.Background(), s.db)
	if err != nil || len(groups) != 0 || !reflect.DeepEqual(before, s.Config()) {
		t.Fatal("pair changed groups or launch configuration")
	}
	unchanged := call(t, s, "", "control.get", map[string]any{})
	call(t, s, "", "control.pair", taskPairReview(t, s, "auto"))
	if string(unchanged) != string(call(t, s, "", "control.get", map[string]any{})) {
		t.Fatal("reapplying the same pair generated a new boundary revision")
	}
	for _, id := range []string{"alice", "bob"} {
		p, err := nativePolicy(context.Background(), s.db, id, s.cfg.Agents[id].NativeDelivery)
		if err != nil || p.Version != 2 {
			t.Fatal("reapplying an unchanged pair invalidated native approvals")
		}
	}
	in.Mode = "review"
	_, problem = invoke(s, credential(t, s, ""), "control.pair", in, "exact-pair")
	if problem == nil || problem.Code != "conflict" {
		t.Fatalf("receipt accepted different pair arguments: %v", problem)
	}
}

func TestTaskPairRejectsRegisteredAdaptersWithoutTaskAndResultTransport(t *testing.T) {
	for _, missing := range []string{"instructions", "observations", "completion"} {
		t.Run(missing, func(t *testing.T) {
			s := taskPairStore(t)
			caps := map[string]bool{"instructions": true, "observations": true, "completion": true}
			caps[missing] = false
			call(t, s, "bob", "native.register", map[string]any{"adapter": "pi", "session": "bob-native", "capabilities": caps})
			in := taskPairReview(t, s, "auto")
			before := call(t, s, "", "control.get", map[string]any{})
			expectFault(t, s, "", "control.pair", "unsupported", in)
			if string(before) != string(call(t, s, "", "control.get", map[string]any{})) {
				t.Fatal("unsupported adapter left partial grants")
			}
		})
	}
}

func TestTaskPairRollsBackWhenLastPolicyWriteFails(t *testing.T) {
	s := taskPairStore(t)
	in := taskPairReview(t, s, "auto")
	before := call(t, s, "", "control.get", map[string]any{})
	var recordsBefore int
	if err := s.db.QueryRow("SELECT count(*) FROM records").Scan(&recordsBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER reject_pair_policy BEFORE INSERT ON records WHEN NEW.kind='native_policy' AND NEW.target='bob' BEGIN SELECT RAISE(ABORT, 'fixture policy failure'); END"); err != nil {
		t.Fatal(err)
	}
	expectFault(t, s, "", "control.pair", "internal", in)
	after := call(t, s, "", "control.get", map[string]any{})
	var recordsAfter int
	if err := s.db.QueryRow("SELECT count(*) FROM records").Scan(&recordsAfter); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || recordsBefore != recordsAfter {
		t.Fatal("failed pair committed partial permissions, policy or events")
	}
	for _, id := range []string{"alice", "bob"} {
		p, err := nativePolicy(context.Background(), s.db, id, s.cfg.Agents[id].NativeDelivery)
		if err != nil || p.Version != 1 || p.Mode != "manual" || p.Results {
			t.Fatalf("failed pair retained native mode/results: %+v %v", p, err)
		}
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_pair_policy"); err != nil {
		t.Fatal(err)
	}
	call(t, s, "", "control.pair", in)
}

func TestTaskPairRejectsStaleReviewBeforeAnyGrant(t *testing.T) {
	for _, change := range []string{"permissions", "policy", "conversation", "isolation"} {
		t.Run(change, func(t *testing.T) {
			s := taskPairStore(t)
			in := taskPairReview(t, s, "review")
			switch change {
			case "permissions":
				call(t, s, "", "control.update", map[string]any{"version": in.Version, "kind": "edge", "from": "alice", "to": "eve", "action": "ping.send", "allow": true})
			case "policy":
				nativeMode(t, s, "bob", "review", nil)
			case "conversation":
				nativeRegister(t, s, "bob", "changed-native")
			case "isolation":
				call(t, s, "", "control.update", map[string]any{"version": in.Version, "kind": "independent", "agents": []string{"bob"}})
			}
			before := call(t, s, "", "control.get", map[string]any{})
			expectFault(t, s, "", "control.pair", "conflict", in)
			if string(before) != string(call(t, s, "", "control.get", map[string]any{})) {
				t.Fatal("stale pair changed access")
			}
			expectFault(t, s, "bob", "instruct", "forbidden", map[string]any{"to": []string{"alice"}, "text": "still denied"})
			if change == "isolation" {
				expectFault(t, s, "", "control.pair", "invalid", taskPairReview(t, s, "auto"))
			}
		})
	}
}

func TestTaskPairEligibilityRejectsUnsupportedAndIsolationWithoutChangingScope(t *testing.T) {
	base := []AgentInfo{{ID: "alice", State: "active", Config: managedconfig.Agent{Harness: "codex", Interaction: "interactive"}}, {ID: "bob", State: "active", Config: managedconfig.Agent{Harness: "claude", Interaction: "interactive"}}}
	for _, change := range []string{"external", "native-only", "managed", "unsupported", "delegated", "isolated-edge"} {
		t.Run(change, func(t *testing.T) {
			agents := append([]AgentInfo(nil), base...)
			control := ControlState{Version: 1}
			switch change {
			case "external":
				agents[1].External = true
			case "native-only":
				agents[1].Config.NativeOnly = true
			case "managed":
				agents[1].Config.Interaction = "managed"
			case "unsupported":
				agents[1].Config.Harness = "antigravity"
			case "delegated":
				agents[1].Parent = "alice"
			case "isolated-edge":
				control.Edges = []EdgeControl{{From: "alice", To: "bob", Action: "instruct", Source: "isolation"}}
			}
			if ValidateTaskPairAgents(agents, control) == nil {
				t.Fatal("unsafe session pair was accepted")
			}
		})
	}
}
