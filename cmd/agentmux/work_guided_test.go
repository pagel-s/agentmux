package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"agentmux/internal/egress"
	"agentmux/internal/workflow"
)

func guidedCLIFixture(t *testing.T) (string, string, string, []string) {
	t.Helper()
	root, _, plan := workflowCLIFixture(t)
	old := configureGuidedWork
	configureGuidedWork = func(options workflow.GuidedOptions) workflow.GuidedOptions {
		options.Coder = &workflow.Candidate{ID: "coder", Harness: "command", Command: []string{"python3", "-c", "from pathlib import Path; Path('answer.txt').write_text('42'+chr(10)); print('Implemented answer.txt')"}}
		options.Reviewer = &workflow.Candidate{ID: "reviewer", Harness: "command", Command: []string{"python3", "-c", `import json; print(json.dumps({'decision':'approve','summary':'Inspected answer.txt against the exact requested value; no findings.','findings':[]}))`}}
		return options
	}
	t.Cleanup(func() { configureGuidedWork = old })
	check := []string{"python3", "-B", "-c", "from pathlib import Path; assert Path('answer.txt').read_bytes()==b'42'+bytes([10]); print('Exact answer passed')"}
	return root, plan.Repository, filepath.Join(root, "history"), check
}

func invokeGuidedCLI(t *testing.T, code int, args ...string) (string, string) {
	t.Helper()
	var out, diagnostics bytes.Buffer
	actual := runGuidedWorkIO(context.Background(), args, &out, &diagnostics)
	if actual != code {
		t.Fatalf("work %q exit=%d want=%d\nstdout:%s\nstderr:%s", args, actual, code, out.String(), diagnostics.String())
	}
	return out.String(), diagnostics.String()
}

