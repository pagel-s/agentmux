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
			Trial:         research.Trial{ID: "run-1", Condition: "individual", Seed: 3, Repetition: 1},
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
	if err != nil || len(rows) != 2 || len(rows[0]) != 17 || len(rows[1][0]) != 24 || rows[1][5] != "alice" || rows[1][10] != "2" {
		t.Fatalf("unexpected behavior rows: %v %v", rows, err)
	}
	if strings.Contains(string(content)+stdout.String(), "PRIVATE_OBSERVATION_SENTINEL") {
		t.Fatal("behavior export leaked prompt text")
	}
	if code := runResearchIO(context.Background(), []string{"analyze", dir, "--behavior-csv", path}, &stdout, &stderr); code != exitRuntime {
		t.Fatal("behavior export overwrote an existing artifact")
	}
}
