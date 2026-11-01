package managed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"agentmux/internal/managedconfig"
)

type messageHistoryPage struct {
	Messages []Message `json:"messages"`
	Cursor   string    `json:"cursor"`
	HasMore  bool      `json:"has_more"`
}

func TestScopedMessageHistoryFiltersBeforePagingAndUsesStableIDs(t *testing.T) {
	s := testStore(t, testConfig())
	for i := 0; i < 12; i++ {
		call(t, s, "", "message.send", map[string]any{"to": []string{"bob"}, "text": fmt.Sprintf("needle inbox %02d", i)})
		call(t, s, "", "message.send", map[string]any{"to": []string{"eve"}, "text": "unrelated inbox"})
		call(t, s, "", "board.post", map[string]any{"board": "team", "text": fmt.Sprintf("needle board %02d", i)})
		call(t, s, "", "board.post", map[string]any{"board": "public", "text": "unrelated board"})
	}
	args := map[string]any{"scope": "inbox", "agent": "bob", "query": "needle", "order": "desc", "limit": 2}
	var first messageHistoryPage
	if err := json.Unmarshal(call(t, s, "", "message.list", args), &first); err != nil || len(first.Messages) != 2 || !first.HasMore || !strings.HasSuffix(first.Messages[0].Text, "11") || first.Cursor != first.Messages[1].ID {
		t.Fatalf("wrong filtered inbox: %+v %v", first, err)
	}
	call(t, s, "", "message.send", map[string]any{"to": []string{"bob"}, "text": "needle arrived between pages"})
	args["after"] = first.Cursor
	seen := map[string]bool{}
	for _, m := range first.Messages {
		seen[m.ID] = true
	}
	total := len(first.Messages)
	for {
		var page messageHistoryPage
		json.Unmarshal(call(t, s, "", "message.list", args), &page)
		for _, m := range page.Messages {
			if seen[m.ID] || !strings.HasPrefix(m.Text, "needle inbox") || !contains(m.To, "bob") {
				t.Fatalf("unstable or unscoped page: %+v", m)
			}
			seen[m.ID] = true
			total++
		}
		if !page.HasMore {
			break
		}
		if page.Cursor == args["after"] {
			t.Fatal("cursor did not advance")
		}
		args["after"] = page.Cursor
	}
	if total != 12 {
		t.Fatalf("history page skipped matching old messages: %d", total)
	}
	var board messageHistoryPage
	json.Unmarshal(call(t, s, "", "board.list", map[string]any{"board": "team", "query": "needle", "order": "desc", "limit": 2}), &board)
	if len(board.Messages) != 2 || board.Messages[0].Board != "team" || !board.HasMore {
		t.Fatal(board)
	}
	var inbox messageHistoryPage
	json.Unmarshal(call(t, s, "bob", "message.list", map[string]any{"scope": "inbox", "order": "desc", "limit": 100}), &inbox)
	for _, m := range inbox.Messages {
		if m.Board != "" || !contains(m.To, "bob") {
			t.Fatalf("inbox contains other scope: %+v", m)
		}
	}
	expectFault(t, s, "eve", "message.list", "forbidden", map[string]any{"agent": "bob", "scope": "inbox"})
	expectFault(t, s, "eve", "message.list", "invalid", map[string]any{"after": first.Cursor})
}

func TestOwnerEventQueriesAreReadOnlyFilteredAndPaged(t *testing.T) {
	s := testStore(t, testConfig())
	for i := 0; i < 8; i++ {
		call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": fmt.Sprintf("event %d", i)})
		call(t, s, "eve", "board.post", map[string]any{"board": "public", "text": "noise"})
	}
	var beforeRecords, beforeRequests int
	s.db.QueryRow("SELECT count(*) FROM records").Scan(&beforeRecords)
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&beforeRequests)
	type eventPage struct {
		Events  []Event `json:"events"`
		Cursor  string  `json:"cursor"`
		HasMore bool    `json:"has_more"`
	}
	args := map[string]any{"agent": "bob", "actor": "alice", "type": "message.send", "limit": 2, "query": "message.send"}
	ids := map[string]bool{}
	total := 0
	for {
		var page eventPage
		if err := json.Unmarshal(call(t, s, "", "events", args), &page); err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Events {
			if ids[e.ID] || e.Actor != "alice" || e.Type != "message.send" {
				t.Fatal(e)
			}
			ids[e.ID] = true
			total++
		}
		if !page.HasMore {
			break
		}
		args["after"] = page.Cursor
	}
	if total != 8 {
		t.Fatalf("event filters hid matching history: %d", total)
	}
	expectFault(t, s, "alice", "events.list", "forbidden", map[string]any{})
	expectFault(t, s, "", "event.list", "invalid", map[string]any{"after": "msg_not-an-event"})
	var afterRecords, afterRequests int
	s.db.QueryRow("SELECT count(*) FROM records").Scan(&afterRecords)
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&afterRequests)
	if beforeRecords != afterRecords || beforeRequests != afterRequests {
		t.Fatal("event queries mutated domain history")
	}
}

