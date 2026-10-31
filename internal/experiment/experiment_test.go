package experiment

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func textPointer(value string) *string { return &value }

func testManifest() Manifest {
	return normalize(Manifest{Version: 1, ID: "pilot", Trials: 2, Seeds: []int64{3, 9}, RandomizationSeed: 17, Config: managedconfig.Config{Version: 2, Mode: "managed", Session: "pilot", Agents: map[string]managedconfig.Agent{
		"alice": {Harness: "command", Command: []string{"/bin/sh", "-c", "printf OK"}, Workdir: "/shared/source", Env: map[string]string{"HOME": "/shared/home"}},
		"bob":   {Harness: "command", Command: []string{"/bin/sh", "-c", "printf NO"}},
	}, Memory: map[string]managedconfig.Access{"project": {Readers: []string{"*"}, Writers: []string{"*"}}}, Policy: managedconfig.Policy{Grants: []managedconfig.Grant{{From: []string{"*"}, To: []string{"*"}, Actions: []string{"message.send", "instruct"}}}}}, Conditions: []Condition{
		{ID: "shared", RewardMode: "shared", Visibility: "board-only", Instructions: map[string]string{"alice": "Return a result", "bob": "Return a result"}},
		{ID: "competitive", RewardMode: "winner", Visibility: "direct", Instructions: map[string]string{"alice": "Return a result", "bob": "Return a result"}},
	}, Assertions: []Assertion{{Agent: "alice", Exact: textPointer("OK"), Weight: 1}, {Agent: "bob", Exact: textPointer("OK"), Weight: 1}}})
}

func TestPlanIsBalancedDeterministicAndDoesNotMutateManifest(t *testing.T) {
	m := testManifest()
	before := digest(m)
	one, err := Plan(m)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Plan(m)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(one, two) || len(one) != 8 || digest(m) != before {
		t.Fatal("plan must be deterministic and leave input unchanged")
	}
	counts := map[string]int{}
	for _, trial := range one {
		key := trial.Condition + "/" + string(rune(trial.Seed)) + "/" + string(rune(trial.Repetition))
		counts[key]++
	}
	for _, count := range counts {
		if count != 1 {
			t.Fatal("condition/seed/repetition repeated")
		}
	}
	m.RandomizationSeed++
	other, err := Plan(m)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(one, other) {
		t.Fatal("randomization seed should change this pilot's order")
	}
}

func TestIndependentAssertionsAndActualPayoffMappings(t *testing.T) {
	m := testManifest()
	tasks := map[string]managed.Task{"alice": {State: "submitted", Result: "OK"}, "bob": {State: "submitted", Result: "I succeeded"}}
	for mode, want := range map[string]map[string]float64{"shared": {"alice": 1, "bob": 1}, "individual": {"alice": 1, "bob": 0}, "winner": {"alice": 1, "bob": 0}} {
		results, scores, payoffs := score(m.Assertions, tasks, m.Config, mode)
		if !results[0].Passed || results[1].Passed || scores["bob"] != 0 || !reflect.DeepEqual(payoffs, want) {
			t.Fatalf("mode %s: results %+v scores %v payoffs %v", mode, results, scores, payoffs)
		}
	}
	tasks["bob"] = managed.Task{State: "submitted", Result: "OK"}
	_, _, ties := score(m.Assertions, tasks, m.Config, "winner")
	if ties["alice"] != 0.5 || ties["bob"] != 0.5 {
		t.Fatal("positive-score ties must split the winner reward")
	}
	tasks["alice"] = managed.Task{State: "failed", Result: "OK"}
	tasks["bob"] = managed.Task{State: "failed", Result: "OK"}
	_, scores, payoffs := score(m.Assertions, tasks, m.Config, "winner")
	if scores["alice"] != 0 || payoffs["alice"] != 0 || payoffs["bob"] != 0 {
		t.Fatal("failed tasks or self-reports must not earn an oracle score")
	}
}

