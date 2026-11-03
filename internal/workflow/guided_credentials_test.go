package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func guidedCredentialFixture(t *testing.T, selection, secret string) (GuidedOptions, string) {
	t.Helper()
	options, root := guidedFixtureOptions(t)
	options.Isolation = &IsolationOptions{}
	options.Coder = &Candidate{Harness: "codex", Model: "fixture-model", Command: []string{"/usr/bin/true"}}
	if selection == "auth" {
		auth := filepath.Join(root, "selected-auth.json")
		data, err := json.Marshal(map[string]string{"OPENAI_API_KEY": secret})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(auth, data, 0600); err != nil {
			t.Fatal(err)
		}
		options.Isolation.Auth = map[string]IsolationAuth{"coder": {File: auth}}
	} else {
		options.Coder.APIKeyEnv = "GUIDED_OWNER_KEY"
		options.Coder.Env = map[string]string{"GUIDED_OWNER_KEY": secret}
	}
	return options, root
}

func invokeGuidedCredentialAPI(ctx context.Context, operation string, options GuidedOptions) error {
	if operation == "preview" {
		_, err := PrepareGuided(ctx, options)
		return err
	}
	_, err := RunGuided(ctx, options, false)
	return err
}

func TestGuidedAPICredentialGuardPrecedesPreflightErrors(t *testing.T) {
	for _, operation := range []string{"preview", "run"} {
		for _, selection := range []string{"environment", "auth"} {
			for _, failure := range []string{"source-git", "snapshot", "metadata", "paths", "check-argv", "profile", "executable", "cancelled"} {
				t.Run(operation+"/"+selection+"/"+failure, func(t *testing.T) {
					secret := "selected-guided-preflight-private-value-730619"
					switch failure {
					case "snapshot":
						secret = "snapshot admission"
					case "metadata":
						secret = "e"
					case "paths":
						secret = "project"
					case "check-argv":
						secret = "check"
					case "profile":
						secret = "guided work"
					case "cancelled":
						secret = "canceled"
					}
					options, root := guidedCredentialFixture(t, selection, secret)
					ctx := context.Background()
					switch failure {
					case "source-git":
						if err := os.RemoveAll(filepath.Join(options.Repository, ".git")); err != nil {
							t.Fatal(err)
						}
						fixtureWrite(t, filepath.Join(options.Repository, ".git"), []byte("gitdir: "+filepath.Join(root, secret)+"\n"))
					case "snapshot":
						fixtureWrite(t, filepath.Join(options.Repository, ".git", "index"), []byte("malformed index"))
					case "metadata":
						options.Goal = ""
					case "paths":
						options.Output += "\xff"
					case "check-argv":
						options.CheckCommand = []string{"bad\xff"}
					case "profile":
						options.Reviewer = &Candidate{Harness: "unsupported"}
					case "executable":
						options.CheckCommand = []string{filepath.Join(root, secret)}
					case "cancelled":
						cancelled, cancel := context.WithCancel(ctx)
						cancel()
						ctx = cancelled
					}
					err := invokeGuidedCredentialAPI(ctx, operation, options)
					if err == nil || strings.Contains(err.Error(), secret) {
						t.Fatalf("selected credential escaped %s: %v", failure, err)
					}
					if failure == "cancelled" && !errors.Is(err, context.Canceled) {
						t.Fatalf("credential guard lost cancellation identity: %v", err)
					}
					if _, err := os.Lstat(options.Output); !errors.Is(err, os.ErrNotExist) && failure != "paths" {
						t.Fatalf("failed preflight created run files: %v", err)
					}
				})
			}
		}
	}
}

