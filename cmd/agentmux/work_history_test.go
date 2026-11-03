package main

import (
	"encoding/json"
	"strings"
	"testing"

	"agentmux/internal/workflow"
)

func guidedHistoryRuns(t *testing.T, goals ...string) (string, string, []workflow.Report) {
	t.Helper()
	_, repo, catalog, check := guidedCLIFixture(t)
	reports := make([]workflow.Report, 0, len(goals))
	for _, goal := range goals {
		args := append([]string{goal, "--repo", repo, "--catalog", catalog, "--allow-models", "--json", "--"}, check...)
		out, _ := invokeGuidedCLI(t, exitSuccess, args...)
		var envelope struct {
			Result workflow.Report `json:"result"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatal(err)
		}
		reports = append(reports, envelope.Result)
	}
	return repo, catalog, reports
}

func TestGuidedHistoryListsStableIDsAndLimitsOnlyWhenRequested(t *testing.T) {
	repo, catalog, reports := guidedHistoryRuns(t, "First task", "Second task", "Third task")
	list, _ := invokeGuidedCLI(t, exitSuccess, "list", "--repo", repo, "--catalog", catalog)
	for _, report := range reports {
		if !strings.Contains(list, report.ID) || !strings.Contains(list, report.Goal) {
			t.Fatalf("human history omitted stable identity: %s", list)
		}
	}
	for _, limit := range []string{"", "1"} {
		args := []string{"list", "--repo", repo, "--catalog", catalog, "--json"}
		if limit != "" {
			args = append(args, "--limit", limit)
		}
		out, _ := invokeGuidedCLI(t, exitSuccess, args...)
		var envelope struct {
			Result []workflow.RunSummary `json:"result"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatal(err)
		}
		want := len(reports)
		if limit != "" {
			want = 1
		}
		if len(envelope.Result) != want || envelope.Result[0].ID != reports[len(reports)-1].ID {
			t.Fatalf("history limit or newest-first ordering changed: %s", out)
		}
	}
	limited, _ := invokeGuidedCLI(t, exitSuccess, "list", "--repo", repo, "--catalog", catalog, "--limit", "1")
	if !strings.Contains(limited, "showing 1 of 3") || !strings.Contains(limited, reports[2].ID) || strings.Contains(limited, reports[0].ID) {
		t.Fatalf("human limit did not explain selected history: %s", limited)
	}
}

func TestGuidedHistorySelectionAndNextCommandsStayOnExplicitOlderRun(t *testing.T) {
	repo, catalog, reports := guidedHistoryRuns(t, "First task", "Second task")
	older, newer := reports[0], reports[1]
	for _, operation := range []string{"status", "show"} {
		out, _ := invokeGuidedCLI(t, exitSuccess, operation, older.ID, "--repo", repo, "--catalog", catalog)
		if !strings.Contains(out, "Run: "+older.ID) || !strings.Contains(out, "Task: "+older.Goal) || strings.Contains(out, newer.ID) || !strings.Contains(out, "amx work apply "+guidedShellWord(older.Output)) {
			t.Fatalf("explicit older run or its next command drifted to newer work: %s", out)
		}
		if operation == "show" && !strings.Contains(out, "amx work show "+guidedShellWord(older.Output)+" --repo "+guidedShellWord(repo)+" --open") {
			t.Fatalf("browser command did not retain the exact selected run and project: %s", out)
		}
	}
}

func TestGuidedHistoryLimitIsExplicitAndOperationScoped(t *testing.T) {
	for _, args := range [][]string{
		{"list", "--limit", "0", "--json"},
		{"list", "--limit", "-1", "--json"},
		{"list", "--limit", "1025", "--json"},
		{"list", "--limit", "1", "--limit", "2", "--json"},
		{"status", "--limit", "1", "--json"},
		{"show", "--limit", "1", "--json"},
	} {
		out, _ := invokeGuidedCLI(t, exitValidation, args...)
		if !json.Valid([]byte(out)) {
			t.Fatalf("invalid history option did not retain structured error: %s", out)
		}
	}
}