func TestFreshTrialStateAndMemoryAreIndependent(t *testing.T) {
	m := testManifest()
	original := digest(m.Config)
	root := t.TempDir()
	aCfg := trialConfig(m, m.Conditions[0], Trial{Seed: 3}, filepath.Join(root, "a"))
	bCfg := trialConfig(m, m.Conditions[1], Trial{Seed: 9}, filepath.Join(root, "b"))
	if len(aCfg.Policy.Grants) != 0 || len(bCfg.Policy.Grants) == 0 || aCfg.Agents["alice"].Workdir == bCfg.Agents["alice"].Workdir || aCfg.Agents["alice"].Env["HOME"] == bCfg.Agents["alice"].Env["HOME"] || digest(m.Config) != original {
		t.Fatal("trial policy/workspace/home isolation or source immutability failed")
	}
	a, err := managed.Init(filepath.Join(root, "a"), aCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := managed.Init(filepath.Join(root, "b"), bCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ac, _ := managed.LoadCredential(managed.CredentialPath(filepath.Join(root, "a"), "alice"))
	bc, _ := managed.LoadCredential(managed.CredentialPath(filepath.Join(root, "b"), "alice"))
	_, problem := a.Call(context.Background(), ac.Token, managed.Request{Operation: "memory.put", RequestID: "write-a", Args: json.RawMessage(`{"namespace":"project","key":"experience","value":"condition-a","version":0}`)})
	if problem != nil {
		t.Fatal(problem)
	}
	_, problem = b.Call(context.Background(), bc.Token, managed.Request{Operation: "memory.get", Args: json.RawMessage(`{"namespace":"project","key":"experience"}`)})
	if problem == nil || problem.Code != "not_found" {
		t.Fatalf("cross-trial memory leaked: %v", problem)
	}
	_, problem = b.Call(context.Background(), ac.Token, managed.Request{Operation: "identity"})
	if problem == nil || problem.Code != "unauthorized" {
		t.Fatal("cross-trial credential accepted")
	}
}

func TestResumeRequiresExplicitNewAttemptForInterruption(t *testing.T) {
	dir := t.TempDir()
	attempt := filepath.Join(dir, "attempt-001")
	if err := os.Mkdir(attempt, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(attempt, "started.json"), map[string]bool{"started": true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resumeTrial(dir, false); err == nil {
		t.Fatal("interrupted work must not silently rerun")
	}
	if next, result, err := resumeTrial(dir, true); err != nil || next != 2 || result != nil {
		t.Fatalf("explicit rerun should allocate a new attempt: %d %+v %v", next, result, err)
	}
	completed := Result{Status: "completed", ConfigHash: "hash", Attempt: 1}
	if err := writeJSON(filepath.Join(attempt, "result.json"), completed); err != nil {
		t.Fatal(err)
	}
	if next, result, err := resumeTrial(dir, true); err != nil || next != 1 || result == nil || result.Status != "completed" {
		t.Fatal("completed attempts must be reused")
	}
}

func TestManifestRejectsAmbiguityAndInvalidOracle(t *testing.T) {
	for _, change := range []func(*Manifest){
		func(m *Manifest) { m.MaxConcurrency = 2 }, func(m *Manifest) { m.Seeds = []int64{1, 1} }, func(m *Manifest) { m.Conditions[0].Visibility = "all" }, func(m *Manifest) { m.Conditions[0].RewardMode = "cooperative-personality" }, func(m *Manifest) { m.Assertions[0].Contains = textPointer("also") }, func(m *Manifest) { delete(m.Conditions[0].Instructions, "bob") },
	} {
		m := testManifest()
		change(&m)
		if m.Validate() == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
	m := testManifest()
	a := m.Config.Agents["alice"]
	a.Harness = "claude"
	a.Command = nil
	m.Config.Agents["alice"] = a
	if _, err := Run(context.Background(), m, RunOptions{OutputDir: filepath.Join(t.TempDir(), "models")}); err == nil || !strings.Contains(err.Error(), "allow-models") {
		t.Fatal("provider-backed runs need explicit CLI opt-in")
	}
}

func TestExperimentCommandIntegration(t *testing.T) {
	if os.Getenv("AGENTMUX_EXPERIMENT_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_EXPERIMENT_INTEGRATION=1 for a real Unix broker run")
	}
	root, err := os.MkdirTemp("", "amx-exp-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	m := testManifest()
	m.Trials = 1
	m.Seeds = []int64{7}
	report, err := Run(context.Background(), m, RunOptions{OutputDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 2 {
		t.Fatal("missing condition results")
	}
	for _, result := range report.Results {
		if result.Status != "completed" || result.Scores["alice"] != 1 || result.Scores["bob"] != 0 {
			t.Fatalf("bad scored result: %+v", result)
		}
		trace := filepath.Join(root, result.Trial.ID, "attempt-001", "coordination.jsonl")
		data, err := os.ReadFile(trace)
		if err != nil || len(data) == 0 || strings.Contains(string(data), "lease_token") {
			t.Fatalf("invalid private trace: %v", err)
		}
	}
	resumed, err := Run(context.Background(), m, RunOptions{OutputDir: root})
	if err != nil || !reflect.DeepEqual(report, resumed) {
		t.Fatalf("completed resume changed result: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "manifest.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("manifest artifact must remain owner-private")
	}
}

func TestExperimentInterruptedIntegration(t *testing.T) {
	if os.Getenv("AGENTMUX_EXPERIMENT_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_EXPERIMENT_INTEGRATION=1 for real interruption/recovery")
	}
	root, err := os.MkdirTemp("", "amx-interrupt-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	m := testManifest()
	m.Trials = 1
	m.Seeds = []int64{7}
	m.TimeoutSeconds = 1
	for id, agent := range m.Config.Agents {
		agent.Command = []string{"/bin/sleep", "30"}
		m.Config.Agents[id] = agent
	}
	started := time.Now()
	report, err := Run(context.Background(), m, RunOptions{OutputDir: root})
	if err == nil || len(report.Results) != 1 || report.Results[0].Status != "interrupted" || time.Since(started) > 8*time.Second {
		t.Fatalf("interruption not bounded: %+v, %v", report, err)
	}
	trialDir := filepath.Join(root, report.Results[0].Trial.ID)
	if data, err := os.ReadFile(filepath.Join(trialDir, "attempt-001", "coordination.jsonl")); err != nil || len(data) == 0 {
		t.Fatalf("interruption lost its coordination trace: %v", err)
	}
	if _, err := Run(context.Background(), m, RunOptions{OutputDir: root}); err == nil || !strings.Contains(err.Error(), "rerun-interrupted") {
		t.Fatal("interrupted trials must not automatically replay")
	}
	if _, err := Run(context.Background(), m, RunOptions{OutputDir: root, RerunInterrupted: true}); err == nil {
		t.Fatal("same sleeping workload should time out in its fresh retry")
	}
	for _, attempt := range []string{"attempt-001", "attempt-002"} {
		if _, err := os.Stat(filepath.Join(trialDir, attempt, "result.json")); err != nil {
			t.Fatalf("attempt history not preserved: %v", err)
		}
	}
}
