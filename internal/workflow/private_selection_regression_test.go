package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const privateSelectionCommitScript = `from pathlib import Path
import subprocess
Path('file.txt').write_bytes(b'fixed\n')
Path('.gitignore').write_bytes(b'file.txt\nignored-build\n')
subprocess.run(['git', 'rm', '--cached', 'file.txt'], check=True)
subprocess.run(['git', 'add', '.gitignore'], check=True)
subprocess.run(['git', '-c', 'maintenance.auto=false', '-c', 'gc.auto=0', '-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.test', 'commit', '-qm', 'worker commits and ignores a starting path'], check=True)
print('committed metadata; checked working bytes remain present')
`

func privateSelectionCommitWorkflow(t *testing.T, isolated bool) {
	t.Helper()
	repo, root := fixtureRepo(t)
	fixtureWrite(t, filepath.Join(repo, ".git", "agentmux-apply.lock"), nil)
	before := privateSourceMetadataWithTimes(t, repo)
	p := comparePlan(repo, pythonCandidate("writer", privateSelectionCommitScript))
	p.Checks = []Check{pythonCheck("from pathlib import Path; assert Path('file.txt').read_bytes()==b'fixed\\n'; Path('ignored-build').write_bytes(b'ignored generated artifact')")}
	if isolated {
		p.Isolation = &IsolationOptions{}
		p.Candidates[0].Command[0] = "/usr/bin/python3"
		p.Candidates[0].Command[2] = "from pathlib import Path; assert not Path('../worktree.selection.json').exists()\n" + p.Candidates[0].Command[2]
		p.Checks[0].Command[0] = "/usr/bin/python3"
	}
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), false)
	if err != nil || len(r.Candidates) != 1 || r.Candidates[0].State != "verified" {
		t.Fatalf("worker commit selection: %+v %v", r, err)
	}
	c := r.Candidates[0]
	if c.FinalSnapshot == nil || c.Verification == nil || !digestID(c.FinalSnapshot.SelectionSHA256) || c.FinalSnapshot.SelectionSHA256 != c.Verification.SelectionSHA256 {
		t.Fatalf("checked selection has no externally pinned identity: %+v", c)
	}
	patch, err := os.ReadFile(c.Patch)
	if err != nil || !strings.Contains(string(patch), "+fixed") || strings.Contains(string(patch), "deleted file mode") || strings.Contains(string(patch), "a/ignored-build") {
		t.Fatalf("patch did not retain the checked starting path or ignored-artifact policy: %s %v", patch, err)
	}
	fixtureWrite(t, filepath.Join(c.Worktree, "file.txt"), []byte("changed after checks\n"))
	if fresh, err := IsFresh(context.Background(), c.Worktree, *c.Verification); err != nil || fresh {
		t.Fatalf("saved verification hid changed ignored starting bytes: %v %v", fresh, err)
	}
	if applied, err := Apply(context.Background(), r.Output, c.ID); err == nil || applied.Applied {
		t.Fatalf("application admitted stale hidden bytes: %+v %v", applied, err)
	}
	fixtureWrite(t, filepath.Join(c.Worktree, "file.txt"), []byte("fixed\n"))
	if fresh, err := IsFresh(context.Background(), c.Worktree, *c.Verification); err != nil || !fresh {
		t.Fatalf("saved verification lost restored checked bytes: %v %v", fresh, err)
	}
	if applied, err := Apply(context.Background(), r.Output, c.ID); err != nil || !applied.Applied || applied.Uncertain {
		t.Fatalf("exact checked application after worker commit: %+v %v", applied, err)
	}
	if data, err := os.ReadFile(filepath.Join(repo, "file.txt")); err != nil || string(data) != "fixed\n" {
		t.Fatalf("application deleted or changed checked file: %q %v", data, err)
	}
	if privateSourceMetadataWithTimes(t, repo) != before {
		t.Fatal("worker commit selection/application changed source Git metadata")
	}
}

