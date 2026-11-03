package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"agentmux/internal/workflow"
)

func requireDemoTools(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture uses a POSIX shell")
	}
	for _, name := range []string{"git", "sh"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s required", name)
		}
	}
}

func TestDemoRunsRealWorkflowAndPreservesOwnerFiles(t *testing.T) {
	requireDemoTools(t)
	root := t.TempDir()
	owner := filepath.Join(root, "owner")
	if err := os.Mkdir(owner, 0700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"keep.txt": "owner working file\n", ".gitconfig": "[user]\n name = Owner\n email = owner@example.invalid\n", "provider-credential": "private-token\n"} {
		if err := os.WriteFile(filepath.Join(owner, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Ambient Git redirection must never make fixture preparation touch this
	// directory. Provider credentials and Agentmux state need no discovery.
	t.Setenv("HOME", owner)
	t.Setenv("XDG_STATE_HOME", owner)
	t.Setenv("GIT_DIR", owner)
	t.Setenv("GIT_WORK_TREE", owner)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(owner, "owner-index"))
	t.Setenv("OPENAI_API_KEY", "private-token")
	t.Chdir(owner)
	before := demoTreeDigest(t, owner)
	output := filepath.Join(root, "demo with 'quotes'")
	var out, diagnostics bytes.Buffer
	if code := runDemoIO(context.Background(), []string{"--output", output}, &out, &diagnostics); code != exitSuccess {
		t.Fatalf("demo failed: %d\n%s\n%s", code, &out, &diagnostics)
	}
	if after := demoTreeDigest(t, owner); !reflect.DeepEqual(before, after) {
		t.Fatalf("demo changed owner files: before=%v after=%v", before, after)
	}
	var record demoRecord
	readDemoJSON(t, filepath.Join(output, "demo.json"), &record)
	if record.State != "completed" || !record.Applied || record.Phase != "finished" {
		t.Fatalf("incomplete demo evidence: %+v", record)
	}
	if err := verifyDemoFile(filepath.Join(record.Repository, "hello.txt"), demoChanged); err != nil {
		t.Fatal(err)
	}
	report, err := workflow.LoadReport(record.Run)
	if err != nil || report.Storage != workflow.StoragePrivate || report.State != "completed" || len(report.Candidates) != 1 || report.Candidates[0].Harness != "command" || report.Candidates[0].Verification == nil || !report.Candidates[0].Verification.Passed {
		t.Fatalf("demo bypassed real private workflow verification: %+v %v", report, err)
	}
	patch, err := os.ReadFile(record.Patch)
	if err != nil || !bytes.Contains(patch, []byte("-Agentmux demo: starting file.")) || !bytes.Contains(patch, []byte("+Agentmux demo: checked change applied.")) {
		t.Fatalf("missing retained change: %s %v", patch, err)
	}
	for _, name := range []string{"report.json", "report.html"} {
		if _, err := os.Stat(filepath.Join(record.Run, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, text := range []string{"Success:", "no model calls", "amx flow status " + shellQuote(record.Run), "only to the throwaway", "not evidence of AI quality"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("missing useful output %q: %s", text, &out)
		}
	}
	if info, err := os.Stat(output); err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatalf("demo output is not private: %v %v", info, err)
	}
}

func TestDemoRefusesExistingOutputsIncludingSymlinks(t *testing.T) {
	requireDemoTools(t)
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			output := filepath.Join(root, "existing")
			switch kind {
			case "file":
				if err := os.WriteFile(output, []byte("preserve me"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(output, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(root, "target")
				if err := os.WriteFile(target, []byte("preserve target"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, output); err != nil {
					t.Fatal(err)
				}
			}
			before := demoTreeDigest(t, root)
			var out, diagnostics bytes.Buffer
			if code := runDemoIO(context.Background(), []string{"--output", output}, &out, &diagnostics); code != exitValidation {
				t.Fatalf("existing output accepted: %d %s %s", code, &out, &diagnostics)
			}
			if after := demoTreeDigest(t, root); !reflect.DeepEqual(before, after) {
				t.Fatalf("collision changed existing paths: before=%v after=%v", before, after)
			}
		})
	}
}

func TestDemoInterruptionKeepsDiagnostics(t *testing.T) {
	requireDemoTools(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output := filepath.Join(t.TempDir(), "interrupted")
	var out, diagnostics bytes.Buffer
	if code := runDemoIO(ctx, []string{"--output", output}, &out, &diagnostics); code != exitRuntime {
		t.Fatalf("canceled demo reported success: %d %s %s", code, &out, &diagnostics)
	}
	var record demoRecord
	readDemoJSON(t, filepath.Join(output, "demo.json"), &record)
	if record.State != "interrupted" || record.Applied || !strings.Contains(record.Error, "context canceled") {
		t.Fatalf("lost interruption evidence: %+v", record)
	}
	if !strings.Contains(out.String(), output) || !strings.Contains(diagnostics.String(), "Retained diagnostics:") {
		t.Fatalf("missing diagnostic location: %s %s", &out, &diagnostics)
	}
}

func TestDemoGitFailureKeepsPhaseAndDoesNotClaimSuccess(t *testing.T) {
	requireDemoTools(t)
	root := t.TempDir()
	fakeBin := filepath.Join(root, "bin")
	if err := os.Mkdir(fakeBin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "git"), []byte("#!/bin/sh\nprintf '%s\\n' 'fixture Git failed' >&2\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output := filepath.Join(root, "failed")
	var out, diagnostics bytes.Buffer
	if code := runDemoIO(context.Background(), []string{"--output", output}, &out, &diagnostics); code != exitRuntime || strings.Contains(out.String(), "Success:") {
		t.Fatalf("failed Git accepted: %d %s %s", code, &out, &diagnostics)
	}
	var record demoRecord
	readDemoJSON(t, filepath.Join(output, "demo.json"), &record)
	if record.State != "failed" || record.Applied || record.Phase != "prepare" || !strings.Contains(record.Error, "fixture Git failed") {
		t.Fatalf("lost failed phase: %+v", record)
	}
}

func TestDemoCandidateFailureRetainsReportWithoutApplying(t *testing.T) {
	requireDemoTools(t)
	root := t.TempDir()
	fakeBin := filepath.Join(root, "bin")
	if err := os.Mkdir(fakeBin, 0700); err != nil {
		t.Fatal(err)
	}
	// Only the deterministic candidate/check runner uses this shell. Git and
	// snapshot plumbing still run normally, so this exercises real failure
	// publication and confirms the original fixture was never integrated.
	if err := os.WriteFile(filepath.Join(fakeBin, "sh"), []byte("#!/bin/sh\nprintf '%s\\n' 'candidate failed before changing files' >&2\nexit 9\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output := filepath.Join(root, "failed-candidate")
	var out, diagnostics bytes.Buffer
	if code := runDemoIO(context.Background(), []string{"--output", output}, &out, &diagnostics); code != exitRuntime || strings.Contains(out.String(), "Success:") {
		t.Fatalf("failed candidate accepted: %d %s %s", code, &out, &diagnostics)
	}
	var record demoRecord
	readDemoJSON(t, filepath.Join(output, "demo.json"), &record)
	if record.State != "failed" || record.Applied || record.Phase != "run-and-check" {
		t.Fatalf("lost failure evidence: %+v", record)
	}
	if err := verifyDemoFile(filepath.Join(record.Repository, "hello.txt"), demoOriginal); err != nil {
		t.Fatalf("failed candidate changed source fixture: %v", err)
	}
	report, err := workflow.LoadReport(record.Run)
	if err != nil || report.State != "failed" || len(report.Candidates) != 1 || report.Candidates[0].State == "verified" {
		t.Fatalf("lost real workflow failure: %+v %v", report, err)
	}
}

func TestDemoHelpAndMissingDependencyNeedNoWorkspace(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{{"--help"}, {"-h"}, {"--output", filepath.Join(t.TempDir(), "help-does-not-create"), "--help"}} {
		var out, diagnostics bytes.Buffer
		if code := runDemoIO(context.Background(), args, &out, &diagnostics); code != exitSuccess || !strings.Contains(out.String(), "Usage: amx demo") {
			t.Fatalf("help required dependencies: %d %s %s", code, &out, &diagnostics)
		}
	}
	output := filepath.Join(t.TempDir(), "must-not-exist")
	var out, diagnostics bytes.Buffer
	if code := runDemoIO(context.Background(), []string{"--output", output}, &out, &diagnostics); code != exitDependency {
		t.Fatalf("missing Git accepted: %d %s", code, &diagnostics)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("dependency failure created output: %v", err)
	}
}

func readDemoJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func demoTreeDigest(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[relative] = "directory"
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			result[relative] = "symlink:" + target
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		result[relative] = string(digest[:])
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
