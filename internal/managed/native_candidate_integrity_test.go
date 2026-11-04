package managed

import (
	"agentmux/internal/nativebridge"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// These regressions protect the boundary between indexed native candidate reads
// and the full Go interpretation used for approval and execution authority.
func TestNativeRetainedCaseAliasSourceCannotBypassAuthority(t *testing.T) {
	for _, variant := range []string{"sender", "origin"} {
		t.Run(variant, func(t *testing.T) {
			s := nativeStore(t)
			nativeRegister(t, s, "bob", "native-bob")
			nativeMode(t, s, "bob", "auto", nil)
			ctx := context.Background()
			task := taskRecord{Task: Task{ID: "probe_alias_source", From: ownerID, To: "bob", State: "proposed", Delivery: "pending", Version: 1, Text: "explicit content", Origin: &TaskOrigin{Kind: "instruction", Provenance: Provenance{Hop: 0}}}}
			if err := saveTask(ctx, s.db, task); err != nil {
				t.Fatal(err)
			}
			patch := `json_set(data,'$.From','eve')`
			if variant == "origin" {
				patch = `json_set(data,'$.Origin',json('{"kind":"instruction","hop":5}'))`
			}
			if _, err := s.db.Exec("UPDATE records SET data="+patch+" WHERE kind='task' AND id=?", task.ID); err != nil {
				t.Fatal(err)
			}
			var reg nativeRegistration
			if err := get(ctx, s.db, "native_registration", "native_registration:bob", &reg); err != nil {
				t.Fatal(err)
			}
			s.nativePresence = map[string]nativePresence{"bob": {State: "idle", Instance: reg.Instance, Session: reg.Session, At: time.Now()}}
			_, problem := invoke(s, credential(t, s, "bob"), "native.claim", map[string]any{"session": "native-bob", "candidate": task.ID}, randomID("probe"))
			if problem == nil {
				t.Fatal("case-alias authority/provenance bypass reserved delivery")
			}
			if n, err := count(ctx, s.db, "native_delivery"); err != nil || n != 0 {
				t.Fatalf("authority change reserved native turn: %d %v", n, err)
			}
		})
	}
}

func TestNativeRetainedResultCaseAliasCannotBypassAuthority(t *testing.T) {
	for _, variant := range []string{"sender", "target", "origin"} {
		t.Run(variant, func(t *testing.T) {
			s := nativeStore(t)
			nativeRegister(t, s, "alice", "native-alice")
			nativeMode(t, s, "alice", "auto", nil)
			ctx := context.Background()
			task := taskRecord{Task: Task{ID: "probe_result_alias_source", From: "alice", To: "bob", State: "submitted", Delivery: "acknowledged", Version: 1, Text: "original instruction", Result: "retained result", Origin: &TaskOrigin{Kind: "instruction", Provenance: Provenance{Hop: 0}}}}
			if err := saveTask(ctx, s.db, task); err != nil {
				t.Fatal(err)
			}
			patch := `json_set(data,'$.From','eve')`
			switch variant {
			case "target":
				patch = `json_set(data,'$.To','eve')`
			case "origin":
				patch = `json_set(data,'$.Origin',json('{"kind":"observation","hop":1}'))`
			}
			if _, err := s.db.Exec("UPDATE records SET data="+patch+" WHERE kind='task' AND id=?", task.ID); err != nil {
				t.Fatal(err)
			}
			var reg nativeRegistration
			if err := get(ctx, s.db, "native_registration", "native_registration:alice", &reg); err != nil {
				t.Fatal(err)
			}
			s.nativePresence = map[string]nativePresence{"alice": {State: "idle", Instance: reg.Instance, Session: reg.Session, At: time.Now()}}
			_, problem := invoke(s, credential(t, s, "alice"), "native.claim", map[string]any{"session": "native-alice", "candidate": "result:" + task.ID}, randomID("probe"))
			if problem == nil {
				t.Fatal("case-alias result authority bypass reserved delivery")
			}
			if n, err := count(ctx, s.db, "native_delivery"); err != nil || n != 0 {
				t.Fatalf("result authority change reserved native turn: %d %v", n, err)
			}
		})
	}
}

func TestNativeRetainedReviewedBodyAliasCannotChangeAtClaim(t *testing.T) {
	for _, kind := range []string{"instruction", "result"} {
		t.Run(kind, func(t *testing.T) {
			s := nativeStore(t)
			id := "bob"
			session := "native-bob"
			if kind == "result" {
				id = "alice"
				session = "native-alice"
			}
			nativeRegister(t, s, id, session)
			nativeMode(t, s, id, "review", nil)
			ctx := context.Background()
			task := taskRecord{Task: Task{ID: "probe_review_body_alias", From: ownerID, To: "bob", State: "proposed", Delivery: "pending", Version: 1, Text: "reviewed benign text"}}
			candidate := task.ID
			patch := `json_set(data,'$.Text','unreviewed replacement text')`
			if kind == "result" {
				task.From = "alice"
				task.To = "bob"
				task.State = "submitted"
				task.Delivery = "acknowledged"
				task.Result = "reviewed benign result"
				candidate = "result:" + task.ID
				patch = `json_set(data,'$.Result','unreviewed replacement result')`
			}
			if err := saveTask(ctx, s.db, task); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec("UPDATE records SET data="+patch+" WHERE kind='task' AND id=?", task.ID); err != nil {
				t.Fatal(err)
			}
			rawStatus, statusProblem := invoke(s, credential(t, s, ""), "native.status", map[string]any{"agent": id}, "")
			if statusProblem != nil {
				if n, err := count(ctx, s.db, "native_delivery"); err != nil || n != 0 {
					t.Fatalf("refused source created delivery: %d %v", n, err)
				}
				return
			}
			var statusResult struct {
				Status NativeStatus `json:"status"`
			}
			if err := json.Unmarshal(rawStatus, &statusResult); err != nil {
				t.Fatal(err)
			}
			st := statusResult.Status
			if !st.PendingKnown {
				if len(st.Pending) != 0 || st.Coverage == "" {
					t.Fatalf("unknown queue leaked source: %+v", st)
				}
				if n, err := count(ctx, s.db, "native_delivery"); err != nil || n != 0 {
					t.Fatalf("quarantined source reserved delivery: %d %v", n, err)
				}
				return
			}
			reviewed := ""
			for _, item := range st.Pending {
				if item.ID == candidate {
					reviewed = item.Text
				}
			}
			if reviewed == "" {
				t.Fatal("missing review body fixture")
			}
			_, problem := invoke(s, credential(t, s, ""), "native.approve", map[string]any{"agent": id, "version": st.Policy.Version, "candidate": candidate}, randomID("probe"))
			if problem != nil {
				return
			}
			var peek struct {
				Candidate string `json:"candidate"`
			}
			_ = json.Unmarshal(call(t, s, id, "native.peek", map[string]any{"session": session, "state": "idle"}), &peek)
			if peek.Candidate != candidate {
				return
			}
			raw, problem := invoke(s, credential(t, s, id), "native.claim", map[string]any{"session": session, "candidate": candidate}, randomID("probe"))
			if problem != nil {
				return
			}
			var out struct {
				Delivery *nativebridge.Delivery `json:"delivery"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			if out.Delivery != nil && out.Delivery.Text != reviewed {
				t.Fatalf("review approved different bytes: reviewed=%q claimed=%q", reviewed, out.Delivery.Text)
			}
		})
	}
}

func TestNativeRetainedObservationRetrySourceValidation(t *testing.T) {
	for _, mode := range []string{"auto", "review"} {
		for _, variant := range []string{"origin-alias", "invalid-trace"} {
			t.Run(mode+"/"+variant, func(t *testing.T) {
				s := nativeStore(t)
				nativeRegister(t, s, "bob", "native-bob")
				nativeMode(t, s, "bob", "auto", map[string]any{"notes": true})
				call(t, s, "alice", "message.send", map[string]any{"to": []string{"bob"}, "text": "source note"})
				d := nativeNext(t, s, "bob", "native-bob", "idle")
				nativeReport(t, s, "bob", "native-bob", d, "running")
				nativeReport(t, s, "bob", "native-bob", d, "uncertain")
				task := taskIn(t, call(t, s, "", "task.get", map[string]any{"id": d.TaskID}))
				call(t, s, "", "task.resolve", map[string]any{"id": task.ID, "version": task.Version, "resolution": "retry"})
				nativeMode(t, s, "bob", mode, nil)
				ctx := context.Background()
				stored, err := loadTask(ctx, s.db, d.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				candidate := fmt.Sprintf("retry:%s:%d", stored.ID, stored.Version)
				status := nativeGetStatus(t, s, "bob")
				patch := `json_set(data,'$.Origin',json('{"kind":"instruction","hop":5}'))`
				if variant == "invalid-trace" {
					patch = `json_set(data,'$.trace',json('true'))`
				}
				if _, err := s.db.Exec("UPDATE records SET data="+patch+" WHERE kind='task' AND id=?", d.TaskID); err != nil {
					t.Fatal(err)
				}
				op := "native.claim"
				actorID := "bob"
				args := map[string]any{"session": "native-bob", "candidate": candidate}
				if mode == "review" {
					op = "native.approve"
					actorID = ""
					args = map[string]any{"agent": "bob", "version": status.Policy.Version, "candidate": candidate}
				}
				raw, problem := invoke(s, credential(t, s, actorID), op, args, randomID("probe"))
				if problem == nil {
					if mode == "review" {
						t.Fatal("damaged observation retry was approved")
					}
					var out struct {
						Delivery *nativebridge.Delivery `json:"delivery"`
					}
					if err := json.Unmarshal(raw, &out); err != nil {
						t.Fatal(err)
					}
					if out.Delivery != nil {
						t.Fatal("damaged observation retry was reserved")
					}
				}
				if n, err := count(ctx, s.db, "native_delivery"); err != nil || n != 1 {
					t.Fatalf("damaged retry acquired extra reservation: %d %v", n, err)
				}
			})
		}
	}
}

func TestNativeRetainedFullStatusSourceAliasCannotExposeDeniedBody(t *testing.T) {
	for _, variant := range []string{"sender", "target", "origin"} {
		t.Run(variant, func(t *testing.T) {
			s := nativeStore(t)
			nativeRegister(t, s, "alice", "native-alice")
			nativeMode(t, s, "alice", "auto", nil)
			ctx := context.Background()
			task := taskRecord{Task: Task{ID: "probe_status_result_source", From: "alice", To: "bob", State: "submitted", Delivery: "acknowledged", Version: 1, Result: "DENIED_RESULT_BODY_SENTINEL", Origin: &TaskOrigin{Kind: "instruction"}}}
			if err := saveTask(ctx, s.db, task); err != nil {
				t.Fatal(err)
			}
			patch := `json_set(data,'$.From','eve')`
			switch variant {
			case "target":
				patch = `json_set(data,'$.To','eve')`
			case "origin":
				patch = `json_set(data,'$.Origin',json('{"kind":"observation","hop":1}'))`
			}
			if _, err := s.db.Exec("UPDATE records SET data="+patch+" WHERE id=?", task.ID); err != nil {
				t.Fatal(err)
			}
			raw, problem := invoke(s, credential(t, s, "alice"), "native.status", map[string]any{}, randomID("probe"))
			if problem != nil {
				return
			}
			if strings.Contains(string(raw), "DENIED_RESULT_BODY_SENTINEL") {
				t.Fatal("full credential-bound status exposed source body denied by Go authority")
			}
		})
	}
}
