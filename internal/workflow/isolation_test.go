package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/harness"
	"agentmux/internal/sandbox"
)

func requireFullWorkflowIsolation(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENTMUX_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_SANDBOX_INTEGRATION=1 for actual full workflow namespaces")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sandbox.ProbeWorkflow(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestIsolationErrorRedactionPreservesCause(t *testing.T) {
	root := errors.New("provider process failed")
	cause := &os.PathError{Op: "execute", Path: "fixture", Err: root}
	original := fmt.Errorf("native stderr: selected-credential-value: %w", cause)
	runtime := &isolationRuntime{secrets: map[string][]string{"writer": {"selected-credential-value"}}}
	ctx := context.WithValue(context.Background(), isolationContextKey{}, runtime)
	got := RedactIsolationError(ctx, "different-step", original)
	if !errors.Is(got, root) || strings.Contains(got.Error(), "selected-credential-value") || !strings.Contains(got.Error(), "[redacted credential]") {
		t.Fatalf("unsafe error redaction: %v", got)
	}
	var extracted *os.PathError
	if !errors.As(got, &extracted) || extracted != cause {
		t.Fatal("error redaction lost errors.As identity")
	}
	result, changed := RedactIsolationResult(ctx, "different-step", harness.Result{Text: "selected-credential-value"})
	if !changed || strings.Contains(result.Text, "selected-credential-value") {
		t.Fatal("selected result credential retained")
	}
}

func TestCredentialScannerFindsValuesAcrossChunks(t *testing.T) {
	scanner := credentialScanner{needles: [][]byte{[]byte("selected-secret")}, window: len("selected-secret") - 1}
	if _, err := scanner.Write([]byte("prefix selected-")); err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Write([]byte("secret suffix")); !errors.Is(err, ErrIsolationCredentialLeak) {
		t.Fatalf("split secret not caught: %v", err)
	}
}