func TestExposureLedgerAuthenticatesReadersVersionsDigestsAndRoutes(t *testing.T) {
	s := testStore(t, testConfig())
	memoryIn(t, call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "fact", "value": "sensitive memory value"}))
	var sent struct {
		Message Message `json:"message"`
	}
	json.Unmarshal(call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "sensitive inbox value"}), &sent)
	countRows := func() int {
		var n int
		if err := s.db.QueryRow("SELECT count(*) FROM exposures").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	call(t, s, "", "memory.get", map[string]any{"namespace": "team", "key": "fact"})
	call(t, s, "", "message.get", map[string]any{"id": sent.Message.ID})
	expectFault(t, s, "eve", "memory.get", "forbidden", map[string]any{"namespace": "team", "key": "fact"})
	expectFault(t, s, "eve", "message.get", "not_found", map[string]any{"id": sent.Message.ID})
	call(t, s, "bob", "memory.head", map[string]any{"namespace": "team", "key": "fact"})
	if countRows() != 0 {
		t.Fatal("owner, forbidden or metadata-only reads counted as exposure")
	}
	call(t, s, "bob", "memory.get", map[string]any{"namespace": "team", "key": "fact"})
	call(t, s, "bob", "message.list", map[string]any{"scope": "inbox"})
	call(t, s, "bob", "memory.search", map[string]any{"namespace": "team", "query": "sensitive"})
	if countRows() != 3 {
		t.Fatalf("read routes not recorded: %d", countRows())
	}
	var response struct {
		Exposures []Exposure `json:"exposures"`
	}
	raw := call(t, s, "", "exposure.list", map[string]any{"reader": "bob"})
	json.Unmarshal(raw, &response)
	if len(response.Exposures) != 3 || strings.Contains(string(raw), "sensitive memory value") || strings.Contains(string(raw), "sensitive inbox value") {
		t.Fatalf("exposure ledger leaked bodies or missed rows: %s", raw)
	}
	bob := credential(t, s, "bob")
	for _, e := range response.Exposures {
		if e.Reader != "bob" || e.Instance != bob.InstanceID || e.Generation != bob.Generation || e.Version != 1 || e.At == "" || len(e.Routes) != 1 {
			t.Fatal(e)
		}
		if e.Kind == "memory" && (e.Key != "fact" || e.Namespace != "team" || e.SHA256 != hash("sensitive memory value")) {
			t.Fatal(e)
		}
		if e.Kind == "message" && (e.EntityID != sent.Message.ID || e.SHA256 != hash("sensitive inbox value")) {
			t.Fatal(e)
		}
	}
	expectFault(t, s, "bob", "exposure.list", "forbidden", map[string]any{})
	if countRows() != 3 {
		t.Fatal("ledger inspection changed exposure evidence")
	}
	call(t, s, "alice", "memory.patch", map[string]any{"namespace": "team", "key": "fact", "version": 1, "value": "revised memory"})
	call(t, s, "bob", "memory.list", map[string]any{"namespace": "team"})
	raw = call(t, s, "", "exposure.list", map[string]any{"reader": "bob", "kind": "memory", "route": "memory.list"})
	json.Unmarshal(raw, &response)
	if len(response.Exposures) != 1 || response.Exposures[0].Version != 2 || response.Exposures[0].SHA256 != hash("revised memory") {
		t.Fatalf("ledger lost exact memory version: %s", raw)
	}
	raw = call(t, s, "", "exposure.list", map[string]any{"namespace": "team", "key": "fact", "version": 1, "limit": 1})
	var exact struct {
		Exposures []Exposure `json:"exposures"`
		HasMore   bool       `json:"has_more"`
	}
	json.Unmarshal(raw, &exact)
	if len(exact.Exposures) != 1 || exact.Exposures[0].Version != 1 || !exact.HasMore {
		t.Fatalf("exact version filter applied after page: %s", raw)
	}
	expectFault(t, s, "", "exposure.list", "invalid", map[string]any{"version": 0})

}

