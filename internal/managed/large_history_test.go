package managed

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type largeHistoryFixture struct {
	store  *Store
	worker Credential
	live   taskRecord
}

// These operational readers share the same declared history workload, not
// mutable package state. Seed it once and run independent sequential subtests;
// repeatedly encoding/indexing 128 MiB under -race otherwise dominates CI.
func TestObservationAndNativeReadsAtDefaultHistoryCapacity(t *testing.T) {
	cfg := testConfig()
	agent := cfg.Agents["alice"]
	agent.Harness, agent.Interaction, agent.Command = "codex", "interactive", nil
	cfg.Agents["alice"] = agent
	s := testStore(t, cfg)
	ctx := context.Background()
	worker := credential(t, s, "bob")
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	payload := strings.Repeat("R", s.cfg.Limits.MaxMessageBytes)
	for i := 0; i < s.cfg.Limits.MaxTasks-1; i++ {
		if err = saveTask(ctx, transaction, taskRecord{Task: Task{ID: fmt.Sprintf("task_large_history_%04d", i), From: "alice", To: "bob", State: "submitted", Delivery: "acknowledged", Version: 2, Text: payload, Result: payload}}); err != nil {
			t.Fatal(err)
		}
	}
	live := taskRecord{Task: Task{ID: "task_live_monitor", From: ownerID, To: "bob", State: "running", Delivery: "dispatching", Version: 2, Text: payload}, Instance: worker.InstanceID, LeaseToken: "exact-live-lease", Expires: time.Now().Add(time.Hour).Unix()}
	if err = saveTask(ctx, transaction, live); err != nil {
		t.Fatal(err)
	}
	if err = transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	fixture := largeHistoryFixture{store: s, worker: worker, live: live}
	for _, check := range []struct {
		name string
		run  func(*testing.T, largeHistoryFixture)
	}{
		{"observation", assertObservationAtDefaultHistoryCapacity},
		{"native-summary", assertNativeSummaryAtDefaultHistoryCapacity},
		{"full-status", assertFullNativeStatusAtDefaultHistoryCapacity},
	} {
		t.Run(check.name, func(t *testing.T) {
			before := largeHistoryCounts(t, s)
			check.run(t, fixture)
			if after := largeHistoryCounts(t, s); after != before {
				t.Fatalf("operational read changed history/receipts/exposures: %v -> %v", before, after)
			}
			retained, err := loadTask(ctx, s.db, live.ID)
			if err != nil || !reflect.DeepEqual(retained, live) {
				t.Fatalf("operational read changed exact live execution attempt: %+v %v", retained, err)
			}
		})
	}
}

func largeHistoryCounts(t *testing.T, s *Store) [3]int {
	t.Helper()
	var counts [3]int
	if err := s.db.QueryRow("SELECT (SELECT COUNT(*) FROM records),(SELECT COUNT(*) FROM requests),(SELECT COUNT(*) FROM exposures)").Scan(&counts[0], &counts[1], &counts[2]); err != nil {
		t.Fatal(err)
	}
	return counts
}
