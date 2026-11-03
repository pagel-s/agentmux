package workflow

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/agentauth"
	"agentmux/internal/managedconfig"
)

func credentialGuardPlan(repo, secret string) Plan {
	return Plan{Version: 1, Storage: StoragePrivate, Execution: "local", Repository: repo, Goal: "Keep the fixture safe", Isolation: &IsolationOptions{}, Candidates: []Candidate{pythonCandidate("writer", "print('safe fixture')")}, Checks: []Check{pythonCheck("print('accepted')")}, TimeoutSeconds: 30}
}

func prepareCredentialGuard(t *testing.T, p Plan) (context.Context, Plan) {
	t.Helper()
	ctx, prepared, cleanup, err := prepareIsolationCredentials(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return ctx, prepared
}

func assertPrivateBlobAbsent(t *testing.T, store string, data []byte) {
	t.Helper()
	hash := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(data))), data...))
	if _, err := privateCommandOutput(context.Background(), store, privateGitArgs(store), nil, "cat-file", "-e", hex.EncodeToString(hash[:])); err == nil {
		t.Fatal("key-bearing blob was published in private Git objects")
	}
}

func TestEarlyCredentialGuardRejectsSourceObjectsBeforeImport(t *testing.T) {
	const secret = "selected-initial-source-key-843012"
	for _, kind := range []string{"head-blob", "head-commit-message", "head-tree-name"} {
		t.Run(kind, func(t *testing.T) {
			repo, root := fixtureRepo(t)
			payload := []byte(secret + "\n")
			switch kind {
			case "head-blob":
				fixtureWrite(t, filepath.Join(repo, "file.txt"), payload)
				fixtureGit(t, repo, "add", "file.txt")
				fixtureGit(t, repo, "commit", "-qm", "Selected source revision")
				fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("already removed from working file\n"))
			case "head-commit-message":
				fixtureGit(t, repo, "commit", "--allow-empty", "-qm", secret)
			case "head-tree-name":
				fixtureWrite(t, filepath.Join(repo, secret), []byte("innocent contents\n"))
				fixtureGit(t, repo, "add", secret)
				fixtureGit(t, repo, "commit", "-qm", "Selected named file")
			}
			p := credentialGuardPlan(repo, secret)
			p.Candidates[0].Env = map[string]string{"OPENAI_API_KEY": secret}
			ctx, _ := prepareCredentialGuard(t, p)
			before := privateSourceMetadata(t, repo)
			store := filepath.Join(root, "guarded.git")
			if _, err := PrepareSnapshotStorage(ctx, StoragePrivate, repo, store); !errors.Is(err, ErrIsolationCredentialLeak) {
				t.Fatalf("source object not refused: %v", err)
			}
			if privateSourceMetadata(t, repo) != before {
				t.Fatal("credential refusal changed source Git metadata")
			}
			assertPrivateBlobAbsent(t, store, payload)
			objects, err := privateCommandOutput(context.Background(), store, privateGitArgs(store), nil, "cat-file", "--batch-all-objects", "--batch-check")
			if err != nil || len(objects) != 0 {
				t.Fatalf("source objects were imported before credential rejection: %q %v", objects, err)
			}
		})
	}
}

