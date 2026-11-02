package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLabFreezesSourceRotatesOrderAndKeepsOriginalIntegrationTarget(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := comparePlan(repo, pythonCandidate("a", "from pathlib import Path; assert Path('file.txt').read_text()=='base\\n'; Path('file.txt').write_text('fixed\\n')"), pythonCandidate("b", "from pathlib import Path; assert Path('file.txt').read_text()=='base\\n'; Path('file.txt').write_text('fixed\\n')"))
	r, err := Lab(context.Background(), p, filepath.Join(root, "lab"), false, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Runs) != 2 || r.Runs[0].Order[0] != "a" || r.Runs[1].Order[0] != "b" || len(r.Candidates) != 2 {
		t.Fatalf("lab schedule: %+v", r)
	}
	for _, summary := range r.Candidates {
		if summary.Runs != 2 || summary.VerifiedRuns != 2 {
			t.Fatalf("bad summary: %+v", summary)
		}
	}
	for _, trial := range r.Runs {
		run, err := LoadReport(trial.Output)
		if err != nil {
			t.Fatal(err)
		}
		if run.Snapshot.Tree != r.Snapshot.Tree || run.Repository != repo || run.Snapshot.Repository != repo {
			t.Fatalf("trial lost pinned original source: %+v", run)
		}
	}
	data, _ := os.ReadFile(filepath.Join(repo, "file.txt"))
	if string(data) != "base\n" {
		t.Fatal("lab changed original project")
	}
}

func TestWatchPersistsRunningReviewBeforeItsProcessStarts(t *testing.T) {
	repo, root := fixtureRepo(t)
	output := filepath.Join(root, "watch")
	started := filepath.Join(root, "review-started")
	quoted, err := json.Marshal(started)
	if err != nil {
		t.Fatal(err)
	}
	p := comparePlan(repo, pythonCandidate("review", "from pathlib import Path; import time; Path("+string(quoted)+").write_text('started'); time.sleep(30)"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type observation struct {
		report WatchReport
		err    error
	}
	observed := make(chan observation, 1)
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(started); err == nil {
				var current WatchReport
				err = DecodeFile(filepath.Join(output, "watch.json"), &current)
				observed <- observation{current, err}
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		observed <- observation{err: os.ErrDeadlineExceeded}
		cancel()
	}()
	r, runErr := Watch(ctx, p, output, false, WatchOptions{IntervalSeconds: 1, MaxRuns: 1, TimeoutSeconds: 20})
	v := <-observed
	if v.err != nil {
		t.Fatal(v.err)
	}
	if len(v.report.Runs) != 1 || v.report.Runs[0].State != "running" || v.report.Runs[0].Output != filepath.Join(output, "review-0001") || v.report.Runs[0].Tree == "" || v.report.Runs[0].ContentSHA256 == "" {
		t.Fatalf("admitted review is absent from durable watch progress: %+v", v.report)
	}
	if !errors.Is(runErr, context.Canceled) || r.State != "interrupted" || len(r.Runs) != 1 || r.Runs[0].State == "running" {
		t.Fatalf("cancellation lost or duplicated the admitted review: %+v %v", r, runErr)
	}
}

func TestLabPersistsRunningTrialBeforeItsProcessStarts(t *testing.T) {
	repo, root := fixtureRepo(t)
	output := filepath.Join(root, "lab")
	started := filepath.Join(root, "trial-started")
	quoted, err := json.Marshal(started)
	if err != nil {
		t.Fatal(err)
	}
	p := comparePlan(repo, pythonCandidate("review", "from pathlib import Path; import time; Path("+string(quoted)+").write_text('started'); time.sleep(30)"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type observation struct {
		report LabReport
		err    error
	}
	observed := make(chan observation, 1)
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(started); err == nil {
				var current LabReport
				err = DecodeFile(filepath.Join(output, "lab.json"), &current)
				observed <- observation{current, err}
				cancel()
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		observed <- observation{err: os.ErrDeadlineExceeded}
		cancel()
	}()
	r, runErr := Lab(ctx, p, output, false, 2)
	v := <-observed
	if v.err != nil {
		t.Fatal(v.err)
	}
	if len(v.report.Runs) != 1 || v.report.Runs[0].State != "running" || v.report.Runs[0].Output != filepath.Join(output, "trial-0001") || v.report.Runs[0].Repetition != 1 || len(v.report.Runs[0].Order) != 1 || v.report.Snapshot.Tree == "" {
		t.Fatalf("admitted trial is absent from durable lab progress: %+v", v.report)
	}
	if !errors.Is(runErr, context.Canceled) || r.State != "interrupted" || len(r.Runs) != 1 || r.Runs[0].State == "running" {
		t.Fatalf("cancellation lost or duplicated the admitted trial: %+v %v", r, runErr)
	}
}

func TestWatchFinalPersistenceFailureCannotReturnCompleted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the directory permission fault")
	}
	repo, root := fixtureRepo(t)
	output := filepath.Join(root, "watch")
	p := comparePlan(repo, pythonCandidate("review", "raise RuntimeError('watch duration must expire before admission')"))
	fault := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var progress WatchReport
			if err := DecodeFile(filepath.Join(output, "watch.json"), &progress); err == nil && progress.State == "watching" {
				fault <- os.Chmod(output, 0500)
				return
			}
			time.Sleep(time.Millisecond)
		}
		fault <- os.ErrDeadlineExceeded
	}()
	t.Cleanup(func() { _ = os.Chmod(output, 0700) })
	r, err := Watch(context.Background(), p, output, false, WatchOptions{IntervalSeconds: 2, MaxRuns: 1, TimeoutSeconds: 1})
	if faultErr := <-fault; faultErr != nil {
		t.Fatal("did not establish final persistence fault:", faultErr)
	}
	if err == nil || r.State != "failed" || r.Error == "" {
		t.Fatalf("failed final persistence returned success state: %+v %v", r, err)
	}
	if len(r.Runs) != 0 {
		t.Fatal("the duration-only watch unexpectedly admitted a reviewer")
	}
	var stored WatchReport
	if readErr := DecodeFile(filepath.Join(output, "watch.json"), &stored); readErr != nil || stored.State != "watching" {
		t.Fatalf("failed final persistence damaged retained progress: %+v %v", stored, readErr)
	}
}

func TestWatchReviewsStableSnapshotAndFlagsModifiedReview(t *testing.T) {
	repo, root := fixtureRepo(t)
	p := comparePlan(repo, pythonCandidate("review", "from pathlib import Path; Path('file.txt').write_text('fixed\\n'); print('modified while reviewing')"))
	r, err := Watch(context.Background(), p, filepath.Join(root, "watch"), false, WatchOptions{IntervalSeconds: 1, MaxRuns: 1, TimeoutSeconds: 10})
	if err == nil || len(r.Runs) != 1 || r.Runs[0].State != "modified_review_snapshot" || r.State != "failed" {
		t.Fatalf("mutating review shown as clean: %+v %v", r, err)
	}
	data, _ := os.ReadFile(filepath.Join(repo, "file.txt"))
	if string(data) != "base\n" {
		t.Fatal("watch changed source")
	}
}
