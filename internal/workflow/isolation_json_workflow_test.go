package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowIsolationRealJSONNumericNativeCredential(t *testing.T) {
	requireFullWorkflowIsolation(t)
	const secret = "8723659412087635"
	for _, mode := range []string{"run", "watch", "lab"} {
		t.Run(mode, func(t *testing.T) {
			repo, root := fixtureRepo(t)
			p := credentialGuardPlan(repo, secret)
			script := `import json,os,sys
sys.stdin.read()
print(json.dumps({"type":"item.completed","item":{"type":"agent_message","text":"fixture answer"}}))
print(json.dumps({"type":"turn.completed","usage":{"input_tokens":int(os.environ["SELECTED_LITERAL_KEY"]),"output_tokens":23}}))`
			p.Candidates = []Candidate{{ID: "writer", Harness: "codex", Model: "fixture-model", Command: []string{"/usr/bin/python3", "-c", script}, APIKeyEnv: "SELECTED_LITERAL_KEY", Env: map[string]string{"SELECTED_LITERAL_KEY": secret}}}
			p.Checks = []Check{{Name: "acceptance", Command: []string{"/usr/bin/true"}}}
			output := filepath.Join(root, mode)
			runOutput := output
			switch mode {
			case "run":
				if _, err := Run(context.Background(), p, output, true); err != nil {
					t.Fatal(err)
				}
			case "watch":
				report, err := Watch(context.Background(), p, output, true, WatchOptions{IntervalSeconds: 1, MaxRuns: 1, TimeoutSeconds: 20})
				if err != nil {
					t.Fatal(err)
				}
				if len(report.Runs) != 1 {
					t.Fatalf("watch runs=%d", len(report.Runs))
				}
				runOutput = report.Runs[0].Output
			case "lab":
				report, err := Lab(context.Background(), p, output, true, 1)
				if err != nil {
					t.Fatal(err)
				}
				if len(report.Runs) != 1 {
					t.Fatalf("lab runs=%d", len(report.Runs))
				}
				runOutput = report.Runs[0].Output
			}
			report, err := LoadReport(runOutput)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Candidates) != 1 || !report.Candidates[0].CredentialsRedacted || report.Candidates[0].Verification == nil || !report.Candidates[0].Verification.Passed {
				t.Fatal("native redaction lost typed successful verification evidence")
			}
			candidate := report.Candidates[0]
			usage := assertIsolationJSONExcludes(t, candidate.Usage, secret)
			assertIsolationJSONNumber(t, usage, "output_tokens", "23")
			events, err := os.ReadFile(candidate.NativeEventsPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range bytes.Split(bytes.TrimSpace(events), []byte{'\n'}) {
				assertIsolationJSONExcludes(t, line, secret)
			}
			assertRetainedFilesExclude(t, output, secret, "")
		})
	}
}

func TestIsolationJSONKeywordCredentialsRefuseBeforePublication(t *testing.T) {
	for _, secret := range []string{"false", "true", "null", "version", "codex", "e", "redacted"} {
		for _, mode := range []string{"run", "watch", "lab"} {
			t.Run(secret+"/"+mode, func(t *testing.T) {
				repo, root := fixtureRepo(t)
				p := credentialGuardPlan(repo, secret)
				p.Candidates[0].APIKeyEnv = "SELECTED_LITERAL_KEY"
				p.Candidates[0].Env = map[string]string{"SELECTED_LITERAL_KEY": secret}
				// Required protocol metadata cannot be renamed into a different plan.
				p.Goal = secret
				output := filepath.Join(root, "refused")
				var result any
				var err error
				switch mode {
				case "run":
					result, err = Run(context.Background(), p, output, false)
				case "watch":
					result, err = Watch(context.Background(), p, output, false, WatchOptions{MaxRuns: 1})
				case "lab":
					result, err = Lab(context.Background(), p, output, false, 1)
				}
				if err == nil || strings.Contains(err.Error(), secret) {
					t.Fatal("credential-bearing metadata was not safely refused")
				}
				encoded, encodeErr := json.Marshal(result)
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				// Schema keys may themselves collide, but all returned text is cleared.
				var fields map[string]any
				if json.Unmarshal(encoded, &fields) != nil {
					t.Fatal("invalid typed return")
				}
				for _, value := range fields {
					if text, ok := value.(string); ok && strings.Contains(text, secret) {
						t.Fatal("refusal returned selected credential text")
					}
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatal("preflight refusal created output")
				}
			})
		}
	}
}

func TestIsolationJSONEarlyRefusalDiagnosticsContainNoUnregisteredCredential(t *testing.T) {
	for _, mode := range []string{"run", "watch", "lab"} {
		for _, failure := range []string{"candidate-bound", "environment-bound", "missing-earlier-key", "pending-native-auth"} {
			t.Run(mode+"/"+failure, func(t *testing.T) {
				repo, root := fixtureRepo(t)
				secret := "credential"
				p := credentialGuardPlan(repo, secret)
				p.Candidates[0].APIKeyEnv = "SELECTED_LITERAL_KEY"
				p.Candidates[0].Env = map[string]string{"SELECTED_LITERAL_KEY": secret}
				switch failure {
				case "candidate-bound":
					for len(p.Candidates) < 17 {
						p.Candidates = append(p.Candidates, p.Candidates[0])
					}
				case "environment-bound":
					p.Candidates[0].Env[strings.Repeat("x", 129)] = "safe"
				case "missing-earlier-key":
					secret = "unavailable"
					p.Candidates[0].APIKeyEnv = "UNAVAILABLE_JSON_FIXTURE_KEY"
					t.Setenv("UNAVAILABLE_JSON_FIXTURE_KEY", "")
					p.Candidates = append(p.Candidates, Candidate{ID: "later", Harness: "command", Command: []string{"/usr/bin/true"}, APIKeyEnv: "LATER_JSON_FIXTURE_KEY", Env: map[string]string{"LATER_JSON_FIXTURE_KEY": secret}})
				case "pending-native-auth":
					secret = "invalid"
					auth := filepath.Join(root, "auth.json")
					if err := os.WriteFile(auth, []byte(`{"OPENAI_API_KEY":"invalid"}`), 0600); err != nil {
						t.Fatal(err)
					}
					p.Candidates[0] = Candidate{ID: "bad/id", Harness: "codex", Command: []string{"/usr/bin/true"}}
					p.Isolation.Auth = map[string]IsolationAuth{"bad/id": {File: auth}}
				}
				assertEarlyCredentialRefusal(t, mode, p, filepath.Join(root, "output"), secret)
			})
		}
	}
}
