package managed

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func nativeSummaryIn(t *testing.T, s *Store) map[string]NativeStatus {
	t.Helper()
	var result struct {
		Statuses []NativeStatus `json:"statuses"`
	}
	if err := json.Unmarshal(call(t, s, "", "native.status", map[string]any{"summary": true}), &result); err != nil {
		t.Fatal(err)
	}
	statuses := map[string]NativeStatus{}
	for _, status := range result.Statuses {
		statuses[status.Agent] = status
	}
	return statuses
}

func TestNativeSummarySeparatesUnknownAdmissionFromRetainedTransportAndReadiness(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "auto", nil)
	instruction(t, s)
	delivery := nativeNext(t, s, "bob", "native-bob", "idle")
	if delivery == nil {
		t.Fatal("missing native delivery fixture")
	}
	nativeReport(t, s, "bob", "native-bob", delivery, "running")
	fresh := nativeSummaryIn(t, s)["bob"]
	if !fresh.Connected || fresh.ReadinessObservedAt == "" || fresh.PendingKnown || fresh.Coverage != nativeObservationCoverage || len(fresh.Pending) != 0 || fresh.Active == nil || fresh.Active.ID != delivery.ID || fresh.Active.Phase != "running" || fresh.State != "retained_delivery" {
		t.Fatalf("summary inferred admission or lost transport/readiness: %+v", fresh)
	}
	if fresh.Active.NativeRef != "" || fresh.Active.Session != "" || fresh.Active.Reason != "" || fresh.Session != "" || fresh.Cancel {
		t.Fatal("summary exposed content-bearing references or inferred cancellation")
	}
	if !nativeGetStatus(t, s, "bob").PendingKnown {
		t.Fatal("full status no longer evaluates its existing pending inventory")
	}
	presence := s.nativePresence["bob"]
	presence.At = time.Now().Add(-time.Minute)
	s.nativePresence["bob"] = presence
	stale := nativeSummaryIn(t, s)["bob"]
	if stale.Connected || !reflect.DeepEqual(stale.Active, fresh.Active) || stale.ReadinessObservedAt == "" {
		t.Fatalf("stale reporting rewrote the retained receipt: %+v", stale)
	}
	nativeRegister(t, s, "bob", "replacement-native-bob")
	replaced := nativeSummaryIn(t, s)["bob"]
	var retained nativeRecord
	if err := get(context.Background(), s.db, "native_delivery", delivery.ID, &retained); err != nil {
		t.Fatal(err)
	}
	if replaced.Connected || replaced.ReadinessObservedAt != "" || replaced.State != "uncertain" || replaced.Active == nil || replaced.Active.Phase != retained.Phase {
		t.Fatalf("replacement borrowed readiness or confirmed old execution: %+v", replaced)
	}
	if _, err := s.Rotate("bob"); err != nil {
		t.Fatal(err)
	}
	rotated := nativeSummaryIn(t, s)["bob"]
	if rotated.Connected || rotated.ReadinessObservedAt != "" || rotated.State != "uncertain" {
		t.Fatalf("rotation inherited native readiness: %+v", rotated)
	}
}

func TestNativeSummaryInventoryAuthorizationPaginationAndRetiredOverrides(t *testing.T) {
	s := nativeStore(t)
	expectFault(t, s, "alice", "native.status", "invalid", map[string]any{"summary": true})
	expectFault(t, s, "", "native.status", "invalid", map[string]any{"summary": true, "agent": "bob"})
	var result struct {
		Statuses []NativeStatus `json:"statuses"`
		Cursor   string         `json:"cursor"`
		HasMore  bool           `json:"has_more"`
	}
	if err := json.Unmarshal(call(t, s, "", "native.status", map[string]any{"summary": true, "limit": 1}), &result); err != nil || len(result.Statuses) != 1 || result.Statuses[0].Agent != "alice" || !result.HasMore {
		t.Fatalf("bad summary first page: %+v %v", result, err)
	}
	if err := json.Unmarshal(call(t, s, "", "native.status", map[string]any{"summary": true, "limit": 1, "after": result.Cursor}), &result); err != nil || len(result.Statuses) != 1 || result.Statuses[0].Agent != "bob" || result.HasMore {
		t.Fatalf("bad summary second page: %+v %v", result, err)
	}
	call(t, s, "", "agent.stop", map[string]any{"id": "alice"})
	call(t, s, "", "agent.retire", map[string]any{"agent": "alice"})
	if _, found := nativeSummaryIn(t, s)["alice"]; found {
		t.Fatal("static config resurrected a retired registry override")
	}
}

func assertNativeSummaryAtDefaultHistoryCapacity(t *testing.T, fixture largeHistoryFixture) {
	t.Helper()
	s := fixture.store

	// Candidate evaluation would read/recheck every result and can hold the
	// broker lock beyond the worker monitor deadline. Summary never needs it.
	started := time.Now()
	statuses := nativeSummaryIn(t, s)
	if len(statuses) != 1 || statuses["alice"].Agent != "alice" || statuses["alice"].PendingKnown || len(statuses["alice"].Pending) != 0 || statuses["alice"].Coverage == "" {
		t.Fatalf("native summary lost unknown-pending semantics: %+v", statuses)
	}
	t.Logf("native summary over full default peer-result history: %s", time.Since(started))
}
