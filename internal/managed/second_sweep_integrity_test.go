package managed

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDisableSavedGroupAfterOriginalMemberRevokedOrRetired(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(fmt.Sprintf("retired=%t", retired), func(t *testing.T) {
			s := workspaceGroupStore(t)
			group := connectWorkspaceGroup(t, s, 1, "alice", "bob", "eve").Group
			other := connectWorkspaceGroup(t, s, 2, "bob", "dave").Group
			call(t, s, "", "control.update", map[string]any{"version": 3, "kind": "edge", "from": "bob", "to": "eve", "action": "instruct", "allow": false})
			call(t, s, "", "agent.stop", map[string]any{"id": "alice"})
			if retired {
				call(t, s, "", "agent.retire", map[string]any{"agent": "alice"})
			}
			var controls ControlState
			json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &controls)
			args := map[string]any{"version": controls.Version, "kind": "collaboration", "agents": group.Agents, "enabled": false}
			stale := map[string]any{"version": controls.Version - 1, "kind": "collaboration", "agents": group.Agents, "enabled": false}
			expectFault(t, s, "", "control.update", "conflict", stale)
			expectFault(t, s, "bob", "control.update", "forbidden", args)
			expectFault(t, s, "", "control.update", "not_found", map[string]any{"version": controls.Version, "kind": "collaboration", "agents": []string{"bob", "eve"}, "enabled": false})
			var result ControlState
			json.Unmarshal(call(t, s, "", "control.update", args), &result)
			if result.Group == nil || result.Group.ID != group.ID || result.Group.State != "inactive" || len(result.Group.Members) != 0 || result.Group.Board != group.Board || result.Group.Memory != group.Memory {
				t.Fatalf("disable changed saved group identity or left grants: %+v", result.Group)
			}
			expectFault(t, s, "bob", "message.send", "forbidden", map[string]any{"to": []string{"eve"}, "text": "disabled group no longer grants"})
			expectFault(t, s, "bob", "board.post", "forbidden", map[string]any{"board": group.Board, "text": "disabled board"})
			call(t, s, "bob", "board.post", map[string]any{"board": other.Board, "text": "unrelated group remains active"})
			call(t, s, "bob", "message.send", map[string]any{"to": []string{"dave"}, "text": "unrelated grant preserved"})
			if len(result.Edges) != 1 || result.Edges[0].Action != "instruct" || result.Edges[0].Allow {
				t.Fatalf("disabling contribution removed owner override: %+v", result.Edges)
			}
			expectFault(t, s, "", "control.update", "invalid", map[string]any{"version": result.Version, "kind": "collaboration", "agents": group.Agents, "enabled": true})
		})
	}
}