func TestGuidedAPIUninspectedAuthRefusesAllDiagnostics(t *testing.T) {
	for _, operation := range []string{"preview", "run"} {
		for _, failure := range []string{"missing-file", "invalid-file", "unknown-harness", "unknown-candidate", "duplicate-candidate", "environment-bound", "missing-environment"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				options, _ := guidedCredentialFixture(t, "auth", "selected")
				auth := options.Isolation.Auth["coder"]
				switch failure {
				case "missing-file":
					if err := os.Remove(auth.File); err != nil {
						t.Fatal(err)
					}
				case "invalid-file":
					fixtureWrite(t, auth.File, []byte("not JSON"))
				case "unknown-harness":
					options.Coder.Harness = "unsupported"
				case "unknown-candidate":
					options.Isolation.Auth = map[string]IsolationAuth{"absent": auth}
				case "duplicate-candidate":
					options.Reviewer.ID = "coder"
				case "environment-bound":
					options.Reviewer.Env = map[string]string{strings.Repeat("x", 129): "safe"}
				case "missing-environment":
					options.Reviewer.APIKeyEnv = "GUIDED_UNAVAILABLE_FIXTURE_KEY"
					t.Setenv(options.Reviewer.APIKeyEnv, "")
				}
				// Unknown auth bytes could equal any static error text, including
				// a metadata error that would otherwise occur before auth loading.
				options.Goal = ""
				err := invokeGuidedCredentialAPI(context.Background(), operation, options)
				if err == nil || err.Error() != "" {
					t.Fatalf("uninspected auth produced unchecked diagnostics: %v", err)
				}
				if _, err := os.Lstat(options.Output); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("uninspected auth created run files: %v", err)
				}
			})
		}
	}
}