func TestEarlyCredentialGuardRejectsRawCaptureBeforeRuntime(t *testing.T) {
	const secret = "selected-uncommitted-source-key-389142"
	repo, root := fixtureRepo(t)
	p := credentialGuardPlan(repo, secret)
	p.Candidates[0].Env = map[string]string{"AUTH_TOKEN": secret}
	ctx, _ := prepareCredentialGuard(t, p)
	ctx, err := PrepareSnapshotStorage(ctx, StoragePrivate, repo, filepath.Join(root, "guarded.git"))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(secret + "\n")
	fixtureWrite(t, filepath.Join(repo, "file.txt"), payload)
	_, err = Capture(ctx, repo, filepath.Join(root, "snapshot"))
	if !errors.Is(err, ErrIsolationCredentialLeak) {
		t.Fatalf("raw source capture accepted selected key: %v", err)
	}
	assertPrivateBlobAbsent(t, filepath.Join(root, "guarded.git"), payload)
	if _, err := os.Stat(filepath.Join(root, "snapshot", "snapshot.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("key-bearing snapshot record was published")
	}
}

func TestEarlyCredentialGuardPinsEnvironmentAndAuth(t *testing.T) {
	const original = "selected-api-before-import-525832"
	const replacement = "changed-api-after-preflight-902817"
	repo, root := fixtureRepo(t)
	p := credentialGuardPlan(repo, original)
	p.Candidates[0].Harness = "codex"
	p.Candidates[0].Command = nil
	p.Candidates[0].APIKeyEnv = "SELECTED_PROVIDER_AUTH"
	t.Setenv("SELECTED_PROVIDER_AUTH", original)
	ctx, prepared := prepareCredentialGuard(t, p)
	t.Setenv("SELECTED_PROVIDER_AUTH", replacement)
	if prepared.Candidates[0].Env["SELECTED_PROVIDER_AUTH"] != original || !containsIsolationCredential(ctx, []byte(original)) {
		t.Fatal("selected environment credential was not pinned before import")
	}
	if containsIsolationCredential(ctx, []byte(replacement)) {
		t.Fatal("preflight adopted an unrelated later ambient change")
	}

	path := filepath.Join(root, "auth.json")
	fixtureWrite(t, path, []byte(`{"OPENAI_API_KEY":"`+original+`"}`))
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	p.Candidates[0].APIKeyEnv = ""
	p.Isolation.Auth = map[string]IsolationAuth{"writer": {File: path}}
	ctx, _ = prepareCredentialGuard(t, p)
	fixtureWrite(t, path, []byte(`{"OPENAI_API_KEY":"`+replacement+`"}`))
	auth, err := agentauth.PrepareAnonymous("codex", managedconfig.NativeAuth{File: path})
	if err != nil {
		t.Fatal(err)
	}
	defer auth.Close()
	if err := validatePinnedIsolationAuth(ctx, "writer", auth); err == nil || strings.Contains(err.Error(), original) || strings.Contains(err.Error(), replacement) {
		t.Fatalf("auth replacement admitted or leaked credentials: %v", err)
	}
}

func TestEarlyCredentialGuardRejectsRetainedOwnerMetadata(t *testing.T) {
	const secret = "selected-plan-metadata-key-982141"
	repo, _ := fixtureRepo(t)
	for _, kind := range []string{"goal", "prompt", "command", "check"} {
		t.Run(kind, func(t *testing.T) {
			p := credentialGuardPlan(repo, secret)
			p.Candidates[0].Env = map[string]string{"API_TOKEN": secret}
			switch kind {
			case "goal":
				p.Goal = secret
			case "prompt":
				p.Candidates[0].Prompt = secret
			case "command":
				p.Candidates[0].Command = []string{"echo", secret}
			case "check":
				p.Checks[0].Command = []string{"echo", secret}
			}
			_, _, cleanup, err := prepareIsolationCredentials(context.Background(), p)
			cleanup()
			if !errors.Is(err, ErrIsolationCredentialLeak) || strings.Contains(err.Error(), secret) {
				t.Fatalf("retained metadata credential was not safely refused: %v", err)
			}
		})
	}
}

func TestEarlyCredentialGuardRejectsJSONEscapedMetadata(t *testing.T) {
	repo, _ := fixtureRepo(t)
	for _, secret := range []string{"selected-key-with-\"quoted\"-bytes", "selected-key-with-\\slash-bytes", "selected-key-with-\n\r\t-control-bytes"} {
		p := credentialGuardPlan(repo, secret)
		p.Candidates[0].Env = map[string]string{"AUTH_TOKEN": secret}
		p.Goal = "Remove " + secret
		_, _, cleanup, err := prepareIsolationCredentials(context.Background(), p)
		cleanup()
		if !errors.Is(err, ErrIsolationCredentialLeak) || strings.Contains(err.Error(), secret) {
			t.Fatalf("escaped retained metadata was not safely refused: %v", err)
		}
	}
}

func TestWorkflowIsolationRealInitialCredentialsNeverPublished(t *testing.T) {
	requireFullWorkflowIsolation(t)
	const secret = "selected-source-before-native-step-748251"
	for _, mode := range []string{"run", "watch", "lab"} {
		t.Run(mode, func(t *testing.T) {
			repo, root := fixtureRepo(t)
			payload := []byte(secret + "\n")
			fixtureWrite(t, filepath.Join(repo, "file.txt"), payload)
			fixtureGit(t, repo, "add", "file.txt")
			fixtureGit(t, repo, "commit", "-qm", "Accidental selected credential")
			p := credentialGuardPlan(repo, secret)
			p.Candidates[0].Env = map[string]string{"OPENAI_API_KEY": secret}
			var err error
			output := filepath.Join(root, mode)
			switch mode {
			case "run":
				_, err = Run(context.Background(), p, output, false)
			case "watch":
				_, err = Watch(context.Background(), p, output, false, WatchOptions{IntervalSeconds: 1, MaxRuns: 1, TimeoutSeconds: 10})
			case "lab":
				_, err = Lab(context.Background(), p, output, false, 1)
			}
			if !errors.Is(err, ErrIsolationCredentialLeak) || strings.Contains(err.Error(), secret) {
				t.Fatalf("initial credential import not safely refused: %v", err)
			}
			assertPrivateBlobAbsent(t, filepath.Join(output, "git"), payload)
		})
	}
}

func TestEarlyCredentialGuardChecksActualQuarantineBeforePublication(t *testing.T) {
	const secret = "selected-racing-extra-object-key-812731"
	repo, root := fixtureRepo(t)
	p := credentialGuardPlan(repo, secret)
	p.Candidates[0].Env = map[string]string{"OPENAI_API_KEY": secret}
	ctx, _ := prepareCredentialGuard(t, p)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "git-shim")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	// Add an unexpected valid loose object only after source graph inspection,
	// inside index-pack's fresh private quarantine. It is outside the requested
	// graph, so checking only the requested IDs cannot detect its publication.
	script := "#!/usr/bin/python3\nimport os,sys,zlib,hashlib,pathlib\na=sys.argv[1:]\nif 'index-pack' in a:\n store=next(x.split('=',1)[1] for x in a if x.startswith('--git-dir='))\n payload=" + fmt.Sprintf("%q", secret+"\n") + ".encode()\n raw=b'blob '+str(len(payload)).encode()+b'\\0'+payload\n oid=hashlib.sha1(raw).hexdigest(); path=pathlib.Path(store,'objects',oid[:2]); path.mkdir(exist_ok=True); (path/oid[2:]).write_bytes(zlib.compress(raw))\nos.execv(" + fmt.Sprintf("%q", realGit) + ",[" + fmt.Sprintf("%q", realGit) + "]+a)\n"
	if err := os.WriteFile(filepath.Join(directory, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	store := filepath.Join(root, "guarded.git")
	if _, err := PrepareSnapshotStorage(ctx, StoragePrivate, repo, store); !errors.Is(err, ErrIsolationCredentialLeak) {
		t.Fatalf("unexpected quarantined key object was published: %v", err)
	}
	assertPrivateBlobAbsent(t, store, []byte(secret+"\n"))
}

func TestWorkflowIsolationRealPinnedAuthAcrossTurnsAndCandidates(t *testing.T) {
	requireFullWorkflowIsolation(t)
	const secret = "selected-pinned-file-key-849215"
	for _, mode := range []string{"two-turn-mission", "parallel-candidates"} {
		t.Run(mode, func(t *testing.T) {
			repo, root := fixtureRepo(t)
			auth := filepath.Join(root, "auth.json")
			fixtureWrite(t, auth, []byte(`{"OPENAI_API_KEY":"`+secret+`"}`))
			code := "import sys,json,pathlib,hashlib; sys.stdin.read(); key=json.loads(pathlib.Path('/home/agent/.codex/auth.json').read_text())['OPENAI_API_KEY']; assert key; p=pathlib.Path('auth-count'); p.write_text(str(int(p.read_text())+1 if p.exists() else 1)); pathlib.Path('auth-digest').write_text(hashlib.sha256(key.encode()).hexdigest()); pathlib.Path('file.txt').write_text('fixed\\n'); print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'read selected authentication'}})); print('{\"type\":\"turn.completed\"}')"
			candidate := Candidate{ID: "writer", Harness: "codex", Command: []string{"/usr/bin/python3", "-c", code}}
			p := comparePlan(repo, candidate)
			p.Isolation = &IsolationOptions{Auth: map[string]IsolationAuth{"writer": {File: auth}}}
			count := "2"
			if mode == "two-turn-mission" {
				p.Mode = "mission"
				p.Steps = []Step{{ID: "first", Candidate: "writer"}, {ID: "second", Candidate: "writer", DependsOn: []string{"first"}}, {ID: "check", DependsOn: []string{"second"}, Verify: true}}
			} else {
				other := candidate
				other.ID = "other"
				p.Candidates = append(p.Candidates, other)
				p.Isolation.Auth["other"] = IsolationAuth{File: auth}
				p.MaxParallel = 2
				count = "1"
			}
			digest := sha256.Sum256([]byte(secret))
			p.Checks = []Check{pythonCheck("from pathlib import Path; assert Path('auth-count').read_text()==" + fmt.Sprintf("%q", count) + "; assert Path('auth-digest').read_text()==" + fmt.Sprintf("%q", hex.EncodeToString(digest[:])))}
			p.Checks[0].Command[0] = "/usr/bin/python3"
			r, err := Run(context.Background(), p, filepath.Join(root, "run"), true)
			if err != nil {
				t.Fatalf("pinned auth was not reusable safely: %+v %v", r, err)
			}
			assertRetainedFilesExclude(t, r.Output, secret, "")
		})
	}
}
