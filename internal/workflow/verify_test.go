package workflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerificationBindsExactContentsAndDetectsStaleness(t *testing.T) {
	repo, _ := fixtureRepo(t)
	ctx := context.Background()
	v, err := Verify(ctx, repo, []Check{{Name: "contents", Command: []string{"/bin/sh", "-c", "test \"$(cat file.txt)\" = base"}}})
	if err != nil || !v.Passed {
		t.Fatalf("verification failed: %+v %v", v, err)
	}
	if fresh, err := IsFresh(ctx, repo, v); err != nil || !fresh {
		t.Fatalf("fresh verification: %v %v", fresh, err)
	}
	fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("different\n"))
	if fresh, err := IsFresh(ctx, repo, v); err != nil || fresh {
		t.Fatalf("stale verification: %v %v", fresh, err)
	}
	fixtureWrite(t, filepath.Join(repo, "file.txt"), []byte("base\n"))
	fixtureWrite(t, filepath.Join(repo, "new-file"), []byte("also changes snapshot"))
	if fresh, err := IsFresh(ctx, repo, v); err != nil || fresh {
		t.Fatalf("untracked change not detected: %v %v", fresh, err)
	}
}

func TestVerificationRejectsMutationAndSkipsRemainingChecks(t *testing.T) {
	repo, _ := fixtureRepo(t)
	v, err := Verify(context.Background(), repo, []Check{{Name: "mutator", Command: []string{"/bin/sh", "-c", "printf changed > file.txt"}}, {Name: "must not execute", Command: []string{"/bin/sh", "-c", "printf wrong > should-not-exist"}}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Passed || v.Tree == v.EndingTree || len(v.Checks) != 1 || v.Checks[0].Passed || !strings.Contains(v.Checks[0].Error, "changed repository") {
		t.Fatalf("mutated verification accepted: %+v", v)
	}
	if fresh, err := IsFresh(context.Background(), repo, v); err != nil || fresh {
		t.Fatalf("mutated verification fresh: %v %v", fresh, err)
	}
}

func TestVerificationFailureCapturesExitAndBoundsOutput(t *testing.T) {
	repo, _ := fixtureRepo(t)
	v, err := Verify(context.Background(), repo, []Check{{Name: "failure", Command: []string{"/bin/sh", "-c", "printf stdout; printf diagnostic >&2; exit 7"}}})
	if err != nil || v.Passed || len(v.Checks) != 1 {
		t.Fatalf("failure report: %+v %v", v, err)
	}
	r := v.Checks[0]
	if r.ExitCode != 7 || r.Output != "stdout" || !strings.Contains(r.Error, "diagnostic") {
		t.Fatalf("lost diagnostic: %+v", r)
	}
	if _, err := Verify(context.Background(), repo, nil); err == nil {
		t.Fatal("empty checks passed")
	}
	if _, err := Verify(context.Background(), repo, []Check{{Name: "bad", Command: []string{"/bin/true"}, TimeoutSeconds: 601}}); err == nil {
		t.Fatal("unbounded timeout accepted")
	}
}

func TestVerificationTimeoutAndLiteralArgv(t *testing.T) {
	repo, _ := fixtureRepo(t)
	start := time.Now()
	v, err := Verify(context.Background(), repo, []Check{{Name: "timeout", Command: []string{"/bin/sh", "-c", "sleep 60"}, TimeoutSeconds: 1}})
	if err != nil || v.Passed || time.Since(start) > 5*time.Second {
		t.Fatalf("timeout not bounded: %+v %v (%v)", v, err, time.Since(start))
	}
	v, err = Verify(context.Background(), repo, []Check{{Name: "literal", Command: []string{"/bin/echo", "$HOME; touch forbidden"}}})
	if err != nil || !v.Passed || v.Checks[0].Output != "$HOME; touch forbidden\n" {
		t.Fatalf("argv interpreted: %+v %v", v, err)
	}
}
