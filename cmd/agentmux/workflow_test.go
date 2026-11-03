package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/workflow"
)

func workflowCLIFixture(t *testing.T) (string, string, workflow.Plan) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "source")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Workflow Test", "-c", "user.email=workflow@example.invalid"}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, output, err)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "README")
	git("commit", "-qm", "fixture")
	plan := workflow.Plan{Version: 1, Repository: repo, Goal: "Create the checked answer", Execution: "local", TimeoutSeconds: 30,
		Candidates: []workflow.Candidate{{ID: "writer", Harness: "command", Command: []string{"python3", "-c", "from pathlib import Path; Path('answer.txt').write_text('42\\n'); print('Created answer.txt')"}}},
		Checks:     []workflow.Check{{Name: "answer", Command: []string{"python3", "-c", "from pathlib import Path; assert Path('answer.txt').read_text() == '42\\n'; print('answer passed')"}, TimeoutSeconds: 5}},
	}
	path := filepath.Join(root, "plan.json")
	writeWorkflowTestJSON(t, path, plan)
	return root, path, plan
}

func writeWorkflowTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func invokeWorkflowCLI(t *testing.T, expected int, args ...string) []byte {
	t.Helper()
	var out, diagnostics bytes.Buffer
	code := runWorkflowIO(context.Background(), args, &out, &diagnostics)
	if code != expected {
		t.Fatalf("flow %v: exit=%d want=%d\n%s\n%s", args, code, expected, &diagnostics, &out)
	}
	if out.Len() > 0 && !json.Valid(out.Bytes()) {
		t.Fatalf("invalid JSON envelope: %s", &out)
	}
	return append([]byte(nil), out.Bytes()...)
}

func TestWorkflowCLIReviewJourney(t *testing.T) {
	root, planPath, plan := workflowCLIFixture(t)
	runDir := filepath.Join(root, "run")
	invokeWorkflowCLI(t, exitSuccess, "run", planPath, "--output", runDir)
	if _, err := os.Stat(filepath.Join(plan.Repository, "answer.txt")); !os.IsNotExist(err) {
		t.Fatal("run changed source")
	}
	invokeWorkflowCLI(t, exitSuccess, "status", runDir)
	invokeWorkflowCLI(t, exitSuccess, "explain", runDir)
	if html, err := os.ReadFile(filepath.Join(runDir, "report.html")); err != nil || !bytes.Contains(html, []byte("answer")) {
		t.Fatalf("HTML report unavailable: %v", err)
	}
	brief := filepath.Join(root, "handoff.md")
	invokeWorkflowCLI(t, exitSuccess, "handoff", runDir, "--output", brief)
	if data, err := os.ReadFile(brief); err != nil || !bytes.Contains(data, []byte("42")) && !bytes.Contains(data, []byte("answer")) {
		t.Fatalf("handoff lacks result: %v %s", err, data)
	}
	if info, err := os.Stat(brief); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private output: %v", err)
	}
	invokeWorkflowCLI(t, exitValidation, "handoff", runDir, "--output", brief)
	invokeWorkflowCLI(t, exitSuccess, "report", runDir, "--output", filepath.Join(root, "review.html"))
	stored, loadErr := workflow.LoadReport(runDir)
	current, currentErr := workflow.CurrentTree(context.Background(), plan.Repository)
	if loadErr != nil || currentErr != nil || stored.Snapshot.Tree != current {
		output, _ := exec.Command("git", "-C", plan.Repository, "status", "--porcelain").CombinedOutput()
		t.Fatalf("source changed before apply: start=%s current=%s load=%v current=%v status=%s", stored.Snapshot.Tree, current, loadErr, currentErr, output)
	}
	invokeWorkflowCLI(t, exitSuccess, "apply", runDir, "--candidate", "writer")
	recovered := invokeWorkflowCLI(t, exitSuccess, "recover", runDir, "--candidate", "writer")
	if !bytes.Contains(recovered, []byte(`"state":"applied"`)) || !bytes.Contains(recovered, []byte(`"source_observation":"matches_final"`)) {
		t.Fatalf("recovery did not identify confirmed application: %s", recovered)
	}
	if data, err := os.ReadFile(filepath.Join(plan.Repository, "answer.txt")); err != nil || string(data) != "42\n" {
		t.Fatalf("apply: %v %q", err, data)
	}
}