func TestExposureRetentionIsBoundedAndOlderCursorFailsClearly(t *testing.T) {
	s := testStore(t, testConfig())
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxExposureRows; i++ {
		e := Exposure{ID: fmt.Sprintf("exposure-%d", i), Reader: "bob", Kind: "message", EntityID: "old", Routes: []string{"message.get"}}
		raw, _ := json.Marshal(e)
		if _, err = tx.Exec("INSERT INTO exposures(id,reader,kind,entity_id,route,data) VALUES(?,?,?,?,?,?)", e.ID, "bob", "message", "old", "message.get", string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "fact", "value": "exposed"})
	call(t, s, "bob", "memory.get", map[string]any{"namespace": "team", "key": "fact"})
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM exposures").Scan(&count); err != nil || count != maxExposureRows {
		t.Fatalf("unbounded exposure retention: %d %v", count, err)
	}
	expectFault(t, s, "", "exposure.list", "invalid", map[string]any{"after": "exposure-0"})
}

func TestNativeExternalStatusGivesManualGuidanceAndAuthErrorsAreSanitized(t *testing.T) {
	s := testStore(t, testConfig())
	a := addedWorkspaceAgent(t, call(t, s, "", "agent.add", map[string]any{"id": "observed", "external": true, "external_ref": "terminal-1", "config": workspaceAgent(t, "claude")}))
	st, err := s.nativeStatus(context.Background(), s.db, a.ID)
	if err != nil || st.State != "manual" || strings.Contains(st.Reason, "relaunch") || !strings.Contains(st.Reason, "external terminal") {
		t.Fatalf("external bridge guidance is misleading: %+v %v", st, err)
	}
	missing := interactiveAuthError(managedconfig.NativeAuth{File: "/tmp/agentmux-definitely-absent-auth-source"}, fmt.Errorf("secret-content-must-not-leak"))
	if !strings.Contains(missing.Error(), "missing") || strings.Contains(missing.Error(), "secret-content") {
		t.Fatal(missing)
	}
	unsafe := interactiveAuthError(managedconfig.NativeAuth{File: s.dir}, fmt.Errorf("native auth source must be an owned private regular file secret-content"))
	if !strings.Contains(unsafe.Error(), "unsafe") || !strings.Contains(unsafe.Error(), "0600") || strings.Contains(unsafe.Error(), "secret-content") {
		t.Fatal(unsafe)
	}
	malformed := interactiveAuthError(managedconfig.NativeAuth{File: s.dir}, fmt.Errorf("native auth source must be a JSON credential object secret-content"))
	if !strings.Contains(malformed.Error(), "malformed") || strings.Contains(malformed.Error(), "secret-content") {
		t.Fatal(malformed)
	}
}

func TestNativeClaimExposureKeepsMessageAndMemorySourceIdentity(t *testing.T) {
	for _, kind := range []string{"message", "memory"} {
		t.Run(kind, func(t *testing.T) {
			s := nativeStore(t)
			nativeRegister(t, s, "bob", "native-bob")
			nativeMode(t, s, "bob", "auto", map[string]any{"notes": true, "memory_updates": true, "cooldown_seconds": 0})
			if kind == "message" {
				call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "native exposure body"})
			} else {
				call(t, s, "alice", "memory.put", map[string]any{"namespace": "team", "key": "native-key", "value": "native exposure body"})
			}
			d := nativeNext(t, s, "bob", "native-bob", "idle")
			if d == nil || d.SourceID == "" || d.CreatedAt == "" {
				t.Fatal("native delivery lost immutable source identity")
			}
			var result struct {
				Exposures []Exposure `json:"exposures"`
			}
			raw := call(t, s, "", "exposure.list", map[string]any{"reader": "bob", "kind": kind, "route": "native.claim"})
			json.Unmarshal(raw, &result)
			if len(result.Exposures) != 1 || result.Exposures[0].SHA256 != hash("native exposure body") || result.Exposures[0].Version != 1 {
				t.Fatalf("native claim exposure missing or wrong: %s", raw)
			}
		})
	}
}

