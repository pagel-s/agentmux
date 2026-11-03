package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func guidedFixtureProfiles() (*Candidate, *Candidate) {
	coder := &Candidate{Harness: "command", Command: []string{os.Args[0], "-test.run=^$"}}
	reviewer := &Candidate{Harness: "command", Command: []string{os.Args[0], "-test.run=^$"}}
	return coder, reviewer
}

func guidedFixtureOptions(t *testing.T) (GuidedOptions, string) {
	t.Helper()
	repo, root := fixtureRepo(t)
	coder, reviewer := guidedFixtureProfiles()
	return GuidedOptions{Repository: repo, Goal: "Fix the supplied regression", Output: filepath.Join(root, "run"), CheckCommand: []string{os.Args[0], "-test.run=^$"}, Coder: coder, Reviewer: reviewer}, root
}

func guidedFakeTools(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 97\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGuidedPreviewHasCompleteReviewLoopWithoutSideEffects(t *testing.T) {
	options, root := guidedFixtureOptions(t)
	options.Output = filepath.Join(root, "catalog", "project", "runs", "new-run")
	beforeIndex := fixtureGit(t, options.Repository, "diff", "--cached", "--binary")
	beforeRefs := fixtureGit(t, options.Repository, "show-ref")
	preview, err := PrepareGuided(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Plan.Kind != "work" || preview.Plan.Execution != "local" || preview.Plan.Mode != "mission" || preview.MaxNativeTurns != 6 || len(preview.Phases) != 5 {
		t.Fatalf("incomplete preview: %+v", preview)
	}
	steps := preview.Plan.Steps
	if len(steps) != 5 || !steps[0].Verify || steps[0].ID != "implement" || steps[2].ID != "address-review" || !steps[2].Verify || !steps[4].Verify {
		t.Fatalf("missing implementation feedback loop: %+v", steps)
	}
	for _, index := range []int{1, 3} {
		if !steps[index].ReadOnly || !steps[index].Review || !steps[index].RequireResult {
			t.Fatalf("review is not bound and read-only: %+v", steps[index])
		}
	}
	if steps[1].GateReview || !steps[3].GateReview {
		t.Fatal("final review must gate integration after findings can be addressed")
	}
	if _, err := os.Stat(filepath.Join(root, "catalog")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview created its catalog parent")
	}
	if fixtureGit(t, options.Repository, "diff", "--cached", "--binary") != beforeIndex || fixtureGit(t, options.Repository, "show-ref") != beforeRefs {
		t.Fatal("preview modified source staging or refs")
	}
	if got := fixtureGit(t, options.Repository, "status", "--porcelain"); got != "" {
		t.Fatalf("preview modified source: %q", got)
	}
}

func TestGuidedDefaultProfilesUseOnlyCodexAndGemini(t *testing.T) {
	options, _ := guidedFixtureOptions(t)
	options.Coder, options.Reviewer = nil, nil
	guidedFakeTools(t, "codex", "agy")
	preview, err := PrepareGuided(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	coder, reviewer := preview.Plan.Candidates[0], preview.Plan.Candidates[1]
	if coder.Harness != "codex" || coder.Model != "gpt-6.1-sol" || reviewer.Harness != "antigravity" || reviewer.Model != "gemini-3.8-flash-low" {
		t.Fatalf("unexpected provider defaults: %+v", preview.Plan.Candidates)
	}
	if strings.Contains(strings.Join(reviewer.Command, " "), "accept-edits") || !strings.Contains(strings.Join(reviewer.Command, " "), "--mode plan") || !strings.Contains(strings.Join(reviewer.Command, " "), "--json-schema") {
		t.Fatalf("reviewer profile lacks structured read-only policy: %v", reviewer.Command)
	}
}

func TestGuidedDetectsChecksAndRefusesAmbiguity(t *testing.T) {
	guidedFakeTools(t, "go", "npm")
	for _, test := range []struct {
		name, goMod, pkg, wantSource string
		want                         []string
		wantError                    bool
	}{
		{name: "go", goMod: "module fixture\n", want: []string{"go", "test", "./..."}, wantSource: "go.mod"},
		{name: "npm", pkg: `{"name":"fixture","scripts":{"test":"node test.js"}}`, want: []string{"npm", "test"}, wantSource: "package.json"},
		{name: "both", goMod: "module fixture\n", pkg: `{"scripts":{"test":"node test.js"}}`, wantError: true},
		{name: "no-npm-test", pkg: `{"name":"fixture","scripts":{"build":"node build.js"}}`, wantError: true},
		{name: "missing-check", wantError: true},
		{name: "ambiguous-package", pkg: `{"scripts":{"test":"true","TEST":"false"}}`, wantError: true},
		{name: "malformed-package", pkg: `{"scripts":`, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, _ := guidedFixtureOptions(t)
			options.CheckCommand = nil
			if test.goMod != "" {
				fixtureWrite(t, filepath.Join(options.Repository, "go.mod"), []byte(test.goMod))
			}
			if test.pkg != "" {
				fixtureWrite(t, filepath.Join(options.Repository, "package.json"), []byte(test.pkg))
			}
			preview, err := PrepareGuided(context.Background(), options)
			if (err != nil) != test.wantError {
				t.Fatalf("preflight=%v wantError=%t", err, test.wantError)
			}
			if test.wantError {
				if _, statErr := os.Stat(options.Output); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatal("failed detection created output")
				}
				return
			}
			if !reflect.DeepEqual(preview.Plan.Checks[0].Command, test.want) || !strings.Contains(preview.CheckSource, test.wantSource) {
				t.Fatalf("detection: %+v %s", preview.Plan.Checks, preview.CheckSource)
			}
		})
	}
}

func TestGuidedExplicitArgvIsLiteralAndOverridesProjectMetadata(t *testing.T) {
	options, root := guidedFixtureOptions(t)
	fixtureWrite(t, filepath.Join(options.Repository, "package.json"), []byte("not JSON"))
	marker := filepath.Join(root, "must-not-execute")
	options.CheckCommand = []string{os.Args[0], "$(touch " + marker + ")", "space and 'quotes'", "; ignored"}
	before := append([]string(nil), options.CheckCommand...)
	preview, err := PrepareGuided(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.Plan.Checks[0].Command, before) || !reflect.DeepEqual(options.CheckCommand, before) {
		t.Fatal("explicit argv changed")
	}
	preview.Plan.Checks[0].Command[1] = "changed"
	if !reflect.DeepEqual(options.CheckCommand, before) {
		t.Fatal("preview shares mutable check argv with caller")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preflight interpreted or executed argv")
	}
}

func TestGuidedPreflightRejectsUnsafeOrUnavailableRequests(t *testing.T) {
	for _, name := range []string{"blank-goal", "invalid-utf8", "invalid-path-utf8", "invalid-argv-utf8", "inside-project", "existing-output", "missing-coder", "missing-check", "missing-parent-link", "unapproved-provider", "non-gemini-reviewer"} {
		t.Run(name, func(t *testing.T) {
			options, root := guidedFixtureOptions(t)
			switch name {
			case "blank-goal":
				options.Goal = " \n\t"
			case "invalid-utf8":
				options.Goal = "bad\xff"
			case "invalid-path-utf8":
				options.Output += "\xff"
			case "invalid-argv-utf8":
				options.CheckCommand = []string{os.Args[0], "bad\xff"}
			case "inside-project":
				folder := filepath.Join(options.Repository, "folder")
				if err := os.Mkdir(folder, 0700); err != nil {
					t.Fatal(err)
				}
				options.Output = filepath.Join(options.Repository, "output")
				options.Repository = folder
			case "existing-output":
				fixtureWrite(t, options.Output, []byte("preserve"))
			case "missing-coder":
				options.Coder = &Candidate{Harness: "command", Command: []string{filepath.Join(root, "not-installed")}}
			case "missing-check":
				options.CheckCommand = []string{filepath.Join(root, "not-installed")}
			case "missing-parent-link":
				link := filepath.Join(root, "broken")
				if err := os.Symlink(filepath.Join(root, "missing"), link); err != nil {
					t.Fatal(err)
				}
				options.Output = filepath.Join(link, "runs", "new")
			case "unapproved-provider":
				options.Coder = &Candidate{Harness: "unsupported"}
			case "non-gemini-reviewer":
				options.Reviewer = &Candidate{Harness: "antigravity", Model: "claude-unapproved"}
			}
			if _, err := PrepareGuided(context.Background(), options); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}

func TestGuidedCancellationAndModelGateProduceNoRun(t *testing.T) {
	options, _ := guidedFixtureOptions(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PrepareGuided(ctx, options); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	options.Coder, options.Reviewer = nil, nil
	guidedFakeTools(t, "codex", "agy")
	if _, err := RunGuided(context.Background(), options, false); err == nil {
		t.Fatal("models executed without the explicit opt-in")
	}
	if _, err := os.Stat(options.Output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("model-gated run created output")
	}
}

func TestGuidedCommandJourneyProducesReviewedCheckedArtifact(t *testing.T) {
	options, _ := guidedFixtureOptions(t)
	options.Coder = &Candidate{Harness: "command", Command: []string{"python3", "-c", "from pathlib import Path; Path('file.txt').write_text('fixed\\n'); print('Fixed file.txt')"}}
	options.Reviewer = &Candidate{Harness: "command", Command: []string{"python3", "-c", "import json; from pathlib import Path; assert Path('file.txt').read_text()=='fixed\\n'; print(json.dumps({'decision':'approve','summary':'Inspected the supplied implementation; no actionable defects.','findings':[]}))"}}
	options.CheckCommand = []string{"python3", "-c", "from pathlib import Path; assert Path('file.txt').read_text()=='fixed\\n'"}
	report, err := RunGuided(context.Background(), options, false)
	if err != nil {
		t.Fatalf("journey failed: %+v %v", report, err)
	}
	if report.Kind != "work" || report.State != "completed" || len(report.Steps) != 5 || report.Verification == nil || !report.Verification.Passed {
		t.Fatalf("incomplete journey: %+v", report)
	}
	data, err := os.ReadFile(filepath.Join(options.Repository, "file.txt"))
	if err != nil || string(data) != "base\n" {
		t.Fatal("guided work modified its source")
	}
	if _, err := LoadReport(report.Output); err != nil {
		t.Fatalf("saved run not inspectable: %v", err)
	}
}