func TestWorkflowCLIMissionWatchAndLab(t *testing.T) {
	root, planPath, plan := workflowCLIFixture(t)
	mission := plan
	mission.Mode = "mission"
	mission.Steps = []workflow.Step{{ID: "implement", Candidate: "writer"}, {ID: "verify", Verify: true, DependsOn: []string{"implement"}}}
	missionPath := filepath.Join(root, "mission.json")
	writeWorkflowTestJSON(t, missionPath, mission)
	invokeWorkflowCLI(t, exitSuccess, "mission", missionPath, "--output", filepath.Join(root, "mission"))
	invokeWorkflowCLI(t, exitValidation, "run", missionPath, "--output", filepath.Join(root, "wrong-mode"))
	reviewPlan := plan
	reviewPlan.Candidates = []workflow.Candidate{{ID: "reviewer", Harness: "command", Command: []string{"python3", "-c", "print('Read-only review completed')"}}}
	reviewPlan.Checks = []workflow.Check{{Name: "fixture-intact", Command: []string{"python3", "-c", "from pathlib import Path; assert Path('README').read_text() == 'fixture' + chr(10)"}}}
	reviewPath := filepath.Join(root, "review.json")
	writeWorkflowTestJSON(t, reviewPath, reviewPlan)
	invokeWorkflowCLI(t, exitSuccess, "watch", reviewPath, "--output", filepath.Join(root, "watch"), "--interval", "1s", "--max-runs", "1", "--duration", "10s")
	invokeWorkflowCLI(t, exitSuccess, "status", filepath.Join(root, "watch"))
	invokeWorkflowCLI(t, exitSuccess, "lab", planPath, "--output", filepath.Join(root, "lab"), "--repetitions", "2")
	invokeWorkflowCLI(t, exitSuccess, "status", filepath.Join(root, "lab"))
}

func TestWorkflowCLICheckFailuresAndOutputRefusal(t *testing.T) {
	root, _, plan := workflowCLIFixture(t)
	checks := filepath.Join(root, "checks.json")
	writeWorkflowTestJSON(t, checks, []workflow.Check{{Name: "owner-check", Command: []string{"python3", "-c", "raise SystemExit(7)"}}})
	path := filepath.Join(root, "failed.json")
	raw := invokeWorkflowCLI(t, exitRuntime, "check", "--repo", plan.Repository, "--checks", checks, "--output", path)
	if !bytes.Contains(raw, []byte(`"ok":false`)) {
		t.Fatalf("failure missing from envelope: %s", raw)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("failed evidence was discarded", err)
	}
	marker := filepath.Join(root, "must-not-execute")
	writeWorkflowTestJSON(t, checks, []workflow.Check{{Name: "side-effect", Command: []string{"python3", "-c", "from pathlib import Path; Path(" + pythonWorkflowQuote(marker) + ").write_text('bad')"}}})
	invokeWorkflowCLI(t, exitValidation, "check", "--repo", plan.Repository, "--checks", checks, "--output", path)
	invokeWorkflowCLI(t, exitValidation, "check", "--repo", plan.Repository, "--checks", checks, "--output", filepath.Join(plan.Repository, "invalidating-report.json"))
	invalidPolicyOutput := filepath.Join(root, "invalid-policy.json")
	invokeWorkflowCLI(t, exitValidation, "check", "--repo", plan.Repository, "--checks", checks, "--output", invalidPolicyOutput, "--containment", "unsupported")
	if _, err := os.Stat(invalidPolicyOutput); !os.IsNotExist(err) {
		t.Fatal("invalid containment reserved output or admitted a check")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("existing output caused checks to execute")
	}
}

