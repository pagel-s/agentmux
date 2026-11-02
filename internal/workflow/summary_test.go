package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestWorkSummaryTracksReadinessStalenessAndApplication(t *testing.T) {
	dir, r := evidenceRun(t)
	ctx := context.Background()
	s, err := Summarize(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.CanApply || s.SourceFresh == nil || !*s.SourceFresh || s.ResultFresh == nil || !*s.ResultFresh || s.ChecksPassed != 1 || len(s.Files) != 1 {
		t.Fatalf("ready summary: %+v", s)
	}
	if _, err = Apply(ctx, dir, "fix"); err != nil {
		t.Fatal(err)
	}
	s, err = Summarize(ctx, dir)
	if err != nil || s.CanApply || s.Outcome != "Applied to source" {
		t.Fatalf("applied summary: %+v %v", s, err)
	}
	fixtureWrite(t, filepath.Join(r.Repository, "other.txt"), []byte("later work"))
	s, err = Summarize(ctx, dir)
	if err != nil || s.Outcome != "Source changed since this run" || s.CanApply {
		t.Fatalf("later source edit: %+v %v", s, err)
	}
}

func summaryPatchFixture(t *testing.T, writer, check string) (string, Report) {
	t.Helper()
	repo, root := fixtureRepo(t)
	p := Plan{Version: 1, Execution: "local", Repository: repo, Goal: "Produce a patch for review", TimeoutSeconds: 30, Candidates: []Candidate{{ID: "patch", Harness: "command", Command: []string{"python3", "-c", writer}}}, Checks: []Check{{Name: "artifact", Command: []string{"python3", "-B", "-c", check}}}}
	dir := filepath.Join(root, "run")
	r, err := Run(context.Background(), p, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return dir, r
}

func TestWorkSummaryBoundsPreviewAndChecksFullArtifactBeforeDisplaying(t *testing.T) {
	dir, r := summaryPatchFixture(t, "from pathlib import Path; Path('file.txt').write_bytes(b'X'*200000+b'\\n')", "from pathlib import Path; assert len(Path('file.txt').read_bytes())==200001")
	s, err := Summarize(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.PatchPath != r.Candidates[0].Patch || len(s.PatchPreview) == 0 || len(s.PatchPreview) > 64<<10 || !s.PatchPreviewTruncated || s.PatchPreviewError != "" || !utf8.ValidString(s.PatchPreview) {
		t.Fatalf("bad bounded preview: bytes=%d truncated=%t error=%q", len(s.PatchPreview), s.PatchPreviewTruncated, s.PatchPreviewError)
	}
	page, err := os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "Review checked patch") || !strings.Contains(string(page), "64 KiB") || len(page) > 160<<10 {
		t.Fatalf("saved report did not show bounded patch: %d bytes", len(page))
	}
	data, err := os.ReadFile(r.Candidates[0].Patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= 64<<10 {
		t.Fatal("fixture does not exceed preview bound")
	}
	data[len(data)-2] ^= 1
	fixtureWrite(t, r.Candidates[0].Patch, data)
	s, err = Summarize(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.PatchPreview != "" || s.PatchPreviewError == "" || s.CanApply || s.Outcome != "Patch integrity needs inspection" {
		t.Fatalf("unverified tail allowed a preview: %+v", s)
	}
}

func TestWorkSummaryBinaryPreviewAndAppliedGuidanceIncludeUntrackedFiles(t *testing.T) {
	dir, r := summaryPatchFixture(t, "from pathlib import Path; Path('binary.dat').write_bytes(bytes(range(256))*8)", "from pathlib import Path; assert Path('binary.dat').read_bytes()==bytes(range(256))*8")
	s, err := Summarize(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.PatchPreview, "GIT binary patch") || strings.ContainsAny(s.PatchPreview, "\x00\x1b") || s.PatchPreviewError != "" {
		t.Fatalf("binary preview: %q %v", s.PatchPreview, s.PatchPreviewError)
	}
	if _, err = Apply(context.Background(), dir, "patch"); err != nil {
		t.Fatal(err)
	}
	diff, err := gitOutput(context.Background(), r.Repository, nil, "diff", "--stat")
	if err != nil || len(diff) != 0 {
		t.Fatalf("fixture should demonstrate untracked-only diff gap: %q %v", diff, err)
	}
	status, err := gitOutput(context.Background(), r.Repository, nil, "status", "--short")
	if err != nil || !strings.Contains(string(status), "?? binary.dat") {
		t.Fatalf("new file not visible: %q %v", status, err)
	}
	s, err = Summarize(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Outcome != "Applied to source" || !strings.HasSuffix(s.NextCommand, " status --short") || !strings.Contains(s.NextAction, "untracked") {
		t.Fatalf("applied guidance hides new file: %+v", s)
	}
}

func TestWorkSummaryPreviewEscapesRawControlsAndInvalidTextBytes(t *testing.T) {
	dir, _ := summaryPatchFixture(t, "from pathlib import Path; Path('file.txt').write_bytes(bytes.fromhex('1b5b324a7878ff0a'))", "from pathlib import Path; assert Path('file.txt').read_bytes().hex()=='1b5b324a7878ff0a'")
	s, err := Summarize(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.PatchPreview, `\u001b`) || !strings.Contains(s.PatchPreview, `\xff`) || strings.ContainsRune(s.PatchPreview, '\x1b') || !utf8.ValidString(s.PatchPreview) {
		t.Fatalf("unsafe raw patch display: %q", s.PatchPreview)
	}
}

func TestWorkSummaryApprovedNoOpNeedsNoApplicationAndPreservesStaleStates(t *testing.T) {
	repo, root := fixtureRepo(t)
	review := `{"decision":"approve","summary":"Inspected the unchanged revision; the declared contract is already satisfied.","findings":[]}`
	plan := Plan{Version: 1, Kind: "work", Execution: "local", Repository: repo, Mode: "mission", Goal: "Confirm the baseline already satisfies its contract", TimeoutSeconds: 30, Candidates: []Candidate{{ID: "coder", Harness: "command", Command: []string{"python3", "-c", "print('The existing project already meets the objective. No edits were required.')"}}, {ID: "reviewer", Harness: "command", Command: []string{"python3", "-c", fmt.Sprintf("print(%q)", review)}}}, Checks: []Check{{Name: "baseline", Command: []string{"python3", "-c", "from pathlib import Path; assert Path('file.txt').read_text()=='base\\n'"}}}, Steps: []Step{{ID: "implement", Candidate: "coder", Verify: true}, {ID: "review", Candidate: "reviewer", DependsOn: []string{"implement"}, RequireResult: true, ReadOnly: true, Review: true, GateReview: true}, {ID: "acceptance", Verify: true, DependsOn: []string{"review"}}}}
	dir := filepath.Join(root, "no-op")
	report, err := RunMission(context.Background(), plan, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := readPatch(dir, report.Patch, report.PatchSHA256)
	if err != nil || len(patch) != 0 {
		t.Fatalf("fixture did not retain a verified empty patch: %d %v", len(patch), err)
	}
	s, err := Summarize(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.CanApply || s.Outcome != "No changes needed" || s.SourceFresh == nil || !*s.SourceFresh || s.ResultFresh == nil || !*s.ResultFresh || s.ReviewState != "approve" || strings.Contains(s.NextCommand, "apply") {
		t.Fatalf("no-op still invites application: %+v", s)
	}
	if !strings.Contains(s.NextAction, "Acceptance checks and final review completed") {
		t.Fatalf("no-op conclusion lost its acceptance evidence: %s", s.NextAction)
	}
	page, err := os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "No changes needed") || strings.Contains(string(page), "amx work apply") {
		t.Fatal("saved no-op report suggests applying an empty result")
	}
	fixtureWrite(t, filepath.Join(repo, "later.txt"), []byte("Unrelated work after acceptance"))
	s, err = Summarize(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.CanApply || s.Outcome != "Source changed since this run" {
		t.Fatalf("empty patch concealed later source work: %+v", s)
	}
	if err = os.Rename(report.Worktree, report.Worktree+"-retained"); err != nil {
		t.Fatal(err)
	}
	s, err = Summarize(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.CanApply || s.Outcome == "No changes needed" || s.ResultFresh != nil || s.ResultFreshnessError == "" {
		t.Fatalf("empty patch concealed unknown result freshness: %+v", s)
	}
}

func TestWorkSummaryShowsUncertainApplicationEvenIfSourceMatchesStart(t *testing.T) {
	dir, r := evidenceRun(t)
	if err := saveApplyReceipt(dir, ApplyResult{Run: r.ID, Repository: r.Repository, PatchSHA256: r.Candidates[0].PatchSHA256, Uncertain: true, Note: "Unknown completion"}); err != nil {
		t.Fatal(err)
	}
	s, err := Summarize(context.Background(), dir)
	if err != nil || s.CanApply || s.Outcome != "Application needs inspection" {
		t.Fatalf("uncertainty concealed: %+v %v", s, err)
	}
}

func TestWorkSummaryDoesNotLoseUncertainReceiptAmongArtifacts(t *testing.T) {
	dir, r := evidenceRun(t)
	if err := saveApplyReceipt(dir, ApplyResult{Run: r.ID, Repository: r.Repository, PatchSHA256: r.Candidates[0].PatchSHA256, Uncertain: true, Note: "Unknown completion"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 140; i++ {
		fixtureWrite(t, filepath.Join(dir, fmt.Sprintf("evidence-%03d.txt", i)), []byte("Retained evidence"))
	}
	s, err := Summarize(context.Background(), dir)
	if err != nil || s.CanApply || s.Outcome != "Application needs inspection" {
		t.Fatalf("artifact count concealed uncertain application: %+v %v", s, err)
	}
}

func TestWorkSummaryRefusesUnreadableApplicationHistory(t *testing.T) {
	dir, _ := evidenceRun(t)
	fixtureWrite(t, filepath.Join(dir, "applied-9999999999999999999.json"), []byte("{damaged receipt"))
	s, err := Summarize(context.Background(), dir)
	if err != nil || s.CanApply || s.Outcome != "Application history needs inspection" {
		t.Fatalf("damaged receipt was treated as no application: %+v %v", s, err)
	}
}

func TestWorkSummaryDoesNotInventLiveActivityOrFreshness(t *testing.T) {
	dir, r := evidenceRun(t)
	r.State = "running"
	r.FinishedAt = ""
	if err := saveJSON(filepath.Join(dir, "report.json"), r); err != nil {
		t.Fatal(err)
	}
	s, err := Summarize(context.Background(), dir)
	if err != nil || s.CanApply || s.Outcome != "Recorded in progress" || !strings.Contains(s.NextAction, "not a live heartbeat") {
		t.Fatalf("invented liveness: %+v %v", s, err)
	}
	r.State = "completed"
	r.FinishedAt = timestamp()
	if err := saveJSON(filepath.Join(dir, "report.json"), r); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(r.Candidates[0].Worktree, r.Candidates[0].Worktree+"-retained"); err != nil {
		t.Fatal(err)
	}
	s, err = Summarize(context.Background(), dir)
	if err != nil || s.CanApply || s.ResultFresh != nil || s.ResultFreshnessError == "" {
		t.Fatalf("missing result treated as fresh: %+v %v", s, err)
	}
}

func summaryReviewedWork(t *testing.T, decision string) (string, Report) {
	t.Helper()
	repo, root := fixtureRepo(t)
	result := `{"decision":"approve","summary":"Reviewed the exact content; no blocking findings.","findings":[]}`
	if decision == "request_changes" {
		result = `{"decision":"request_changes","summary":"A follow-up is required.","findings":[{"file":"file.txt","line":1,"message":"Explain this change."}]}`
	}
	p := Plan{Version: 1, Kind: "work", Execution: "local", Repository: repo, Mode: "mission", Goal: "Fix then review", TimeoutSeconds: 30, Candidates: []Candidate{{ID: "coder", Harness: "command", Command: []string{"python3", "-c", "from pathlib import Path; Path('file.txt').write_text('fixed\\n'); print('Implemented')"}}, {ID: "reviewer", Harness: "command", Command: []string{"python3", "-c", fmt.Sprintf("print(%q)", result)}}}, Checks: []Check{{Name: "contents", Command: []string{"python3", "-c", "from pathlib import Path; assert Path('file.txt').read_text()=='fixed\\n'"}}}, Steps: []Step{{ID: "implement", Candidate: "coder", Verify: true}, {ID: "review", Candidate: "reviewer", DependsOn: []string{"implement"}, RequireResult: true, ReadOnly: true, Review: true, GateReview: true}, {ID: "acceptance", Verify: true, DependsOn: []string{"review"}}}}
	dir := filepath.Join(root, "work")
	report, err := RunMission(context.Background(), p, dir, false)
	if decision == "approve" && err != nil {
		t.Fatal(err)
	}
	if decision != "approve" && err == nil {
		t.Fatal("blocking review passed")
	}
	return dir, report
}

func TestWorkSummarySeparatesReviewVerdictAndChecks(t *testing.T) {
	for _, decision := range []string{"approve", "request_changes"} {
		t.Run(decision, func(t *testing.T) {
			dir, _ := summaryReviewedWork(t, decision)
			s, err := Summarize(context.Background(), dir)
			if err != nil {
				t.Fatal(err)
			}
			if s.ReviewState != decision || s.Review == "" {
				t.Fatalf("review absent: %+v", s)
			}
			if s.CanApply != (decision == "approve") {
				t.Fatalf("wrong approval readiness: %+v", s)
			}
		})
	}
}

func TestWorkReportLeadsWithOutcomeReviewAndChangedFiles(t *testing.T) {
	dir, _ := summaryReviewedWork(t, "approve")
	data, err := os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{"Ready for your review", "Next step", "Approved review", "Changed files", "file.txt", "Recorded evidence", "amx work apply"} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing everyday review element %q", want)
		}
	}
	if strings.Index(page, "Ready for your review") > strings.Index(page, "Recorded evidence") {
		t.Fatal("technical evidence displaced the outcome")
	}
}

func TestWorkSummarySuppressesHostileCopyCommands(t *testing.T) {
	dir, r := summaryReviewedWork(t, "approve")
	renamed := r.Repository + "\u202e"
	if err := os.Rename(r.Repository, renamed); err != nil {
		t.Fatal(err)
	}
	// Keep source path consistent; the reviewed worktree remains where Git
	// registered it, so this is a freshness-unknown display case, not approval.
	r.Repository = renamed
	r.Snapshot.Repository = renamed
	if err := saveJSON(filepath.Join(dir, "report.json"), r); err != nil {
		t.Fatal(err)
	}
	s, err := Summarize(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !safeCommandText(s.NextCommand) {
		t.Fatal("unsafe command offered for copying")
	}
}
