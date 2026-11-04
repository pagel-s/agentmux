package managed

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentmux/internal/nativebridge"
)

func TestNativeCandidateMetadataIndexMigratesExistingState(t *testing.T) {
	s := testStore(t, testConfig())
	ctx := context.Background()
	if err := saveTask(ctx, s.db, taskRecord{Task: Task{ID: "task_before_native_metadata", From: ownerID, To: "bob", State: "proposed", Delivery: "pending", Version: 1, Text: "retained exact instruction"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP INDEX records_native_candidate_metadata"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	page, err := nativeCandidateTaskPage(ctx, reopened.db, "bob", 0, false)
	if err != nil || len(page) != 1 || page[0].ID != "task_before_native_metadata" {
		t.Fatalf("existing state did not receive additive metadata index: %+v %v", page, err)
	}
	retained, err := loadTask(ctx, reopened.db, page[0].ID)
	if err != nil || retained.Text != "retained exact instruction" || retained.Version != 1 {
		t.Fatalf("index migration changed retained source: %+v %v", retained, err)
	}
}

func TestNativeCandidateEligibilityReadsCoveringMetadataWithoutBodies(t *testing.T) {
	s := testStore(t, testConfig())
	var recordsRoot, indexRoot int
	if err := s.db.QueryRow("SELECT rootpage FROM sqlite_master WHERE name='records'").Scan(&recordsRoot); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT rootpage FROM sqlite_master WHERE name='records_native_candidate_metadata'").Scan(&indexRoot); err != nil {
		t.Fatal(err)
	}
	for _, results := range []bool{false, true} {
		rows, err := s.db.Query("EXPLAIN "+nativeCandidateTaskPageQuery(results), 0, "alice")
		if err != nil {
			t.Fatal(err)
		}
		dataCursors := map[int]bool{}
		usedIndex := false
		for rows.Next() {
			var address, p1, p2, p3, p5 int
			var opcode string
			var p4, comment any
			if err = rows.Scan(&address, &opcode, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
				t.Fatal(err)
			}
			if opcode == "OpenRead" {
				dataCursors[p1] = p2 == recordsRoot
				usedIndex = usedIndex || p2 == indexRoot
			}
			if opcode == "Column" && dataCursors[p1] && p2 == 6 {
				t.Fatalf("candidate eligibility reads retained bodies: results=%v address=%d", results, address)
			}
			if (opcode == "Function" || opcode == "PureFunc") && strings.Contains(fmt.Sprint(p4), "json_") {
				t.Fatalf("candidate eligibility reparses retained JSON: results=%v %v", results, p4)
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !usedIndex {
			t.Fatal("candidate metadata index was not used")
		}
	}
}

func TestNativeStatusHydrationBoundsSourceWorkAsWellAsPublishedBytes(t *testing.T) {
	validated := 0
	infos := []nativeCandidateInfo{}
	for i := 0; i < 8; i++ {
		infos = append(infos, nativeCandidateInfo{
			Delivery:   nativebridge.Delivery{ID: fmt.Sprintf("candidate_%d", i)},
			text:       func() (string, error) { return "tiny result", nil },
			validate:   func() error { validated++; return nil },
			inputBytes: func() int { return 180 },
		})
	}
	items, more, err := nativeCandidateBodies(infos, 500)
	if err != nil || !more || len(items) != 2 || validated != 2 {
		t.Fatalf("small output permitted unbounded source validation: items=%d validated=%d more=%v err=%v", len(items), validated, more, err)
	}
}

func TestNativeManualTransitionAndCancellationDoNotDependOnPendingBodies(t *testing.T) {
	for _, variant := range []string{"sender", "origin", "body"} {
		t.Run(variant, func(t *testing.T) {
			s := nativeStore(t)
			nativeRegister(t, s, "bob", "native-bob")
			nativeMode(t, s, "bob", "auto", nil)
			instruction(t, s)
			active := nativeNext(t, s, "bob", "native-bob", "idle")
			if active == nil {
				t.Fatal("missing active native fixture")
			}
			nativeReport(t, s, "bob", "native-bob", active, "running")
			policyVersion := nativeGetStatus(t, s, "bob").Policy.Version
			ctx := context.Background()
			source := taskRecord{Task: Task{ID: "task_bad_pending_source", From: ownerID, To: "bob", State: "proposed", Delivery: "pending", Version: 1, Text: "unrelated pending content", Origin: &TaskOrigin{Kind: "instruction"}}}
			if err := saveTask(ctx, s.db, source); err != nil {
				t.Fatal(err)
			}
			expression := `json_set(data,'$.From','eve')`
			if variant == "origin" {
				expression = `json_set(data,'$.Origin',json('{"kind":"instruction","hop":5}'))`
			}
			if variant == "body" {
				expression = `json_set(data,'$.Text','unreviewed content')`
			}
			if _, err := s.db.Exec("UPDATE records SET data="+expression+" WHERE kind='task' AND id=?", source.ID); err != nil {
				t.Fatal(err)
			}
			// Configure returns the committed policy, not a fallible queue view.
			ownerView := nativeGetStatus(t, s, "bob")
			if ownerView.PendingKnown || len(ownerView.Pending) != 0 || ownerView.Policy.Version != policyVersion || ownerView.Active == nil || ownerView.Coverage == "" {
				t.Fatalf("pending validation hid owner policy/transport controls: %+v", ownerView)
			}
			call(t, s, "", "native.configure", map[string]any{"agent": "bob", "mode": "manual", "version": ownerView.Policy.Version})
			raw, problem := invoke(s, credential(t, s, "bob"), "native.status", map[string]any{"session": "native-bob"}, "")
			if problem != nil {
				t.Fatal(problem)
			}
			var result struct {
				Status NativeStatus `json:"status"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if result.Status.Policy.Mode != "manual" || result.Status.Active == nil || result.Status.Active.ID != active.ID || !result.Status.Cancel || result.Status.PendingKnown || len(result.Status.Pending) != 0 || result.Status.Coverage == "" {
				t.Fatalf("pending validation hid committed Manual/cancel evidence: %+v", result.Status)
			}
		})
	}
}

func assertFullNativeStatusAtDefaultHistoryCapacity(t *testing.T, fixture largeHistoryFixture) {
	t.Helper()
	s, worker, monitorTask := fixture.store, fixture.worker, fixture.live
	lease := monitorTask.LeaseToken
	ctx := context.Background()
	var err error

	owner := credential(t, s, "")
	var before int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM requests").Scan(&before); err != nil {
		t.Fatal(err)
	}
	statusDone := make(chan error, 1)
	started := time.Now()
	go func() {
		raw, problem := s.Call(ctx, owner.Token, Request{Operation: "native.status", Args: json.RawMessage(`{"limit":200}`)})
		if problem != nil {
			statusDone <- problem
			return
		}
		var result struct {
			Statuses []NativeStatus `json:"statuses"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			statusDone <- err
			return
		}
		if len(result.Statuses) != 1 || result.Statuses[0].PendingCount != nativeCandidateLimit || !result.Statuses[0].PendingMore || len(result.Statuses[0].Pending) != 0 {
			statusDone <- fmt.Errorf("full status lost count/body-window evidence: %+v", result.Statuses)
			return
		}
		statusDone <- nil
	}()
	// The original all-body reader still holds Store.mu at this point and
	// causes the real worker's unchanged 500ms lease query to time out.
	time.Sleep(20 * time.Millisecond)
	monitorCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	_, monitorErr := workerGetLeasedTask(monitorCtx, workerTestCaller(s, worker), monitorTask.ID, lease)
	cancel()
	statusErr := <-statusDone
	if statusErr != nil || monitorErr != nil {
		t.Fatalf("an operational read interrupted exact lease monitoring: status=%v monitor=%v", statusErr, monitorErr)
	}
	t.Logf("full native status and concurrent exact lease observation: %s", time.Since(started))
	selected, problem := s.Call(ctx, owner.Token, Request{Operation: "native.status", Args: json.RawMessage(`{"agent":"alice"}`)})
	if problem != nil {
		t.Fatal(problem)
	}
	var details struct {
		Status NativeStatus `json:"status"`
	}
	if err = json.Unmarshal(selected, &details); err != nil || details.Status.PendingCount != nativeCandidateLimit || !details.Status.PendingMore || len(details.Status.Pending) == 0 || len(details.Status.Pending) >= nativeCandidateLimit {
		t.Fatalf("selected native status lost bounded content/count evidence: %+v %v", details, err)
	}
	var after int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM requests").Scan(&after); err != nil || after != before {
		t.Fatalf("status/lease observation consumed durable receipts: %d -> %d %v", before, after, err)
	}
}