func TestGuidedCLIEntireProjectJourneyWithoutJSONConfiguration(t *testing.T) {
	root, repo, catalog, check := guidedCLIFixture(t)
	goal := "Create the checked answer"
	previewArgs := append([]string{goal, "--repo", repo, "--catalog", catalog, "--json", "--"}, check...)
	preview, _ := invokeGuidedCLI(t, exitSuccess, previewArgs...)
	var envelope struct {
		OK     bool                   `json:"ok"`
		Result workflow.GuidedPreview `json:"result"`
	}
	if err := json.Unmarshal([]byte(preview), &envelope); err != nil || !envelope.OK {
		t.Fatalf("preview %s %v", preview, err)
	}
	if len(envelope.Result.Plan.Steps) != 5 || !strings.Contains(envelope.Result.CheckSource, "explicit") {
		t.Fatalf("missing guided plan: %+v", envelope.Result)
	}
	if envelope.Result.MinNativeTurns != 2 || envelope.Result.MaxNativeTurns != 6 || envelope.Result.Plan.ReviewPolicy != workflow.ReviewPolicyEarlyApproval {
		t.Fatalf("preview lost conditional native turn bounds: %+v", envelope.Result)
	}
	if envelope.Result.Selection.Files < 1 || envelope.Result.Selection.Bytes < 1 || envelope.Result.Plan.ProcessContainment != "process-group" {
		t.Fatalf("missing snapshot or containment preflight: %+v", envelope.Result)
	}
	if _, err := os.Stat(catalog); !os.IsNotExist(err) {
		t.Fatal("preview wrote catalog")
	}
	if _, err := os.Stat(envelope.Result.Output); !os.IsNotExist(err) {
		t.Fatal("preview wrote run")
	}
	args := append([]string{goal, "--repo", repo, "--catalog", catalog, "--allow-models", "--"}, check...)
	out, diagnostics := invokeGuidedCLI(t, exitSuccess, args...)
	if !strings.Contains(out, "Checks:") || !strings.Contains(out, "Evidence:") || !strings.Contains(diagnostics, "Review final revision") {
		t.Fatalf("missing everyday result/progress: %s\n%s", out, diagnostics)
	}
	if !strings.Contains(out, "skipped; initial fresh review approved this revision") {
		t.Fatalf("CLI represented a skipped phase as an executed review: %s", out)
	}
	if _, err := os.Stat(filepath.Join(repo, "answer.txt")); !os.IsNotExist(err) {
		t.Fatal("run changed source")
	}
	status, _ := invokeGuidedCLI(t, exitSuccess, "status", "--repo", repo, "--catalog", catalog, "--json")
	var summary struct {
		Result workflow.WorkSummary `json:"result"`
	}
	if err := json.Unmarshal([]byte(status), &summary); err != nil || !summary.Result.CanApply {
		t.Fatalf("not ready to apply: %s %v", status, err)
	}
	shown, _ := invokeGuidedCLI(t, exitSuccess, "show", "latest", "--repo", repo, "--catalog", catalog)
	if !strings.Contains(shown, "Patch preview") || !strings.Contains(shown, "diff --git") || !strings.Contains(shown, "+42") {
		t.Fatalf("show did not reveal checked changes: %s", shown)
	}
	compact, _ := invokeGuidedCLI(t, exitSuccess, "status", "latest", "--repo", repo, "--catalog", catalog)
	if strings.Contains(compact, "diff --git") {
		t.Fatal("status should remain compact")
	}
	listed, _ := invokeGuidedCLI(t, exitSuccess, "list", "--repo", repo, "--catalog", catalog)
	if !strings.Contains(listed, goal) {
		t.Fatalf("work missing from history: %s", listed)
	}
	indexBefore, err := exec.Command("git", "-C", repo, "diff", "--cached", "--binary").Output()
	if err != nil {
		t.Fatal(err)
	}
	invokeGuidedCLI(t, exitSuccess, "apply", "latest", "--repo", repo, "--catalog", catalog)
	data, err := os.ReadFile(filepath.Join(repo, "answer.txt"))
	if err != nil || string(data) != "42\n" {
		t.Fatalf("applied answer %q %v", data, err)
	}
	indexAfter, err := exec.Command("git", "-C", repo, "diff", "--cached", "--binary").Output()
	if err != nil || !bytes.Equal(indexBefore, indexAfter) {
		t.Fatal("apply changed staging")
	}
	invokeGuidedCLI(t, exitRuntime, "apply", "latest", "--repo", repo, "--catalog", catalog)
	if _, err := os.Stat(filepath.Join(root, "unrelated")); !os.IsNotExist(err) {
		t.Fatal("unrelated path touched")
	}
}

func TestGuidedCLIPreviewAndDryRunNeverStartCommands(t *testing.T) {
	root, repo, catalog, check := guidedCLIFixture(t)
	for _, extra := range [][]string{nil, {"--dry-run", "--allow-models"}} {
		args := append([]string{"Task with $(touch forbidden) and 'quotes'", "--repo", repo, "--catalog", catalog}, extra...)
		args = append(args, "--")
		args = append(args, check...)
		out, _ := invokeGuidedCLI(t, exitSuccess, args...)
		if !strings.Contains(out, "Preview only") || !strings.Contains(out, "no run files") {
			t.Fatalf("not a clear preview: %s", out)
		}
		if !strings.Contains(out, "native turns: 2–6") || !strings.Contains(out, "final checks always run") {
			t.Fatalf("preview obscures conditional execution: %s", out)
		}
	}
	if _, err := os.Stat(catalog); !os.IsNotExist(err) {
		t.Fatal("dry run created history")
	}
	if _, err := os.Stat(filepath.Join(repo, "answer.txt")); !os.IsNotExist(err) {
		t.Fatal("preview started writer")
	}
	if _, err := os.Stat(filepath.Join(root, "forbidden")); !os.IsNotExist(err) {
		t.Fatal("goal was executed as shell")
	}
}

