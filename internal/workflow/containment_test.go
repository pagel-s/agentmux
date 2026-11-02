package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProcessContainmentDefaultsAreExplicitAndUnknownPoliciesFail(t *testing.T) {
	p := normalized(Plan{})
	if p.ProcessContainment != ProcessGroups {
		t.Fatalf("default lifetime policy is not recorded: %q", p.ProcessContainment)
	}
	if err := ValidateProcessContainment("best-effort"); err == nil {
		t.Fatal("unknown policy accepted")
	}
	if _, err := VerifyWithContainment(context.Background(), ".", nil, "best-effort"); err == nil {
		t.Fatal("unknown verification policy accepted")
	}
}

func TestProcessContainmentUnavailableNeverAdmitsCandidateOrCreatesOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux PID namespace preflight fixture")
	}
	repo, root := fixtureRepo(t)
	tools := filepath.Join(root, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "bwrap"), []byte("#!/bin/sh\nexit 125\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	marker := filepath.Join(root, "must-not-execute")
	p := auditPlan(repo, []string{"python3", "-c", "from pathlib import Path; Path('" + marker + "').write_text('bad')"})
	p.ProcessContainment = PIDNamespace
	output := filepath.Join(root, "run")
	if _, err := Run(context.Background(), p, output, false); err == nil || !strings.Contains(err.Error(), "containment") {
		t.Fatalf("unavailable boundary admitted work: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("candidate was started after containment preflight failed")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed containment preflight created a run")
	}
	if _, err := VerifyWithContainment(context.Background(), repo, []Check{pythonCheck("raise RuntimeError('must not execute')")}, PIDNamespace); err == nil {
		t.Fatal("unavailable boundary admitted acceptance checks")
	}
}

func TestProcessContainmentRejectsUnsafePeerConfigurationBeforeAnyWork(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux containment launcher configuration")
	}
	repo, root := fixtureRepo(t)
	tools := filepath.Join(root, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "bwrap"), []byte("#!/bin/sh\nexit 125\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := comparePlan(repo, pythonCandidate("first", "raise RuntimeError('must not execute')"), pythonCandidate("invalid", "raise RuntimeError('must not execute')"))
	p.ProcessContainment = PIDNamespace
	p.Candidates[1].Env = map[string]string{"LD_PRELOAD": "/owner/injected-loader.so"}
	if err := ValidatePlan(p); err == nil || !strings.Contains(err.Error(), "containment configuration") {
		t.Fatalf("unsafe launcher peer was not rejected during whole-plan validation: %v", err)
	}
	output := filepath.Join(root, "run")
	if _, err := Run(context.Background(), p, output, false); err == nil || !strings.Contains(err.Error(), "LD_PRELOAD") {
		t.Fatalf("unsafe launcher configuration did not fail before admission: %v", err)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("invalid peer configuration created a run")
	}
}

func TestProcessContainmentGitLeaseIsRetainedByNamespaceMonitor(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux containment launcher")
	}
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "bwrap"), []byte("#!/bin/sh\nexit 125\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	lease, err := os.CreateTemp(t.TempDir(), "lease-")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	// The context is prepared synthetically to inspect argv only. Real probe
	// and hard-kill tests separately verify the actual namespace boundary.
	ctx := context.WithValue(context.Background(), processContainmentContextKey{}, PIDNamespace)
	cmd := exec.Command("/usr/bin/git", "--version")
	cmd.ExtraFiles = []*os.File{lease}
	if err := wrapGitProcess(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	if len(cmd.Args) < 3 || cmd.Args[1] != "--sync-fd" || cmd.Args[2] != "3" || len(cmd.ExtraFiles) != 1 || cmd.ExtraFiles[0] != lease {
		t.Fatalf("namespace monitor lost application lifetime lease: args=%v", cmd.Args)
	}
	cmd = exec.Command("/usr/bin/git", "--version")
	cmd.ExtraFiles = []*os.File{lease, lease}
	if err := wrapGitProcess(ctx, cmd); err == nil {
		t.Fatal("multiple inherited descriptors bypassed lifetime-lease validation")
	}
}