func TestExpiredTaskOwnerRecoveryProjectionMatchesResolutionAndSummary(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%t", cancelled), func(t *testing.T) {
			s := testStore(t, testConfig())
			instruction(t, s)
			task, lease := claim(t, s)
			if cancelled {
				task = taskIn(t, call(t, s, "", "task.cancel", map[string]any{"id": task.ID, "version": task.Version}))
			}
			record, err := loadTask(context.Background(), s.db, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			record.Expires = time.Now().Add(-time.Minute).Unix()
			if err = saveTask(context.Background(), s.db, record); err != nil {
				t.Fatal(err)
			}
			var requestsBefore, recordsBefore int
			s.db.QueryRow("SELECT count(*) FROM requests").Scan(&requestsBefore)
			s.db.QueryRow("SELECT count(*) FROM records").Scan(&recordsBefore)
			raw := call(t, s, "", "task.get", map[string]any{"id": task.ID})
			view := taskIn(t, raw)
			wantDelivery := "uncertain"
			wantActions := []string{"fail", "retry"}
			if cancelled {
				wantDelivery = "cancel_uncertain"
				wantActions = []string{"fail"}
			}
			if view.Recovery == nil || view.Recovery.Delivery != wantDelivery || !reflect.DeepEqual(view.Recovery.Actions, wantActions) || !strings.Contains(view.Recovery.Reason, "expired") || view.Version != record.Version || view.State != record.State || view.Delivery != record.Delivery {
				t.Fatalf("owner recovery projection is inconsistent: %+v", view)
			}
			for _, secret := range []string{lease, "lease_token", "lease_expires", "expires", "instance"} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("public recovery exposed lease details %q: %s", secret, raw)
				}
			}
			for _, actor := range []string{"alice", "bob"} {
				if got := taskIn(t, call(t, s, actor, "task.get", map[string]any{"id": task.ID})); got.Recovery != nil {
					t.Fatalf("owner-only recovery exposed to %s", actor)
				}
			}
			for _, op := range []string{"task.list", "activity"} {
				var page struct {
					Tasks []Task `json:"tasks"`
				}
				json.Unmarshal(call(t, s, "", op, map[string]any{}), &page)
				if len(page.Tasks) != 1 || !reflect.DeepEqual(page.Tasks[0].Recovery, view.Recovery) {
					t.Fatalf("%s recovery differs from task.get: %+v", op, page.Tasks)
				}
			}
			var summary struct {
				Agents []TaskSummary `json:"agents"`
			}
			json.Unmarshal(call(t, s, "", "task.summary", map[string]any{"agent": "bob"}), &summary)
			if len(summary.Agents) != 1 || summary.Agents[0].Uncertain != 1 || summary.Agents[0].Running != 0 || summary.Agents[0].RecoveryEligible != 1 || summary.Agents[0].Deliveries[record.Delivery] != 1 || summary.Agents[0].EffectiveDeliveries[wantDelivery] != 1 {
				t.Fatalf("summary hid expired execution hold: %+v", summary.Agents)
			}
			stored, err := loadTask(context.Background(), s.db, task.ID)
			if err != nil || stored.Version != record.Version || stored.Delivery != record.Delivery || stored.State != record.State || stored.Expires != record.Expires || stored.Recovery != nil {
				t.Fatalf("read projection rewrote execution history: %+v %v", stored, err)
			}
			var requestsAfter, recordsAfter int
			s.db.QueryRow("SELECT count(*) FROM requests").Scan(&requestsAfter)
			s.db.QueryRow("SELECT count(*) FROM records").Scan(&recordsAfter)
			if requestsAfter != requestsBefore || recordsAfter != recordsBefore {
				t.Fatal("owner recovery reads consumed durable mutations")
			}
			expectFault(t, s, "", "task.resolve", "conflict", map[string]any{"id": task.ID, "version": view.Version - 1, "resolution": "fail"})
			resolution := "retry"
			if cancelled {
				expectFault(t, s, "", "task.resolve", "conflict", map[string]any{"id": task.ID, "version": view.Version, "resolution": "retry"})
				resolution = "fail"
			}
			resolved := taskIn(t, call(t, s, "", "task.resolve", map[string]any{"id": task.ID, "version": view.Version, "resolution": resolution}))
			if resolved.Recovery != nil || resolved.Version != view.Version+1 {
				t.Fatalf("resolved task retained recovery authority: %+v", resolved)
			}
			if cancelled && (resolved.State != "cancelled" || resolved.Delivery != "stopped") || !cancelled && (resolved.State != "proposed" || resolved.Delivery != "pending") {
				t.Fatalf("resolution eligibility differed from backend guard: %+v", resolved)
			}
			expectFault(t, s, "bob", "task.complete", "conflict", map[string]any{"id": task.ID, "version": resolved.Version, "lease": lease, "result": "old worker must stay fenced"})
		})
	}
}

func TestTaskRecoveryExpiryBoundaryAndActiveAttemptAreReadOnly(t *testing.T) {
	at := time.Unix(100, 0)
	for _, delivery := range []string{"dispatching", "cancel_requested"} {
		state := "running"
		if delivery == "cancel_requested" {
			state = "cancelled"
		}
		record := taskRecord{Task: Task{State: state, Delivery: delivery, Version: 3}, Expires: 100}
		if got := taskView(record, actor{ID: ownerID}, at); got.Recovery != nil {
			t.Fatalf("lease expiring at current Unix second was prematurely uncertain: %+v", got)
		}
		if got := taskView(record, actor{ID: ownerID}, at.Add(time.Second)); got.Recovery == nil {
			t.Fatal("expired attempt omitted recovery")
		}
		if record.Delivery != delivery || record.Version != 3 || record.Recovery != nil {
			t.Fatal("projection modified input")
		}
	}
}

