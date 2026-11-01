package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/research"
)

func TestResearchPlanIsReadOnlyAndReportsTaskBudget(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runResearchIO(context.Background(), []string{"plan", filepath.Join("..", "..", "examples", "research", "public-goods.yaml")}, &stdout, &stderr)
	if code != exitSuccess {
		t.Fatalf("plan: %d %s", code, stderr.String())
	}
	var envelope struct {
		OK     bool                `json:"ok"`
		Result research.PlanResult `json:"result"`
	}
	if json.Unmarshal(stdout.Bytes(), &envelope) != nil || !envelope.OK || envelope.Result.TotalTasks != 72 {
		t.Fatalf("invalid planning envelope: %s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := runResearchIO(context.Background(), []string{"plan", "ignored.yaml", "--allow-models"}, &stdout, &stderr); code != exitValidation {
		t.Fatal("plan accepted execution opt-in")
	}
}

func TestResearchLiveCLIRequiresVersionAndReason(t *testing.T) {
	for _, args := range [][]string{
		{"pause", "unused"},
		{"step", "unused", "--version", "1"},
		{"stop", "unused", "--reason", "inspect"},
		{"status", "unused", "--version", "1"},
		{"watch", "unused", "--reason", "inspect"},
	} {
		var stdout, stderr bytes.Buffer
		if code := runResearchIO(context.Background(), args, &stdout, &stderr); code != exitValidation {
			t.Fatalf("accepted unaudited/ambiguous live request %v: %d %s", args, code, stderr.String())
		}
	}
}

func TestResearchAnalysisCSVIsPrivateAndContainsNoObservations(t *testing.T) {
	dir := t.TempDir()
	report := research.Report{Research: "test", ManifestHash: "hash", Results: []research.Result{{Status: "completed", Trial: research.Trial{Condition: "shared", Seed: 1, Repetition: 1}, Rounds: []research.Outcome{{SocialWelfare: 5, Funded: true}}}}}
	data, _ := json.Marshal(report)
	os.WriteFile(filepath.Join(dir, "results.json"), data, 0600)
	path := filepath.Join(dir, "analysis.csv")
	var stdout, stderr bytes.Buffer
	if code := runResearchIO(context.Background(), []string{"analyze", dir, "--csv", path}, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("analyze: %d %s", code, stderr.String())
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("analysis CSV not private")
	}
	if strings.Contains(stdout.String(), "private_bit") || !strings.Contains(stdout.String(), "total_social_welfare") {
		t.Fatal("analysis unexpectedly includes private observations or omits objective metrics")
	}
	if code := runResearchIO(context.Background(), []string{"analyze", dir, "--csv", path}, &stdout, &stderr); code != exitRuntime {
		t.Fatal("CSV overwrote an existing artifact")
	}
}

func TestResearchBehaviorCSVExportsDecisionsWithoutPrompts(t *testing.T) {
	dir := t.TempDir()
	report := research.Report{
		Research: "test", ManifestHash: "hash",
		Results: []research.Result{{
			Status: "completed", Attempt: 1,
			Trial:         research.Trial{ID: "run-1", Condition: "individual", Seed: 3, Repetition: 1, Order: 4},
			ConfigHash:    "config-hash",
			ActorMetadata: map[string]research.AgentLabel{"alice": {Harness: "command", Model: "fixture"}},
			Phases:        []research.Phase{{Instructions: map[string]string{"alice": "PRIVATE_OBSERVATION_SENTINEL"}}},
			Rounds:        []research.Outcome{{Round: 1, Actions: map[string]research.ActionResult{"alice": {Valid: true, Action: research.Action{Contribute: 2}}}, Utilities: map[string]float64{"alice": 1}, Payoffs: map[string]float64{"alice": 1}, Credits: map[string]int{"alice": 2}}},
		}},
	}
	data, _ := json.Marshal(report)
	if err := os.WriteFile(filepath.Join(dir, "results.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "behavior.csv")
	var stdout, stderr bytes.Buffer
	if code := runResearchIO(context.Background(), []string{"analyze", dir, "--behavior-csv", path}, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("behavior export: %d %s", code, stderr.String())
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("behavior CSV is not private")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(content)).ReadAll()
	if err != nil || len(rows) != 2 || len(rows[0]) != 26 || len(rows[1][0]) != 24 || rows[1][5] != "alice" || rows[1][10] != "2" {
		t.Fatalf("unexpected behavior rows: %v %v", rows, err)
	}
	if rows[1][18] != "test" || rows[1][19] != "hash" || rows[1][20] != "3" || rows[1][21] != "1" || rows[1][22] != "4" || rows[1][23] != "1" || rows[1][24] != "config-hash" {
		t.Fatalf("behavior export lost study/block/attempt provenance: %v", rows[1])
	}
	if strings.Contains(string(content)+stdout.String(), "PRIVATE_OBSERVATION_SENTINEL") {
		t.Fatal("behavior export leaked prompt text")
	}
	if code := runResearchIO(context.Background(), []string{"analyze", dir, "--behavior-csv", path}, &stdout, &stderr); code != exitRuntime {
		t.Fatal("behavior export overwrote an existing artifact")
	}
}

func TestResearchCSVRetainsInterpretationAndExclusionsWithoutEstimates(t *testing.T) {
	a := research.Analysis{Research: "portable", ManifestHash: "study-hash", IntervenedRuns: 2, InterruptedRuns: 1, Interpretation: "Exploratory only", PowerWarning: "Too few blocks", EvidenceWarnings: []string{"Owner-intervened runs excluded"}}
	path := filepath.Join(t.TempDir(), "summary.csv")
	if err := writeResearchCSV(path, a); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil || len(rows) != 2 || rows[1][0] != "" || rows[1][7] != a.Research || rows[1][8] != a.ManifestHash || rows[1][9] != "2" || rows[1][10] != "1" || rows[1][12] != a.PowerWarning {
		t.Fatalf("metadata lost: %v %v", rows, err)
	}
	var decoded research.Analysis
	if err := json.Unmarshal([]byte(rows[1][13]), &decoded); err != nil || len(decoded.EvidenceWarnings) != 1 || decoded.EvidenceWarnings[0] != a.EvidenceWarnings[0] {
		t.Fatalf("analysis metadata lost: %+v %v", decoded, err)
	}
}

func TestResearchCSVExportsSelectedPrimaryOutcomeAndMetricSpecificN(t *testing.T) {
	a := research.Analysis{Research: "selected", ManifestHash: "hash", PrimaryOutcome: "teams_with_multiple_members", Conditions: []research.ConditionSummary{{Condition: "c", CompletedRuns: 4, SocialWelfare: research.Estimate{N: 4, Mean: -20}, PrimaryEstimate: research.Estimate{N: 3, Mean: 2}}}, PairedContrasts: []research.Contrast{{From: "a", To: "c", PrimaryOutcome: "teams_with_multiple_members", PrimaryDifference: research.Estimate{N: 2, Mean: 1}}}}
	path := filepath.Join(t.TempDir(), "selected.csv")
	if err := writeResearchCSV(path, a); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil || len(rows) != 2 || len(rows[0]) != 17 || rows[0][14] != "primary_outcome" || rows[1][2] != "-20" || rows[1][14] != a.PrimaryOutcome || rows[1][15] != "2" || rows[1][16] != "3" {
		t.Fatalf("selected metric lost or confused with legacy welfare: %v %v", rows, err)
	}
	var portable research.Analysis
	if err := json.Unmarshal([]byte(rows[1][13]), &portable); err != nil || portable.PairedContrasts[0].PrimaryDifference.N != 2 || portable.PairedContrasts[0].PrimaryOutcome != a.PrimaryOutcome {
		t.Fatal("primary contrasts lost in portable metadata")
	}
}
