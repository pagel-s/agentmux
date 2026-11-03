package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowIsolationRealMissionRedactsPreviousCredential(t *testing.T) {
	requireFullWorkflowIsolation(t)
	const secret = "review-only-selected-secret-825217"
	repo, root := fixtureRepo(t)
	writer := Candidate{ID: "writer", Harness: "command", Env: map[string]string{"OPENAI_API_KEY": secret}, Command: []string{"/usr/bin/python3", "-c", "import os,pathlib; pathlib.Path('ignored-secret').write_text(os.environ['OPENAI_API_KEY']); print('first step done')"}}
	reader := Candidate{ID: "reader", Harness: "command", Command: []string{"/usr/bin/python3", "-c", "import pathlib; print(pathlib.Path('ignored-secret').read_text())"}}
	p := Plan{Version: 1, Execution: "local", Mode: "mission", Repository: repo, Goal: "Credential retention fixture", TimeoutSeconds: 30, Storage: StoragePrivate, Isolation: &IsolationOptions{}, Candidates: []Candidate{writer, reader}, Checks: []Check{{Name: "acceptance", Command: []string{"/usr/bin/true"}}}, Steps: []Step{{ID: "first", Candidate: "writer"}, {ID: "second", Candidate: "reader", DependsOn: []string{"first"}}}}
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(r.Output, "steps", "second", "attempt-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Steps[1].CredentialsRedacted {
		t.Fatal("later step did not label run-wide redaction")
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("selected credential retained in second step attempt evidence; report result=%q", r.Steps[1].Result)
	}
}