func TestTaskRecoveryHonorsExpiredRuntimeRetryRestriction(t *testing.T) {
	s := testStore(t, testConfig())
	instruction(t, s)
	task, _ := claim(t, s)
	record, err := loadTask(context.Background(), s.db, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.Expires = time.Now().Add(-time.Minute).Unix()
	if err = saveTask(context.Background(), s.db, record); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("INSERT INTO metadata(key,value) VALUES('deadline',?)", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	view := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": task.ID}))
	if view.Recovery == nil || !reflect.DeepEqual(view.Recovery.Actions, []string{"fail"}) || !strings.Contains(view.Recovery.Reason, "runtime budget") {
		t.Fatalf("expired runtime advertised an impossible retry: %+v", view.Recovery)
	}
	expectFault(t, s, "", "task.resolve", "limit", map[string]any{"id": task.ID, "version": view.Version, "resolution": "retry"})
	call(t, s, "", "task.resolve", map[string]any{"id": task.ID, "version": view.Version, "resolution": "fail"})
}

func TestTaskSummaryCountsOldExpiredHoldsOutsideRecentActivity(t *testing.T) {
	s := testStore(t, testConfig())
	ctx := context.Background()
	expired := time.Now().Add(-time.Minute).Unix()
	active := time.Now().Add(time.Hour).Unix()
	for i := 0; i < 254; i++ {
		state, delivery, expiry := "verified", "acknowledged", int64(0)
		switch i {
		case 0:
			state, delivery, expiry = "running", "dispatching", expired
		case 1:
			state, delivery, expiry = "cancelled", "cancel_requested", expired
		case 252:
			state, delivery, expiry = "running", "dispatching", active
		case 253:
			state, delivery, expiry = "cancelled", "cancel_requested", active
		}
		if err := saveTask(ctx, s.db, taskRecord{Task: Task{ID: fmt.Sprintf("old-hold-%03d", i), From: "alice", To: "bob", State: state, Delivery: delivery, Version: 1}, Expires: expiry}); err != nil {
			t.Fatal(err)
		}
	}
	var page struct {
		Agents []TaskSummary `json:"agents"`
	}
	json.Unmarshal(call(t, s, "", "task.summary", map[string]any{"limit": 1}), &page)
	if len(page.Agents) != 1 || page.Agents[0].Total != 254 || page.Agents[0].Uncertain != 2 || page.Agents[0].RecoveryEligible != 2 || page.Agents[0].Running != 2 || page.Agents[0].Deliveries["dispatching"] != 2 || page.Agents[0].EffectiveDeliveries["dispatching"] != 1 {
		t.Fatalf("summary filtered away old expired holds: %+v", page.Agents)
	}
}

func TestIsolationTeamImpactsCanonicalAndOwnerDissolutionTakesPrecedence(t *testing.T) {
	teams := []Team{
		{ID: "team-c", Version: 2, State: "active", Owner: "dave", Invited: []string{"bob"}},
		{ID: "team-b", Version: 3, State: "active", Owner: "dave", Members: []string{"bob"}, Invited: []string{"alice"}},
		{ID: "team-a", Version: 4, State: "active", Owner: "alice", Members: []string{"alice", "bob"}, Invited: []string{"bob"}},
		{ID: "team-old", Version: 5, State: "dissolved", Owner: "alice"},
	}
	got := IsolationTeamImpacts(teams, []string{"bob", "alice"})
	want := []IsolationTeamImpact{{ID: "team-a", Version: 4, Agent: "alice", Action: "dissolve"}, {ID: "team-b", Version: 3, Agent: "alice", Action: "withdraw_invitation"}, {ID: "team-b", Version: 3, Agent: "bob", Action: "remove_member"}, {ID: "team-c", Version: 2, Agent: "bob", Action: "withdraw_invitation"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("review does not describe actual permanent team edits: %+v", got)
	}
	if !reflect.DeepEqual(teams[2].Members, []string{"alice", "bob"}) {
		t.Fatal("pure impact inspection mutated team input")
	}
	if empty := IsolationTeamImpacts(teams, []string{"unrelated"}); empty == nil || len(empty) != 0 {
		t.Fatal("empty impact review should serialize as []")
	}
}

func TestIsolationTeamImpactFenceRejectsNewTeamAndChangedReviewBeforeMutation(t *testing.T) {
	s := testStore(t, dynamicConfig())
	team := teamIn(t, call(t, s, "alice", "team.create", map[string]any{"name": "newly appeared owned team"}))
	current := func() ControlState {
		t.Helper()
		var c ControlState
		json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &c)
		return c
	}
	apply := func(expected []IsolationTeamImpact) {
		t.Helper()
		expectFault(t, s, "", "control.update", "conflict", map[string]any{"version": current().Version, "kind": "independent", "agents": []string{"alice"}, "expected_teams": expected})
		if after := ownerTeamAccess(t, s, team.ID); after.State != "active" {
			t.Fatal("stale review partially dissolved team")
		}
		if contains(current().Independent, "alice") {
			t.Fatal("stale review partially isolated agent")
		}
	}
	// Even with the newest general boundary revision, an explicitly empty
	// permanent-impact review cannot authorize a newly discovered team.
	apply([]IsolationTeamImpact{})
	old := IsolationTeamImpacts([]Team{team}, []string{"alice"})
	team = teamIn(t, call(t, s, "alice", "team.update", map[string]any{"id": team.ID, "version": team.Version, "name": "changed after review", "description": "new context"}))
	apply(old)
	wrong := IsolationTeamImpacts([]Team{team}, []string{"alice"})
	wrong[0].Action = "remove_member"
	apply(wrong)
	exact := IsolationTeamImpacts([]Team{team}, []string{"alice"})
	apply(append(append([]IsolationTeamImpact{}, exact...), exact...))
	call(t, s, "", "control.update", map[string]any{"version": current().Version, "kind": "independent", "agents": []string{"alice"}, "expected_teams": exact})
	if after := ownerTeamAccess(t, s, team.ID); after.State != "dissolved" {
		t.Fatal("reviewed permanent team impact was not applied")
	}
}

func TestIsolationTeamImpactFenceMatchesOwnedMemberAndInvitationEdits(t *testing.T) {
	s := testStore(t, dynamicConfig())
	owned := teamIn(t, call(t, s, "alice", "team.create", map[string]any{"name": "owned"}))
	member := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "member"}))
	member = addTeamMember(t, s, member, "alice")
	invited := teamIn(t, call(t, s, "", "team.create", map[string]any{"name": "invited"}))
	invited = teamIn(t, call(t, s, "", "team.invite", map[string]any{"id": invited.ID, "version": invited.Version, "agent": "alice"}))
	impacts := IsolationTeamImpacts([]Team{owned, member, invited}, []string{"alice"})
	if len(impacts) != 3 {
		t.Fatalf("missing permanent impact: %+v", impacts)
	}
	// Caller ordering is not authority: compare the same exact canonical set.
	impacts[0], impacts[2] = impacts[2], impacts[0]
	var c ControlState
	json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &c)
	call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "independent", "agents": []string{"alice"}, "expected_teams": impacts})
	if after := ownerTeamAccess(t, s, owned.ID); after.State != "dissolved" {
		t.Fatal("owner team not dissolved")
	}
	if after := ownerTeamAccess(t, s, member.ID); after.State != "active" || contains(after.Members, "alice") {
		t.Fatal("selected member not removed exactly")
	}
	if after := ownerTeamAccess(t, s, invited.ID); after.State != "active" || contains(after.Invited, "alice") {
		t.Fatal("selected invitation not withdrawn exactly")
	}
}

func TestIsolationExplicitEmptyReviewAndOmittedLegacyReviewRemainDistinct(t *testing.T) {
	s := testStore(t, dynamicConfig())
	call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "independent", "agents": []string{"bob"}, "expected_teams": []IsolationTeamImpact{}})
	owned := teamIn(t, call(t, s, "alice", "team.create", map[string]any{"name": "legacy caller"}))
	var c ControlState
	json.Unmarshal(call(t, s, "", "control.get", map[string]any{}), &c)
	call(t, s, "", "control.update", map[string]any{"version": c.Version, "kind": "independent", "agents": []string{"alice"}})
	if after := ownerTeamAccess(t, s, owned.ID); after.State != "dissolved" {
		t.Fatal("omitted legacy fence changed established API behavior")
	}
}
