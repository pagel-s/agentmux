package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func auditPlan(repo string, command []string) Plan {
	return Plan{Version: 1, Repository: repo, Execution: "local", Mode: "compare", Goal: "Review the snapshot", TimeoutSeconds: 5, Candidates: []Candidate{{ID: "review", Harness: "command", Command: command}}, Checks: []Check{{Name: "fixture check", Command: []string{"/bin/true"}}}}
}

func TestAuditWatchDoesNotReportFailedReviewAsSuccessful(t *testing.T) {
	repo, root := fixtureRepo(t)
	r, err := Watch(context.Background(), auditPlan(repo, []string{"/bin/sh", "-c", "exit 7"}), filepath.Join(root, "watch"), false, WatchOptions{IntervalSeconds: 1, MaxRuns: 1, TimeoutSeconds: 5})
	if err == nil || r.State == "completed" {
		t.Fatalf("failed review reported success: %+v %v", r, err)
	}
	if len(r.Runs) != 1 || r.Runs[0].Error == "" {
		t.Fatalf("lost failed review evidence: %+v", r)
	}
}

func TestAuditWatchFindingsBecomeSupersededWhenSourceChangesDuringReview(t *testing.T) {
	repo, root := fixtureRepo(t)
	marker := filepath.Join(root, "review-started")
	p := auditPlan(repo, []string{"/bin/sh", "-c", "printf ready > '" + marker + "'; sleep 0.3; printf reviewed"})
	changed := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				changed <- os.WriteFile(filepath.Join(repo, "file.txt"), []byte("source moved\n"), 0600)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		changed <- context.DeadlineExceeded
	}()
	r, err := Watch(context.Background(), p, filepath.Join(root, "watch"), false, WatchOptions{IntervalSeconds: 1, MaxRuns: 1, TimeoutSeconds: 5})
	if changeErr := <-changed; changeErr != nil {
		t.Fatal(changeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Runs) != 1 || !r.Runs[0].Superseded {
		t.Fatalf("review of old revision shown as current: %+v", r)
	}
}

func TestAuditOutputCannotBeCreatedInsideActualRepositoryViaSubdirectory(t *testing.T) {
	repo, _ := fixtureRepo(t)
	subdir := filepath.Join(repo, "subdir")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatal(err)
	}
	p := auditPlan(subdir, []string{"/bin/true"})
	output := filepath.Join(repo, "should-not-be-created")
	if _, err := Run(context.Background(), p, output, false); err == nil {
		t.Fatal("accepted output inside actual repository root")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("invalid workflow modified source before refusal: %v", err)
	}
}
