package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"
)

func waitForOperationalConnection(t *testing.T, db *sql.DB, previous int64) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for db.Stats().WaitCount <= previous {
		select {
		case <-deadline.C:
			t.Fatal("actual Store.Call did not enter the independent observation connection")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestOperationalReadBlockedConnectionCannotStarveExactLeaseMonitor(t *testing.T) {
	for _, request := range []Request{
		{Operation: "observation.get", Args: json.RawMessage(`{}`)},
		{Operation: "native.status", Args: json.RawMessage(`{"summary":true}`)},
	} {
		t.Run(request.Operation, func(t *testing.T) {
			s := testStore(t, testConfig())
			instruction(t, s)
			task, lease := claim(t, s)
			worker, owner := credential(t, s, "bob"), credential(t, s, "")
			before := largeHistoryCounts(t, s)
			_, reader, _, _, problem := s.operationalReader()
			if problem != nil || reader == s.db || reader.Stats().MaxOpenConnections != 1 {
				t.Fatalf("observation lacks a separate bounded connection: %v", problem)
			}
			held, err := reader.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			waits := reader.Stats().WaitCount
			finished := make(chan *Fault, 1)
			started := time.Now()
			go func() {
				raw, problem := s.Call(context.Background(), owner.Token, request)
				if raw != nil {
					finished <- fault("unexpected", "blocked read published a result")
					return
				}
				finished <- problem
			}()
			waitForOperationalConnection(t, reader, waits)
			// The actual owner call is blocked on its independent connection.
			// Keep it occupied through cancellation, as with slow driver cleanup.
			monitor, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			current, err := workerGetLeasedTask(monitor, workerTestCaller(s, worker), task.ID, lease)
			if err != nil || current.ID != task.ID || current.Delivery != "dispatching" {
				t.Fatalf("blocked observation starved the unchanged 500ms monitor: %v", err)
			}
			if _, problem := s.Call(context.Background(), owner.Token, request); problem == nil || problem.Code != "unavailable" || problem.ReasonCode != "operational_read_busy" {
				t.Fatalf("concurrent diagnostic was not bounded: %v", problem)
			}
			if problem := <-finished; problem == nil || problem.Code != "cancelled" || problem.ReasonCode != "query_budget_exhausted" {
				t.Fatalf("blocked observer did not honor the unchanged query budget: %v", problem)
			}
			if elapsed := time.Since(started); elapsed < operationalObservationReadBudget || elapsed > 2*time.Second {
				t.Fatalf("single blocked read changed its 250ms query budget: %v", elapsed)
			}
			// The one authenticated worker read legitimately records one
			// instruction disclosure. Neither owner observation adds anything.
			want := before
			want[2]++
			if after := largeHistoryCounts(t, s); after != want {
				t.Fatalf("observation added durable effects beyond the worker disclosure: want %v got %v", want, after)
			}
		})
	}
}

func TestOperationalReadConnectionIsReadOnlyAndCloseCancelsIt(t *testing.T) {
	for _, request := range []Request{{Operation: "observation.get"}, {Operation: "native.status", Args: json.RawMessage(`{"summary":true}`)}} {
		t.Run(request.Operation, func(t *testing.T) { testOperationalReadClose(t, request) })
	}
}

func testOperationalReadClose(t *testing.T, request Request) {
	s := testStore(t, testConfig())
	owner := credential(t, s, "")
	_, reader, _, _, problem := s.operationalReader()
	if problem != nil {
		t.Fatal(problem)
	}
	var queryOnly int
	if err := reader.QueryRow("PRAGMA query_only").Scan(&queryOnly); err != nil || queryOnly != 1 {
		t.Fatalf("observation connection is not query_only: %d %v", queryOnly, err)
	}
	if _, err := reader.Exec("INSERT INTO metadata(key,value) VALUES('observer_write','forbidden')"); err == nil {
		t.Fatal("observation connection admitted a durable write")
	}
	// Verify the file was opened mode=ro independently of query_only.
	if _, err := reader.Exec("PRAGMA query_only=0"); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Exec("INSERT INTO metadata(key,value) VALUES('observer_write','forbidden')"); err == nil {
		t.Fatal("observation file was not opened read-only")
	}
	if _, err := reader.Exec("PRAGMA query_only=1"); err != nil {
		t.Fatal(err)
	}
	held, err := reader.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	waits := reader.Stats().WaitCount
	finished := make(chan *Fault, 1)
	go func() {
		_, problem := s.Call(context.Background(), owner.Token, request)
		finished <- problem
	}()
	waitForOperationalConnection(t, reader, waits)
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	if problem := <-finished; problem == nil || problem.Code != "cancelled" || problem.ReasonCode != "shutdown" {
		t.Fatalf("Close did not cancel the observation lifetime: %v", problem)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if _, problem := s.Call(context.Background(), owner.Token, request); problem == nil || problem.Code != "unavailable" || problem.ReasonCode != "shutdown" {
		t.Fatalf("closed store admitted an observation: %v", problem)
	}
}

func TestOperationalReadRoutingPreservesNativeSummarySelectionAndAuthentication(t *testing.T) {
	for _, fixture := range []struct {
		raw  string
		read bool
	}{
		{`{"summary":true}`, true}, {`{"Summary":true}`, true},
		{`{"summary":true,"summary":false}`, false}, {`{"summary":false,"SUMMARY":true}`, true},
		{`{"summary":null}`, false}, {`{"summary":"true"}`, false},
		{`{"agent":"bob"}`, false}, {`{"summary":true,`, false},
	} {
		if got := operationalRead(Request{Operation: "native.status", Args: json.RawMessage(fixture.raw)}); got != fixture.read {
			t.Fatalf("routing differs from native JSON selection for %s: %t", fixture.raw, got)
		}
	}
	s := testStore(t, testConfig())
	for _, request := range []Request{
		{Operation: "observation.get", Args: json.RawMessage(`{"unknown":true}`)},
		{Operation: "native.status", Args: json.RawMessage(`{"summary":true,"unknown":true}`)},
	} {
		if _, problem := s.Call(context.Background(), "invalid-token", request); problem == nil || problem.Code != "unauthorized" {
			t.Fatalf("argument diagnosis preceded authentication: %v", problem)
		}
		if _, problem := s.Call(context.Background(), credential(t, s, "").Token, request); problem == nil || problem.Code != "invalid" {
			t.Fatalf("independent read lost strict arguments: %v", problem)
		}
	}
	proof, err := s.InitResearchRunner()
	if err != nil {
		t.Fatal(err)
	}
	if _, problem := s.Call(context.Background(), credential(t, s, "").Token, Request{Operation: "observation.get", ResearchRunnerProof: proof}); problem != nil {
		t.Fatalf("authorized research owner observation changed behavior: %v", problem)
	}
}

func TestOperationalReadShadowsDoNotPruneLiveProgressOrBorrowOldGeneration(t *testing.T) {
	s := testStore(t, testConfig())
	instruction(t, s)
	task, lease := claim(t, s)
	worker := credential(t, s, "bob")
	stale := taskProgressObservation{TaskID: task.ID, Instance: worker.InstanceID, Lease: lease, Generation: worker.Generation - 1, Progress: TaskProgress{WorkerObservedAt: now(), EventCount: 7, EventKind: "tool"}}
	s.taskProgress = map[string]taskProgressObservation{"bob": stale}
	result := observationIn(t, s, map[string]any{})
	if len(result.Active) != 1 || result.Active[0].Progress != nil {
		t.Fatalf("observation borrowed another credential generation: %+v", result.Active)
	}
	if s.taskProgress["bob"] != stale {
		t.Fatal("read-only shadow pruned the live progress map")
	}
}

func TestOperationalReadRevalidatesOwnerAfterSnapshotRotation(t *testing.T) {
	s := testStore(t, testConfig())
	old := credential(t, s, "")
	_, reader, _, _, problem := s.operationalReader()
	if problem != nil {
		t.Fatal(problem)
	}
	connection, err := reader.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	tx, err := connection.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	prior, err := authenticateWith(context.Background(), tx, old.Token)
	if err != nil {
		t.Fatal(err)
	}
	external, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	current, err := external.Rotate(ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authenticateWith(context.Background(), tx, old.Token); err != nil {
		t.Fatal("fixture did not retain its old authenticated snapshot")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if problem := currentOperationalCredential(context.Background(), connection, old.Token, prior); problem == nil || problem.Code != "unauthorized" {
		t.Fatalf("revoked snapshot could publish its old sample: %v", problem)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if _, problem := s.Call(context.Background(), current.Token, Request{Operation: "observation.get"}); problem != nil {
		t.Fatalf("new owner could not observe after rotation: %v", problem)
	}
}
