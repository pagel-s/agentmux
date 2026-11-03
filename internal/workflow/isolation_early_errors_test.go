package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func invokeEarlyCredentialWorkflow(ctx context.Context, mode string, p Plan, output string) ([]byte, error) {
	switch mode {
	case "run":
		report, err := Run(ctx, p, output, true)
		encoded, _ := json.Marshal(report)
		return encoded, err
	case "watch":
		report, err := Watch(ctx, p, output, true, WatchOptions{IntervalSeconds: 1, MaxRuns: 1, TimeoutSeconds: 10})
		encoded, _ := json.Marshal(report)
		return encoded, err
	default:
		report, err := Lab(ctx, p, output, true, 1)
		encoded, _ := json.Marshal(report)
		return encoded, err
	}
}

func assertEarlyCredentialRefusal(t *testing.T, mode string, p Plan, output, secret string) error {
	t.Helper()
	report, err := invokeEarlyCredentialWorkflow(context.Background(), mode, p, output)
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(string(report), secret) {
		t.Fatalf("early refusal exposed selected authentication or succeeded: report=%s error=%v", report, err)
	}
	if _, statErr := os.Lstat(output); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("early refusal created workflow output: %v", statErr)
	}
	return err
}

func TestEarlyCredentialErrorsProtectPlanValidation(t *testing.T) {
	const secret = "selected-plan-error-secret-782635"
	for _, mode := range []string{"run", "watch", "lab"} {
		for _, invalid := range []string{"candidate-id", "duplicate-id", "harness", "environment"} {
			t.Run(mode+"/"+invalid, func(t *testing.T) {
				repo, root := fixtureRepo(t)
				p := credentialGuardPlan(repo, secret)
				p.Candidates[0].Env = map[string]string{"SELECTED_API_KEY": secret}
				switch invalid {
				case "candidate-id":
					p.Candidates[0].ID = "invalid/" + secret
				case "duplicate-id":
					p.Candidates[0].ID = secret
					p.Candidates = append(p.Candidates, p.Candidates[0])
				case "harness":
					p.Candidates[0].Harness = secret
				case "environment":
					p.Candidates[0].Env["invalid-"+secret] = "safe"
				}
				assertEarlyCredentialRefusal(t, mode, p, filepath.Join(root, "output"), secret)
			})
		}
	}
}

func TestEarlyCredentialEnvironmentRegistrationIsBoundedAndPinsAmbient(t *testing.T) {
	const secret = "selected-before-validation-632871"
	const later = "selected-after-validation-592610"
	p := credentialGuardPlan(t.TempDir(), secret)
	p.Candidates[0].APIKeyEnv = "SELECTED_AMBIENT_CREDENTIAL"
	t.Setenv(p.Candidates[0].APIKeyEnv, secret)
	ctx, p, cleanup, err := registerIsolationEnvironment(context.Background(), p)
	defer cleanup()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(p.Candidates[0].APIKeyEnv, later)
	if p.Candidates[0].Env[p.Candidates[0].APIKeyEnv] != secret || !containsIsolationCredential(ctx, []byte(secret)) || containsIsolationCredential(ctx, []byte(later)) {
		t.Fatal("initial environment selection was not pinned")
	}
	p.Candidates[0].Env["SELECTED_API_KEY"] = strings.Repeat("s", (128<<10)+1)
	_, _, oversizedCleanup, err := registerIsolationEnvironment(ctx, p)
	oversizedCleanup()
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unbounded early credential input was admitted: %v", err)
	}
}

func TestWorkflowIsolationRealEarlyCredentialGitErrors(t *testing.T) {
	requireFullWorkflowIsolation(t)
	const secret = "selected-initial-git-error-secret-425837"
	for _, mode := range []string{"run", "watch", "lab"} {
		for _, selection := range []string{"environment", "native-auth"} {
			t.Run(mode+"/"+selection, func(t *testing.T) {
				root := t.TempDir()
				repo := filepath.Join(root, "source")
				if err := os.Mkdir(repo, 0700); err != nil {
					t.Fatal(err)
				}
				fixtureGit(t, repo, "init", "-q")
				// A missing gitdir diagnostic names either the pointer or its target
				// depending on Git's version. An invalid numeric config value emits
				// the selected value itself, without putting it in plan metadata.
				config := "[core]\n\trepositoryformatversion = " + secret + "\n"
				fixtureWrite(t, filepath.Join(repo, ".git", "config"), []byte(config))
				metadata := privateSourceMetadata(t, repo)
				_, gitErr := repositoryRoot(context.Background(), repo)
				if gitErr == nil || !strings.Contains(gitErr.Error(), secret) {
					t.Fatalf("fixture must produce an initial Git diagnostic containing the selected credential: %v", gitErr)
				}
				p := credentialGuardPlan(repo, secret)
				if selection == "environment" {
					p.Candidates[0].Harness = "codex"
					p.Candidates[0].APIKeyEnv = "SELECTED_API_KEY"
					t.Setenv("SELECTED_API_KEY", secret)
				} else {
					auth := filepath.Join(root, "auth.json")
					fixtureWrite(t, auth, []byte(`{"OPENAI_API_KEY":"`+secret+`"}`))
					if err := os.Chmod(auth, 0600); err != nil {
						t.Fatal(err)
					}
					p.Candidates[0].Harness = "codex"
					p.Isolation.Auth = map[string]IsolationAuth{"writer": {File: auth}}
				}
				err := assertEarlyCredentialRefusal(t, mode, p, filepath.Join(root, "output"), secret)
				if !strings.Contains(err.Error(), "[redacted credential]") {
					t.Fatalf("expected the initial Git diagnostic with a redacted credential: %v", err)
				}
				if expected := strings.ReplaceAll(gitErr.Error(), secret, "[redacted credential]"); err.Error() != expected {
					t.Fatalf("early refusal did not preserve the redacted initial Git diagnostic: got %q, want %q", err, expected)
				}
				if privateSourceMetadata(t, repo) != metadata {
					t.Fatal("early refusal changed source metadata")
				}
			})
		}
	}
}

func TestEarlyCredentialAuthDependencyPolicyUsesCanonicalPaths(t *testing.T) {
	repo, root := fixtureRepo(t)
	authDir := filepath.Join(root, "credentials")
	if err := os.Mkdir(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "dependency")
	if err := os.Symlink(authDir, alias); err != nil {
		t.Fatal(err)
	}
	p := credentialGuardPlan(repo, "")
	p.Candidates[0].Harness = "codex"
	p.Isolation.ReadOnlyPaths = []string{alias}
	p.Isolation.Auth = map[string]IsolationAuth{"writer": {File: filepath.Join(authDir, "auth.json")}}
	if err := ValidatePlan(p); err == nil || !strings.Contains(err.Error(), "dependency would expose") {
		t.Fatalf("canonical authentication dependency was admitted: %v", err)
	}
}
