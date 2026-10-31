package main

import (
	"bytes"
	"context"
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
