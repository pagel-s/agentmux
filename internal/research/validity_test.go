package research

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func putEvidence(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

func TestFingerprintMatchesWorkerExplicitPATHAndPinsArguments(t *testing.T) {
	dir := t.TempDir()
	worker := filepath.Join(dir, "worker")
	first, selected := filepath.Join(dir, "host", "agent"), filepath.Join(dir, "selected", "agent")
	putEvidence(t, worker, "worker", 0700)
	putEvidence(t, first, "host binary", 0700)
	putEvidence(t, selected, "selected binary", 0700)
	t.Setenv("PATH", filepath.Dir(first))
	cfg := managedconfig.Config{Agents: map[string]managedconfig.Agent{"alice": {Harness: "command", Command: []string{"agent"}, Env: map[string]string{"PATH": filepath.Dir(selected)}}}}
	snapshot, err := snapshotExecution(worker, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Harness) != 1 || snapshot.Harness[0].Path != selected {
		t.Fatal("fingerprint ignored the runtime PATH override")
	}
	guard := executionGuard{worker: worker, config: cfg, expected: snapshot}
	putEvidence(t, first, "host change is irrelevant", 0700)
	if err := guard.check(); err != nil {
		t.Fatal(err)
	}
	putEvidence(t, selected, "changed actual runtime", 0700)
	if err := guard.check(); err == nil {
		t.Fatal("actual selected runtime changed without rejection")
	}
	a := cfg.Agents["alice"]
	a.Command = []string{"./agent"}
	cfg.Agents["alice"] = a
	if _, err := snapshotExecution(worker, cfg); err == nil {
		t.Fatal("relative executable was resolved against controller cwd")
	}
	script, auth := filepath.Join(dir, "agent.py"), filepath.Join(dir, "auth.json")
	putEvidence(t, script, "original code", 0600)
	putEvidence(t, auth, "original auth", 0600)
	a.Command = []string{selected, script, auth}
	a.Auth.File = auth
	cfg.Agents["alice"] = a
	snapshot, err = snapshotExecution(worker, cfg)
	if err != nil {
		t.Fatal(err)
	}
	guard = executionGuard{worker: worker, config: cfg, expected: snapshot}
	putEvidence(t, auth, "refreshed credential", 0600)
	if err := guard.check(); err != nil {
		t.Fatal("supported auth refresh changed execution fingerprint")
	}
	putEvidence(t, script, "changed code", 0600)
	if err := guard.check(); err == nil {
		t.Fatal("absolute argv code changed without rejection")
	}
}

func TestNativeResearchRequiresTracesIncludingTemplates(t *testing.T) {
	m := testManifest()
	a := m.Config.Agents["alice"]
	a.Harness = "claude"
	m.Config.Agents["alice"] = a
	if err := requireNativeEvidence(m); err == nil {
		t.Fatal("native static actor accepted without trace")
	}
	a.Trace.Enabled = true
	m.Config.Agents["alice"] = a
	if err := requireNativeEvidence(m); err != nil {
		t.Fatal(err)
	}
	m.Population.Mode = "dynamic"
	m.Config.Spawn.Templates = map[string]managedconfig.Agent{"helper": {Harness: "codex"}}
	if err := requireNativeEvidence(m); err == nil {
		t.Fatal("native template accepted without trace before spawn")
	}
	a = m.Config.Spawn.Templates["helper"]
	a.Trace.Enabled = true
	m.Config.Spawn.Templates["helper"] = a
	if err := requireNativeEvidence(m); err != nil {
		t.Fatal(err)
	}
}

func TestUntracedNativeStudyStopsBeforeAnyModelOrManifest(t *testing.T) {
	m := factorialManifest()
	m.Trials = 1
	m.Seeds = []int64{11}
	m.Game.Rounds = 1
	m.Budget.MaxModelTasks = 64
	a := m.Config.Agents["alice"]
	a.Harness = "claude"
	a.Command = nil
	a.Sandbox = "bubblewrap"
	a.Network = "host"
	m.Config.Agents["alice"] = a
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	_, err := Run(context.Background(), m, RunOptions{OutputDir: dir, Executable: "/bin/true", AllowModels: true})
	if err == nil || !strings.Contains(err.Error(), "trace.enabled") {
		t.Fatalf("missing trace not blocked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("untraced study started collection")
	}
}

func traceFixture(t *testing.T, state, actor, taskID string) *managed.TraceSummary {
	t.Helper()
	dir := filepath.Join(state, "traces", actor)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	key := []byte("01234567890123456789012345678901")
	putEvidence(t, filepath.Join(state, "traces", "trace.key"), string(key), 0600)
	id := taskID + "-evidence"
	previous := ""
	var content []byte
	for i, kind := range []string{"input", "end"} {
		r := managed.TraceRecord{Agent: actor, TaskID: taskID, TraceID: id, Sequence: i + 1, At: "2026-10-31T00:00:00Z", Kind: kind, Data: json.RawMessage(`{"ok":true}`), Previous: previous}
		unsigned, _ := json.Marshal(r)
		mac := hmac.New(sha256.New, key)
		mac.Write(unsigned)
		r.MAC = hex.EncodeToString(mac.Sum(nil))
		previous = r.MAC
		line, _ := json.Marshal(r)
		content = append(content, append(line, '\n')...)
	}
	putEvidence(t, filepath.Join(dir, id+".jsonl"), string(content), 0600)
	summary, err := managed.VerifyTrace(state, actor, id, previous)
	if err != nil {
		t.Fatal(err)
	}
	return &summary
}

func TestNativeEvidenceAuthenticatesActualTaskBoundTrace(t *testing.T) {
	state := t.TempDir()
	agent := managedconfig.Agent{Harness: "claude", Trace: managedconfig.Trace{Enabled: true}}
	task := managed.Task{ID: "task-one"}
	if err := verifyNativeTaskTrace(state, "alice", task, agent); err == nil {
		t.Fatal("native task without anchor accepted")
	}
	task.Trace = traceFixture(t, state, "alice", task.ID)
	if err := verifyNativeTaskTrace(state, "alice", task, agent); err != nil {
		t.Fatal(err)
	}
	if err := verifyNativeTaskTrace(state, "bob", task, agent); err == nil {
		t.Fatal("another actor's trace accepted")
	}
	other := task
	other.ID = "task-other"
	if err := verifyNativeTaskTrace(state, "alice", other, agent); err == nil {
		t.Fatal("another task's trace accepted")
	}
	path := filepath.Join(state, "traces", "alice", task.Trace.ID+".jsonl")
	contents, _ := os.ReadFile(path)
	contents[10] ^= 1
	os.WriteFile(path, contents, 0600)
	if err := verifyNativeTaskTrace(state, "alice", task, agent); err == nil {
		t.Fatal("tampered native trace accepted")
	}
}

func cacheFixture(t *testing.T, native bool) (Manifest, Trial, string, Result, *modelBudget) {
	t.Helper()
	m := testManifest()
	m.Trials = 1
	m.Seeds = []int64{11}
	m.Game.Rounds = 1
	m.Conditions = m.Conditions[:1]
	if native {
		a := m.Config.Agents["alice"]
		a.Harness = "claude"
		a.Trace.Enabled = true
		a.Sandbox = "bubblewrap"
		a.Network = "none"
		m.Config.Agents["alice"] = a
	}
	m = normalize(m)
	plan, err := Plan(m)
	if err != nil {
		t.Fatal(err)
	}
	trial := plan.Trials[0]
	dir := t.TempDir()
	cfg := trialConfig(m, m.Conditions[0], filepath.Join(dir, "state")).Normalized()
	result := Result{Protocol: decisionProtocol, Status: "completed", Trial: trial, Attempt: 1, ConfigHash: digest(cfg), Rounds: []Outcome{{Round: 1}}, ActorMetadata: map[string]AgentLabel{}}
	if err := writeJSON(filepath.Join(dir, "config.json"), map[string]any{"sha256": result.ConfigHash, "config": cfg}); err != nil {
		t.Fatal(err)
	}
	budget := &modelBudget{Limit: 64}
	for _, kind := range []string{"coordinate", "act"} {
		phase := Phase{Round: 1, Kind: kind, Instructions: map[string]string{}, Tasks: map[string]managed.Task{}}
		for _, actor := range actors(cfg) {
			id := "task-" + actor + "-" + kind
			instruction := "owner instruction " + id
			task := managed.Task{ID: id, From: "$owner", To: actor, Text: instruction, State: "submitted", Delivery: "acknowledged"}
			a := cfg.Agents[actor]
			result.ActorMetadata[actor] = AgentLabel{Harness: a.Harness, Model: a.Model}
			if a.Harness != "command" {
				task.Trace = traceFixture(t, filepath.Join(dir, "state"), actor, id)
				budget.Reserved = append(budget.Reserved, id)
				if phase.TraceVerified == nil {
					phase.TraceVerified = map[string]bool{}
				}
				phase.TraceVerified[actor] = true
			}
			phase.Tasks[actor] = task
			phase.Instructions[actor] = instruction
		}
		result.Phases = append(result.Phases, phase)
	}
	return m, trial, dir, result, budget
}

func TestCachedNativeCannotDowngradeHarnessProtocolOrTrace(t *testing.T) {
	m, trial, dir, result, budget := cacheFixture(t, true)
	check := func(r Result) error { return checkCachedResult(m, trial, 1, dir, r, budget, map[string]bool{}, true) }
	if err := check(result); err != nil {
		t.Fatal(err)
	}
	result.ActorMetadata["alice"] = AgentLabel{Harness: "command"}
	if err := check(result); err == nil {
		t.Fatal("native cache downgraded through editable actor label")
	}
	result.ActorMetadata["alice"] = AgentLabel{Harness: "claude"}
	result.Protocol = ""
	if err := check(result); err == nil {
		t.Fatal("strict cache downgraded by stripping result protocol")
	}
	result.Protocol = decisionProtocol
	budget.Reserved = nil
	if err := check(result); err == nil {
		t.Fatal("cached native task absent from ledger accepted")
	}
	for _, p := range result.Phases {
		budget.Reserved = append(budget.Reserved, p.Tasks["alice"].ID)
	}
	trace := result.Phases[0].Tasks["alice"].Trace
	os.Remove(filepath.Join(dir, "state", "traces", "alice", trace.ID+".jsonl"))
	if err := check(result); err == nil {
		t.Fatal("trace_verified boolean substituted for missing authenticated trace")
	}
}

func TestCachedConfigMustMatchFrozenPlanNotItsOwnEditableLabel(t *testing.T) {
	m, trial, dir, result, budget := cacheFixture(t, false)
	if err := checkCachedResult(m, trial, 1, dir, result, budget, map[string]bool{}, true); err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Config managedconfig.Config `json:"config"`
	}
	if err := privateJSON(filepath.Join(dir, "config.json"), &saved); err != nil {
		t.Fatal(err)
	}
	a := saved.Config.Agents["alice"]
	a.Command = []string{"/bin/false"}
	saved.Config.Agents["alice"] = a
	result.ConfigHash = digest(saved.Config)
	writeJSON(filepath.Join(dir, "config.json"), map[string]any{"sha256": result.ConfigHash, "config": saved.Config})
	if err := checkCachedResult(m, trial, 1, dir, result, budget, map[string]bool{}, true); err == nil {
		t.Fatal("self-consistent altered configuration accepted against frozen plan")
	}
}

func TestSavedManifestAndPlanCannotKeepStaleHash(t *testing.T) {
	m := testManifest()
	plan, _ := Plan(m)
	dir := t.TempDir()
	writeJSON(filepath.Join(dir, "manifest.json"), map[string]any{"sha256": digest(m), "manifest": m})
	writeJSON(filepath.Join(dir, "plan.json"), plan)
	if err := checkStoredManifest(dir, digest(m)); err != nil {
		t.Fatal(err)
	}
	if err := checkStoredPlan(dir, plan); err != nil {
		t.Fatal(err)
	}
	changed := m
	changed.ID = "changed"
	writeJSON(filepath.Join(dir, "manifest.json"), map[string]any{"sha256": digest(m), "manifest": changed})
	if err := checkStoredManifest(dir, digest(m)); err == nil {
		t.Fatal("edited manifest preserved stale hash")
	}
	plan.Trials[0].Seed++
	if err := checkStoredPlan(dir, plan); err == nil {
		t.Fatal("edited saved plan accepted")
	}
}

func TestExplicitRerunRefusesLivePriorProcessesAndMalformedAttempts(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "attempt-001", "state")
	os.MkdirAll(state, 0700)
	unlock, err := lockOutput(filepath.Join(state, "broker.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := resume(dir, true); err == nil {
		t.Fatal("fresh attempt started while old broker lock held")
	}
	unlock()
	if number, _, err := resume(dir, true); err != nil || number != 2 {
		t.Fatal("stopped prior attempt could not be preserved")
	}
	os.Mkdir(filepath.Join(dir, "attempt-1"), 0700)
	if _, _, err := resume(dir, true); err == nil {
		t.Fatal("noncanonical attempt directory accepted")
	}
}

func TestHistoricalClaimCannotDisappearFromModelBudget(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "run-001", "attempt-001", "state")
	cfg := testManifest().Config
	a := cfg.Agents["alice"]
	a.Harness = "claude"
	a.Sandbox = "bubblewrap"
	a.Network = "none"
	a.Trace.Enabled = true
	cfg.Agents["alice"] = a
	store, err := managed.Init(state, cfg)
	if err != nil {
		t.Fatal(err)
	}
	call := func(actor, op string, args any, id string) json.RawMessage {
		t.Helper()
		cred, err := managed.LoadCredential(managed.CredentialPath(state, actor))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(args)
		result, fault := store.Call(context.Background(), cred.Token, managed.Request{Operation: op, RequestID: id, Args: raw})
		if fault != nil {
			t.Fatalf("%s: %v", op, fault)
		}
		return result
	}
	raw := call("", "instruct", map[string]any{"to": []string{"alice"}, "text": "bounded fixture"}, "fixture-instruct")
	var response struct {
		Task managed.Task `json:"task"`
	}
	json.Unmarshal(raw, &response)
	call("alice", "task.claim", map[string]any{}, "fixture-claim")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	budget := &modelBudget{Limit: 2}
	if err := checkBudgetHistory(context.Background(), dir, budget); err == nil {
		t.Fatal("claimed native call vanished from ledger after crash before phase checkpoint")
	}
	budget.Reserved = []string{response.Task.ID}
	if err := checkBudgetHistory(context.Background(), dir, budget); err != nil {
		t.Fatal(err)
	}
}

