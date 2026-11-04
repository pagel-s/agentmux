package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/workflow"
)

func TestGuidedProviderDefaultsUseAvailableNativeCLIs(t *testing.T) {
	for _, tc := range []struct {
		name, coder, reviewer, wantCoder, wantReviewer string
		binaries                                       []string
	}{
		{name: "usual subscription pair", binaries: []string{"codex", "claude"}, wantCoder: "codex", wantReviewer: "claude"},
		{name: "one Codex install", binaries: []string{"codex"}, wantCoder: "codex", wantReviewer: "codex"},
		{name: "one Claude install", binaries: []string{"claude"}, wantCoder: "claude", wantReviewer: "claude"},
		{name: "Antigravity pair", binaries: []string{"codex", "agy"}, wantCoder: "codex", wantReviewer: "antigravity"},
		{name: "explicit coding role", coder: "claude", binaries: []string{"codex", "claude", "agy"}, wantCoder: "claude", wantReviewer: "codex"},
		{name: "explicit Antigravity coding role", coder: "antigravity", binaries: []string{"codex", "claude", "agy"}, wantCoder: "antigravity", wantReviewer: "claude"},
		{name: "explicit review role", reviewer: "antigravity", binaries: []string{"codex", "claude", "agy"}, wantCoder: "codex", wantReviewer: "antigravity"},
		{name: "missing explicit coder is not replaced", coder: "codex", binaries: []string{"claude"}, wantCoder: "codex", wantReviewer: "claude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(name string) (string, error) {
				for _, binary := range tc.binaries {
					if name == binary {
						return "/selected/" + name, nil
					}
				}
				return "", exec.ErrNotFound
			}
			options, err := selectGuidedProviders(workflow.GuidedOptions{CoderHarness: tc.coder, ReviewerHarness: tc.reviewer}, lookup)
			if err != nil || options.CoderHarness != tc.wantCoder || options.ReviewerHarness != tc.wantReviewer {
				t.Fatalf("profiles = %s/%s, %v; want %s/%s", options.CoderHarness, options.ReviewerHarness, err, tc.wantCoder, tc.wantReviewer)
			}
		})
	}
}

func TestGuidedProviderDiscoveryPreservesExplicitAndIsolatedProfiles(t *testing.T) {
	for _, options := range []workflow.GuidedOptions{
		{CoderHarness: "codex", ReviewerHarness: "claude"},
		{Isolation: &workflow.IsolationOptions{Mode: "bubblewrap"}},
		{Coder: &workflow.Candidate{Harness: "command", Command: []string{"/selected/coder"}}},
	} {
		result, err := selectGuidedProviders(options, func(string) (string, error) {
			t.Fatal("discovery inspected an explicit or isolated profile")
			return "", nil
		})
		if err != nil || result.CoderHarness != options.CoderHarness || result.ReviewerHarness != options.ReviewerHarness || result.Isolation != options.Isolation || result.Coder != options.Coder {
			t.Fatalf("selected authority changed: %+v %v", result, err)
		}
	}
	_, err := selectGuidedProviders(workflow.GuidedOptions{}, func(string) (string, error) { return "", errors.New("not installed") })
	if err == nil || !strings.Contains(err.Error(), "PATH") || !strings.Contains(err.Error(), "install and sign in") {
		t.Fatalf("missing actionable first-install refusal: %v", err)
	}
}

func TestGuidedNativePreviewDiscoversAndPinsProfilesWithoutLaunching(t *testing.T) {
	root, _, plan := workflowCLIFixture(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	tools := filepath.Join(root, "native-tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(git, filepath.Join(tools, "git")); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "provider-started")
	for _, name := range []string{"codex", "claude"} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\nprintf started > "+guidedShellWord(marker)+"\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Exclude every real provider from discovery; only the inert fixture CLIs
	// and Git are available. Neither CLI may execute during a normal preview.
	t.Setenv("PATH", tools)
	catalog := filepath.Join(root, "history")
	var out, diagnostics bytes.Buffer
	args := []string{"Describe the existing fixture", "--repo", plan.Repository, "--catalog", catalog, "--timeout", "435s", "--", "/bin/true"}
	if code := runGuidedWorkIO(context.Background(), args, &out, &diagnostics); code != exitSuccess {
		t.Fatalf("preview exit=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
	for _, want := range []string{"Coder: codex · reviewer: claude (fresh context)", "Profile turn limits: coder 7m15s · reviewer 7m15s (10m ceiling).", "Turns and acceptance checks share the remaining overall deadline.", "'--coder' 'codex'", "'--reviewer' 'claude'", "--timeout 7m15s", "Preview only. No models started and no run files were written."} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("preview lost %q: %s", want, &out)
		}
	}
	for _, absent := range []string{marker, catalog} {
		if _, err := os.Stat(absent); !os.IsNotExist(err) {
			t.Fatalf("preview created %s: %v", absent, err)
		}
	}

	if err := os.Remove(filepath.Join(tools, "claude")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostics.Reset()
	if code := runGuidedWorkIO(context.Background(), args, &out, &diagnostics); code != exitSuccess || !strings.Contains(out.String(), "Review uses a separate fresh context with the same provider.") || !strings.Contains(out.String(), "'--reviewer' 'codex'") {
		t.Fatalf("single-provider preview obscures its review: code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
	// An explicitly requested missing reviewer must fail instead of silently
	// selecting the available Codex account or invoking either provider.
	explicit := []string{"Describe the existing fixture", "--repo", plan.Repository, "--catalog", catalog, "--reviewer", "claude", "--", "/bin/true"}
	out.Reset()
	diagnostics.Reset()
	if code := runGuidedWorkIO(context.Background(), explicit, &out, &diagnostics); code != exitValidation || !strings.Contains(diagnostics.String(), "reviewer is unavailable") {
		t.Fatalf("explicit missing reviewer was replaced: code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("preview/refusal launched a provider: %v", err)
	}
}

func TestGuidedTurnBudgetDisplayIncludesNativeLimits(t *testing.T) {
	var out bytes.Buffer
	printGuidedTurnBudgets(&out, []workflow.Candidate{
		{ID: "coder", Harness: "codex", TimeoutSeconds: 435},
		{ID: "reviewer", Harness: "antigravity", TimeoutSeconds: 90, Command: []string{"agy", "--mode", "plan", "--print-timeout", "90s"}},
	})
	for _, want := range []string{"coder 7m15s · reviewer 1m30s", "share the remaining overall deadline", "Native print option (reviewer): --print-timeout '90s'"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("preview hid %q: %s", want, &out)
		}
	}
	out.Reset()
	printGuidedTurnBudgets(&out, []workflow.Candidate{
		{ID: "coder", Harness: "codex", TimeoutSeconds: 600},
		{ID: "reviewer", Harness: "antigravity-bridge", TimeoutSeconds: 180},
	})
	if !strings.Contains(out.String(), "coder 10m0s · reviewer 3m0s") || !strings.Contains(out.String(), "Bridge request limit (reviewer): 1m30s") {
		t.Fatalf("bridge request/profile limits were conflated: %s", &out)
	}
}