func TestPrivateSelectionPinsStartingPathsAcrossWorkerCommits(t *testing.T) {
	privateSelectionCommitWorkflow(t, false)
}

func privateSelectionGitlinkWorkflow(t *testing.T, isolated bool) {
	t.Helper()
	repo, root := fixtureRepo(t)
	before := privateSourceMetadataWithTimes(t, repo)
	script := `from pathlib import Path
import subprocess
Path('file.txt').write_bytes(b'fixed\n')
Path('selected-dir').mkdir()
Path('selected-dir/required.txt').write_bytes(b'checked bytes must not disappear\n')
commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
subprocess.run(['git', 'update-index', '--add', '--cacheinfo', '160000,'+commit+',selected-dir'], check=True)
print('index directory entry installed')
`
	p := comparePlan(repo, pythonCandidate("writer", script))
	p.Checks = []Check{pythonCheck("from pathlib import Path; Path('check-ran').write_text('unexpected'); assert Path('selected-dir/required.txt').exists()")}
	if isolated {
		p.Isolation = &IsolationOptions{}
		p.Candidates[0].Command[0] = "/usr/bin/python3"
		p.Checks[0].Command[0] = "/usr/bin/python3"
	}
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), false)
	if err == nil || len(r.Candidates) != 1 || r.Candidates[0].State == "verified" || r.Candidates[0].Verification != nil && r.Candidates[0].Verification.Passed {
		t.Fatalf("gitlink index hid a directory from independent checks: %+v %v", r, err)
	}
	c := r.Candidates[0]
	if _, err := os.Stat(filepath.Join(c.Worktree, "check-ran")); !os.IsNotExist(err) {
		t.Fatalf("unsupported directory index entry reached checks: %v", err)
	}
	if applied, err := Apply(context.Background(), r.Output, c.ID); err == nil || applied.Applied {
		t.Fatalf("application admitted unsupported directory index result: %+v %v", applied, err)
	}
	if data, err := os.ReadFile(filepath.Join(repo, "file.txt")); err != nil || string(data) != "base\n" {
		t.Fatalf("refused directory index changed source: %q %v", data, err)
	}
	if privateSourceMetadataWithTimes(t, repo) != before {
		t.Fatal("directory index refusal changed source Git metadata")
	}
}

func TestPrivateSelectionRejectsWorkerGitlinkIndexBeforeChecks(t *testing.T) {
	privateSelectionGitlinkWorkflow(t, false)
}

func TestWorkflowIsolationRealPrivateSelectionControls(t *testing.T) {
	requireFullWorkflowIsolation(t)
	t.Run("worker-commit", func(t *testing.T) { privateSelectionCommitWorkflow(t, true) })
	t.Run("gitlink-index", func(t *testing.T) { privateSelectionGitlinkWorkflow(t, true) })
	t.Run("missing-marker", func(t *testing.T) { privateSelectionMissingMarkerWorkflow(t, true) })
}

func privateHiddenStartingPathFixture(t *testing.T) (string, string, string, Snapshot) {
	t.Helper()
	ctx, repo, root, baseline := preparePrivateFixture(t)
	fork := filepath.Join(root, "fork")
	if err := CreateWorktree(ctx, baseline, fork); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, filepath.Join(fork, ".gitignore"), []byte("file.txt\n"))
	fixtureGit(t, fork, "rm", "--cached", "file.txt")
	fixtureGit(t, fork, "add", ".gitignore")
	fixtureGit(t, fork, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-qm", "hide the starting path from ordinary Git selection")
	return repo, root, fork, baseline
}

