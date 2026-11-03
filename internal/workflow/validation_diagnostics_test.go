package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agentmux/internal/egress"
)

func TestNativeAuthPlanValidationDoesNotReflectMetadata(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native workflow isolation policy requires Linux")
	}
	const secret = "native_validation_secret_781233"
	cases := []struct {
		name, category string
		mission        bool
		mutate         func(*Plan)
	}{
		{"candidate-id", "invalid or duplicate ID", false, func(p *Plan) { p.Candidates[0].ID = "invalid/" + secret }},
		{"duplicate-candidate", "invalid or duplicate ID", false, func(p *Plan) {
			p.Candidates[0].ID = secret
			p.Candidates = append(p.Candidates, p.Candidates[0])
		}},
		{"harness", "unsupported harness", false, func(p *Plan) { p.Candidates[0].Harness = secret }},
		{"command", "requires command argv", false, func(p *Plan) {
			p.Candidates[0].ID, p.Candidates[0].Harness = secret, "command"
		}},
		{"candidate-bounds", "prompt/argv/time bounds", false, func(p *Plan) {
			p.Candidates[0].ID, p.Candidates[0].TimeoutSeconds = secret, 601
		}},
		{"metadata-bounds", "metadata exceeds bounds", false, func(p *Plan) {
			p.Candidates[0].ID, p.Candidates[0].Model = secret, strings.Repeat("m", 1025)
		}},
		{"environment", "valid names", false, func(p *Plan) {
			p.Candidates[0].Env = map[string]string{"invalid-" + secret: "safe"}
		}},
		{"containment", "unsafe for the containment launcher", false, func(p *Plan) {
			p.Candidates[0].Env = map[string]string{"LD_" + secret: "fixture-launcher-env-value-98231"}
		}},
		{"egress-origin", "egress origin must be", false, func(p *Plan) {
			p.Isolation.Network = "allowlist"
			p.Isolation.Allow = []egress.Endpoint{{Origin: "https://" + secret + ".example/private"}}
		}},
		{"egress-ip", "invalid pinned egress address", false, func(p *Plan) {
			p.Isolation.Network = "allowlist"
			p.Isolation.Allow = []egress.Endpoint{{Origin: "https://example.test", IPs: []string{secret}}}
		}},
		{"step-id", "invalid ID", true, func(p *Plan) { p.Steps[0].ID = "invalid/" + secret }},
		{"duplicate-step", "duplicate ID", true, func(p *Plan) { p.Steps = append(p.Steps, p.Steps[0]) }},
		{"step-candidate", "must reference one candidate", true, func(p *Plan) { p.Steps[0].Candidate = "missing" }},
		{"step-result", "cannot require result text", true, func(p *Plan) {
			p.Steps[0].Candidate, p.Steps[0].Verify, p.Steps[0].RequireResult = "", true, true
		}},
		{"step-controls", "requires an agent for review controls", true, func(p *Plan) {
			p.Steps[0].Candidate, p.Steps[0].Verify, p.Steps[0].ReadOnly = "", true, true
		}},
		{"step-review", "review must be read_only and require_result", true, func(p *Plan) { p.Steps[0].Review = true }},
		{"step-review-verify", "separate phases", true, func(p *Plan) {
			p.Steps[0].Review, p.Steps[0].ReadOnly, p.Steps[0].RequireResult, p.Steps[0].Verify = true, true, true, true
		}},
		{"step-gate", "requires a structured review", true, func(p *Plan) { p.Steps[0].GateReview = true }},
		{"step-dependency", "invalid dependency", true, func(p *Plan) { p.Steps[0].DependsOn = []string{secret + "_missing"} }},
	}
	for _, tc := range cases {
		for _, entry := range []string{"validate", "load", "run", "watch", "lab"} {
			t.Run(tc.name+"/"+entry, func(t *testing.T) {
				root := t.TempDir()
				repo, tools := filepath.Join(root, "source"), filepath.Join(root, "tools")
				for _, dir := range []string{repo, tools} {
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				// Validation may locate the launcher but must never execute it.
				marker := filepath.Join(root, "launcher-started")
				if err := os.WriteFile(filepath.Join(tools, "bwrap"), []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 125\n"), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
				auth := filepath.Join(root, "auth.json")
				authJSON := []byte(`{"OPENAI_API_KEY":"` + secret + `"}`)
				fixtureWrite(t, auth, authJSON)
				p := Plan{Version: 1, Storage: StoragePrivate, Execution: "local", Repository: repo, Goal: "Validate privately", Isolation: &IsolationOptions{Auth: map[string]IsolationAuth{"writer": {File: auth}}}, Candidates: []Candidate{{ID: "writer", Harness: "codex"}}, Checks: []Check{pythonCheck("print('accepted')")}}
				if tc.mission {
					p.Mode, p.Steps = "mission", []Step{{ID: secret, Candidate: "writer"}}
				}
				if err := ValidatePlan(p); err != nil {
					t.Fatalf("valid native authentication plan refused: %v", err)
				}
				tc.mutate(&p)
				// Keep the selected native file associated with any renamed ID.
				p.Isolation.Auth = map[string]IsolationAuth{p.Candidates[0].ID: {File: auth}}
				output := filepath.Join(root, "output")
				var err error
				switch entry {
				case "validate":
					err = ValidatePlan(p)
				case "load":
					data, marshalErr := json.Marshal(p)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					path := filepath.Join(root, "plan.json")
					fixtureWrite(t, path, data)
					_, err = LoadPlan(path)
				default:
					err = assertEarlyCredentialRefusal(t, entry, p, output, secret)
				}
				category := tc.category
				if tc.mission && (entry == "watch" || entry == "lab") {
					// These entry points reject mission mode before step validation.
					category = "requires compare mode"
				}
				// Execution entry points withhold diagnostic text until selected
				// native authentication has been read: even a fixed category could
				// coincide with an unread credential. Pure plan previews retain
				// useful categories without opening the selected login file.
				suppressed := entry != "validate" && entry != "load" && err != nil && err.Error() == ""
				if err == nil || strings.Contains(err.Error(), secret) || !suppressed && !strings.Contains(err.Error(), category) {
					t.Fatalf("expected private %q diagnostic, got %v", category, err)
				}
				for _, path := range []string{output, marker} {
					if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
						t.Fatalf("validation created output or executed a launcher: %s: %v", path, statErr)
					}
				}
				if actual, readErr := os.ReadFile(auth); readErr != nil || string(actual) != string(authJSON) {
					t.Fatalf("validation changed selected authentication: %v", readErr)
				}
				if entries, readErr := os.ReadDir(repo); readErr != nil || len(entries) != 0 {
					t.Fatalf("validation changed source files: %v", readErr)
				}
			})
		}
	}
}

