package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGuidedRetainedCredentialsRemainInspectableWithoutAmbientKey(t *testing.T) {
	const key = "GUIDED_RETAINED_OWNER_KEY"
	const secret = "selected-retained-guided-private-value-792041"
	options, _ := guidedFixtureOptions(t)
	t.Setenv(key, "")
	t.Setenv("OPENAI_API_KEY", "")
	code := "import sys,json,pathlib,hashlib,os; sys.stdin.read(); key=os.environ['OPENAI_API_KEY']; assert key; pathlib.Path('auth-digest').write_text(hashlib.sha256(key.encode()).hexdigest()); pathlib.Path('file.txt').write_text('fixed\\n'); print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'Used selected authentication'}})); print('{\"type\":\"turn.completed\"}')"
	options.Coder = &Candidate{Harness: "codex", Model: "fixture-model", Command: []string{"python3", "-c", code}, APIKeyEnv: key, Env: map[string]string{key: secret}}
	options.Reviewer = &Candidate{Harness: "command", Command: []string{"python3", "-c", "import json,pathlib; assert pathlib.Path('file.txt').read_text()=='fixed\\n'; print(json.dumps({'decision':'approve','summary':'Inspected the current revision.','findings':[]}))"}}
	digest := sha256.Sum256([]byte(secret))
	check := "import os; from pathlib import Path; assert Path('file.txt').read_text()=='fixed\\n'; assert Path('auth-digest').read_text()==" + fmt.Sprintf("%q", hex.EncodeToString(digest[:])) + "; assert not os.environ.get('OPENAI_API_KEY'); assert not os.environ.get('" + key + "')"
	options.CheckCommand = []string{"python3", "-c", check}
	options.MaxAttempts = 1
	options.TimeoutSeconds = 30
	// This is a trusted-local fake protocol executable. No native provider or
	// Bubblewrap capability is needed to exercise the exact retained-plan path.
	report, err := RunGuided(context.Background(), options, true)
	if err != nil || report.State != "completed" || report.EarlyApproval == nil {
		t.Fatalf("guided selected credential could not publish a completed report: state=%s error=%v", report.State, err)
	}
	var plan Plan
	planPath := filepath.Join(report.Output, "plan.json")
	if err := DecodeFile(planPath, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates[0].Env) != 0 || plan.Candidates[0].APIKeyEnv != key || options.Coder.Env[key] != secret {
		t.Fatal("retained metadata or the original execution credential was changed")
	}
	retained, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	for i, ambient := range []string{"", "rotated-unselected-ambient-value"} {
		t.Setenv(key, ambient)
		loaded, err := LoadReport(report.Output)
		if err != nil || RequireApprovedReview(loaded) != nil {
			t.Fatalf("retained approval depended on current credential availability: %v", err)
		}
		if _, err := RenderReportTo(report.Output, filepath.Join(report.Output, fmt.Sprintf("offline-report-%d.html", i))); err != nil {
			t.Fatalf("retained rendering required a current credential: %v", err)
		}
		summary, err := Summarize(context.Background(), report.Output)
		if err != nil || !summary.CanApply {
			t.Fatalf("retained readiness required a current credential: %v", err)
		}
	}
	assertRetainedFilesExclude(t, report.Output, secret, "")
	for _, test := range []struct {
		name   string
		mutate func(*Plan)
		want   string
	}{
		{"invalid-key-name", func(p *Plan) { p.Candidates[0].APIKeyEnv = "invalid-key-name" }, "retained plan is invalid"},
		{"unexpected-retained-env", func(p *Plan) { p.Candidates[0].Env = map[string]string{key: secret} }, "retained plan is invalid"},
		{"command-key-override", func(p *Plan) { p.Candidates[0].Harness, p.Candidates[0].Model = "command", "" }, "retained plan is invalid"},
		{"antigravity-key-override", func(p *Plan) { p.Candidates[0].Harness = "antigravity" }, "retained plan is invalid"},
		{"pi-key-without-endpoint", func(p *Plan) { p.Candidates[0].Harness = "pi" }, "retained plan is invalid"},
		{"endpoint-userinfo", func(p *Plan) { p.Candidates[0].BaseURL = "https://owner:private@example.invalid/v1" }, "retained plan is invalid"},
		{"check-argv", func(p *Plan) { p.Checks[0].Command = []string{"invalid\x00argv"} }, "retained plan is invalid"},
		{"review-authority", func(p *Plan) { p.Steps[1].ReadOnly = false }, "retained plan is invalid"},
		{"changed-valid-key", func(p *Plan) { p.Candidates[0].APIKeyEnv = "GUIDED_OTHER_OWNER_KEY" }, "profile or role differs"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var altered Plan
			if err := json.Unmarshal(retained, &altered); err != nil {
				t.Fatal(err)
			}
			test.mutate(&altered)
			if err := saveJSON(planPath, &altered); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.WriteFile(planPath, retained, 0600); err != nil {
					t.Fatal(err)
				}
			})
			if _, err := LoadReport(report.Output); err == nil || !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), secret) {
				t.Fatalf("altered retained metadata accepted or improperly diagnosed: %v", err)
			}
			if _, err := Apply(context.Background(), report.Output, ""); err == nil || strings.Contains(err.Error(), secret) {
				t.Fatal("altered retained metadata authorized application or exposed a credential")
			}
		})
	}
	if data, err := os.ReadFile(filepath.Join(options.Repository, "file.txt")); err != nil || string(data) != "base\n" {
		t.Fatal("guided execution changed its source")
	}
	t.Setenv(key, "")
	if applied, err := Apply(context.Background(), report.Output, ""); err != nil || !applied.Applied {
		t.Fatalf("checked reviewed result could not apply without execution credentials: %v", err)
	}
	assertRetainedFilesExclude(t, report.Output, secret, "")
	if data, err := os.ReadFile(planPath); err != nil || !bytes.Equal(data, retained) {
		t.Fatal("metadata validation changed the retained plan")
	}
}