func TestWorkflowIsolationRealPreflightRefusesAuthInsideCanonicalSource(t *testing.T) {
	requireFullWorkflowIsolation(t)
	repo, root := fixtureRepo(t)
	nested := filepath.Join(repo, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(repo, "login.json")
	fixtureWrite(t, auth, []byte(`{"OPENAI_API_KEY":"unselected-private-login"}`))
	p := comparePlan(nested, Candidate{ID: "writer", Harness: "codex", Command: []string{"/usr/bin/false"}})
	p.Storage = StoragePrivate
	p.Isolation = &IsolationOptions{Auth: map[string]IsolationAuth{"writer": {File: auth}}}
	out := filepath.Join(root, "must-not-exist")
	if _, err := Run(context.Background(), p, out, true); err == nil || !strings.Contains(err.Error(), "outside the source repository") {
		t.Fatalf("unsafe auth admitted: %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("auth rejection occurred after durable run admission")
	}
}

func assertRetainedFilesExclude(t *testing.T, dir, secret string, quarantine string) {
	t.Helper()
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && quarantine != "" && path == quarantine {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), secret) {
				return fmt.Errorf("selected credential retained in %s", path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowIsolationRealNativeCredentialSafety(t *testing.T) {
	requireFullWorkflowIsolation(t)
	const secret = "selected-workflow-test-key-481927-only"
	t.Setenv("SELECTED_WORKFLOW_TEST_KEY", secret)
	for _, scenario := range []string{"stderr-api-env", "workspace-api-env", "stderr-explicit-env", "ignored-check-output"} {
		t.Run(scenario, func(t *testing.T) {
			leak := scenario == "workspace-api-env" || scenario == "ignored-check-output"
			repo, root := fixtureRepo(t)
			script := `import sys,os,pathlib,json
sys.stdin.read()
key=os.environ['OPENAI_API_KEY']
`
			if leak {
				script += `pathlib.Path('file.txt').write_text(key)
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'done'}}))
print('{"type":"turn.completed"}')
`
			} else {
				script += `print(key,file=sys.stderr)
sys.exit(9)
`
			}
			p := comparePlan(repo, Candidate{ID: "writer", Harness: "codex", Command: []string{"/usr/bin/python3", "-c", script}, APIKeyEnv: "SELECTED_WORKFLOW_TEST_KEY"})
			if scenario == "stderr-explicit-env" {
				p.Candidates[0].APIKeyEnv = ""
				p.Candidates[0].Env = map[string]string{"OPENAI_API_KEY": secret}
			}
			ignored := scenario == "ignored-check-output"
			if ignored {
				p.Candidates[0].Command[2] = strings.ReplaceAll(script, "pathlib.Path('file.txt').write_text(key)", "pathlib.Path('ignored-secret').write_text(key)")
				p.Checks = []Check{{Name: "ignored-output", Command: []string{"/usr/bin/python3", "-c", "import pathlib,sys; key=pathlib.Path('ignored-secret').read_text(); print(key); print(key,file=sys.stderr); sys.exit(7)"}}}
			}

			p.Storage = StoragePrivate
			p.Isolation = &IsolationOptions{}
			r, err := Run(context.Background(), p, filepath.Join(root, "run"), true)
			if err == nil || len(r.Candidates) != 1 || r.Candidates[0].Attempts != 1 {
				t.Fatalf("unsafe run result: %+v %v", r, err)
			}
			c := r.Candidates[0]
			if leak && !ignored && (c.State != "credential_leak" || c.PatchSHA256 != "" || c.FinalSnapshot != nil) {
				t.Fatalf("credential file was published: %+v", c)
			}
			if !leak && (!c.CredentialsRedacted || strings.Contains(c.Error, secret)) {
				t.Fatalf("native stderr leaked or lacked redaction evidence: %+v", c)
			}
			if ignored && (c.State != "checks_failed" || c.Verification == nil || len(c.Verification.Checks) != 1 || !c.Verification.Checks[0].CredentialsRedacted) {
				t.Fatalf("ignored credential output not scrubbed: %+v", c)
			}
			if _, err := Apply(context.Background(), r.Output, "writer"); err == nil {
				t.Fatal("credential-bearing/failed result was applicable")
			}
			assertRetainedFilesExclude(t, r.Output, secret, c.Worktree)
			data, _ := os.ReadFile(filepath.Join(repo, "file.txt"))
			if string(data) != "base\n" {
				t.Fatal("source changed")
			}
		})
	}
}

func TestWorkflowIsolationRealKeepsOwnerGlobalIgnorePolicy(t *testing.T) {
	requireFullWorkflowIsolation(t)
	repo, root := fixtureRepo(t)
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	excludes := filepath.Join(home, "global-ignore")
	fixtureWrite(t, excludes, []byte("owner-global-secret\n"))
	fixtureWrite(t, filepath.Join(home, ".gitconfig"), []byte("[core]\n\texcludesfile = "+excludes+"\n"))
	t.Setenv("HOME", home)
	fixtureWrite(t, filepath.Join(repo, "owner-global-secret"), []byte("must-never-be-selected"))
	p := comparePlan(repo, pythonCandidate("writer", "from pathlib import Path; assert not Path('owner-global-secret').exists(); Path('file.txt').write_text('fixed\\n')"))
	p.Candidates[0].Command[0] = "/usr/bin/python3"
	p.Checks[0].Command[0] = "/usr/bin/python3"
	p.Storage = StoragePrivate
	p.Isolation = &IsolationOptions{}
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), true)
	if err != nil || r.Candidates[0].State != "verified" {
		t.Fatalf("global ignore selection: %+v %v", r, err)
	}
	assertRetainedFilesExclude(t, r.Output, "must-never-be-selected", "")
	fresh, err := IsFresh(context.Background(), r.Candidates[0].Worktree, *r.Candidates[0].Verification)
	if err != nil || !fresh {
		t.Fatalf("standalone freshness lost required isolation: %v %v", fresh, err)
	}
	// Source application coordination deliberately owns this persistent lock.
	fixtureWrite(t, filepath.Join(repo, ".git", "agentmux-apply.lock"), nil)
	before := privateSourceMetadata(t, repo)
	applied, err := Apply(context.Background(), r.Output, "writer")
	if err != nil || !applied.Applied || applied.Uncertain {
		t.Fatalf("full isolated source apply: %+v %v", applied, err)
	}
	if privateSourceMetadata(t, repo) != before {
		t.Fatal("isolated application changed source Git metadata")
	}
	inspected, err := InspectApply(context.Background(), r.Output, "writer")
	if err != nil || inspected.State != "applied" {
		t.Fatalf("full isolated application inspection: %+v %v", inspected, err)
	}
	recovered, err := RecoverApply(context.Background(), r.Output, "writer")
	if err != nil || recovered.State != "applied" {
		t.Fatalf("full isolated recovery observation: %+v %v", recovered, err)
	}
}

func TestIsolationCredentialGuardDoesNotForwardSplitToken(t *testing.T) {
	runtime := &isolationRuntime{secrets: map[string][]string{"writer": {"selected-secret"}}}
	ctx := context.WithValue(context.Background(), isolationContextKey{}, runtime)
	var output bytes.Buffer
	writer, flush, err := isolationCredentialWriter(ctx, "file.txt", &output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write([]byte("prefix selected-")); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write([]byte("secret suffix")); !errors.Is(err, ErrIsolationCredentialLeak) {
		t.Fatal("split token admitted")
	}
	if err = flush(); !errors.Is(err, ErrIsolationCredentialLeak) {
		t.Fatal("failed writer flushed its withheld suffix")
	}
	if strings.Contains(output.String(), "selected-") {
		t.Fatal("unverified token prefix was forwarded")
	}
	if _, _, err = isolationCredentialWriter(ctx, "selected-secret.txt", &output); !errors.Is(err, ErrIsolationCredentialLeak) {
		t.Fatal("raw credential pathname admitted")
	}
}

func TestIsolationInheritedPolicyCannotBeChanged(t *testing.T) {
	opts := NormalizedIsolation(&IsolationOptions{})
	existing := &isolationRuntime{opts: *opts}
	if err := compatibleInheritedIsolation(existing, nil); err != nil {
		t.Fatal(err)
	}
	changed := *opts
	changed.TempBytes *= 2
	if err := compatibleInheritedIsolation(existing, &changed); err == nil {
		t.Fatal("nested policy expansion admitted")
	}
	changed = *opts
	changed.Mode = ""
	if err := compatibleInheritedIsolation(existing, &changed); err != nil {
		t.Fatal("equivalent normalized inherited policy refused")
	}
}

func TestFreshnessDoesNotDowngradeRecordedIsolation(t *testing.T) {
	v := Verification{Isolation: "unknown-required-mode", Storage: StoragePrivate, Store: "/tmp/missing/.git", Tree: "tree", EndingTree: "tree", ContentSHA256: strings.Repeat("a", 64), EndingContentSHA256: strings.Repeat("a", 64), Passed: true, Checks: []CheckResult{{Passed: true}}}
	if fresh, err := IsFresh(context.Background(), "/tmp/missing", v); fresh || err == nil {
		t.Fatalf("unknown required mode downgraded: %v %v", fresh, err)
	}
}

func TestWorkflowIsolationRealMetadataRuntimeStartsOwnBridge(t *testing.T) {
	requireFullWorkflowIsolation(t)
	repo, root := fixtureRepo(t)
	run := filepath.Join(root, "run")
	if err := os.Mkdir(run, 0700); err != nil {
		t.Fatal(err)
	}
	script := `import json,sys
request=json.load(sys.stdin)
print(json.dumps({'event':'result','result':{'status':'SUCCESS','response':json.dumps({'type':'final','text':request['message']['content']}),'usage':{'input_tokens':1}}}))`
	fixturePath := filepath.Join(root, "bridge-fixture.py")
	fixtureWrite(t, fixturePath, []byte(script))
	opts := &IsolationOptions{Network: "allowlist", BridgeCommand: []string{"/usr/bin/python3", fixturePath}}
	ctx, closeMetadata, err := PrepareReportIsolation(context.Background(), Report{Repository: repo, Output: run, Isolation: opts})
	if err != nil {
		t.Fatal(err)
	}
	defer closeMetadata()
	if runtime := isolationFromContext(ctx); !runtime.metadataOnly || len(runtime.bridges) != 0 {
		t.Fatal("metadata inspection started native inference")
	}
	plan := Plan{Repository: repo, Storage: StoragePrivate, Candidates: []Candidate{{ID: "reviewer", Harness: "antigravity-bridge", Model: "gemini-fixture-model"}}}
	child := filepath.Join(run, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	childCtx, closeChild, err := PrepareIsolation(ctx, plan, child)
	if err != nil {
		t.Fatal(err)
	}
	runtime := isolationFromContext(childCtx)
	if runtime == isolationFromContext(ctx) || runtime.metadataOnly || runtime.run != child || len(runtime.bridges) != 1 {
		t.Fatal("nested execution reused a metadata-only service boundary")
	}
	bridge := runtime.bridges["reviewer"]
	req, _ := http.NewRequest("GET", bridge.base+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+bridge.key)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("ephemeral bridge was unavailable")
	}
	body, _ := json.Marshal(map[string]any{"model": "gemini-fixture-model", "messages": []any{map[string]string{"role": "user", "content": bridge.key}}})
	post, _ := http.NewRequest("POST", bridge.base+"/chat/completions", bytes.NewReader(body))
	post.Header.Set("Authorization", "Bearer "+bridge.key)
	native, err := http.DefaultClient.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	responseData, err := io.ReadAll(native.Body)
	native.Body.Close()
	if err != nil || native.StatusCode != 200 || !bytes.Contains(responseData, []byte(bridge.key)) {
		t.Fatalf("native inference fixture failed: %d %s %v", native.StatusCode, responseData, err)
	}
	retained, err := os.ReadFile(filepath.Join(child, "inference", "reviewer", "request-000001.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(retained, []byte(bridge.key)) || !bytes.Contains(retained, []byte(`"credentials_redacted": true`)) {
		t.Fatal("selected bridge credential retained in private native inference evidence")
	}
	closeChild()
	if _, err := http.DefaultClient.Do(req); err == nil {
		t.Fatal("bridge listener survived workflow cleanup")
	}
	if len(IsolationUsage(childCtx)) != 1 {
		t.Fatal("native bridge usage was not inspectable")
	}
}

func TestIsolationCheckCapabilitiesExcludeProviderCredentials(t *testing.T) {
	runtime := &isolationRuntime{opts: *NormalizedIsolation(&IsolationOptions{Network: "allowlist", Auth: map[string]IsolationAuth{"writer": {File: "/unavailable-owner-auth.json"}}}), secrets: map[string][]string{}}
	ctx := context.WithValue(context.Background(), isolationContextKey{}, runtime)
	request, cleanup, err := IsolationCheckRequest(ctx, "/unused/private-worktree", harness.Request{Harness: "command", Command: []string{"/usr/bin/true"}, Env: map[string]string{"OPENAI_API_KEY": "must-not-reach-check"}})
	defer cleanup()
	if err != nil || len(request.Env) != 0 || request.Wrap == nil {
		t.Fatal("check inherited selected provider capabilities")
	}
	for _, name := range []string{"OPENAI_API_KEY", "GITHUB_TOKEN", "CLIENT_SECRET", "OWNER_PASSWORD", "PROVIDER_CREDENTIAL"} {
		if !isolationCredentialEnvironment(name) {
			t.Fatalf("explicit credential name not registered: %s", name)
		}
	}
	for _, name := range []string{"PATH", "HOME", "LANG", "NATIVE_MODE"} {
		if isolationCredentialEnvironment(name) {
			t.Fatalf("runtime setting falsely selected: %s", name)
		}
	}
}