func TestGuidedCLIPIDPreviewPreservesPolicyWithoutProbing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("PID namespace preview is Linux-specific")
	}
	root, repo, catalog, check := guidedCLIFixture(t)
	tools := filepath.Join(root, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "probe-ran")
	if err := os.WriteFile(filepath.Join(tools, "bwrap"), []byte("#!/bin/sh\n: > "+guidedShellWord(marker)+"\nexit 125\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	args := append([]string{"Create the checked answer", "--repo", repo, "--catalog", catalog, "--containment", "pid-namespace", "--"}, check...)
	preview, _ := invokeGuidedCLI(t, exitSuccess, args...)
	if !strings.Contains(preview, "--containment 'pid-namespace'") || !strings.Contains(preview, "Snapshot:") {
		t.Fatalf("suggested execution lost containment or storage admission: %s", preview)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("preview executed the namespace probe")
	}
	if _, err := os.Stat(catalog); !os.IsNotExist(err) {
		t.Fatal("preview created catalog state")
	}
}

func TestGuidedPreviewRetainsIsolationCapabilities(t *testing.T) {
	o := workflow.NormalizedIsolation(&workflow.IsolationOptions{
		Mode: "bubblewrap", Network: "allowlist",
		Allow:         []egress.Endpoint{{Origin: "https://api.example.com"}, {Origin: "https://second.example.com"}},
		ReadOnlyPaths: []string{"/tmp/runtime dependencies", "/tmp/second"},
		Auth:          map[string]workflow.IsolationAuth{"coder": {File: "/tmp/login 'quoted'.json"}, "reviewer": {File: "/tmp/review.json", Provider: "anthropic"}},
	})
	preview := workflow.GuidedPreview{Repository: "/tmp/project", Output: "/tmp/result", Plan: workflow.Plan{
		Goal: "Check quotes ' and $(literal)", Storage: workflow.StoragePrivate, Isolation: o,
		Candidates:         []workflow.Candidate{{Harness: "codex"}, {Harness: "claude"}},
		Checks:             []workflow.Check{{Command: []string{"/usr/bin/true"}, TimeoutSeconds: 60}},
		ProcessContainment: "pid-namespace", TimeoutSeconds: 600, MaxAttempts: 2,
	}}
	var out bytes.Buffer
	printGuidedPreview(&out, preview, "", true, false)
	for _, want := range []string{"'--storage' 'private'", "'--isolation' 'bubblewrap'", "'--network' 'allowlist'", "'--coder' 'codex'", "'--reviewer' 'claude'", "'--allow-origin' 'https://api.example.com'", "'--allow-origin' 'https://second.example.com'", "'--read-only-path' '/tmp/runtime dependencies'", "'--auth' 'reviewer=/tmp/review.json'", "'--auth-provider' 'reviewer=anthropic'"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("suggestion lost %q: %s", want, out.String())
		}
	}
	preview.Plan.Isolation.Allow[0].IPs = []string{"127.0.0.1"}
	out.Reset()
	printGuidedPreview(&out, preview, "", true, false)
	if strings.Contains(out.String(), "Run: ") || !strings.Contains(out.String(), "retain all configured capabilities") {
		t.Fatalf("preview weakened plan-only pinned capability: %s", out.String())
	}
}

