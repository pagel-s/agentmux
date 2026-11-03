package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/workflow"
)

func isolatedCLIPublicationPlan(repository, secret string) workflow.Plan {
	return workflow.Plan{Version: 1, Repository: repository, Goal: "Synthetic credential publication fixture", Execution: "local", Mode: "compare", Storage: workflow.StoragePrivate,
		Isolation:  &workflow.IsolationOptions{Mode: "bubblewrap", Network: "none"},
		Candidates: []workflow.Candidate{{ID: "writer", Harness: "codex", Model: "fixture-model", Command: []string{"/usr/bin/true"}, APIKeyEnv: "SELECTED_LITERAL_KEY", Env: map[string]string{"SELECTED_LITERAL_KEY": secret}}},
		Checks:     []workflow.Check{{Name: "acceptance", Command: []string{"/usr/bin/true"}}}}
}

func assertCLIPublicationExcludes(t *testing.T, out, diagnostics []byte, secret string) {
	t.Helper()
	if bytes.Contains(out, []byte(secret)) || bytes.Contains(diagnostics, []byte(secret)) {
		t.Fatalf("selected credential appeared in CLI output: stdout=%q diagnostics=%q", out, diagnostics)
	}
	if len(out) > 0 && !json.Valid(out) {
		t.Fatalf("CLI published partial JSON: %q", out)
	}
}

func TestIsolatedCLICompleteEnvelopeAndDiagnosticCredentialGuard(t *testing.T) {
	root := t.TempDir()
	for _, operation := range []string{"run", "mission", "watch", "lab"} {
		for _, secret := range []string{"false", "null", "0", "version", "workflow", "Execution:", "flow:", "e", "[redacted credential]"} {
			t.Run(operation+"/"+secret, func(t *testing.T) {
				plan := isolatedCLIPublicationPlan(root, secret)
				if operation == "mission" {
					plan.Mode = "mission"
					plan.Steps = []workflow.Step{{ID: "implement", Candidate: "writer", Verify: true}}
				}
				path := filepath.Join(t.TempDir(), "plan.json")
				writeWorkflowTestJSON(t, path, plan)
				var out, diagnostics bytes.Buffer
				code := runWorkflowIO(context.Background(), []string{operation, path, "--output", filepath.Join(root, "never-created")}, &out, &diagnostics)
				if code == exitSuccess {
					t.Fatal("a native turn without --allow-models was admitted")
				}
				assertCLIPublicationExcludes(t, out.Bytes(), diagnostics.Bytes(), secret)
				if _, err := os.Stat(filepath.Join(root, "never-created")); !os.IsNotExist(err) {
					t.Fatal("refused native turn created output")
				}
			})
		}
	}
}

func TestIsolatedCLIReadsSelectedNativeAuthBeforeFlagDiagnostics(t *testing.T) {
	for _, secret := range []string{"synthetic-selected-native-auth-value", "flow:", "false"} {
		t.Run(secret, func(t *testing.T) {
			root := t.TempDir()
			auth := filepath.Join(root, "auth.json")
			writeWorkflowTestJSON(t, auth, map[string]string{"OPENAI_API_KEY": secret})
			plan := isolatedCLIPublicationPlan(root, "unused")
			plan.Candidates[0].APIKeyEnv, plan.Candidates[0].Env = "", nil
			plan.Isolation.Auth = map[string]workflow.IsolationAuth{"writer": {File: auth}}
			path := filepath.Join(root, "plan.json")
			writeWorkflowTestJSON(t, path, plan)
			var out, diagnostics bytes.Buffer
			code := runWorkflowIO(context.Background(), []string{"run", path, "--unknown=" + secret}, &out, &diagnostics)
			if code == exitSuccess {
				t.Fatal("invalid flags succeeded")
			}
			assertCLIPublicationExcludes(t, out.Bytes(), diagnostics.Bytes(), secret)
		})
	}
	root := t.TempDir()
	plan := isolatedCLIPublicationPlan(root, "unused")
	plan.Candidates[0].APIKeyEnv, plan.Candidates[0].Env = "", nil
	plan.Isolation.Auth = map[string]workflow.IsolationAuth{"writer": {File: filepath.Join(root, "unreadable-auth.json")}}
	path := filepath.Join(root, "plan.json")
	writeWorkflowTestJSON(t, path, plan)
	var out, diagnostics bytes.Buffer
	if code := runWorkflowIO(context.Background(), []string{"run", path, "--unknown=anything"}, &out, &diagnostics); code == exitSuccess || out.Len()+diagnostics.Len() != 0 {
		t.Fatal("uninspected selected native authentication published a static fallback")
	}
}

