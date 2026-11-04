package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type blockedObservationQueryer struct{ queryer }

func (q blockedObservationQueryer) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.HasPrefix(query, observedTasksSQL) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return q.queryer.QueryContext(ctx, query, args...)
}

func TestObservationReadBudgetPreservesTheExactLeaseMonitor(t *testing.T) {
	s := testStore(t, testConfig())
	instruction(t, s)
	task, lease := claim(t, s)
	worker := credential(t, s, "bob")
	view, reader, _, _, problem := s.operationalReader()
	if problem != nil {
		t.Fatal(problem)
	}
	entered := make(chan struct{})
	finished := make(chan *Fault, 1)
	go func() {
		close(entered)
		_, problem := view.observation(context.Background(), blockedObservationQueryer{reader}, actor{ID: ownerID}, json.RawMessage(`{}`))
		finished <- problem
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	current, err := workerGetLeasedTask(ctx, workerTestCaller(s, worker), task.ID, lease)
	if problem := <-finished; problem == nil || problem.Code != "cancelled" {
		t.Fatalf("blocked operational query did not become an unavailable observation: %v", problem)
	}
	if err != nil || current.ID != task.ID || current.Delivery != "dispatching" {
		t.Fatalf("unavailable observation starved the exact 500ms worker monitor: %v", err)
	}
}

func TestObservationMetadataIndexMigratesExistingState(t *testing.T) {
	s := testStore(t, testConfig())
	if err := saveTask(context.Background(), s.db, taskRecord{Task: Task{ID: "task_before_migration", To: "bob", State: "submitted", Delivery: "acknowledged", Version: 2, Text: "retained original body"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP INDEX records_task_observation_metadata"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		reopened, err := Open(s.dir)
		if err != nil {
			t.Fatal(err)
		}
		result := observationIn(t, reopened, map[string]any{})
		if result.Tasks != 1 || len(result.Attention) != 1 || result.Attention[0].ID != "task_before_migration" {
			t.Fatalf("additive index migration lost existing task: %+v", result)
		}
		var body string
		if err = reopened.db.QueryRow("SELECT json_extract(data,'$.text') FROM records WHERE id='task_before_migration'").Scan(&body); err != nil || body != "retained original body" {
			t.Fatalf("migration changed retained task bytes: %s %v", body, err)
		}
		if err = reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestObservationQueriesKeepRetainedBodiesOutOfCountingAndRanking(t *testing.T) {
	s := testStore(t, testConfig())
	var tableRoot int
	if err := s.db.QueryRow("SELECT rootpage FROM sqlite_schema WHERE name='records'").Scan(&tableRoot); err != nil {
		t.Fatal(err)
	}
	// An expression index alone is insufficient: SQLite may still carry data
	// through a flattened GROUP BY. Inspect bytecode for actual table-data reads.
	query := observedTasksSQL + `SELECT state,delivery,effective_delivery,COUNT(*) FROM observed_tasks GROUP BY state,delivery,effective_delivery`
	rows, err := s.db.Query("EXPLAIN "+query, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	tableCursors := map[int]bool{}
	for rows.Next() {
		var addr, p1, p2, p3, p5 int
		var operation string
		var p4, comment any
		if err = rows.Scan(&addr, &operation, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
			t.Fatal(err)
		}
		if operation == "OpenRead" && p2 == tableRoot {
			tableCursors[p1] = true
		}
		if operation == "Column" && tableCursors[p1] && p2 == 6 {
			t.Fatalf("aggregate reads records.data into its sorter at instruction %d", addr)
		}
		if (operation == "Function" || operation == "PureFunc") && strings.Contains(fmt.Sprint(p4), "json_extract") {
			t.Fatal("aggregate reparses retained task bodies instead of indexed metadata")
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, active := range []bool{false, true} {
		rows, err = s.db.Query("EXPLAIN QUERY PLAN "+observationTaskPageQuery(active), time.Now().Unix(), time.Now().Unix(), 25)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(strings.ToLower(detail) + "\n")
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		for _, required := range []string{"materialize chosen_tasks", "using index records_task_observation", "search body using index records_task_observation_metadata"} {
			if !strings.Contains(plan.String(), required) {
				t.Fatalf("page lacks metadata-first bounded body lookup %q: %s", required, plan.String())
			}
		}
	}
}

func TestObservationPagesAndNativeSummaryNeverReadBodyColumns(t *testing.T) {
	s := testStore(t, testConfig())
	var tableRoot int
	if err := s.db.QueryRow("SELECT rootpage FROM sqlite_schema WHERE name='records'").Scan(&tableRoot); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		name, sql string
		args      []any
	}{
		{"attention", observationTaskPageQuery(false), []any{time.Now().Unix(), time.Now().Unix(), 200}},
		{"active", observationTaskPageQuery(true), []any{time.Now().Unix(), time.Now().Unix(), 200}},
		{"native agent registry", nativeObservedAgentsSQL, []any{""}},
		{"native active receipt", nativeObservedActiveSQL, []any{"bob"}},
	} {
		rows, err := s.db.Query("EXPLAIN "+query.sql, query.args...)
		if err != nil {
			t.Fatal(err)
		}
		tableCursors := map[int]bool{}
		for rows.Next() {
			var addr, p1, p2, p3, p5 int
			var operation string
			var p4, comment any
			if err = rows.Scan(&addr, &operation, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
				t.Fatal(err)
			}
			if operation == "OpenRead" && p2 == tableRoot {
				tableCursors[p1] = true
			}
			if operation == "Column" && tableCursors[p1] && p2 == 6 {
				t.Fatalf("%s reads a task/native/config body at instruction %d", query.name, addr)
			}
			if (operation == "Function" || operation == "PureFunc") && strings.Contains(fmt.Sprint(p4), "json_extract") {
				t.Fatalf("%s reparses bodies instead of using covering metadata", query.name)
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
}

func assertObservationAtDefaultHistoryCapacity(t *testing.T, fixture largeHistoryFixture) {
	t.Helper()
	s, worker, live := fixture.store, fixture.worker, fixture.live
	ctx := context.Background()
	var err error

	var recordsBefore, requestsBefore, exposuresBefore int
	if err = s.db.QueryRow("SELECT (SELECT COUNT(*) FROM records),(SELECT COUNT(*) FROM requests),(SELECT COUNT(*) FROM exposures)").Scan(&recordsBefore, &requestsBefore, &exposuresBefore); err != nil {
		t.Fatal(err)
	}
	type observationOutcome struct {
		result  any
		problem *Fault
	}
	finished := make(chan observationOutcome, 1)
	owner := credential(t, s, "")
	_, reader, _, _, problem := s.operationalReader()
	if problem != nil {
		t.Fatal(problem)
	}
	admission, err := reader.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer admission.Close()
	waits := reader.Stats().WaitCount
	started := time.Now()
	go func() {
		// Exercise the actual owner route. Operational SQL and cancellation
		// run on its independent read-only connection, outside the lease lock.
		raw, problem := s.Call(ctx, owner.Token, Request{Operation: "observation.get", Args: json.RawMessage(`{"limit":25}`)})
		var result ObservationSnapshot
		if problem == nil {
			if err := json.Unmarshal(raw, &result); err != nil {
				finished <- observationOutcome{nil, internal(err)}
				return
			}
			finished <- observationOutcome{result, nil}
			return
		}
		finished <- observationOutcome{nil, problem}
	}()
	// Admit the real Store.Call before racing its SQL against the worker.
	// This tests the independent connection, rather than manually imposing a
	// mutex layout the production path no longer uses.
	waitForOperationalConnection(t, reader, waits)
	if err := admission.Close(); err != nil {
		t.Fatal(err)
	}
	monitorCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	current, readErr := workerGetLeasedTask(monitorCtx, workerTestCaller(s, worker), live.ID, live.LeaseToken)
	outcome := <-finished
	if readErr != nil || current.ID != live.ID || current.Delivery != "dispatching" || current.Version != live.Version {
		t.Fatalf("operational read interrupted the live worker's 500ms lease monitor: %v", readErr)
	}
	if outcome.problem != nil {
		if outcome.problem.Code != "cancelled" || outcome.result != nil {
			t.Fatalf("observation failed outside its explicit unavailable budget outcome: %v", outcome.problem)
		}
		t.Logf("metadata budget returned unavailable while the exact lease monitor succeeded: %s", time.Since(started))
	} else {
		snapshot, ok := outcome.result.(ObservationSnapshot)
		if !ok || snapshot.Tasks != s.cfg.Limits.MaxTasks || snapshot.AttentionCount != s.cfg.Limits.MaxTasks-1 || snapshot.ActiveCount != 1 || len(snapshot.Attention) != 25 || len(snapshot.Active) != 1 || snapshot.Active[0].ID != live.ID {
			t.Fatalf("available large-history observation is inconsistent: %+v", outcome.result)
		}
		t.Logf("available full default history observation and exact lease read: %s", time.Since(started))
	}
	retained, err := loadTask(ctx, s.db, live.ID)
	if err != nil || retained.LeaseToken != live.LeaseToken || retained.Instance != live.Instance || retained.Expires != live.Expires || retained.Version != live.Version || retained.State != live.State || retained.Delivery != live.Delivery || retained.Text != live.Text {
		t.Fatalf("operational read changed the exact execution attempt: %+v %v", retained, err)
	}
	var recordsAfter, requestsAfter, exposuresAfter int
	if err = s.db.QueryRow("SELECT (SELECT COUNT(*) FROM records),(SELECT COUNT(*) FROM requests),(SELECT COUNT(*) FROM exposures)").Scan(&recordsAfter, &requestsAfter, &exposuresAfter); err != nil || recordsAfter != recordsBefore || requestsAfter != requestsBefore || exposuresAfter != exposuresBefore {
		t.Fatalf("operational read changed durable history/receipts/exposure counts: %v", err)
	}
}

func TestObservationReportsLogicalCapacityWithoutConsumingReceipts(t *testing.T) {
	s := testStore(t, testConfig())
	instruction(t, s)
	var records, requests, messages int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM records").Scan(&records); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM requests").Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM records WHERE kind='message'").Scan(&messages); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result := observationIn(t, s, map[string]any{})
		got := result.Capacity
		limits := s.cfg.Limits
		if got.Records != records || got.Requests != requests || got.Messages != messages || got.Tasks != 1 || got.MaxRecords != limits.MaxRecords || got.MaxRequests != limits.MaxRequests || got.MaxMessages != limits.MaxMessages || got.MaxTasks != limits.MaxTasks {
			t.Fatalf("logical storage observation is missing counts or configured ceilings: %+v", got)
		}
	}
}

func observationIn(t *testing.T, s *Store, args any) ObservationSnapshot {
	t.Helper()
	var result ObservationSnapshot
	if err := json.Unmarshal(call(t, s, "", "observation.get", args), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestObservationCountsRetainedHistoryAndPrioritizesRecoveryWithoutBodiesOrMutation(t *testing.T) {
	s := testStore(t, testConfig())
	at := time.Now().UTC()
	sentinel := "PRIVATE_BODY_CREDENTIAL_SENTINEL"
	fixtures := []struct {
		state, delivery string
		expires         int64
	}{
		{"running", "dispatching", at.Add(-time.Minute).Unix()},
		{"cancelled", "cancel_requested", at.Add(-time.Minute).Unix()},
		{"failed", "failed", 0},
		{"submitted", "completed", 0},
		{"proposed", "pending", 0},
		{"running", "dispatching", at.Add(time.Hour).Unix()},
	}
	for i, f := range fixtures {
		record := taskRecord{Task: Task{ID: fmt.Sprintf("task_attention_%03d", i), To: "retired-recipient", From: "$owner", State: f.state, Delivery: f.delivery, Version: i + 7, Text: sentinel, Result: sentinel, CreatedAt: at.Add(-time.Hour).Format(time.RFC3339Nano), UpdatedAt: at.Format(time.RFC3339Nano)}, Expires: f.expires, LeaseToken: sentinel, Instance: sentinel}
		if i == 0 {
			record.Native = &NativeReceipt{ID: "native-receipt", Adapter: "codex", Phase: "running", UpdatedAt: at.Format(time.RFC3339Nano), Evidence: "native-events", Session: sentinel, NativeRef: sentinel, Reason: sentinel}
			record.Trace = &TraceSummary{ID: "trace-id", Records: 3, Bytes: 120, MAC: "trace-mac", Complete: true, Coverage: sentinel}
		}
		if err := saveTask(context.Background(), s.db, record); err != nil {
			t.Fatal(err)
		}
	}
	// Old failures/recovery must remain visible behind a large completed tail.
	for i := 0; i < 230; i++ {
		if err := saveTask(context.Background(), s.db, taskRecord{Task: Task{ID: fmt.Sprintf("task_done_%03d", i), To: "bob", State: "verified", Delivery: "completed", Version: 1, Text: strings.Repeat(sentinel, 100)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := event(context.Background(), s.db, actor{ID: ownerID}, "test.observed", "entity", map[string]any{"body": sentinel}); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.db.QueryRow("SELECT group_concat(data,'') FROM records ORDER BY seq").Scan(&before); err != nil {
		t.Fatal(err)
	}
	var requestsBefore int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM requests").Scan(&requestsBefore); err != nil {
		t.Fatal(err)
	}
	result := observationIn(t, s, map[string]any{"limit": 2})
	if result.Tasks != 236 || result.TaskStates["verified"] != 230 || result.Deliveries["dispatching"] != 2 || result.EffectiveDeliveries["dispatching"] != 1 || result.EffectiveDeliveries["uncertain"] != 1 || result.EffectiveDeliveries["cancel_uncertain"] != 1 || result.RecoveryEligible != 2 || result.AttentionCount != 5 || !result.AttentionMore || len(result.Attention) != 2 {
		t.Fatalf("incorrect complete-history counts: %+v", result)
	}
	if result.Attention[0].ID != "task_attention_001" || result.Attention[1].ID != "task_attention_002" || result.Attention[0].Kind != "recovery" || result.Attention[1].Kind != "failed" {
		t.Fatalf("lost priority or exact recovery evidence: %+v", result.Attention)
	}
	if result.LastEventAt == "" {
		t.Fatal("missing lifecycle observation time")
	}
	if _, err := time.Parse(time.RFC3339Nano, result.ObservedAt); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	for _, private := range []string{sentinel, "lease_token", "\"text\"", "\"result\"", "\"usage\"", "native_ref"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("observation disclosed private body/credentials: %s", private)
		}
	}
	all := observationIn(t, s, map[string]any{})
	wantKinds := []string{"recovery", "failed", "review", "pending", "recovery"}
	for i, item := range all.Attention {
		if item.Kind != wantKinds[i] {
			t.Fatalf("wrong attention order: %+v", all.Attention)
		}
	}
	if all.AttentionMore {
		t.Fatal("complete bounded page incorrectly reports truncation")
	}
	olderRecovery := all.Attention[4]
	if olderRecovery.ID != "task_attention_000" || olderRecovery.Version != 7 || olderRecovery.Recovery == nil || olderRecovery.NativeReceipt == nil || olderRecovery.Trace == nil || !olderRecovery.Trace.Complete {
		t.Fatalf("lost older exact recovery evidence: %+v", olderRecovery)
	}
	if receipt := olderRecovery.NativeReceipt; receipt.Session != "" || receipt.NativeRef != "" || receipt.Reason != "" {
		t.Fatal("native receipt projection retained content-bearing fields")
	}
	var after string
	if err := s.db.QueryRow("SELECT group_concat(data,'') FROM records ORDER BY seq").Scan(&after); err != nil {
		t.Fatal(err)
	}
	var requestsAfter int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM requests").Scan(&requestsAfter); err != nil {
		t.Fatal(err)
	}
	if before != after || requestsBefore != requestsAfter {
		t.Fatal("observation mutated records or idempotency history")
	}
	var status struct {
		Tasks               int            `json:"tasks"`
		EffectiveDeliveries map[string]int `json:"effective_deliveries"`
		Deliveries          map[string]int `json:"deliveries"`
		RecoveryEligible    int            `json:"recovery_eligible"`
	}
	if err := json.Unmarshal(call(t, s, "", "status", map[string]any{}), &status); err != nil {
		t.Fatal(err)
	}
	if status.Tasks != all.Tasks || status.RecoveryEligible != 2 || !reflect.DeepEqual(status.EffectiveDeliveries, all.EffectiveDeliveries) || !reflect.DeepEqual(status.Deliveries, all.Deliveries) {
		t.Fatalf("status hides effective uncertainty: %+v", status)
	}
}

func TestObservationAuthorizationBoundsAndDeadlineRecovery(t *testing.T) {
	s := testStore(t, testConfig())
	expectFault(t, s, "alice", "observation.get", "forbidden", map[string]any{})
	for _, args := range []any{map[string]any{"limit": nil}, map[string]any{"limit": 0}, map[string]any{"limit": 201}, map[string]any{"limit": -1}, map[string]any{"limit": 1.5}, map[string]any{"query": "body"}} {
		expectFault(t, s, "", "observation.get", "invalid", args)
	}
	result := observationIn(t, s, map[string]any{})
	if result.Tasks != 0 || len(result.Attention) != 0 || result.AttentionMore || result.Attention == nil || result.TaskStates == nil || result.EffectiveDeliveries == nil {
		t.Fatalf("bad empty observation: %+v", result)
	}
	at := time.Now().Add(-time.Minute)
	if _, err := s.db.Exec("INSERT OR REPLACE INTO metadata(key,value) VALUES('deadline',?)", at.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := saveTask(context.Background(), s.db, taskRecord{Task: Task{ID: "task_expired", To: "bob", State: "running", Delivery: "dispatching", Version: 3}, Expires: at.Unix()}); err != nil {
		t.Fatal(err)
	}
	result = observationIn(t, s, map[string]any{"limit": 1})
	if result.RuntimeDeadline != at.UTC().Format(time.RFC3339Nano) || len(result.Attention) != 1 || result.Attention[0].Recovery == nil || !reflect.DeepEqual(result.Attention[0].Recovery.Actions, []string{"fail"}) {
		t.Fatalf("runtime expiration offered retry: %+v", result)
	}
	got := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": "task_expired"}))
	if !reflect.DeepEqual(got.Recovery, result.Attention[0].Recovery) || got.Delivery != "dispatching" || got.Version != 3 {
		t.Fatal("observation differs from exact task recovery or rewrites delivery")
	}
}

func TestObservationAttentionBoundsKeepCompleteCounts(t *testing.T) {
	s := testStore(t, testConfig())
	for i := 0; i < 206; i++ {
		if err := saveTask(context.Background(), s.db, taskRecord{Task: Task{ID: fmt.Sprintf("task_pending_%03d", i), To: "bob", State: "proposed", Delivery: "pending", Version: 1, Text: strings.Repeat("private-body", 2000)}}); err != nil {
			t.Fatal(err)
		}
	}
	// This checks query semantics, not a scheduling guarantee for a maximum
	// page on a race-instrumented CI host. The public operation deliberately
	// returns unavailable when its 250ms read budget expires; the separate
	// blocked-read and large-history tests enforce that budget and the exact
	// 500ms worker monitor. Exercise the same count/page queries directly so
	// an unavailable sample cannot skip any of the bounds/privacy assertions.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	at := time.Now()
	for _, limit := range []int{25, 200} {
		result, err := taskObservationCounts(ctx, s.db, at)
		if err != nil {
			t.Fatal(err)
		}
		var problem *Fault
		result.Attention, problem = s.observationTaskPage(ctx, s.db, actor{ID: ownerID}, at, limit, false)
		if problem != nil {
			t.Fatal(problem)
		}
		if result.Tasks != 206 || result.AttentionCount != 206 || len(result.Attention) != limit || len(result.Attention) >= result.AttentionCount {
			t.Fatalf("bounded page discarded counts: %+v", result)
		}
		encoded, _ := json.Marshal(result)
		if len(encoded) > maxWireBytes || strings.Contains(string(encoded), "private-body") {
			t.Fatal("body-free attention page exceeded its transport bound")
		}
	}
}

func TestObservationHistoricalFailuresCannotHideOtherAttentionKinds(t *testing.T) {
	s := testStore(t, testConfig())
	ctx := context.Background()
	// Recovery is deliberately older than a failure history larger than the
	// largest view. The latest review and queued work must still be visible.
	if err := saveTask(ctx, s.db, taskRecord{Task: Task{ID: "task_old_recovery", To: "retired", State: "running", Delivery: "dispatching", Version: 9}, Expires: time.Now().Add(-time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 225; i++ {
		if err := saveTask(ctx, s.db, taskRecord{Task: Task{ID: fmt.Sprintf("task_failed_%03d", i), To: "bob", State: "failed", Delivery: "failed", Version: 2, Text: "private historical task", Result: "private result"}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range []Task{
		{ID: "task_new_review", To: "alice", State: "submitted", Delivery: "acknowledged", Version: 4},
		{ID: "task_new_pending", To: "bob", State: "proposed", Delivery: "pending", Version: 1},
	} {
		if err := saveTask(ctx, s.db, taskRecord{Task: task}); err != nil {
			t.Fatal(err)
		}
	}
	var before string
	if err := s.db.QueryRow("SELECT group_concat(data,'') FROM records ORDER BY seq").Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Check fairness and privacy independently of the public operation's
	// deliberate unavailable result when its read budget expires under load.
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	at := time.Now()
	for _, limit := range []int{25, 200} {
		result, err := taskObservationCounts(queryCtx, s.db, at)
		if err != nil {
			t.Fatal(err)
		}
		var problem *Fault
		result.Attention, problem = s.observationTaskPage(queryCtx, s.db, actor{ID: ownerID}, at, limit, false)
		if problem != nil {
			t.Fatal(problem)
		}
		if result.Tasks != 228 || result.AttentionCount != 228 || result.TaskStates["failed"] != 225 || len(result.Attention) != limit || len(result.Attention) >= result.AttentionCount {
			t.Fatalf("fair page changed complete counts: %+v", result)
		}
		for i, id := range []string{"task_old_recovery", "task_failed_224", "task_new_review", "task_new_pending"} {
			if result.Attention[i].ID != id {
				t.Fatalf("kind starved behind historical failures: position=%d got=%s want=%s", i, result.Attention[i].ID, id)
			}
		}
		if result.Attention[0].Recovery == nil || result.Attention[0].Version != 9 {
			t.Fatal("fairness discarded exact old recovery authority")
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "private historical task") || strings.Contains(string(encoded), "private result") {
			t.Fatal("fair attention page disclosed historical bodies")
		}
	}
	var after string
	if err := s.db.QueryRow("SELECT group_concat(data,'') FROM records ORDER BY seq").Scan(&after); err != nil || before != after {
		t.Fatalf("fair observation mutated task history: %v", err)
	}
}

func TestNativeReadinessCannotCrossCredentialGeneration(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	call(t, s, "bob", "native.peek", map[string]any{"session": "native-bob", "state": "idle"})
	if !nativeGetStatus(t, s, "bob").Connected {
		t.Fatal("missing fresh readiness")
	}
	if _, err := s.Rotate("bob"); err != nil {
		t.Fatal(err)
	}
	nativeRegister(t, s, "bob", "native-bob")
	st := nativeGetStatus(t, s, "bob")
	if st.Connected || st.ReadinessObservedAt != "" {
		t.Fatal("new credential generation borrowed old readiness")
	}
	call(t, s, "bob", "native.peek", map[string]any{"session": "native-bob", "state": "idle"})
	if st = nativeGetStatus(t, s, "bob"); !st.Connected || st.ReadinessObservedAt == "" {
		t.Fatal("current generation readiness was not observed")
	}
}

func TestNativeReadinessFreshnessIsSeparateFromRetainedReceiptAndSession(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "auto", nil)
	d := nativeNext(t, s, "bob", "native-bob", "idle")
	if d == nil {
		t.Fatal("missing delivery fixture")
	}
	nativeReport(t, s, "bob", "native-bob", d, "running")
	fresh := nativeGetStatus(t, s, "bob")
	if !fresh.Connected || fresh.ReadinessObservedAt == "" || fresh.Active == nil || fresh.Active.Phase != "running" {
		t.Fatalf("missing fresh evidence: %+v", fresh)
	}
	presence := s.nativePresence["bob"]
	presence.At = time.Now().Add(-time.Minute)
	s.nativePresence["bob"] = presence
	stale := nativeGetStatus(t, s, "bob")
	if stale.Connected || stale.ReadinessObservedAt != presence.At.UTC().Format(time.RFC3339Nano) || stale.Active == nil || !reflect.DeepEqual(stale.Active, fresh.Active) || stale.State != "active" {
		t.Fatalf("stale readiness rewrote retained outcome: %+v", stale)
	}
	presence.At = time.Now().Add(time.Minute)
	s.nativePresence["bob"] = presence
	if nativeGetStatus(t, s, "bob").Connected {
		t.Fatal("future readiness accepted as fresh")
	}
	// A new session cannot borrow the old session's readiness observation.
	nativeRegister(t, s, "bob", "replacement-bob")
	replaced := nativeGetStatus(t, s, "bob")
	if replaced.Connected || replaced.ReadinessObservedAt != "" {
		t.Fatalf("replacement borrowed old readiness: %+v", replaced)
	}
	delete(s.nativePresence, "bob")
	unknown := nativeGetStatus(t, s, "bob")
	if unknown.Connected || unknown.ReadinessObservedAt != "" {
		t.Fatal("missing readiness presented as observed")
	}
}
