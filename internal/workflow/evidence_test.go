package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func evidenceRun(t *testing.T) (string, Report) {
	t.Helper()
	repo, root := fixtureRepo(t)
	p := Plan{Version: 1, Execution: "local", Repository: repo, Goal: "Replace base with fixed", TimeoutSeconds: 30, Candidates: []Candidate{{ID: "fix", Harness: "command", Command: []string{"python3", "-c", "from pathlib import Path; Path('file.txt').write_text('fixed\\n'); print('Fixed the supplied file')"}}}, Checks: []Check{{Name: "contents", Command: []string{"python3", "-c", "from pathlib import Path; assert Path('file.txt').read_text() == 'fixed\\n'"}}}}
	dir := filepath.Join(root, "run")
	r, err := Run(context.Background(), p, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return dir, r
}

func TestEvidenceHandoffAndExplicitApply(t *testing.T) {
	dir, r := evidenceRun(t)
	ctx := context.Background()
	e, err := Explain(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Items) != 1 || e.Items[0].Fresh == nil || !*e.Items[0].Fresh || !e.Items[0].PatchIntact {
		t.Fatalf("evidence: %+v", e)
	}
	brief, err := Handoff(dir, "fix")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{r.Goal, r.Snapshot.Tree, r.Candidates[0].PatchSHA256, "contents", "grants no permissions"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("missing %q in %s", want, brief)
		}
	}
	before := fixtureGit(t, r.Repository, "diff", "--cached", "--binary")
	result, err := Apply(ctx, dir, "fix")
	if err != nil || !result.Applied {
		t.Fatalf("apply: %+v %v", result, err)
	}
	b, err := os.ReadFile(filepath.Join(r.Repository, "file.txt"))
	if err != nil || string(b) != "fixed\n" {
		t.Fatalf("source: %q %v", b, err)
	}
	if fixtureGit(t, r.Repository, "diff", "--cached", "--binary") != before {
		t.Fatal("apply modified index")
	}
	if fixtureGit(t, r.Repository, "rev-parse", "HEAD") != r.Snapshot.BaseCommit {
		t.Fatal("apply moved source HEAD")
	}
	if _, err = Apply(ctx, dir, "fix"); err == nil {
		t.Fatal("duplicate apply accepted changed source")
	}
}

func TestApplyRefusesStaleSourceAndCandidate(t *testing.T) {
	for _, where := range []string{"source", "candidate"} {
		t.Run(where, func(t *testing.T) {
			dir, r := evidenceRun(t)
			repo := r.Repository
			if where == "candidate" {
				repo = r.Candidates[0].Worktree
			}
			fixtureWrite(t, filepath.Join(repo, "new.txt"), []byte("new work"))
			if _, err := Apply(context.Background(), dir, "fix"); err == nil {
				t.Fatal("stale content applied")
			}
			b, _ := os.ReadFile(filepath.Join(r.Repository, "file.txt"))
			if string(b) != "base\n" {
				t.Fatal("source changed after rejected integration")
			}
		})
	}
}

func TestApplyBindsPatchToRecordedTrees(t *testing.T) {
	dir, r := evidenceRun(t)
	patch, err := os.ReadFile(r.Candidates[0].Patch)
	if err != nil {
		t.Fatal(err)
	}
	patch = []byte(strings.ReplaceAll(string(patch), "+fixed", "+other"))
	if err = os.WriteFile(r.Candidates[0].Patch, patch, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(patch)
	r.Candidates[0].PatchSHA256 = hex.EncodeToString(sum[:])
	if err = saveJSON(filepath.Join(dir, "report.json"), r); err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(context.Background(), dir, "fix"); err == nil || !strings.Contains(err.Error(), "recorded starting and final") {
		t.Fatalf("unbound patch accepted: %v", err)
	}
}

func TestReportEscapesContentAndProtectsEvidence(t *testing.T) {
	dir, r := evidenceRun(t)
	r.Goal = "<script>alert('x')</script>"
	r.Candidates[0].Result = "</pre><script>bad()</script>"
	if err := saveJSON(filepath.Join(dir, "report.json"), r); err != nil {
		t.Fatal(err)
	}
	path, err := RenderReportTo(dir, filepath.Join(dir, "review.html"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "<script>") || !strings.Contains(string(data), "&lt;script&gt;") {
		t.Fatal("report failed to escape untrusted result")
	}
	if !strings.Contains(string(data), "Content-Security-Policy") {
		t.Fatal("missing content restriction")
	}
	if _, err = RenderReport(dir); err == nil {
		t.Fatal("existing report overwritten")
	}
	if _, err = RenderReportTo(dir, filepath.Join(r.Repository, "report.html")); err == nil {
		t.Fatal("report polluted source")
	}
	if _, err = RenderReportTo(dir, filepath.Join(r.Candidates[0].Worktree, "report.html")); err == nil {
		t.Fatal("report polluted checked candidate")
	}
}

func TestHandoffRefusesPatchOutsideRun(t *testing.T) {
	dir, r := evidenceRun(t)
	external := filepath.Join(filepath.Dir(dir), "external.patch")
	data, _ := os.ReadFile(r.Candidates[0].Patch)
	fixtureWrite(t, external, data)
	r.Candidates[0].Patch = external
	if err := saveJSON(filepath.Join(dir, "report.json"), r); err != nil {
		t.Fatal(err)
	}
	if _, err := Handoff(dir, "fix"); err == nil {
		t.Fatal("external artifact exported")
	}
}

func TestReportCommandsQuoteShellMetacharacters(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-exist")
	path := "run 'quoted' $(touch " + marker + ")"
	output, err := exec.Command("sh", "-c", "printf '%s' "+shellWord(path)).Output()
	if err != nil || string(output) != path {
		t.Fatalf("shell-word roundtrip: %q %v", output, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("quoted path executed command substitution")
	}
}

func TestHandoffRefusesSymlinkPatchEvenInsideRun(t *testing.T) {
	dir, r := evidenceRun(t)
	c := r.Candidates[0]
	link := filepath.Join(dir, "alias.patch")
	if err := os.Symlink(c.Patch, link); err != nil {
		t.Fatal(err)
	}
	r.Candidates[0].Patch = link
	if err := saveJSON(filepath.Join(dir, "report.json"), r); err != nil {
		t.Fatal(err)
	}
	if _, err := Handoff(dir, c.ID); err == nil {
		t.Fatal("symlinked patch accepted")
	}
}

func TestFailedMissionHandoffRetainsFailureReason(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := Plan{Version: 1, Repository: repo, Execution: "local", Mode: "mission", Goal: "record a failure", Candidates: []Candidate{{ID: "fail", Harness: "command", Command: []string{"python3", "-c", "raise SystemExit('Deliberate test failure')"}}}, Checks: []Check{{Name: "check", Command: []string{"python3", "-c", "print('check')"}}}, Steps: []Step{{ID: "failed-step", Candidate: "fail"}}}
	dir := filepath.Join(root, "run")
	r, err := Run(context.Background(), p, dir, false)
	if err == nil || r.Error == "" {
		t.Fatal("fixture should fail")
	}
	brief, err := Handoff(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief, "Recorded failure: "+r.Error) {
		t.Fatalf("mission failure lost in handoff: %s", brief)
	}
}