func TestGuidedHelpGuardsExplicitAuthenticationBeforeOutput(t *testing.T) {
	root := t.TempDir()
	auth := filepath.Join(root, "auth.json")
	writeWorkflowTestJSON(t, auth, map[string]string{"OPENAI_API_KEY": "Usage"})
	for _, args := range [][]string{
		{"--auth=coder=" + auth, "--help"},
		{"Task", "--auth", "coder=" + auth, "--isolation", "bubblewrap", "--help"},
		{"status", "--auth=coder=" + auth, "--help"},
	} {
		var out, diagnostics bytes.Buffer
		if code := runGuidedWorkIO(context.Background(), args, &out, &diagnostics); code == exitSuccess {
			t.Fatal("help containing a selected credential was published successfully")
		}
		if bytes.Contains(out.Bytes(), []byte("Usage")) || bytes.Contains(diagnostics.Bytes(), []byte("Usage")) {
			t.Fatal("selected authentication bypassed the help output guard")
		}
	}
	var out, diagnostics bytes.Buffer
	if code := runGuidedWorkIO(context.Background(), []string{"--help"}, &out, &diagnostics); code != exitSuccess || !strings.Contains(out.String(), "Usage") {
		t.Fatal("ordinary help changed without an explicit credential selection")
	}
}

