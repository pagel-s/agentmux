package managed

import (
	"context"
	"fmt"
	"testing"
	"time"

	"agentmux/internal/managedconfig"
	"agentmux/internal/nativebridge"
)

func seedNativeQueueResult(t *testing.T, s *Store, index int, target string, origin *TaskOrigin) string {
	t.Helper()
	id := fmt.Sprintf("task_queue_page_%04d", index)
	if err := saveTask(context.Background(), s.db, taskRecord{Task: Task{ID: id, From: "alice", To: target, State: "submitted", Delivery: "acknowledged", Version: 2, Result: "retained result", Origin: origin}}); err != nil {
		t.Fatal(err)
	}
	return id
}

func queueResultIDs(items []nativebridge.Delivery) []string {
	var result []string
	for _, item := range items {
		if item.Kind == "result" {
			result = append(result, item.ID)
		}
	}
	return result
}

func TestNativeCandidatePagesContinuePastIneligibleResults(t *testing.T) {
	for _, excluded := range []string{"seen", "revoked-target", "observation-origin", "hop-limit"} {
		t.Run(excluded, func(t *testing.T) {
			s := nativeStore(t)
			for i := 0; i < 205; i++ {
				target := "bob"
				var origin *TaskOrigin
				if excluded == "revoked-target" {
					target = "eve" // Alice has no command authority over Eve.
				}
				if excluded == "observation-origin" {
					origin = &TaskOrigin{Kind: "observation"}
				}
				if excluded == "hop-limit" {
					origin = &TaskOrigin{}
					origin.Hop = 4 // The returned result adds another causal hop.
				}
				id := seedNativeQueueResult(t, s, i, target, origin)
				if excluded == "seen" {
					nativeID := fmt.Sprintf("delivery_seen_%04d", i)
					if err := save(context.Background(), s.db, "native_delivery", nativeID, "alice", "alice", "result:"+id, nativeRecord{Delivery: nativebridge.Delivery{ID: nativeID, To: "alice"}, Phase: "completed"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			var want []string
			for i := 205; i < 210; i++ {
				want = append(want, "result:"+seedNativeQueueResult(t, s, i, "bob", nil))
			}
			items, err := s.nativeCandidates(context.Background(), s.db, "alice", NativePolicy{NativeDelivery: managedconfig.NativeDelivery{Results: true, MaxHops: 4}})
			if err != nil {
				t.Fatal(err)
			}
			got := queueResultIDs(items)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("eligibility filtering hid a later page or accepted an excluded result: got %v want %v", got, want)
			}
		})
	}
}

func TestNativeCandidateBoundPreservesInstructionThenResultOrder(t *testing.T) {
	s := nativeStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("task_instruction_priority_%d", i)
		if err := saveTask(ctx, s.db, taskRecord{Task: Task{ID: id, From: ownerID, To: "alice", State: "proposed", Delivery: "pending", Version: 1, Text: "explicit owner instruction"}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 250; i++ {
		seedNativeQueueResult(t, s, i, "bob", nil)
	}
	before, err := count(ctx, s.db, "task")
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.nativeCandidates(ctx, s.db, "alice", NativePolicy{NativeDelivery: managedconfig.NativeDelivery{Results: true, MaxHops: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 200 {
		t.Fatalf("bounded candidate window changed: got %d", len(items))
	}
	for i := 0; i < 3; i++ {
		if items[i].Kind != "instruction" || items[i].ID != fmt.Sprintf("task_instruction_priority_%d", i) {
			t.Fatalf("owner instruction priority/order changed at %d: %+v", i, items[i])
		}
	}
	for i, item := range items[3:] {
		if item.Kind != "result" || item.ID != fmt.Sprintf("result:task_queue_page_%04d", i) || item.Text != fmt.Sprintf("Result of your instruction task_queue_page_%04d (submitted):\nretained result", i) {
			t.Fatalf("result order/body changed at %d: %+v", i, item)
		}
	}
	after, err := count(ctx, s.db, "task")
	if err != nil || before != after {
		t.Fatalf("candidate inspection changed task history: before %d after %d err %v", before, after, err)
	}
}

func TestNativeStatusHistorySkipsObsoleteReceiptButKeepsChargeAndPause(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "auto", nil)
	instruction(t, s)
	delivery := nativeNext(t, s, "bob", "native-bob", "idle")
	if delivery == nil {
		t.Fatal("missing native delivery")
	}
	nativeReport(t, s, "bob", "native-bob", delivery, "running")
	ctx := context.Background()
	var obsolete nativeRecord
	if err := get(ctx, s.db, "native_delivery", delivery.ID, &obsolete); err != nil {
		t.Fatal(err)
	}
	// A newer retained receipt from an obsolete attempt must not hide the older
	// receipt to which this task still points. Its retained charge still counts.
	obsolete.ID, obsolete.Candidate = "native_obsolete_queue_fixture", "obsolete-candidate"
	if err := save(ctx, s.db, "native_delivery", obsolete.ID, "bob", "bob", obsolete.Candidate, obsolete); err != nil {
		t.Fatal(err)
	}
	if err := save(ctx, s.db, "native_usage", "native_usage:bob", ownerID, "bob", "owner", 2); err != nil {
		t.Fatal(err)
	}
	if err := saveStateRecoveryDigest(ctx, s.db, "native_usage", "bob", 2); err != nil {
		t.Fatal(err)
	}
	status := nativeGetStatus(t, s, "bob")
	if status.Turns != 2 || status.Active == nil || status.Active.ID != delivery.ID || status.State != "active" || status.Cancel {
		t.Fatalf("history shortcut lost charge or current receipt: %+v", status)
	}
	nativeMode(t, s, "bob", "manual", nil)
	paused := nativeGetStatus(t, s, "bob")
	if !paused.Cancel || paused.Active == nil || paused.Active.ID != delivery.ID || paused.Policy.Mode != "manual" {
		t.Fatalf("history shortcut prevented definite pause/cancel control: %+v", paused)
	}
}

func TestNativeCandidateOptionalTextFramingPreservesEmptyContent(t *testing.T) {
	for _, field := range []string{"text", "result"} {
		for _, framing := range []string{"missing", "null", "empty"} {
			t.Run(field+"/"+framing, func(t *testing.T) {
				s := nativeStore(t)
				recipient, candidate := "bob", "task_optional_native_content"
				task := taskRecord{Task: Task{ID: candidate, From: ownerID, To: "bob", State: "proposed", Delivery: "pending", Version: 1, Text: "source"}}
				if field == "result" {
					recipient, candidate = "alice", "result:"+task.ID
					task.From, task.State, task.Delivery, task.Result = "alice", "submitted", "acknowledged", "source"
				}
				if err := saveTask(context.Background(), s.db, task); err != nil {
					t.Fatal(err)
				}
				patch := "json_remove(data,?)"
				if framing == "null" {
					patch = "json_set(data,?,json('null'))"
				} else if framing == "empty" {
					patch = "json_set(data,?,'')"
				}
				if _, err := s.db.Exec("UPDATE records SET data="+patch+" WHERE id=?", "$."+field, task.ID); err != nil {
					t.Fatal(err)
				}
				status := nativeGetStatus(t, s, recipient)
				found := false
				for _, item := range status.Pending {
					if item.ID == candidate {
						found = true
						want := ""
						if field == "result" {
							want = "Result of your instruction " + task.ID + " (submitted):\n"
						}
						if item.Text != want {
							t.Fatalf("optional string changed during lazy hydration: got %q want %q", item.Text, want)
						}
					}
				}
				if !found {
					t.Fatalf("optional content hid the eligible candidate %q", candidate)
				}
			})
		}
	}
}

func TestNativeCandidateInvalidSelectedTextCannotReserveDelivery(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "auto", nil)
	ctx := context.Background()
	task := taskRecord{Task: Task{ID: "task_invalid_native_text", From: ownerID, To: "bob", State: "proposed", Delivery: "pending", Version: 1, Text: "source"}}
	if err := saveTask(ctx, s.db, task); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE records SET data=json_set(data,'$.text',42) WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	var registration nativeRegistration
	if err := get(ctx, s.db, "native_registration", "native_registration:bob", &registration); err != nil {
		t.Fatal(err)
	}
	s.nativePresence = map[string]nativePresence{"bob": {State: "idle", Instance: registration.Instance, Session: registration.Session, At: time.Now()}}
	expectFault(t, s, "bob", "native.claim", "internal", map[string]any{"session": "native-bob", "candidate": task.ID})
	var state, delivery string
	var version int
	if err := s.db.QueryRow("SELECT json_extract(data,'$.state'),json_extract(data,'$.delivery'),json_extract(data,'$.version') FROM records WHERE id=?", task.ID).Scan(&state, &delivery, &version); err != nil {
		t.Fatal(err)
	}
	if state != "proposed" || delivery != "pending" || version != 1 {
		t.Fatalf("invalid content acquired a lease: %s %s v%d", state, delivery, version)
	}
	if n, err := count(ctx, s.db, "native_delivery"); err != nil || n != 0 {
		t.Fatalf("invalid content created a native reservation: %d %v", n, err)
	}
}

func TestNativeStatusMissingTaskCannotReleaseUnresolvedNativeReceipt(t *testing.T) {
	s := nativeStore(t)
	nativeRegister(t, s, "bob", "native-bob")
	nativeMode(t, s, "bob", "auto", nil)
	instruction(t, s)
	delivery := nativeNext(t, s, "bob", "native-bob", "idle")
	if delivery == nil {
		t.Fatal("missing native delivery")
	}
	nativeReport(t, s, "bob", "native-bob", delivery, "running")
	// Missing task storage cannot prove that a previously dispatched native
	// process stopped. Keep the receipt's execution slot even if task lookup is
	// damaged; only a known obsolete or released task can release it.
	if _, err := s.db.Exec("DELETE FROM records WHERE kind='task' AND id=?", delivery.TaskID); err != nil {
		t.Fatal(err)
	}
	status := nativeGetStatus(t, s, "bob")
	if status.Active == nil || status.Active.ID != delivery.ID || status.State == "ready" {
		t.Fatalf("missing task was treated as stopped native execution: %+v", status)
	}
	call(t, s, "", "instruct", map[string]any{"to": []string{"bob"}, "text": "Do not dispatch while prior execution is unresolved"})
	if next := nativeNext(t, s, "bob", "native-bob", "idle"); next != nil {
		t.Fatalf("damaged storage enabled concurrent native execution: %+v", next)
	}
}

func TestNativeStatusInvalidDeliveryFramingCannotReleaseUnresolvedReceipt(t *testing.T) {
	for _, framing := range []string{"42", "true", "{}", "[]"} {
		t.Run(framing, func(t *testing.T) {
			s := nativeStore(t)
			nativeRegister(t, s, "bob", "native-bob")
			nativeMode(t, s, "bob", "auto", nil)
			instruction(t, s)
			delivery := nativeNext(t, s, "bob", "native-bob", "idle")
			if delivery == nil {
				t.Fatal("missing native delivery")
			}
			nativeReport(t, s, "bob", "native-bob", delivery, "running")
			if _, err := s.db.Exec("UPDATE records SET data=json_set(data,'$.delivery',json(?)) WHERE kind='task' AND id=?", framing, delivery.TaskID); err != nil {
				t.Fatal(err)
			}
			status := nativeGetStatus(t, s, "bob")
			if status.Active == nil || status.Active.ID != delivery.ID || status.State == "ready" {
				t.Fatalf("invalid delivery framing was treated as stopped native execution: %+v", status)
			}
			call(t, s, "", "instruct", map[string]any{"to": []string{"bob"}, "text": "Must not replace an unresolved native process"})
			if next := nativeNext(t, s, "bob", "native-bob", "idle"); next != nil {
				t.Fatalf("invalid slot metadata enabled concurrent native execution: %+v", next)
			}
		})
	}
}
