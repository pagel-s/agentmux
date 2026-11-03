package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEarlyCredentialGuardRejectsPinnedIgnoreMetadata(t *testing.T) {
	const secret = "selected-secret-in-source-exclude-889190"
	for _, kind := range []string{"local-rule", "local-comment", "global-comment"} {
		t.Run(kind, func(t *testing.T) {
			repo, root := fixtureRepo(t)
			policy := []byte(secret + "\n")
			if strings.HasSuffix(kind, "comment") {
				policy = []byte("# " + secret + "\n")
			}
			if kind == "global-comment" {
				ignore := filepath.Join(root, "global-ignore")
				fixtureWrite(t, ignore, policy)
				home := filepath.Join(root, "home")
				if err := os.Mkdir(home, 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("HOME", home)
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
				fixtureWrite(t, filepath.Join(home, ".gitconfig"), []byte("[core]\n excludesfile = "+ignore+"\n"))
			} else {
				fixtureWrite(t, filepath.Join(repo, ".git", "info", "exclude"), policy)
			}
			p := credentialGuardPlan(repo, secret)
			p.Candidates[0].Env = map[string]string{"OPENAI_API_KEY": secret}
			ctx, _ := prepareCredentialGuard(t, p)
			before := privateSourceMetadata(t, repo)
			store := filepath.Join(root, "private.git")
			_, err := PrepareSnapshotStorage(ctx, StoragePrivate, repo, store)
			if !errors.Is(err, ErrIsolationCredentialLeak) {
				t.Fatalf("key-bearing ignore metadata accepted: %v", err)
			}
			retained, readErr := os.ReadFile(filepath.Join(store, "info", "exclude"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if bytes.Contains(retained, []byte(secret)) {
				t.Fatal("key-bearing ignore metadata reached the retained store")
			}
			if privateSourceMetadata(t, repo) != before {
				t.Fatal("ignore refusal modified source Git")
			}
		})
	}
}

func TestEarlyCredentialGuardRejectsQuarantineMetadata(t *testing.T) {
	for _, kind := range []string{"config", "exclude", "shallow", "ref-name", "ref-comment"} {
		t.Run(kind, func(t *testing.T) {
			secret := "selected-private-metadata-secret-813290"
			if kind == "shallow" {
				secret = strings.Repeat("e", 40)
			}
			ctx, repo, root, snap := preparePrivateFixture(t)
			p := credentialGuardPlan(repo, secret)
			p.Candidates[0].Env = map[string]string{"OPENAI_API_KEY": secret}
			early, _, cleanup, err := prepareIsolationCredentials(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			tools := filepath.Join(root, "metadata-shim")
			if err := os.Mkdir(tools, 0700); err != nil {
				t.Fatal(err)
			}
			script := "#!/usr/bin/python3\nimport os,sys,pathlib,subprocess\na=sys.argv[1:]\nif 'update-ref' in a:\n subprocess.run([" + fmt.Sprintf("%q", realGit) + "]+a,check=True)\n root=pathlib.Path(next(x.split('=',1)[1] for x in a if x.startswith('--git-dir=')))\n key=os.environ['PRIVATE_METADATA_SECRET']; kind=os.environ['PRIVATE_METADATA_KIND']\n if kind=='config':\n  with (root/'config').open('a') as f: f.write('\\n# '+key+'\\n')\n elif kind=='exclude': (root/'info'/'exclude').write_text('# '+key+'\\n')\n elif kind=='shallow': (root/'shallow').write_text(key+'\\n')\n elif kind=='ref-name': (root/'refs'/'heads'/key).write_text(os.environ['PRIVATE_METADATA_COMMIT']+'\\n')\n else: (root/'info'/'metadata-note').write_text(key+'\\n')\n sys.exit(0)\nos.execv(" + fmt.Sprintf("%q", realGit) + ",[" + fmt.Sprintf("%q", realGit) + "]+a)\n"
			if err := os.WriteFile(filepath.Join(tools, "git"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PRIVATE_METADATA_SECRET", secret)
			t.Setenv("PRIVATE_METADATA_KIND", kind)
			t.Setenv("PRIVATE_METADATA_COMMIT", snap.Commit)
			t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
			if _, err := privateCommandOutput(early, snap.Store, privateGitArgs(snap.Store), nil, "update-ref", "refs/agentmux/metadata-test", snap.Commit); err == nil {
				t.Fatal("key-bearing quarantine metadata was published")
			}
			if err := filepath.WalkDir(snap.Store, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if strings.Contains(path, secret) {
					return errors.New("credential in retained metadata path")
				}
				if entry.Type().IsRegular() {
					data, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					if bytes.Contains(data, []byte(secret)) {
						return errors.New("credential in retained metadata bytes")
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIsolationEvidenceSanitizesControllerFailures(t *testing.T) {
	const secret = "selected-controller-error-key-902199"
	repo, root := fixtureRepo(t)
	p := credentialGuardPlan(repo, secret)
	p.Candidates[0].Env = map[string]string{"OPENAI_API_KEY": secret}
	ctx, _ := prepareCredentialGuard(t, p)
	original := &os.PathError{Op: "open", Path: filepath.Join(root, secret), Err: os.ErrPermission}
	redacted := redactEarlyIsolationError(ctx, original)
	var extracted *os.PathError
	if !errors.As(redacted, &extracted) || extracted != original || strings.Contains(redacted.Error(), secret) {
		t.Fatal("redacted controller error lost its cause or retained the key")
	}
	report := Report{Error: original.Error(), Candidates: []CandidateResult{{ID: "writer", State: "failed", Error: original.Error(), Verification: &Verification{Passed: true}}}, Steps: []StepResult{{ID: "check", CandidateResult: CandidateResult{Error: original.Error()}}}}
	output := filepath.Join(root, "evidence.json")
	if err := saveIsolationJSON(ctx, output, &report); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil || bytes.Contains(data, []byte(secret)) || strings.Contains(report.Error, secret) || strings.Contains(report.Candidates[0].Error, secret) || strings.Contains(report.Steps[0].Error, secret) {
		t.Fatalf("controller diagnostics leaked: %v", err)
	}
	if report.Candidates[0].Verification != nil || !report.Candidates[0].CredentialsRedacted {
		t.Fatal("missing revision was represented as passing verification or redaction was unlabelled")
	}
}

func TestWorkflowIsolationRealControllerErrorNeverRetainsCredential(t *testing.T) {
	requireFullWorkflowIsolation(t)
	const secret = "selected-private-dir-error-secret-902199"
	for _, mode := range []string{"candidate-verification", "mission-verifier"} {
		t.Run(mode, func(t *testing.T) {
			repo, root := fixtureRepo(t)
			code := "import os; from pathlib import Path; p=Path('.git')/os.environ['OPENAI_API_KEY']; p.mkdir(); p.chmod(0); print('done')"
			p := comparePlan(repo, Candidate{ID: "writer", Harness: "command", Command: []string{"/usr/bin/python3", "-c", code}, Env: map[string]string{"OPENAI_API_KEY": secret}})
			p.Isolation = &IsolationOptions{}
			p.Checks[0].Command[0] = "/usr/bin/python3"
			if mode == "mission-verifier" {
				p.Mode = "mission"
				p.Candidates[0].Command[2] = "import os; from pathlib import Path; Path('ignored-secret').write_text(os.environ['OPENAI_API_KEY']); print('done')"
				p.Steps = []Step{{ID: "write", Candidate: "writer"}, {ID: "check", DependsOn: []string{"write"}, Verify: true}}
				p.Checks = []Check{{Name: "metadata-failure", Command: []string{"/usr/bin/python3", "-c", "from pathlib import Path; key=Path('ignored-secret').read_text(); p=Path('.git')/key; p.mkdir(); p.chmod(0); print('check ran')"}}}
			}
			output := filepath.Join(root, "run")
			r, err := Run(context.Background(), p, output, false)
			quarantine := r.Worktree
			if mode == "candidate-verification" && len(r.Candidates) > 0 {
				quarantine = r.Candidates[0].Worktree
			}
			defer os.Chmod(filepath.Join(quarantine, ".git", secret), 0700)
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("unsafe controller error: %v", err)
			}
			if _, err := LoadReport(output); err != nil {
				t.Fatalf("failed run cannot be inspected: %v", err)
			}
			if _, err := Explain(output); err != nil {
				t.Fatal(err)
			}
			if _, err := RenderReportTo(output, filepath.Join(output, "reinspected.html")); err != nil {
				t.Fatal(err)
			}
			assertRetainedFilesExclude(t, output, secret, quarantine)
			if mode == "candidate-verification" && r.Candidates[0].Verification != nil {
				t.Fatal("failed fingerprint produced a fabricated verification")
			}
			data, _ := os.ReadFile(filepath.Join(repo, "file.txt"))
			if string(data) != "base\n" {
				t.Fatal("controller failure changed source contents")
			}
		})
	}
}