func TestPrivateDiscoveryRefusesLostOrInvalidProvenanceBeforeAdmission(t *testing.T) {
	mutations := []struct {
		name string
		edit func(*testing.T, string, string, Snapshot)
	}{
		{"missing-marker", func(t *testing.T, _, fork string, _ Snapshot) {
			if err := os.Remove(filepath.Join(fork, ".git", privateStoreMarkerName)); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing-marker-and-manifests", func(t *testing.T, _, fork string, _ Snapshot) {
			if err := os.Remove(filepath.Join(fork, ".git", privateStoreMarkerName)); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(fork, ".git", privateSelectionDirectory)); err != nil {
				t.Fatal(err)
			}
		}},
		{"invalid-marker", func(t *testing.T, _, fork string, _ Snapshot) {
			fixtureWrite(t, filepath.Join(fork, ".git", privateStoreMarkerName), []byte("{"))
		}},
		{"missing-manifest", func(t *testing.T, _, fork string, baseline Snapshot) {
			if err := os.Remove(filepath.Join(fork, ".git", privateSelectionDirectory, baseline.SelectionSHA256)); err != nil {
				t.Fatal(err)
			}
		}},
		{"changed-manifest", func(t *testing.T, _, fork string, baseline Snapshot) {
			fixtureWrite(t, filepath.Join(fork, ".git", privateSelectionDirectory, baseline.SelectionSHA256), []byte(".gitignore\x00"))
		}},
		{"missing-sidecar", func(t *testing.T, _, fork string, _ Snapshot) {
			if err := os.Remove(fork + ".selection.json"); err != nil {
				t.Fatal(err)
			}
		}},
		{"malformed-sidecar", func(t *testing.T, _, fork string, _ Snapshot) {
			fixtureWrite(t, fork+".selection.json", []byte("{"))
		}},
		{"oversized-sidecar", func(t *testing.T, _, fork string, _ Snapshot) {
			fixtureWrite(t, fork+".selection.json", []byte(strings.Repeat(" ", (32<<10)+1)))
		}},
		{"symlink-sidecar", func(t *testing.T, _, fork string, _ Snapshot) {
			if err := os.Rename(fork+".selection.json", fork+".original-selection.json"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(fork+".original-selection.json", fork+".selection.json"); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory-sidecar", func(t *testing.T, _, fork string, _ Snapshot) {
			if err := os.Remove(fork + ".selection.json"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(fork+".selection.json", 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"replaced-gitfile", func(t *testing.T, repo, fork string, _ Snapshot) {
			if err := os.RemoveAll(filepath.Join(fork, ".git")); err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, filepath.Join(fork, ".git"), []byte("gitdir: "+filepath.Join(repo, ".git")+"\n"))
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			repo, root, fork, baseline := privateHiddenStartingPathFixture(t)
			mutation.edit(t, repo, fork, baseline)
			beforeSource := privateSourceMetadataWithTimes(t, repo)
			beforeFork := privateSourceMetadataWithTimes(t, fork)
			checks := []Check{pythonCheck("from pathlib import Path; Path('check-ran').write_text('unexpected')")}
			if v, err := Verify(context.Background(), fork, checks); err == nil || v.Passed {
				t.Fatalf("incomplete private identity reached standalone verification: %+v %v", v, err)
			}
			if _, err := Capture(context.Background(), fork, filepath.Join(root, "refused-capture")); err == nil {
				t.Fatal("incomplete private identity reached capture")
			}
			if _, _, err := contentState(context.Background(), fork); err == nil {
				t.Fatal("incomplete private identity reached content fingerprinting")
			}
			if _, err := SnapshotUsage(context.Background(), fork); err == nil {
				t.Fatal("incomplete private identity reached snapshot admission")
			}
			for _, policy := range []string{"", StoragePrivate} {
				name := policy
				if name == "" {
					name = "default"
				}
				store := filepath.Join(root, "refused-store-"+name)
				if _, err := PrepareSnapshotStorage(context.Background(), policy, fork, store); err == nil {
					t.Fatal("incomplete private identity was replaced by a new storage baseline")
				}
				if _, err := os.Lstat(store); !os.IsNotExist(err) {
					t.Fatalf("private admission created a store before refusing provenance: %v", err)
				}
			}
			if _, err := os.Lstat(filepath.Join(fork, "check-ran")); !os.IsNotExist(err) {
				t.Fatalf("incomplete private identity admitted a check: %v", err)
			}
			if privateSourceMetadataWithTimes(t, repo) != beforeSource || privateSourceMetadataWithTimes(t, fork) != beforeFork {
				t.Fatal("private discovery refusal changed source or worktree Git metadata")
			}
		})
	}
}

func TestPrivateDiscoveryRestoresSelectionForUsageAndDefaultStorage(t *testing.T) {
	repo, root, fork, _ := privateHiddenStartingPathFixture(t)
	beforeSource := privateSourceMetadataWithTimes(t, repo)
	beforeFork := privateSourceMetadataWithTimes(t, fork)
	usage, err := SnapshotUsage(context.Background(), fork)
	if err != nil || usage.Files != 2 || usage.Bytes != int64(len("file.txt\n")+len("base\n")) {
		t.Fatalf("private usage lost the ignored starting path: %+v %v", usage, err)
	}
	if privateSourceMetadataWithTimes(t, repo) != beforeSource || privateSourceMetadataWithTimes(t, fork) != beforeFork {
		t.Fatal("private snapshot usage changed source or worktree Git metadata")
	}
	ctx, err := PrepareSnapshotStorage(context.Background(), "", fork, filepath.Join(root, "fresh-check.git"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := Verify(ctx, fork, []Check{pythonCheck("from pathlib import Path; assert Path('file.txt').read_bytes()==b'base\\n'")})
	if err != nil || !v.Passed || v.Storage != StoragePrivate || !digestID(v.SelectionSHA256) {
		t.Fatalf("default check storage lost private selection provenance: %+v %v", v, err)
	}
	if got, err := snapshotGitText(ctx, v.Store, true, "show", v.Tree+":file.txt"); err != nil || got != "base" {
		t.Fatalf("new check store omitted a checked starting path: %q %v", got, err)
	}
	final, err := Capture(ctx, fork, filepath.Join(root, "recaptured"))
	if err != nil || final.SelectedFiles != usage.Files || final.SelectedBytes != usage.Bytes || final.Tree != v.Tree || final.ContentSHA256 != v.ContentSHA256 {
		t.Fatalf("usage, default checks and capture selected different bytes: usage=%+v final=%+v %v", usage, final, err)
	}
	if privateSourceMetadataWithTimes(t, repo) != beforeSource || privateSourceMetadataWithTimes(t, fork) != beforeFork {
		t.Fatal("new private check storage changed source or worktree Git metadata")
	}
}

func privateSelectionMissingMarkerWorkflow(t *testing.T, isolated bool) {
	t.Helper()
	repo, root := fixtureRepo(t)
	beforeSource := privateSourceMetadataWithTimes(t, repo)
	script := privateSelectionCommitScript + "\nimport shutil\nPath('.git/" + privateStoreMarkerName + "').unlink()\nshutil.rmtree('.git/" + privateSelectionDirectory + "')\n"
	p := comparePlan(repo, pythonCandidate("writer", script))
	p.Checks = []Check{pythonCheck("from pathlib import Path; Path('check-ran').write_text('unexpected')")}
	if isolated {
		p.Isolation = &IsolationOptions{}
		p.Candidates[0].Command[0] = "/usr/bin/python3"
		p.Checks[0].Command[0] = "/usr/bin/python3"
	}
	r, err := Run(context.Background(), p, filepath.Join(root, "run"), false)
	if err == nil || len(r.Candidates) != 1 || r.Candidates[0].State == "verified" || r.Candidates[0].Verification != nil && r.Candidates[0].Verification.Passed {
		t.Fatalf("worker deleted private provenance before checks: %+v %v", r, err)
	}
	if _, err := os.Lstat(filepath.Join(r.Candidates[0].Worktree, "check-ran")); !os.IsNotExist(err) {
		t.Fatalf("deleted private provenance admitted a check: %v", err)
	}
	if privateSourceMetadataWithTimes(t, repo) != beforeSource {
		t.Fatal("deleted private provenance refusal changed source Git metadata")
	}
}

func TestPrivateSelectionMissingMarkerCannotDowngradeWorkflow(t *testing.T) {
	privateSelectionMissingMarkerWorkflow(t, false)
}

func TestPrivateStandaloneVerificationRestoresPinnedPathsAcrossMetadataChanges(t *testing.T) {
	ctx, _, root, baseline := preparePrivateFixture(t)
	fork := filepath.Join(root, "fork")
	if err := CreateWorktree(ctx, baseline, fork); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, filepath.Join(fork, ".gitignore"), []byte("file.txt\n"))
	fixtureGit(t, fork, "add", ".gitignore")
	fixtureGit(t, fork, "rm", "--cached", "file.txt")
	fixtureGit(t, fork, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-qm", "hide the starting path before standalone checks")
	v, err := Verify(context.Background(), fork, []Check{pythonCheck("from pathlib import Path; assert Path('file.txt').read_bytes()==b'base\\n'")})
	if err != nil || !v.Passed || !digestID(v.SelectionSHA256) {
		t.Fatalf("standalone verification did not pin its selected paths: %+v %v", v, err)
	}
	if got := fixtureGit(t, fork, "show", v.Tree+":file.txt"); got != "base" {
		t.Fatalf("standalone verification lost the external starting path: %q", got)
	}
	fixtureGit(t, fork, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-qm", "metadata-only change after verification")
	if fresh, err := IsFresh(context.Background(), fork, v); err != nil || !fresh {
		t.Fatalf("metadata-only edits changed restored path selection: %v %v", fresh, err)
	}
	fixtureWrite(t, filepath.Join(fork, "file.txt"), []byte("stale hidden bytes\n"))
	if fresh, err := IsFresh(context.Background(), fork, v); err != nil || fresh {
		t.Fatalf("restored path selection hid changed bytes: %v %v", fresh, err)
	}
	manifest := filepath.Join(v.Store, privateSelectionDirectory, v.SelectionSHA256)
	fixtureWrite(t, manifest, []byte(".gitignore\x00"))
	if fresh, err := IsFresh(context.Background(), fork, v); err == nil || fresh {
		t.Fatalf("modified worker manifest replaced external selection identity: %v %v", fresh, err)
	}
	v.SelectionSHA256 = ""
	if fresh, err := IsFresh(context.Background(), fork, v); err == nil || fresh {
		t.Fatalf("private verification without path identity remained usable: %v %v", fresh, err)
	}
}

func TestPrivateStandaloneVerificationRequiresExternalStartingSelection(t *testing.T) {
	ctx, _, root, baseline := preparePrivateFixture(t)
	fork := filepath.Join(root, "fork")
	if err := CreateWorktree(ctx, baseline, fork); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fork + ".selection.json"); err != nil {
		t.Fatal(err)
	}
	v, err := Verify(context.Background(), fork, []Check{pythonCheck("from pathlib import Path; Path('check-ran').write_text('unexpected')")})
	if err == nil || v.Passed {
		t.Fatalf("private metadata invented a missing external baseline: %+v %v", v, err)
	}
	if _, err := os.Stat(filepath.Join(fork, "check-ran")); !os.IsNotExist(err) {
		t.Fatalf("missing external baseline reached checks: %v", err)
	}
}

func TestPrivateSourceIndexGitlinkAdmissionFailsWithoutGitWrites(t *testing.T) {
	repo, root := fixtureRepo(t)
	if err := os.Mkdir(filepath.Join(repo, "selected-dir"), 0700); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, filepath.Join(repo, "selected-dir", "required.txt"), []byte("owner-selected directory\n"))
	commit := fixtureGit(t, repo, "rev-parse", "HEAD")
	fixtureGit(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+commit+",selected-dir")
	before := privateSourceMetadataWithTimes(t, repo)
	ctx, err := PrepareSnapshotStorage(context.Background(), StoragePrivate, repo, filepath.Join(root, "private.git"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Capture(ctx, repo, filepath.Join(root, "snapshot")); err == nil {
		t.Fatal("source index gitlink silently omitted owner directory contents")
	}
	if privateSourceMetadataWithTimes(t, repo) != before {
		t.Fatal("source index admission changed source Git metadata")
	}
}

func TestPrivateSelectionRestoresMissionAndLaboratoryIntegration(t *testing.T) {
	for _, mode := range []string{"mission", "lab"} {
		t.Run(mode, func(t *testing.T) {
			repo, root := fixtureRepo(t)
			fixtureWrite(t, filepath.Join(repo, ".git", "agentmux-apply.lock"), nil)
			before := privateSourceMetadataWithTimes(t, repo)
			p := comparePlan(repo, pythonCandidate("writer", privateSelectionCommitScript))
			var r Report
			var err error
			candidate := "writer"
			if mode == "mission" {
				p.Mode = "mission"
				p.Candidates = append(p.Candidates, pythonCandidate("reader", "from pathlib import Path; assert Path('file.txt').read_bytes()==b'fixed\\n'; print('checked current files')"))
				p.Steps = []Step{{ID: "implement", Candidate: "writer", Verify: true}, {ID: "observe", Candidate: "reader", DependsOn: []string{"implement"}, ReadOnly: true, Verify: true}}
				r, err = Run(context.Background(), p, filepath.Join(root, "run"), false)
				candidate = ""
			} else {
				var lab LabReport
				lab, err = Lab(context.Background(), p, filepath.Join(root, "lab"), false, 1)
				if err == nil {
					r, err = LoadReport(lab.Runs[0].Output)
				}
			}
			if err != nil || r.State != "completed" {
				t.Fatalf("%s worker commit selection: %+v %v", mode, r, err)
			}
			if applied, err := Apply(context.Background(), r.Output, candidate); err != nil || !applied.Applied || applied.Uncertain {
				t.Fatalf("%s restored selection application: %+v %v", mode, applied, err)
			}
			if data, err := os.ReadFile(filepath.Join(repo, "file.txt")); err != nil || string(data) != "fixed\n" {
				t.Fatalf("%s application lost checked file: %q %v", mode, data, err)
			}
			if privateSourceMetadataWithTimes(t, repo) != before {
				t.Fatalf("%s selection application changed source Git metadata", mode)
			}
		})
	}
}

func TestPrivateSelectionIgnoresWorkerObjectReplacementRefs(t *testing.T) {
	repo, root := fixtureRepo(t)
	fixtureWrite(t, filepath.Join(repo, ".git", "agentmux-apply.lock"), nil)
	before := privateSourceMetadataWithTimes(t, repo)
	r, err := Run(context.Background(), comparePlan(repo, pythonCandidate("writer", "from pathlib import Path; Path('file.txt').write_text('fixed\\n')")), filepath.Join(root, "run"), false)
	if err != nil {
		t.Fatal(err)
	}
	c := r.Candidates[0]
	cmd := exec.Command("git", "-C", c.Worktree, "mktree")
	empty, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, c.Worktree, "replace", c.FinalSnapshot.Tree, strings.TrimSpace(string(empty)))
	if fresh, err := IsFresh(context.Background(), c.Worktree, *c.Verification); err != nil || !fresh {
		t.Fatalf("worker replacement ref reinterpreted immutable checked objects: %v %v", fresh, err)
	}
	if applied, err := Apply(context.Background(), r.Output, c.ID); err != nil || !applied.Applied || applied.Uncertain {
		t.Fatalf("replacement ref changed canonical application: %+v %v", applied, err)
	}
	if data, err := os.ReadFile(filepath.Join(repo, "file.txt")); err != nil || string(data) != "fixed\n" {
		t.Fatalf("replacement ref changed applied bytes: %q %v", data, err)
	}
	if privateSourceMetadataWithTimes(t, repo) != before {
		t.Fatal("replacement ref validation changed source Git metadata")
	}
}