func TestGuidedCLIRepeatedCapabilitiesAndScalarRefusal(t *testing.T) {
	root, repo, catalog, check := guidedCLIFixture(t)
	args := []string{"Inspect the current files", "--repo", repo, "--catalog", catalog, "--isolation", "bubblewrap", "--allow-origin", "https://api.example.com", "--allow-origin", "https://second.example.com", "--read-only-path", root, "--json", "--"}
	out, _ := invokeGuidedCLI(t, exitSuccess, append(args, check...)...)
	var envelope struct {
		Result workflow.GuidedPreview `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil || envelope.Result.Plan.Isolation == nil || len(envelope.Result.Plan.Isolation.Allow) != 2 || envelope.Result.Plan.Isolation.Network != "allowlist" {
		t.Fatalf("repeatable capability parse failed: %s %v", out, err)
	}
	invokeGuidedCLI(t, exitValidation, "Inspect", "--repo", repo, "--network", "none", "--json")
	invokeGuidedCLI(t, exitValidation, "Inspect", "--repo", repo, "--storage", "private", "--storage", "shared", "--json")
	if _, err := os.Stat(catalog); !os.IsNotExist(err) {
		t.Fatal("capability preview or refusal created run files")
	}
}

func TestGuidedCLIFirstPreviewSuggestedCommandCanBeExecuted(t *testing.T) {
	_, repo, catalog, check := guidedCLIFixture(t)
	args := append([]string{"Create the checked answer with 'quotes' and $(literal text)", "--repo", repo, "--catalog", catalog, "--"}, check...)
	preview, _ := invokeGuidedCLI(t, exitSuccess, args...)
	var suggestion string
	for _, line := range strings.Split(preview, "\n") {
		if strings.HasPrefix(line, "Run: ") {
			suggestion = strings.TrimPrefix(line, "Run: ")
		}
	}
	if suggestion == "" {
		t.Fatalf("preview has no executable suggestion: %s", preview)
	}
	if _, err := os.Stat(catalog); !os.IsNotExist(err) {
		t.Fatal("preview created the private catalog")
	}
	// Parse the suggested shell words without executing any task text as code.
	parse := exec.Command("python3", "-c", "import json,shlex,sys; print(json.dumps(shlex.split(sys.stdin.read())))")
	parse.Stdin = strings.NewReader(suggestion)
	data, err := parse.Output()
	if err != nil {
		t.Fatal(err)
	}
	var command []string
	if err = json.Unmarshal(data, &command); err != nil || len(command) < 3 || command[0] != "amx" || command[1] != "work" {
		t.Fatalf("invalid suggested command: %q %v", command, err)
	}
	out, _ := invokeGuidedCLI(t, exitSuccess, command[2:]...)
	if !strings.Contains(out, "Ready for your review") {
		t.Fatalf("suggested command did not complete the fixture journey: %s", out)
	}
}

func TestGuidedCLIRecoveryResolvesJournalWithoutReplayingSourceChanges(t *testing.T) {
	_, repo, catalog, check := guidedCLIFixture(t)
	args := append([]string{"Create the checked answer", "--repo", repo, "--catalog", catalog, "--allow-models", "--json", "--"}, check...)
	out, _ := invokeGuidedCLI(t, exitSuccess, args...)
	var envelope struct {
		Result workflow.Report `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	r := envelope.Result
	seedIntent := func(name string) {
		t.Helper()
		path := filepath.Join(r.Output, name)
		writeWorkflowTestJSON(t, path, workflow.ApplyResult{Run: r.ID, Candidate: "mission-result", Repository: repo, PatchSHA256: r.PatchSHA256, Phase: "intent", Uncertain: true})
	}
	seedIntent("applied-0000000000000000000.json")
	status, _ := invokeGuidedCLI(t, exitSuccess, "status", "latest", "--repo", repo, "--catalog", catalog)
	if !strings.Contains(status, "Application needs inspection") || !strings.Contains(status, "amx work recover") {
		t.Fatalf("interrupted intent has no recovery path: %s", status)
	}
	invokeGuidedCLI(t, exitRuntime, "apply", "latest", "--repo", repo, "--catalog", catalog)
	recovered, _ := invokeGuidedCLI(t, exitSuccess, "recover", "latest", "--repo", repo, "--catalog", catalog)
	if !strings.Contains(recovered, "not_applied") {
		t.Fatalf("unstarted patch was not recovered: %s", recovered)
	}
	if _, err := os.Stat(filepath.Join(repo, "answer.txt")); !os.IsNotExist(err) {
		t.Fatal("recover replayed a source patch")
	}
	invokeGuidedCLI(t, exitSuccess, "apply", "latest", "--repo", repo, "--catalog", catalog)
	seedIntent("applied-0000000000000000001.json")
	before, err := os.ReadFile(filepath.Join(repo, "answer.txt"))
	if err != nil {
		t.Fatal(err)
	}
	recovered, _ = invokeGuidedCLI(t, exitSuccess, "recover", "latest", "--repo", repo, "--catalog", catalog, "--json")
	var inspection struct {
		Result workflow.ApplyInspection `json:"result"`
	}
	if err := json.Unmarshal([]byte(recovered), &inspection); err != nil || inspection.Result.State != "applied" || inspection.Result.SourceObservation != "matches_final" {
		t.Fatalf("applied patch recovery: %s %v", recovered, err)
	}
	after, err := os.ReadFile(filepath.Join(repo, "answer.txt"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("recovery changed applied source content")
	}
}

func TestGuidedCLISafeLiteralArgumentsAndPreflightErrors(t *testing.T) {
	root, repo, catalog, check := guidedCLIFixture(t)
	for _, args := range [][]string{
		{"Task", "--repo", repo, "--catalog", catalog, "--unknown"},
		{"Task", "--repo", repo, "--catalog", catalog, "--attempts", "0"},
		{"Task", "--repo", repo, "--catalog", catalog, "--timeout", "1ms"},
		{"Task", "--repo", repo, "--repo", repo, "--catalog", catalog},
		{"Task", "--repo", repo, "--catalog", catalog, "--check-timeout", "1ms"},
		{"Task", "--repo", repo, "--catalog", catalog, "--containment", "unsupported", "--", "python3", "-c", "print('must not run')"},
		{"Task", "--repo", repo, "--catalog", catalog, "--output", filepath.Join(root, "missing-parent", "run"), "--allow-models", "--", "python3", "-c", "print('must not run')"},
		{"Task", "--repo", repo, "--catalog", catalog, "--"},
		{"Task", "extra goal", "--repo", repo, "--catalog", catalog},
	} {
		invokeGuidedCLI(t, exitValidation, args...)
	}
	if _, err := os.Stat(catalog); !os.IsNotExist(err) {
		t.Fatal("invalid invocation created history")
	}
	args := append([]string{"Task\x1b[2J", "--repo", repo, "--catalog", catalog, "--"}, check...)
	out, diagnostics := invokeGuidedCLI(t, exitSuccess, args...)
	if strings.ContainsAny(out+diagnostics, "\x1b\u202e") {
		t.Fatal("raw terminal controls rendered")
	}
	if !strings.Contains(out, "original command") {
		t.Fatal("unsafe copy suggestion not suppressed")
	}
	output := filepath.Join(root, "chosen")
	args = append([]string{"Task", "--repo", repo, "--catalog", catalog, "--output", output, "--"}, check...)
	out, _ = invokeGuidedCLI(t, exitSuccess, args...)
	if !strings.Contains(out, "--catalog '"+catalog+"'") {
		t.Fatal("suggested command lost selected catalog")
	}
}

func TestGuidedCLIIsRoutedByMainAndHasDiscoverableHelp(t *testing.T) {
	code, out, diagnostics := captureRunOutput(t, []string{"work", "--help"})
	if code != exitSuccess || !strings.Contains(out, "Preview is the default") || diagnostics != "" {
		t.Fatalf("main route: %d %s %s", code, out, diagnostics)
	}
	for _, args := range [][]string{{"status", "--help"}, {"show", "--help"}, {"apply", "--help"}, {"list", "--help"}} {
		out, _ := invokeGuidedCLI(t, exitSuccess, args...)
		if !strings.Contains(out, "amx work") {
			t.Fatal("missing help")
		}
	}
}

func TestGuidedCLIInterruptedWorkRemainsInspectable(t *testing.T) {
	root, repo, catalog, check := guidedCLIFixture(t)
	previous := configureGuidedWork
	configureGuidedWork = func(options workflow.GuidedOptions) workflow.GuidedOptions {
		options = previous(options)
		options.Coder.Command = []string{"python3", "-c", "import time; time.sleep(10)"}
		return options
	}
	var out, diagnostics bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	args := append([]string{"Task", "--repo", repo, "--catalog", catalog, "--allow-models", "--"}, check...)
	code := runGuidedWorkIO(ctx, args, &out, &diagnostics)
	if code != exitRuntime {
		t.Fatalf("cancelled work exit=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
	status, _ := invokeGuidedCLI(t, exitSuccess, "status", "latest", "--repo", repo, "--catalog", catalog, "--json")
	var summary struct {
		Result workflow.WorkSummary `json:"result"`
	}
	if err := json.Unmarshal([]byte(status), &summary); err != nil || summary.Result.State != "interrupted" || summary.Result.CanApply {
		t.Fatalf("cancelled evidence: %s %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "answer.txt")); !os.IsNotExist(err) {
		t.Fatal("cancelled run touched source")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}
}

func TestGuidedCLIHistoryCannotApplyAnotherProject(t *testing.T) {
	_, repo, catalog, check := guidedCLIFixture(t)
	args := append([]string{"Task", "--repo", repo, "--catalog", catalog, "--allow-models", "--json", "--"}, check...)
	out, _ := invokeGuidedCLI(t, exitSuccess, args...)
	var run struct {
		Result workflow.Report `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &run); err != nil {
		t.Fatal(err)
	}
	_, _, other := workflowCLIFixture(t)
	invokeGuidedCLI(t, exitRuntime, "apply", run.Result.Output, "--repo", other.Repository, "--catalog", catalog)
	if _, err := os.Stat(filepath.Join(other.Repository, "answer.txt")); !os.IsNotExist(err) {
		t.Fatal("wrong project modified")
	}
}

func TestGuidedCLIJSONErrorsAreSingleStructuredResults(t *testing.T) {
	_, repo, catalog, check := guidedCLIFixture(t)
	out, _ := invokeGuidedCLI(t, exitValidation, "Task", "--repo", repo, "--catalog", catalog, "--json")
	var preflight struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &preflight); err != nil || preflight.OK || preflight.Error == "" {
		t.Fatalf("unstructured preflight: %s %v", out, err)
	}
	previous := configureGuidedWork
	configureGuidedWork = func(options workflow.GuidedOptions) workflow.GuidedOptions {
		options = previous(options)
		options.Reviewer.Command = []string{"python3", "-c", `import json; print(json.dumps({'decision':'request_changes','summary':'A remaining issue needs owner inspection.','findings':[{'file':'answer.txt','line':1,'message':'Inspect this before applying.'}]}))`}
		return options
	}
	args := append([]string{"Task", "--repo", repo, "--catalog", catalog, "--allow-models", "--json", "--"}, check...)
	out, _ = invokeGuidedCLI(t, exitRuntime, args...)
	var failed struct {
		OK     bool            `json:"ok"`
		Error  string          `json:"error"`
		Result workflow.Report `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &failed); err != nil || failed.OK || failed.Result.State != "failed" || failed.Error == "" {
		t.Fatalf("run failure should have one result: %s %v", out, err)
	}
	shown, _ := invokeGuidedCLI(t, exitSuccess, "show", "latest", "--repo", repo, "--catalog", catalog)
	if !strings.Contains(shown, "answer.txt:1: Inspect this before applying.") {
		t.Fatalf("human review hides the actionable finding: %s", shown)
	}
}

func TestGuidedCLIJSONValidationErrorsKeepOneStructuredResult(t *testing.T) {
	for _, args := range [][]string{
		{"Task", "--unknown", "--json"},
		{"Task", "--attempts", "invalid", "--json"},
		{"Task", "--json", "--timeout"},
		{"list", "extra", "--json"},
		{"status", "first", "second", "--json"},
		{"show", "--unknown", "--json"},
		{"recover", "--json", "--repo"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, _ := invokeGuidedCLI(t, exitValidation, args...)
			var result struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil || result.OK || result.Error == "" {
				t.Fatalf("validation error lost JSON contract: %q %v", out, err)
			}
		})
	}
}

func TestGuidedCLIJSONModeDoesNotConsumeLiteralValuesOrCheckArguments(t *testing.T) {
	for _, args := range [][]string{
		{"Task", "--repo", "--json", "--unknown"},
		{"Task", "--unknown", "--", "--json"},
		{"Task", "--unknown", "--json=false"},
		{"list", "--catalog", "--json", "--unknown"},
	} {
		out, _ := invokeGuidedCLI(t, exitValidation, args...)
		if out != "" {
			t.Fatalf("literal data changed validation output mode for %q: %q", args, out)
		}
	}
}

func TestGuidedCLIShowEscapesPatchControlsAndKeepsLayout(t *testing.T) {
	_, repo, catalog, check := guidedCLIFixture(t)
	previous := configureGuidedWork
	configureGuidedWork = func(options workflow.GuidedOptions) workflow.GuidedOptions {
		options = previous(options)
		options.Coder.Command = []string{"python3", "-c", `from pathlib import Path; Path('answer.txt').write_text('42'+chr(10)); Path('notice.txt').write_text(chr(27)+'[2J'+chr(0x202e)+'payload'+chr(10)); print('Created fixture files')`}
		return options
	}
	args := append([]string{"Create the checked fixture files", "--repo", repo, "--catalog", catalog, "--allow-models", "--"}, check...)
	invokeGuidedCLI(t, exitSuccess, args...)
	out, _ := invokeGuidedCLI(t, exitSuccess, "show", "latest", "--repo", repo, "--catalog", catalog)
	if strings.ContainsAny(out, "\x1b\r\u202e") {
		t.Fatal("patch content executed raw terminal controls")
	}
	if !strings.Contains(out, "\\u001b[2J\\u202epayload") || !strings.Contains(out, "\n+42\n") {
		t.Fatalf("escaped patch/layout missing: %s", out)
	}
	raw, _ := invokeGuidedCLI(t, exitSuccess, "show", "latest", "--repo", repo, "--catalog", catalog, "--json")
	var envelope struct {
		Result workflow.WorkSummary `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil || envelope.Result.PatchPreview == "" || envelope.Result.PatchPath == "" {
		t.Fatalf("missing structured patch preview: %s %v", raw, err)
	}
}

func TestGuidedPatchPreviewIsBoundedWithoutSplittingEscapesOrUTF8(t *testing.T) {
	block, truncated := guidedPatchBlock("line\n\tindent\r\x1b[2J\u202epayload")
	if truncated || !strings.Contains(block, "line\n\tindent\\u000d\\u001b[2J\\u202e") {
		t.Fatalf("incorrect escaped layout: %q", block)
	}
	for _, input := range []string{strings.Repeat("\x1b", 64<<10), strings.Repeat("x", (64<<10)-1) + "界"} {
		block, truncated = guidedPatchBlock(input)
		if !truncated || len(block) > 64<<10 || !utf8.ValidString(block) {
			t.Fatalf("unsafe preview length=%d truncated=%t", len(block), truncated)
		}
	}
	var out bytes.Buffer
	printGuidedPatch(&out, workflow.WorkSummary{PatchPreview: strings.Repeat("x", 64<<10), PatchPreviewTruncated: true, PatchPath: "/private/run/changes.patch"})
	if !strings.Contains(out.String(), "truncated at 64 KiB") || !strings.Contains(out.String(), "Full patch: /private/run/changes.patch") {
		t.Fatal("truncated preview lost full artifact route")
	}
}