func TestIsolatedCLIAmbiguousCredentialSelectionsPublishNothing(t *testing.T) {
	for _, duplicate := range []string{
		`"env":{"FIXTURE_TOKEN":"Execution:"},"env":{"FIXTURE_TOKEN":"replacement"}`,
		`"env":{"FIXTURE_TOKEN":"Execution:","FIXTURE_TOKEN":"replacement"}`,
	} {
		t.Run(duplicate, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.json")
			data := `{"isolation":{"mode":"bubblewrap"},"candidates":[{"id":"writer","harness":"codex",` + duplicate + `}]}`
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			var out, diagnostics bytes.Buffer
			code := runWorkflowIO(context.Background(), []string{"run", path, "--unknown=Execution:"}, &out, &diagnostics)
			if code == exitSuccess || out.Len()+diagnostics.Len() != 0 {
				t.Fatal("ambiguous selected authentication published CLI bytes")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "plan.json")
	data := `{"isolation":{"auth":{"writer":{"file":"/Execution:"}},"auth":{}},"candidates":[]}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := runWorkflowIO(context.Background(), []string{"run", path, "--unknown=Execution:"}, &out, &diagnostics); code == exitSuccess || out.Len()+diagnostics.Len() != 0 {
		t.Fatal("duplicate auth selection published CLI bytes")
	}
}

func TestIsolatedCLINormalRefusalKeepsUsefulError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "plan.json")
	writeWorkflowTestJSON(t, path, isolatedCLIPublicationPlan(root, "synthetic-unrelated-private-value"))
	var out, diagnostics bytes.Buffer
	code := runWorkflowIO(context.Background(), []string{"run", path, "--output", filepath.Join(root, "result")}, &out, &diagnostics)
	if code == exitSuccess || !json.Valid(out.Bytes()) || !strings.Contains(out.String(), "--allow-models") || !strings.Contains(diagnostics.String(), "Execution:") {
		t.Fatalf("ordinary failure became unusable: code=%d stdout=%q diagnostics=%q", code, &out, &diagnostics)
	}
}

func configureNativePublicationPreview(t *testing.T) {
	t.Helper()
	previous := configureGuidedWork
	configureGuidedWork = func(options workflow.GuidedOptions) workflow.GuidedOptions {
		options.Coder = &workflow.Candidate{ID: "coder", Harness: "codex", Model: "fixture-model", Command: []string{"/usr/bin/true"}}
		options.Reviewer = &workflow.Candidate{ID: "reviewer", Harness: "codex", Model: "fixture-model", Command: []string{"/usr/bin/true"}}
		return options
	}
	t.Cleanup(func() { configureGuidedWork = previous })
}

func TestIsolatedGuidedPreviewAndEarlyErrorsGuardSelectedAuth(t *testing.T) {
	root, _, fixture := workflowCLIFixture(t)
	configureNativePublicationPreview(t)
	for index, testcase := range []struct {
		secret string
		json   bool
		bad    bool
	}{
		{"synthetic-guided-private-credential", true, false},
		{"Preview only.", false, false},
		{"true", true, false},
		{"false", true, true},
		{"work:", true, true},
		{"synthetic-guided-flag-credential", true, true},
	} {
		t.Run(testcase.secret, func(t *testing.T) {
			// Some supported Go versions include the subtest's credential literal
			// in TempDir paths. Keep this ordinary success fixture outside that
			// name so the output-path guard is not correctly refusing the fixture.
			auth := filepath.Join(root, fmt.Sprintf("auth-%d.json", index))
			writeWorkflowTestJSON(t, auth, map[string]string{"OPENAI_API_KEY": testcase.secret})
			args := []string{"Review fixture", "--repo", fixture.Repository, "--output", filepath.Join(root, "preview-output"), "--isolation", "bubblewrap", "--reviewer", "codex", "--auth", "coder=" + auth}
			if testcase.json {
				args = append(args, "--json")
			}
			if testcase.bad {
				args = append(args, "--unknown="+testcase.secret)
			}
			args = append(args, "--", "/usr/bin/true")
			var out, diagnostics bytes.Buffer
			code := runGuidedWorkIO(context.Background(), args, &out, &diagnostics)
			if bytes.Contains(out.Bytes(), []byte(testcase.secret)) || bytes.Contains(diagnostics.Bytes(), []byte(testcase.secret)) {
				t.Fatalf("guided CLI disclosed selected auth: stdout=%q diagnostics=%q", &out, &diagnostics)
			}
			if testcase.json && out.Len() != 0 && !json.Valid(out.Bytes()) {
				t.Fatal("guided CLI published partial JSON")
			}
			if testcase.secret == "synthetic-guided-private-credential" && (code != exitSuccess || !json.Valid(out.Bytes())) {
				t.Fatalf("ordinary isolated preview failed: code=%d stdout=%q diagnostics=%q", code, &out, &diagnostics)
			}
		})
	}
}

func TestIsolatedGuidedPreviewGuardsSelectedCredentialInOutputPath(t *testing.T) {
	root, _, fixture := workflowCLIFixture(t)
	configureNativePublicationPreview(t)
	const secret = "synthetic-output-path-credential"
	auth := filepath.Join(root, "auth.json")
	writeWorkflowTestJSON(t, auth, map[string]string{"OPENAI_API_KEY": secret})
	var out, diagnostics bytes.Buffer
	code := runGuidedWorkIO(context.Background(), []string{"Review fixture", "--repo", fixture.Repository, "--output", filepath.Join(root, secret), "--isolation", "bubblewrap", "--reviewer", "codex", "--auth", "coder=" + auth, "--json", "--", "/usr/bin/true"}, &out, &diagnostics)
	if code == exitSuccess {
		t.Fatal("credential-bearing output path was published")
	}
	assertCLIPublicationExcludes(t, out.Bytes(), diagnostics.Bytes(), secret)
	if _, err := os.Stat(filepath.Join(root, secret)); !os.IsNotExist(err) {
		t.Fatal("preview created a credential-bearing output path")
	}
}

func TestCLIPublicationOverflowCancelsExecutionAndRefusesBufferedOutput(t *testing.T) {
	plan := isolatedCLIPublicationPlan(t.TempDir(), "synthetic-overflow-credential")
	var out, diagnostics bytes.Buffer
	code := runCLIPublicationSession(context.Background(), &out, &diagnostics, false, func(ctx context.Context, session *cliPublicationSession) (context.Context, error) {
		prepared, err := session.selectPlan(ctx, plan)
		// Exercise the actual remaining-budget boundary without allocating the
		// allowance just to mirror its configured production size.
		session.count = maxCLIOutputBytes - 2
		return prepared, err
	}, func(ctx context.Context, out, diagnostics io.Writer) int {
		if _, err := io.WriteString(out, "overflow"); err == nil || ctx.Err() == nil {
			t.Fatal("overflow did not stop the operation")
		}
		return exitSuccess
	})
	if code == exitSuccess || out.Len() != 0 || !strings.Contains(diagnostics.String(), "bounded memory") {
		t.Fatal("overflow did not retain an explicit safe failure")
	}
}

func TestCLIPublicationRetainsCrossWriteAndLaterChildSelections(t *testing.T) {
	for _, child := range []bool{false, true} {
		t.Run(fmt.Sprint(child), func(t *testing.T) {
			initial := isolatedCLIPublicationPlan(t.TempDir(), "synthetic-initial-credential")
			var out, diagnostics bytes.Buffer
			code := runCLIPublicationSession(context.Background(), &out, &diagnostics, false, func(ctx context.Context, session *cliPublicationSession) (context.Context, error) {
				return session.selectPlan(ctx, initial)
			}, func(ctx context.Context, out, diagnostics io.Writer) int {
				io.WriteString(out, "fa")
				io.WriteString(out, "lse")
				if child {
					plan := initial
					plan.Candidates = append([]workflow.Candidate(nil), initial.Candidates...)
					plan.Candidates[0].Env = map[string]string{"SELECTED_LITERAL_KEY": "false"}
					workflow.Run(ctx, plan, filepath.Join(initial.Repository, "never-created"), false)
				}
				return exitSuccess
			})
			if child && (code == exitSuccess || out.Len() != 0) {
				t.Fatal("later child credential escaped through earlier buffered writes")
			}
			if !child && (code != exitSuccess || out.String() != "false") {
				t.Fatal("unrelated output was unnecessarily refused")
			}
		})
	}
}