func TestWorkflowCLIStandaloneCheckUsesPrivateObjectsByDefault(t *testing.T) {
	root, _, plan := workflowCLIFixture(t)
	if err := os.WriteFile(filepath.Join(plan.Repository, "untracked-private-content"), []byte("uncommitted raw bytes\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	objects := func() map[string]string {
		t.Helper()
		result := map[string]string{}
		git := filepath.Join(plan.Repository, ".git")
		if err := filepath.WalkDir(git, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			data, err := os.ReadFile(path)
			result[path] = string(data)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before, _ := json.Marshal(objects())
	checks, output := filepath.Join(root, "private-checks.json"), filepath.Join(root, "private-verification.json")
	writeWorkflowTestJSON(t, checks, []workflow.Check{{Name: "pass", Command: []string{"/bin/true"}}})
	invokeWorkflowCLI(t, exitSuccess, "check", "--repo", plan.Repository, "--checks", checks, "--output", output)
	after, _ := json.Marshal(objects())
	if !bytes.Equal(before, after) {
		t.Fatal("private standalone check changed source Git metadata")
	}
	var v workflow.Verification
	if err := workflow.DecodeFile(output, &v); err != nil || v.Storage != workflow.StoragePrivate || v.Store != output+".git" {
		t.Fatalf("verification lost its private cache: %+v %v", v, err)
	}
	if fresh, err := workflow.IsFresh(context.Background(), plan.Repository, v); err != nil || !fresh {
		t.Fatalf("private standalone verification cannot be refreshed: %v %v", fresh, err)
	}
	final, _ := json.Marshal(objects())
	if !bytes.Equal(before, final) {
		t.Fatal("private freshness check changed source Git metadata")
	}
}

func pythonWorkflowQuote(s string) string { raw, _ := json.Marshal(s); return string(raw) }

func TestWorkflowCLIRejectsInvalidOptionsBeforeExecution(t *testing.T) {
	root, path, plan := workflowCLIFixture(t)
	for _, args := range [][]string{
		{"run", path, "--output", filepath.Join(root, "unknown"), "--typo"},
		{"run", path, "--output", filepath.Join(root, "extra"), "unexpected"},
		{"watch", path, "--output", filepath.Join(root, "fast"), "--interval", "1ms"},
		{"lab", path, "--output", filepath.Join(root, "many"), "--repetitions", "21"},
		{"handoff", root, "--output", filepath.Join(root, "brief"), "--to", "peer"},
		{"status", root, "extra"},
	} {
		invokeWorkflowCLI(t, exitValidation, args...)
	}
	plan.Candidates[0].Harness = "codex"
	plan.Candidates[0].Command = nil
	writeWorkflowTestJSON(t, path, plan)
	invokeWorkflowCLI(t, exitRuntime, "run", path, "--output", filepath.Join(root, "model-denied"))
	if _, err := os.Stat(filepath.Join(root, "model-denied")); !os.IsNotExist(err) {
		t.Fatal("model gate should reject before output/process creation")
	}
}

func TestWorkflowHandoffExactSavedBriefDeliveryIsIdempotent(t *testing.T) {
	root, path, _ := workflowCLIFixture(t)
	runDir := filepath.Join(root, "run")
	invokeWorkflowCLI(t, exitSuccess, "run", path, "--output", runDir)
	brief := filepath.Join(root, "brief.md")
	invokeWorkflowCLI(t, exitSuccess, "handoff", runDir, "--output", brief)
	state, client, _ := inventoryBroker(t)
	args := []string{"handoff", runDir, "--brief", brief, "--state", state, "--to", "peer", "--request-id", "handoff-stable-1"}
	invokeWorkflowCLI(t, exitSuccess, args...)
	invokeWorkflowCLI(t, exitSuccess, args...)
	raw, err := workCall(context.Background(), client, "task.list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Tasks) != 1 {
		t.Fatalf("replayed handoff duplicated tasks: %s", raw)
	}
	if err := os.WriteFile(brief, []byte("changed payload"), 0600); err != nil {
		t.Fatal(err)
	}
	result := invokeWorkflowCLI(t, exitRuntime, args...)
	if !bytes.Contains(result, []byte("conflict")) {
		t.Fatalf("changed payload not rejected: %s", result)
	}
}

func TestWorkflowBriefRejectsSymlinkAndNUL(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "brief")
	if err := os.WriteFile(file, []byte("a\x00b"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkflowBrief(file); err == nil {
		t.Fatal("accepted NUL")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readWorkflowBrief(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatal("accepted symlink", err)
	}
}

func TestWorkflowExamplePlansExecuteWithoutModels(t *testing.T) {
	root, _, plan := workflowCLIFixture(t)
	for _, example := range []struct{ name, operation string }{{"compare", "run"}, {"mission", "mission"}, {"shadow", "watch"}} {
		args := []string{example.operation, filepath.Join("..", "..", "examples", "workflows", example.name+".json"), "--repo", plan.Repository, "--output", filepath.Join(root, example.name)}
		if example.operation == "watch" {
			args = append(args, "--interval", "1s", "--max-runs", "1", "--duration", "10s")
		}
		invokeWorkflowCLI(t, exitSuccess, args...)
		if example.operation != "watch" {
			report, err := workflow.LoadReport(filepath.Join(root, example.name))
			if err != nil {
				t.Fatal(err)
			}
			worktrees := []string{report.Worktree}
			if example.operation == "run" {
				worktrees = nil
				for _, result := range report.Candidates {
					worktrees = append(worktrees, result.Worktree)
				}
			}
			for _, worktree := range worktrees {
				data, err := os.ReadFile(filepath.Join(worktree, "answer.txt"))
				if err != nil || !bytes.Equal(data, []byte{52, 50, 10}) {
					t.Fatalf("example %s violated its actual newline objective: %v %q", example.name, err, data)
				}
			}
		}
	}
}

func TestWorkflowNativeExampleIsConfiguredButModelGated(t *testing.T) {
	root, _, plan := workflowCLIFixture(t)
	path := filepath.Join("..", "..", "examples", "workflows", "native-compare.json")
	loaded, err := workflow.LoadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Candidates) != 2 || len(loaded.Checks) != 1 {
		t.Fatal("incomplete native comparison")
	}
	invokeWorkflowCLI(t, exitRuntime, "run", path, "--repo", plan.Repository, "--output", filepath.Join(root, "blocked-models"))
	if _, err := os.Stat(filepath.Join(root, "blocked-models")); !os.IsNotExist(err) {
		t.Fatal("native example bypassed explicit provider gate")
	}
	implementation := "def normalize_tags(tags):\n    values = []\n    for value in tags:\n        if not isinstance(value, str):\n            raise TypeError('expected string')\n        value = value.strip().lower()\n        if value:\n            values.append(value)\n    return sorted(set(values))\n"
	path = filepath.Join(plan.Repository, "tag_normalizer.py")
	if err := os.WriteFile(path, []byte(implementation), 0600); err != nil {
		t.Fatal(err)
	}
	verification, err := workflow.Verify(context.Background(), plan.Repository, loaded.Checks)
	if err != nil || !verification.Passed {
		t.Fatalf("native example verifier rejected a known-good artifact: %+v %v", verification, err)
	}
	if err := os.WriteFile(path, []byte("def normalize_tags(tags):\n    return tags\n"), 0600); err != nil {
		t.Fatal(err)
	}
	verification, err = workflow.Verify(context.Background(), plan.Repository, loaded.Checks)
	if err != nil || verification.Passed {
		t.Fatalf("native example verifier accepted a known-bad artifact: %+v %v", verification, err)
	}
}

func TestWorkflowHandoffExportCannotInvalidateSourceOrCandidate(t *testing.T) {
	root, planPath, plan := workflowCLIFixture(t)
	runDir := filepath.Join(root, "run")
	invokeWorkflowCLI(t, exitSuccess, "run", planPath, "--output", runDir)
	r, err := workflow.LoadReport(runDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{plan.Repository, r.Candidates[0].Worktree} {
		path := filepath.Join(repo, "handoff.md")
		invokeWorkflowCLI(t, exitValidation, "handoff", runDir, "--output", path)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("rejected export created %s", path)
		}
	}
	if fresh, err := workflow.IsFresh(context.Background(), r.Candidates[0].Worktree, *r.Candidates[0].Verification); err != nil || !fresh {
		t.Fatalf("rejected export invalidated evidence: %v %v", fresh, err)
	}
}

func TestWorkflowSavedBriefRequiresExactUTF8Text(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "brief")
	for _, data := range [][]byte{nil, []byte(" \t\n"), {0xff, 0xfe}, []byte("zero\x00byte")} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readWorkflowBrief(path); err == nil {
			t.Fatalf("accepted invalid saved text %q", data)
		}
	}
	want := "Selected result: café\n"
	if err := os.WriteFile(path, []byte(want), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readWorkflowBrief(path); err != nil || got != want {
		t.Fatalf("saved bytes changed: %q %v", got, err)
	}
}

func TestWorkflowFlagDiagnosticsEscapeTerminalControls(t *testing.T) {
	var out, diagnostics bytes.Buffer
	code := runWorkflowIO(context.Background(), []string{"run", "unused.json", "--bad\x1b[2J\u202e"}, &out, &diagnostics)
	if code != exitValidation {
		t.Fatalf("unexpected exit %d", code)
	}
	if strings.ContainsAny(diagnostics.String(), "\x1b\u202e") {
		t.Fatalf("terminal controls escaped into diagnostics: %q", diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), "\\x1b") {
		t.Fatalf("missing quoted argument %q", diagnostics.String())
	}
}

func TestWorkflowJSONEscapesDirectionalControlsWithoutChangingValue(t *testing.T) {
	var out bytes.Buffer
	value := "result\u202e and \u2066 café"
	if err := encodeWorkflow(&out, "test", value, nil); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\u202e\u2066") {
		t.Fatalf("raw direction controls emitted: %q", out.String())
	}
	var decoded struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || decoded.Result != value {
		t.Fatalf("JSON value changed: %+v %v", decoded, err)
	}
}

func TestWorkflowHandoffRequestIdentityRejectsAmbiguousBytes(t *testing.T) {
	for _, id := range []string{"bad\u2003id", "bad\u202eid", string([]byte{0xff}), "bad\x00id"} {
		invokeWorkflowCLI(t, exitValidation, "handoff", "unused-run", "--brief", "unused-brief", "--state", "unused-state", "--to", "receiver", "--request-id", id)
	}
}

func TestWorkflowCheckOutputPreservesTrailingSpaceRepositoryPath(t *testing.T) {
	root, _, plan := workflowCLIFixture(t)
	destination := plan.Repository + " "
	if err := os.Rename(plan.Repository, destination); err != nil {
		t.Fatal(err)
	}
	checks := filepath.Join(root, "checks.json")
	writeWorkflowTestJSON(t, checks, []workflow.Check{{Name: "pass", Command: []string{"python3", "-c", "print('pass')"}}})
	invokeWorkflowCLI(t, exitSuccess, "check", "--repo", destination, "--checks", checks, "--output", filepath.Join(root, "checks-result.json"))
}

func TestWorkflowStatusRejectsIncompleteSuccessClaims(t *testing.T) {
	for _, name := range []string{"watch.json", "lab.json"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"version":1,"state":"completed"}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadWorkflowStatus(dir); err == nil {
			t.Fatalf("accepted incomplete %s", name)
		}
	}
}

func TestWorkflowStatusReadsAggregateAboveChecksLimit(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r := workflow.WatchReport{Version: 1, Repository: filepath.Join(dir, "repo"), Output: dir, State: "failed", StartedAt: now, FinishedAt: now, Options: workflow.WatchOptions{IntervalSeconds: 1, MaxRuns: 100, TimeoutSeconds: 600}, Error: strings.Repeat("x", (1<<20)+1)}
	writeWorkflowTestJSON(t, filepath.Join(dir, "watch.json"), r)
	if _, err := loadWorkflowStatus(dir); err != nil {
		t.Fatalf("aggregate rejected using small checks limit: %v", err)
	}
}
