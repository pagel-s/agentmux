package managed

import (
	"agentmux/internal/managedconfig"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestTraceAuthenticationTruncationAndTaskAnchor(t *testing.T) {
	state := t.TempDir()
	policy := managedconfig.Trace{Enabled: true, MaxTurnBytes: 8192, MaxTotalBytes: 16384}
	tr, err := beginTrace(state, "alice", "task_example", policy, map[string]string{"prompt": "exact prepared input"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.append("native_event", map[string]string{"text": "observed reply"}); err != nil {
		t.Fatal(err)
	}
	sum, err := tr.finish("submitted")
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyTrace(state, "alice", sum.ID, sum.MAC)
	if err != nil || verified != sum {
		t.Fatalf("verify: %+v %v", verified, err)
	}
	path := filepath.Join(state, "traces", "alice", sum.ID+".jsonl")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	variants := [][]byte{bytes.Replace(original, []byte("observed reply"), []byte("tampered reply"), 1), original[:len(original)-1], append(append([]byte(nil), original...), []byte("{}\n")...), bytes.Replace(original, []byte("\n"), []byte(" {}\n"), 1)}
	for _, bad := range variants {
		if err := os.WriteFile(path, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyTrace(state, "alice", sum.ID, ""); err == nil {
			t.Fatal("invalid framing/auth accepted")
		}
	}
	os.WriteFile(path, original, 0600)
	if _, err := VerifyTrace(state, "alice", sum.ID, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong task anchor accepted")
	}
	if _, err := VerifyTrace(state, "bob", sum.ID, sum.MAC); err == nil {
		t.Fatal("peer trace exposed")
	}
}

func TestTraceBoundedCapacityAndFailedHeaderCleanup(t *testing.T) {
	state := t.TempDir()
	p := managedconfig.Trace{Enabled: true, MaxTurnBytes: 4096, MaxTotalBytes: 4096}
	if _, err := beginTrace(state, "alice", "task_x", p, strings.Repeat("x", 4096)); err == nil {
		t.Fatal("oversize header accepted")
	}
	dir := filepath.Join(state, "traces", "alice")
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("failed header leaked trace file")
	}
	tr, err := beginTrace(state, "alice", "task_x", p, map[string]string{"prompt": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.append("native_event", strings.Repeat("x", 4096)); err == nil {
		t.Fatal("turn cap ignored")
	}
	sum, err := tr.finish("failed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTrace(state, "alice", sum.ID, sum.MAC); err != nil {
		t.Fatal(err)
	}
	if _, err := traceCapacity(state, "alice", p); err == nil {
		t.Fatal("next turn admitted without full reservation")
	}
}

func TestTraceKeyAtomicConcurrentPublication(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	keys := make(chan []byte, 24)
	failures := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key, err := privateTraceKey(dir)
			if err != nil {
				failures <- err
			} else {
				keys <- key
			}
		}()
	}
	wg.Wait()
	close(keys)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var first []byte
	for key := range keys {
		if first == nil {
			first = key
		}
		if !bytes.Equal(first, key) {
			t.Fatal("workers received different trace keys")
		}
	}
}
