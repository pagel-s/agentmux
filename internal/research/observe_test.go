package research

import (
	"agentmux/internal/managed"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func observationStudy(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	os.Chmod(root, 0700)
	manifest, err := Load(filepath.Join("..", "..", "examples", "research", "public-goods.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.ID = "fixture"
	manifest.Seeds = []int64{11}
	manifest.Conditions = manifest.Conditions[:2]
	plan, err := Plan(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, "manifest.json"), map[string]any{"sha256": digest(manifest), "manifest": manifest}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, "plan.json"), plan); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestStudyObservationRejectsPlanChangesAgainstFrozenManifest(t *testing.T) {
	for _, change := range []string{"omit", "reorder", "relabel"} {
		t.Run(change, func(t *testing.T) {
			root := observationStudy(t)
			plan, err := verifiedStudyPlan(root)
			if err != nil {
				t.Fatal(err)
			}
			for i, trial := range plan.Trials {
				dir := filepath.Join(root, trial.ID, "attempt-001")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					if err := writeJSON(filepath.Join(dir, "result.json"), Result{Trial: trial, Attempt: 1, Status: "completed"}); err != nil {
						t.Fatal(err)
					}
				}
				record, _ := json.Marshal(managed.ExportRecord{Kind: "event", ID: trial.ID, Data: json.RawMessage(`{"actor":"alice"}`)})
				if err := os.WriteFile(filepath.Join(dir, "coordination.jsonl"), append(record, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := StudyStatus(context.Background(), root)
			if err != nil || before.Completed != 1 || before.Total != 2 || before.State == "completed" {
				t.Fatalf("valid initial study: %+v %v", before, err)
			}
			history, err := ReadHistory(root, HistoryFilter{Limit: 1})
			if err != nil || len(history.Records) != 1 || !history.HasMore {
				t.Fatalf("valid initial history: %+v %v", history, err)
			}
			switch change {
			case "omit":
				plan.Trials = plan.Trials[:1]
			case "reorder":
				plan.Trials[0], plan.Trials[1] = plan.Trials[1], plan.Trials[0]
			case "relabel":
				plan.Trials[0].Condition = "false-condition"
			}
			if err := writeJSON(filepath.Join(root, "plan.json"), plan); err != nil {
				t.Fatal(err)
			}
			if _, err := StudyStatus(context.Background(), root); err == nil || !strings.Contains(err.Error(), "frozen manifest") {
				t.Fatalf("accepted changed completion/condition matrix: %v", err)
			}
			if _, err := ReadHistory(root, HistoryFilter{After: history.Cursor}); err == nil || !strings.Contains(err.Error(), "frozen manifest") {
				t.Fatalf("accepted changed archive ordering: %v", err)
			}
		})
	}
}

func TestArchivedExposureHistoryCanBeFilteredByItsReader(t *testing.T) {
	root := observationStudy(t)
	dir := filepath.Join(root, "run-00001", "attempt-001")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "coordination.jsonl"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, reader := range []string{"alice", "bob", "alice"} {
		data, _ := json.Marshal(map[string]any{"reader": reader, "kind": "memory", "entity_id": "notes/spec", "version": 3})
		if err := json.NewEncoder(f).Encode(managed.ExportRecord{Kind: "exposure", ID: reader, Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := ReadHistory(root, HistoryFilter{Actor: "alice", Type: "exposure", Limit: 1})
	if err != nil || len(page.Records) != 1 || !page.HasMore {
		t.Fatalf("reader first page: %+v %v", page, err)
	}
	next, err := ReadHistory(root, HistoryFilter{Actor: "alice", Type: "exposure", Limit: 1, After: page.Cursor})
	if err != nil || len(next.Records) != 1 || next.HasMore || next.Records[0].Sequence != 3 {
		t.Fatalf("reader next page: %+v %v", next, err)
	}
}

func TestStudyDoesNotInferRunningFromMissingResultAndRetainsOfflineStop(t *testing.T) {
	for _, state := range []string{"starting", "stopped", "interrupted"} {
		t.Run(state, func(t *testing.T) {
			root := observationStudy(t)
			plan, err := verifiedStudyPlan(root)
			if err != nil {
				t.Fatal(err)
			}
			stateDir := filepath.Join(root, "run-00001", "attempt-001", "state")
			if err := os.MkdirAll(stateDir, 0700); err != nil {
				t.Fatal(err)
			}
			view, err := StudyStatus(context.Background(), root)
			if err != nil || view.Results[0].State != "incomplete" || view.Live {
				t.Fatalf("missing result: %+v %v", view, err)
			}
			manifest, err := Load(filepath.Join("..", "..", "examples", "research", "public-goods.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			store, err := managed.Init(stateDir, manifest.Config)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := store.InitResearchRunner()
			if err != nil {
				t.Fatal(err)
			}
			owner, err := managed.LoadCredential(managed.CredentialPath(stateDir, ""))
			if err != nil {
				t.Fatal(err)
			}
			l := &liveRunner{path: filepath.Join(root, "live.json"), budget: &modelBudget{}, progress: managed.ResearchProgress{Trial: "run-00001", Attempt: 1, Condition: plan.Trials[0].Condition, StateDir: stateDir, State: state, Tasks: map[string]string{}, Population: []string{"alice", "bob"}, AckVersion: 1, Deadline: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}}
			l.call = func(ctx context.Context, op string, args any) (json.RawMessage, error) {
				data, _ := json.Marshal(args)
				raw, fault := store.Call(ctx, owner.Token, managed.Request{Operation: op, Args: data, ResearchRunnerProof: proof, RequestID: "publish"})
				if fault != nil {
					return nil, fault
				}
				return raw, nil
			}
			if _, err := l.publish(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			view, err = StudyStatus(context.Background(), root)
			want := state
			if state == "starting" {
				want = "unavailable"
			}
			if err != nil || view.Live || view.State != want || (state != "starting" && view.Results[0].State != state) {
				t.Fatalf("offline %s: %+v %v", state, view, err)
			}
		})
	}
}

func TestReadReportValidatesAndBackfillsFrozenPopulationPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{ID: "legacy", Population: Population{Mode: "dynamic"}}
	if err := writeJSON(filepath.Join(root, "manifest.json"), map[string]any{"sha256": digest(manifest), "manifest": manifest}); err != nil {
		t.Fatal(err)
	}
	report := Report{Research: "legacy", ManifestHash: digest(manifest)}
	if err := writeJSON(filepath.Join(root, "results.json"), report); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadReport(root)
	if err != nil || loaded.Population != manifest.Population || Analyze(loaded).PopulationPolicy.Mode != "dynamic" {
		t.Fatalf("legacy policy backfill: %+v %v", loaded, err)
	}
	report.Population.Mode = "fixed"
	if err := writeJSON(filepath.Join(root, "results.json"), report); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReport(root); err == nil {
		t.Fatal("accepted contradictory population policy")
	}
}
func TestStudyObservationPersistsAcrossFiniteTrialsAndReadsArchivedHistory(t *testing.T) {
	root := observationStudy(t)
	plan, err := verifiedStudyPlan(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		trial := fmt.Sprintf("run-%05d", i)
		dir := filepath.Join(root, trial, "attempt-001")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(filepath.Join(dir, "result.json"), Result{Trial: plan.Trials[i-1], Attempt: 1, Status: "completed"}); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			view, err := StudyStatus(context.Background(), root)
			if err != nil || view.Completed != 1 || view.Total != 2 || view.State == "completed" {
				t.Fatalf("between attempts: %+v %v", view, err)
			}
		}
		f, err := os.OpenFile(filepath.Join(dir, "coordination.jsonl"), os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 60; n++ {
			data, _ := json.Marshal(managed.Event{ID: fmt.Sprint(i, n), Actor: "alice", Type: "team.joined"})
			if err := json.NewEncoder(f).Encode(managed.ExportRecord{Kind: "event", ID: fmt.Sprint(i, n), Data: data}); err != nil {
				t.Fatal(err)
			}
		}
		f.Close()
	}
	view, err := StudyStatus(context.Background(), root)
	if err != nil || view.Completed != 2 || view.State != "completed" {
		t.Fatalf("completed: %+v %v", view, err)
	}
	first, err := ReadHistory(root, HistoryFilter{Actor: "alice", Type: "event", Limit: 70})
	if err != nil || len(first.Records) != 70 || !first.HasMore {
		t.Fatalf("first history page: %d %+v %v", len(first.Records), first, err)
	}
	second, err := ReadHistory(root, HistoryFilter{Actor: "alice", Type: "event", Limit: 70, After: first.Cursor})
	if err != nil || len(second.Records) != 50 || second.HasMore || second.Records[0].Sequence != 71 {
		t.Fatalf("second history page: %+v %v", second, err)
	}
}
func TestValueChoicesAndPopulationLimitationsSurvivePromptFreeExport(t *testing.T) {
	partner := "bob"
	project := 1
	round := Outcome{Round: 1, Actions: map[string]ActionResult{"alice": {Valid: true, Action: Action{Project: &project, RewardTeam: "team-a"}}}, Exchange: &ExchangeOutcome{Investments: map[string]InvestmentAction{"alice": {To: &partner, Amount: 2}}, Transfers: []Transfer{{From: "bob", To: "alice", Escrow: 8, Returned: 3}}}}
	result := Result{Trial: Trial{ID: "run-1", Condition: "a"}, Status: "completed", ActorMetadata: map[string]AgentLabel{"alice": {Parent: "parent"}}, Phases: []Phase{{Round: 1, Kind: "invest", Tasks: map[string]managed.Task{"alice": {ID: "task-choice", State: "verified"}}}}, Population: []PopulationSnapshot{{Round: 1, Actors: []string{"alice", "bob"}, Changes: []PopulationChange{{Actor: "alice", Kind: "joined"}}}}, Rounds: []Outcome{round}}
	report := Report{Population: Population{Mode: "dynamic"}, Results: []Result{result}}
	rows := Behaviors(report)
	if len(rows) != 1 || rows[0].Parent != "parent" || rows[0].InvestedTo == nil || *rows[0].InvestedTo != "bob" || rows[0].ReceivedEscrow != 8 || rows[0].PhaseTaskIDs["invest"] != "task-choice" {
		t.Fatalf("decision provenance: %+v", rows)
	}
	analysis := Analyze(report)
	found := false
	for _, warning := range analysis.EvidenceWarnings {
		if strings.Contains(warning, "not matched") {
			found = true
		}
	}
	if !found || analysis.BaselineScope != "initial_fixed_population" {
		t.Fatalf("baseline comparability lost: %+v", analysis)
	}
}