func TestGuidedCredentialRegistrationStartsNoProcessAndKeepsOuterGuard(t *testing.T) {
	const secret = "selected-guided-registration-private-value-601927"
	options, root := guidedCredentialFixture(t, "auth", secret)
	marker := filepath.Join(root, "process-started")
	tools := filepath.Join(root, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	for _, binary := range []string{"git", "bwrap", "codex"} {
		fixtureWrite(t, filepath.Join(tools, binary), []byte(fmt.Sprintf("#!/bin/sh\n: > %q\nexit 97\n", marker)))
		if err := os.Chmod(filepath.Join(tools, binary), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools)
	ctx, outer, closeOuter, err := PrepareCLIPublication(context.Background(), Plan{Isolation: &IsolationOptions{}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeOuter()
	_, _, cleanup, err := prepareGuidedCredentials(ctx, options)
	cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("credential selection started a process")
	}
	if err := outer.CheckOutput([]byte(secret), false); err == nil {
		t.Fatal("guided selection hid credentials from the outer publication guard")
	}
	if _, err := os.Lstat(options.Output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("credential selection created run files")
	}
}

func TestGuidedCredentialPreviewPreservesSelectedExecutionValues(t *testing.T) {
	const secret = "selected-guided-preview-private-value-154862"
	for _, selection := range []string{"environment", "ambient", "auth"} {
		t.Run(selection, func(t *testing.T) {
			options, _ := guidedCredentialFixture(t, selection, secret)
			if selection == "ambient" {
				options.Coder.Env = nil
				t.Setenv(options.Coder.APIKeyEnv, secret)
			}
			before := privateSourceMetadata(t, options.Repository)
			preview, err := PrepareGuided(context.Background(), options)
			if err != nil {
				t.Fatalf("valid credential selection refused: %v", err)
			}
			candidate := preview.Plan.Candidates[0]
			if selection == "auth" {
				if preview.Plan.Isolation.Auth["coder"] != options.Isolation.Auth["coder"] {
					t.Fatal("preview changed the selected native authentication capability")
				}
			} else if candidate.APIKeyEnv != options.Coder.APIKeyEnv || candidate.Env[candidate.APIKeyEnv] != secret {
				t.Fatal("preview redacted or lost the exact execution credential")
			}
			encoded, err := json.Marshal(preview)
			if err != nil {
				t.Fatalf("safe preview could not be serialized: %v", err)
			}
			assertIsolationJSONExcludes(t, encoded, secret)
			var published GuidedPreview
			if err := json.Unmarshal(encoded, &published); err != nil {
				t.Fatal(err)
			}
			if len(published.Plan.Candidates[0].Env) != 0 {
				t.Fatal("serialized preview retained execution environment values")
			}
			if selection != "auth" && preview.Plan.Candidates[0].Env[candidate.APIKeyEnv] != secret {
				t.Fatal("serializing the preview changed the execution plan")
			}
			if selection == "ambient" {
				t.Setenv(options.Coder.APIKeyEnv, "changed-after-preview")
				if candidate.Env[candidate.APIKeyEnv] != secret || options.Coder.Env != nil {
					t.Fatal("ambient selection was not pinned independently of owner options")
				}
			}
			if privateSourceMetadata(t, options.Repository) != before {
				t.Fatal("credential preview changed source metadata")
			}
			if _, err := os.Lstat(options.Output); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("credential preview created run files")
			}
		})
	}
}

func TestGuidedCredentialPreviewRefusesPublicMetadataCollisions(t *testing.T) {
	for _, selection := range []string{"environment", "auth"} {
		for _, collision := range []struct {
			name, secret string
		}{
			{"schema", "plan"},
			{"boolean", "true"},
			{"number", "6"},
			{"path", "selected-guided-output-private-value-483729"},
			{"escaped-metadata", "selected-guided-<&>-private-value-871625"},
			{"static-authority", "Required filesystem"},
		} {
			t.Run(selection+"/"+collision.name, func(t *testing.T) {
				options, _ := guidedCredentialFixture(t, selection, collision.secret)
				if collision.name == "path" {
					options.Output += collision.secret
				}
				if collision.name == "escaped-metadata" {
					options.Goal = "Review " + collision.secret
				}
				preview, err := PrepareGuided(context.Background(), options)
				if err == nil || strings.Contains(err.Error(), collision.secret) {
					t.Fatalf("credential-bearing preview was not safely refused: %v", err)
				}
				if preview.Repository != "" || preview.Output != "" || len(preview.Plan.Candidates) != 0 {
					t.Fatal("refused preview returned its private execution plan")
				}
				if _, err := os.Lstat(options.Output); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("refused preview created run files")
				}
			})
		}
	}
}

func TestWorkflowIsolationRealGuidedCredentialsRemainUsable(t *testing.T) {
	requireFullWorkflowIsolation(t)
	const secret = "selected-guided-native-private-value-982731"
	for _, selection := range []string{"environment", "auth"} {
		t.Run(selection, func(t *testing.T) {
			options, _ := guidedCredentialFixture(t, selection, secret)
			key := "os.environ['OPENAI_API_KEY']"
			if selection == "auth" {
				key = "json.loads(pathlib.Path('/home/agent/.codex/auth.json').read_text())['OPENAI_API_KEY']"
			}
			code := "import sys,json,pathlib,hashlib,os; sys.stdin.read(); key=" + key + "; assert key; pathlib.Path('auth-digest').write_text(hashlib.sha256(key.encode()).hexdigest()); pathlib.Path('file.txt').write_text('fixed\\n'); print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'Used selected authentication'}})); print('{\"type\":\"turn.completed\"}')"
			options.Coder.Command = []string{"/usr/bin/python3", "-c", code}
			options.Reviewer = &Candidate{Harness: "command", Command: []string{"/usr/bin/python3", "-c", "import json,pathlib; assert pathlib.Path('file.txt').read_text()=='fixed\\n'; print(json.dumps({'decision':'approve','summary':'Inspected the current revision.','findings':[]}))"}}
			digest := sha256.Sum256([]byte(secret))
			check := "import os; from pathlib import Path; assert Path('file.txt').read_text()=='fixed\\n'; assert Path('auth-digest').read_text()==" + fmt.Sprintf("%q", hex.EncodeToString(digest[:])) + "; assert not os.environ.get('OPENAI_API_KEY'); assert not Path('/home/agent/.codex/auth.json').exists()"
			options.CheckCommand = []string{"/usr/bin/python3", "-c", check}
			options.MaxAttempts = 1
			report, err := RunGuided(context.Background(), options, true)
			if err != nil || report.State != "completed" || report.Verification == nil || !report.Verification.Passed {
				t.Fatalf("guided execution lost its selected capability: state=%s error=%v", report.State, err)
			}
			assertRetainedFilesExclude(t, report.Output, secret, "")
			if data, err := os.ReadFile(filepath.Join(options.Repository, "file.txt")); err != nil || string(data) != "base\n" {
				t.Fatal("guided execution changed its source")
			}
		})
	}
}