func TestLoadPlanJSONDiagnosticsDoNotReflectMetadata(t *testing.T) {
	const secret = "native_json_secret_472119"
	for _, tc := range []struct{ name, input, category string }{
		{"unknown-key", `{"` + secret + `":true}`, "unknown field"},
		{"duplicate-key", `{"` + secret + `":true,"` + secret + `":false}`, "duplicate JSON object key"},
		{"field-type", `{"version":` + strings.Repeat("4", 400) + `}`, "invalid workflow JSON field type at byte"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.json")
			fixtureWrite(t, path, []byte(tc.input))
			_, err := LoadPlan(path)
			if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), tc.category) || strings.Contains(err.Error(), strings.Repeat("4", 25)) {
				t.Fatalf("expected private JSON diagnostic, got %v", err)
			}
		})
	}
}

func TestPlanPreviewDoesNotOpenSelectedNativeAuth(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("native workflow isolation policy requires Linux")
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
	p := credentialGuardPlan(repo, "")
	p.Candidates[0] = Candidate{ID: "writer", Harness: "codex"}
	p.Isolation.Auth = map[string]IsolationAuth{"writer": {File: filepath.Join(root, "not-opened-auth.json")}}
	if err := ValidatePlan(p); err != nil {
		t.Fatalf("preview attempted to open selected authentication: %v", err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "plan.json")
	fixtureWrite(t, path, data)
	if _, err := LoadPlan(path); err != nil {
		t.Fatalf("load preview attempted to open selected authentication: %v", err)
	}
}