func TestReportRejectsDuplicateCellsAndTasksButKeepsLegacyReads(t *testing.T) {
	report := Report{Research: "legacy", ManifestHash: "hash", Results: []Result{{Status: "completed", Trial: Trial{Condition: "a", Seed: 1, Repetition: 1}, Rounds: []Outcome{{SocialWelfare: 3}}}}}
	dir := t.TempDir()
	writeJSON(filepath.Join(dir, "results.json"), report)
	loaded, err := ReadReport(dir)
	if err != nil || !reflect.DeepEqual(loaded, report) {
		t.Fatal("legacy read changed historical evidence")
	}
	if a := Analyze(loaded); len(a.Conditions) != 1 || a.Conditions[0].SocialWelfare.Mean != 3 || len(a.EvidenceWarnings) != 1 {
		t.Fatal("legacy scores changed or lower coverage not disclosed")
	}
	report.Results = append(report.Results, report.Results[0])
	writeJSON(filepath.Join(dir, "results.json"), report)
	if _, err := ReadReport(dir); err == nil {
		t.Fatal("duplicate cell increased apparent sample count")
	}
	if a := Analyze(report); len(a.Conditions) != 0 || len(a.EvidenceWarnings) == 0 {
		t.Fatal("programmatic analysis silently estimated duplicated rows")
	}
	report.Results = report.Results[:1]
	report.Conditions = []Condition{{ID: "b"}}
	if err := validateReportEntries(report); err == nil {
		t.Fatal("undeclared condition silently entered analysis")
	}
	report.Conditions = nil
	report.Results[0].Phases = []Phase{{Tasks: map[string]managed.Task{"alice": {ID: "same"}}}, {Tasks: map[string]managed.Task{"alice": {ID: "same"}}}}
	if err := validateReportEntries(report); err == nil {
		t.Fatal("duplicated native task identity silently reused")
	}
}
