package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
	"agentmux/internal/research"
)

type studyWrapperAttempt struct {
	state  string
	client *managed.Client
	status *managed.ResearchStatus
	hash   string
}

type studyWrapperFixture struct {
	output string
	a, b   studyWrapperAttempt
}

func newStudyWrapperFixture(t *testing.T) studyWrapperFixture {
	t.Helper()
	output, err := os.MkdirTemp("", "amx-study-wrapper-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(output) })
	writeJSON := func(path string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := json.RawMessage(`{"version":1,"id":"private-wrapper-study"}`)
	manifestHash := fmt.Sprintf("%x", sha256.Sum256(manifest))
	writeJSON(filepath.Join(output, "manifest.json"), map[string]any{"sha256": manifestHash, "manifest": manifest})
	writeJSON(filepath.Join(output, "plan.json"), research.PlanResult{Trials: []research.Trial{{ID: "run-001", Condition: "controlled"}}, Participants: []string{"peer"}})
	makeAttempt := func(number int) studyWrapperAttempt {
		t.Helper()
		state := filepath.Join(output, "run-001", fmt.Sprintf("attempt-%03d", number), "state")
		if err := os.MkdirAll(filepath.Dir(state), 0700); err != nil {
			t.Fatal(err)
		}
		cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: fmt.Sprintf("study-attempt-%d", number), Agents: map[string]managedconfig.Agent{"peer": {Harness: "command", Command: []string{"/bin/cat"}}}}
		store, err := managed.Init(state, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		proof, err := store.InitResearchRunner()
		if err != nil {
			t.Fatal(err)
		}
		owner, err := managed.LoadCredential(managed.CredentialPath(state, ""))
		if err != nil {
			t.Fatal(err)
		}
		progress := managed.ResearchProgress{Trial: "run-001", Attempt: number, Condition: "controlled", StateDir: state, State: "paused", Population: []string{"peer"}, Tasks: map[string]string{}, Revision: 1, AckVersion: 1}
		args, err := json.Marshal(progress)
		if err != nil {
			t.Fatal(err)
		}
		raw, problem := store.Call(context.Background(), owner.Token, managed.Request{Operation: "research.publish", ResearchRunnerProof: proof, RequestID: "publish-fixture", Args: args})
		if problem != nil {
			t.Fatal(problem)
		}
		var published struct {
			Research *managed.ResearchStatus `json:"research"`
		}
		if err = json.Unmarshal(raw, &published); err != nil || published.Research == nil {
			t.Fatalf("published checkpoint missing: %s %v", raw, err)
		}
		// Frozen coordination history is private evidence, not a live broker.
		data, _ := json.Marshal(managed.Event{ID: fmt.Sprintf("fixture-event-%d", number), Actor: "peer", Type: "fixture.recorded", Details: map[string]any{"attempt": number}})
		record, _ := json.Marshal(managed.ExportRecord{Kind: "event", ID: fmt.Sprintf("fixture-event-%d", number), Data: data})
		if err = os.WriteFile(filepath.Join(filepath.Dir(state), "coordination.jsonl"), append(record, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		return studyWrapperAttempt{state: state, client: managed.NewClient(owner), status: published.Research, hash: research.AttemptIdentity(state, published.Research)}
	}
	fixture := studyWrapperFixture{output: output, a: makeAttempt(1), b: makeAttempt(2)}
	fixture.pointTo(t, fixture.a)
	return fixture
}

func (f studyWrapperFixture) pointTo(t *testing.T, attempt studyWrapperAttempt) {
	t.Helper()
	raw, err := json.Marshal(research.LiveView{Live: false, Research: attempt.status})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.output, "live.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

// Compare durable metadata/records/receipts rather than SQLite file bytes:
// opening authenticated read-only checkpoints may change WAL presentation.
func studyWrapperCheckpoint(t *testing.T, state string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(state, "state.sqlite")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT 'metadata',key,value FROM metadata UNION ALL SELECT 'record',id,data FROM records UNION ALL SELECT 'request',actor||'/'||request_id,digest||response FROM requests ORDER BY 1,2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var kind, id, data string
		if err = rows.Scan(&kind, &id, &data); err != nil {
			t.Fatal(err)
		}
		result = append(result, kind+"\x00"+id+"\x00"+data)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestWorkStudyAttemptFenceRoutesOnlyTheDisplayedAttempt(t *testing.T) {
	fixture := newStudyWrapperFixture(t)
	const host, token = "127.0.0.1:12345", "private-study-wrapper-capability"
	delegated := []string{}
	build := func(state string, client *managed.Client, capability, allowedHost string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if capability != token || allowedHost != host {
				t.Fatal("wrapper changed observer capability or host")
			}
			// The selected client must authenticate against this exact checkpoint;
			// forwarding A's owner credential to B cannot pass this assertion.
			status, problem := client.LocalResearchStatus(r.Context(), state)
			if problem != nil || status == nil {
				t.Fatalf("wrong attempt owner credential forwarded: %s %v", state, problem)
			}
			delegated = append(delegated, state)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "state": state})
		})
	}
	handler := workStudyHandler(fixture.a.state, fixture.a.client, token, host, build)
	request := func(t *testing.T, follow, attempt string, wantCode int, wantState string) {
		t.Helper()
		before := len(delegated)
		r := httptest.NewRequest(http.MethodPost, "http://"+host+"/api/call", strings.NewReader(`{"operation":"research.control","request_id":"frozen-form","args":{"version":1,"action":"pause","reason":"reviewed attempt"}}`))
		r.Host = host
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		if follow != "" {
			r.Header.Set("X-Agentmux-Follow-Study", follow)
		}
		if attempt != "" {
			r.Header.Set("X-Agentmux-Attempt", attempt)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != wantCode {
			t.Fatalf("follow=%q attempt=%q: %d %s", follow, attempt, w.Code, w.Body.String())
		}
		if wantState == "" {
			if len(delegated) != before {
				t.Fatalf("stale/missing attempt reached broker handler: %v", delegated[before:])
			}
			var failure struct {
				OK   bool   `json:"ok"`
				Code string `json:"code"`
			}
			json.Unmarshal(w.Body.Bytes(), &failure)
			if failure.OK || failure.Code != "conflict" {
				t.Fatalf("missing review conflict: %s", w.Body.String())
			}
		} else if len(delegated) != before+1 || delegated[before] != wantState {
			t.Fatalf("attempt routed to wrong checkpoint: %v, want %s", delegated[before:], wantState)
		}
	}
	for _, test := range []struct {
		name, follow, attempt string
		code                  int
		state                 string
	}{
		{"fixed missing hash", "", "", 409, ""},
		{"fixed stale hash", "", fixture.b.hash, 409, ""},
		{"fixed exact hash", "", fixture.a.hash, 200, fixture.a.state},
		{"follow missing hash", "1", "", 409, ""},
		{"follow stale hash", "1", fixture.b.hash, 409, ""},
		{"follow exact first attempt", "1", fixture.a.hash, 200, fixture.a.state},
	} {
		t.Run(test.name, func(t *testing.T) { request(t, test.follow, test.attempt, test.code, test.state) })
	}
	fixture.pointTo(t, fixture.b)
	for _, test := range []struct {
		name, follow, attempt string
		code                  int
		state                 string
	}{
		{"follow transition missing hash", "1", "", 409, ""},
		{"follow transition rejects old displayed attempt", "1", fixture.a.hash, 409, ""},
		{"follow exact second attempt", "1", fixture.b.hash, 200, fixture.b.state},
		{"fixed observer stays at original attempt", "0", fixture.a.hash, 200, fixture.a.state},
		{"turning follow off while displaying second attempt rejects original", "0", fixture.b.hash, 409, ""},
	} {
		t.Run(test.name, func(t *testing.T) { request(t, test.follow, test.attempt, test.code, test.state) })
	}
}

func TestWorkStudyReadEndpointsRequireCapabilityAndDoNotMutateCheckpoints(t *testing.T) {
	fixture := newStudyWrapperFixture(t)
	fixture.pointTo(t, fixture.b)
	const host, token = "127.0.0.1:12345", "private-study-read-capability"
	beforeA, beforeB := studyWrapperCheckpoint(t, fixture.a.state), studyWrapperCheckpoint(t, fixture.b.state)
	delegated := 0
	build := func(state string, client *managed.Client, capability, allowedHost string) http.Handler {
		protected := workWebHandler(state, client, capability, allowedHost)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { delegated++; protected.ServeHTTP(w, r) })
	}
	handler := workStudyHandler(fixture.a.state, fixture.a.client, token, host, build)
	for _, path := range []string{"/api/study", "/api/research-history?actor=peer&type=fixture.recorded&limit=1"} {
		for _, authorized := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/authorized=%t", path, authorized), func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
				r.Host = host
				if authorized {
					r.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				before := delegated
				handler.ServeHTTP(w, r)
				if !authorized {
					if w.Code != http.StatusUnauthorized || delegated != before+1 {
						t.Fatalf("unauthorized read bypassed base capability guard: %d %s", w.Code, w.Body.String())
					}
					for _, private := range []string{fixture.output, fixture.a.state, fixture.b.state, "run-001", "fixture.recorded", "current_state", "records"} {
						if strings.Contains(w.Body.String(), private) {
							t.Fatalf("unauthorized read disclosed study metadata %q: %s", private, w.Body.String())
						}
					}
					return
				}
				if w.Code != http.StatusOK || delegated != before || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("authenticated read failed or reached mutation handler: %d %s", w.Code, w.Body.String())
				}
				if strings.HasPrefix(path, "/api/study") {
					var out struct {
						OK    bool               `json:"ok"`
						Study research.StudyView `json:"study"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK || out.Study.AttemptID != fixture.b.hash || out.Study.CurrentState != fixture.b.state || out.Study.Live {
						t.Fatalf("study read did not reflect authenticated offline checkpoint B: %s %v", w.Body.String(), err)
					}
				} else {
					var out struct {
						OK      bool                 `json:"ok"`
						History research.HistoryPage `json:"history"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.OK || len(out.History.Records) != 1 || !out.History.HasMore || out.History.Records[0].Attempt != 1 {
						t.Fatalf("archived history paging failed: %s %v", w.Body.String(), err)
					}
				}
			})
		}
	}
	if !reflect.DeepEqual(beforeA, studyWrapperCheckpoint(t, fixture.a.state)) || !reflect.DeepEqual(beforeB, studyWrapperCheckpoint(t, fixture.b.state)) {
		t.Fatal("study/history GET changed durable metadata, records or receipts")
	}
}

func TestWorkStudyAttemptMatchesHeadlessIdentity(t *testing.T) {
	fixture := newStudyWrapperFixture(t)
	for _, attempt := range []studyWrapperAttempt{fixture.a, fixture.b} {
		progress := attempt.status.Progress
		want := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", attempt.state, progress.Trial, progress.Attempt))))
		if got := workStudyAttempt(attempt.state, managedlaunch.Runtime{AttemptID: "unrelated-tmux-attempt"}, attempt.status); got != want || got != research.AttemptIdentity(attempt.state, attempt.status) {
			t.Fatalf("web/headless attempt identity disagreed: got %q want %q", got, want)
		}
	}
	if fixture.a.hash == fixture.b.hash {
		t.Fatal("two attempts with the same trial/control versions reused a fence")
	}
	if got := workStudyAttempt(fixture.a.state, managedlaunch.Runtime{AttemptID: "native-workspace-attempt"}, nil); got != "native-workspace-attempt" {
		t.Fatalf("native workspace attempt fallback changed: %q", got)
	}
}
