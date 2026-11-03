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
	changedMarker := filepath.Join(root, "source-changed")
	p := auditPlan(repo, []string{"/bin/sh", "-c", "printf ready > '" + marker + "'; while [ ! -e '" + changedMarker + "' ]; do sleep 0.01; done; printf reviewed"})
	p.TimeoutSeconds = 30
	// Allow snapshot setup on a loaded race-test runner, and keep the review
	// running until the source write completes instead of relying on a sleep.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	changed := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(marker); err == nil {
				err = os.WriteFile(filepath.Join(repo, "file.txt"), []byte("source moved\n"), 0600)
				if err == nil {
					err = os.WriteFile(changedMarker, []byte("changed"), 0600)
				}
				changed <- err
				if err != nil {
					cancel()
				}
				return
			} else if !os.IsNotExist(err) {
				changed <- err
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				changed <- ctx.Err()
				return
			case <-ticker.C:
			}
		}
	}()
	r, err := Watch(ctx, p, filepath.Join(root, "watch"), false, WatchOptions{IntervalSeconds: 1, MaxRuns: 1, TimeoutSeconds: 30})
	cancel()
	if changeErr := <-changed; changeErr != nil {
		t.Fatalf("source change failed: %v; watch error: %v", changeErr, err)
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