func TestRetainedCredentialValidationPreservesAllPlanConstraints(t *testing.T) {
	options, _ := guidedCredentialFixture(t, "environment", "selected-validation-private-value-825130")
	options.Isolation = nil
	preview, err := PrepareGuided(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	plan := redactedPlan(preview.Plan)
	before, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(plan.Candidates[0].APIKeyEnv, "")
	if err := validateRetainedPolicyPlan(plan); err != nil {
		t.Fatalf("valid retained metadata required its discarded credential: %v", err)
	}
	after, err := json.Marshal(plan)
	if err != nil || !bytes.Equal(before, after) || len(plan.Candidates[0].Env) != 0 {
		t.Fatal("retained validation mutated the original plan")
	}
	for _, test := range []struct {
		name   string
		mutate func(*Plan)
	}{
		{"key-syntax", func(p *Plan) { p.Candidates[0].APIKeyEnv = "invalid key" }},
		{"command-key", func(p *Plan) { p.Candidates[0].Harness, p.Candidates[0].Model = "command", "" }},
		{"pi-key-without-endpoint", func(p *Plan) { p.Candidates[0].Harness = "pi" }},
		{"auth-and-selected-key", func(p *Plan) {
			p.Isolation = &IsolationOptions{Auth: map[string]IsolationAuth{p.Candidates[0].ID: {File: filepath.Join(filepath.Dir(p.Repository), "auth.json")}}}
		}},
		{"invalid-containment", func(p *Plan) { p.ProcessContainment = "unsupported" }},
		{"retained-env", func(p *Plan) { p.Candidates[0].Env = map[string]string{"BAD=NAME": "private"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "auth-and-selected-key" && runtime.GOOS != "linux" {
				t.Skip("isolated authentication policy requires Linux")
			}
			var altered Plan
			if err := json.Unmarshal(before, &altered); err != nil {
				t.Fatal(err)
			}
			test.mutate(&altered)
			if err := validateRetainedPolicyPlan(altered); err == nil {
				t.Fatal("credential metadata adaptation bypassed a normal plan constraint")
			}
		})
	}
}