func TestNativeHumanControlledRunningDiagnosticSurvivesPeerCancellation(t *testing.T) {
	for _, cancel := range []string{"task", "policy"} {
		t.Run(cancel, func(t *testing.T) {
			s := nativeStore(t)
			nativeRegister(t, s, "bob", "native-bob")
			nativeMode(t, s, "bob", "auto", nil)
			instruction(t, s)
			d := nativeNext(t, s, "bob", "native-bob", "idle")
			if d == nil {
				t.Fatal("no delivery")
			}
			call(t, s, "bob", "native.event", map[string]any{"session": "native-bob", "id": d.ID, "phase": "running", "native_ref": "native-ref"})
			if cancel == "task" {
				task, err := loadTask(context.Background(), s.db, d.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				call(t, s, "", "task.cancel", map[string]any{"id": task.ID, "version": task.Version})
			} else {
				nativeMode(t, s, "bob", "manual", nil)
			}
			reason := "human-controlled turn continues; future peer delivery is paused; interrupt explicitly in the native terminal to stop this turn"
			call(t, s, "bob", "native.event", map[string]any{"session": "native-bob", "id": d.ID, "phase": "running", "native_ref": "native-ref", "reason": reason})
			st, err := s.nativeStatus(context.Background(), s.db, "bob")
			if err != nil || !st.Cancel || st.ActiveID != d.ID || st.Reason != reason || st.Active == nil || st.Active.Reason != reason {
				t.Fatalf("human-controlled cancellation lost its pending status: %+v %v", st, err)
			}
			expectFault(t, s, "bob", "native.event", "conflict", map[string]any{"session": "native-bob", "id": d.ID, "phase": "running", "native_ref": "another-turn", "reason": reason})
			current, err := loadTask(context.Background(), s.db, d.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Delivery != "dispatching" && current.Delivery != "cancel_requested" {
				t.Fatalf("diagnostic released execution slot: %+v", current)
			}
			call(t, s, "bob", "native.event", map[string]any{"session": "native-bob", "id": d.ID, "phase": "completed", "native_ref": "native-ref", "result": "settled after human control"})
			settled, err := loadTask(context.Background(), s.db, d.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if cancel == "task" && settled.Delivery != "stopped" || cancel == "policy" && settled.Delivery != "acknowledged" {
				t.Fatalf("correlated settlement lost: %+v", settled)
			}
		})
	}
}

func TestHistoryQueriesSearchDecodedContent(t *testing.T) {
	s := testStore(t, testConfig())
	call(t, s, "", "message.send", map[string]any{"to": []string{"bob"}, "text": "decoded \"quoted\" content"})
	var messages messageHistoryPage
	json.Unmarshal(call(t, s, "", "message.list", map[string]any{"query": "\"quoted\""}), &messages)
	if len(messages.Messages) != 1 {
		t.Fatalf("quoted content was hidden by JSON encoding: %+v", messages)
	}
	if err := event(context.Background(), s.db, actor{ID: ownerID}, "test.diagnostic", "record", map[string]any{"reason": "decoded \"quoted\" content"}); err != nil {
		t.Fatal(err)
	}
	var events struct {
		Events []Event `json:"events"`
	}
	json.Unmarshal(call(t, s, "", "events", map[string]any{"query": "\"quoted\""}), &events)
	if len(events.Events) != 1 {
		t.Fatalf("quoted event content was hidden by JSON encoding: %+v", events)
	}
}

func TestSessionEventHistoryIncludesOwnerBoundaryAndFormerMembershipChanges(t *testing.T) {
	s := workspaceGroupStore(t)
	call(t, s, "", "control.update", map[string]any{"version": 1, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "allow": false})
	for _, field := range []string{"agent", "from", "to", "subject", "owner", "before_owner", "pending_owner"} {
		if err := event(context.Background(), s.db, actor{ID: ownerID}, "probe.scalar", "unrelated", map[string]any{field: "bob"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, field := range []string{"recipients", "agents", "members", "before_members", "invited", "before_invited"} {
		if err := event(context.Background(), s.db, actor{ID: ownerID}, "probe.array", "unrelated", map[string]any{field: []string{"bob"}}); err != nil {
			t.Fatal(err)
		}
	}
	var page struct {
		Events  []Event `json:"events"`
		Cursor  string  `json:"cursor"`
		HasMore bool    `json:"has_more"`
	}
	args := map[string]any{"agent": "bob", "limit": 2}
	total := 0
	for {
		json.Unmarshal(call(t, s, "", "events.list", args), &page)
		total += len(page.Events)
		if !page.HasMore {
			break
		}
		args["after"] = page.Cursor
	}
	if total != 14 {
		t.Fatalf("session history omitted owner/subject/membership events: %d", total)
	}
	json.Unmarshal(call(t, s, "", "events.list", map[string]any{"agent": "bob", "type": "boundaries.changed"}), &page)
	if len(page.Events) != 1 {
		t.Fatal("owner changed Alice -> Bob boundary absent from Bob's history")
	}
	connectWorkspaceGroup(t, s, 2, "alice", "bob")
	call(t, s, "", "control.update", map[string]any{"version": 3, "kind": "collaboration", "agents": []string{"alice", "bob"}, "enabled": false})
	json.Unmarshal(call(t, s, "", "events.list", map[string]any{"agent": "bob", "type": "workspace_group.changed"}), &page)
	if len(page.Events) != 2 || page.Events[0].Details["state"] != "inactive" {
		t.Fatalf("leaving/disabling group erased member event relevance: %+v", page.Events)
	}
}

func TestTaskSummaryCountsEntireHistoryBeforeRecipientPagination(t *testing.T) {
	s := testStore(t, testConfig())
	ctx := context.Background()
	for i := 0; i < 250; i++ {
		state, delivery := "verified", "acknowledged"
		if i < 3 {
			state, delivery = "failed", "failed"
		}
		if i == 3 {
			state, delivery = "running", "uncertain"
		}
		if i == 4 {
			state, delivery = "cancelled", "cancel_uncertain"
		}
		task := taskRecord{Task: Task{ID: fmt.Sprintf("summary-bob-%03d", i), From: "alice", To: "bob", State: state, Delivery: delivery, Version: 1, Text: "hidden task content"}}
		if err := saveTask(ctx, s.db, task); err != nil {
			t.Fatal(err)
		}
	}
	if err := saveTask(ctx, s.db, taskRecord{Task: Task{ID: "summary-eve", From: ownerID, To: "eve", State: "proposed", Delivery: "pending", Version: 1}}); err != nil {
		t.Fatal(err)
	}
	var recordsBefore, requestsBefore int
	s.db.QueryRow("SELECT count(*) FROM records").Scan(&recordsBefore)
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&requestsBefore)
	var page struct {
		Agents  []TaskSummary `json:"agents"`
		Cursor  string        `json:"cursor"`
		HasMore bool          `json:"has_more"`
	}
	raw := call(t, s, "", "task.summary", map[string]any{"limit": 1})
	json.Unmarshal(raw, &page)
	if len(page.Agents) != 1 || page.Agents[0].Agent != "bob" || page.Agents[0].Total != 250 || page.Agents[0].Failed != 3 || page.Agents[0].Uncertain != 2 || !page.HasMore || page.Cursor != "bob" || strings.Contains(string(raw), "hidden task content") {
		t.Fatalf("summary hid old failures or leaked task body: %s", raw)
	}
	json.Unmarshal(call(t, s, "", "task.summary", map[string]any{"after": page.Cursor, "limit": 1}), &page)
	if len(page.Agents) != 1 || page.Agents[0].Agent != "eve" || page.Agents[0].Pending != 1 || page.HasMore {
		t.Fatalf("summary recipient page incorrect: %+v", page)
	}
	json.Unmarshal(call(t, s, "", "task.summary", map[string]any{"agent": "bob"}), &page)
	if len(page.Agents) != 1 || page.Agents[0].Total != 250 {
		t.Fatalf("summary exact agent filter incorrect: %+v", page)
	}
	expectFault(t, s, "bob", "task.summary", "forbidden", map[string]any{})
	var recordsAfter, requestsAfter int
	s.db.QueryRow("SELECT count(*) FROM records").Scan(&recordsAfter)
	s.db.QueryRow("SELECT count(*) FROM requests").Scan(&requestsAfter)
	if recordsBefore != recordsAfter || requestsBefore != requestsAfter {
		t.Fatal("owner task summary mutated history")
	}
}
