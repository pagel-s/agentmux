package managed

import (
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestTraceMACBindsAgentTaskAndTraceFilename(t *testing.T) {
	state := t.TempDir()
	tr, err := beginTrace(state, "alice", "task_original", managedconfig.Trace{MaxTurnBytes: 8192, MaxTotalBytes: 16384}, map[string]string{"prompt": "bound input"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := tr.finish("submitted")
	if err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(state, "traces", "alice", summary.ID+".jsonl"))
	bob := filepath.Join(state, "traces", "bob")
	os.Mkdir(bob, 0700)
	os.WriteFile(filepath.Join(bob, summary.ID+".jsonl"), original, 0600)
	if _, err = VerifyTrace(state, "bob", summary.ID, summary.MAC); err == nil {
		t.Fatal("signed trace relabeled as another agent")
	}
	newID := "task_other-attempt_copy"
	os.WriteFile(filepath.Join(state, "traces", "alice", newID+".jsonl"), original, 0600)
	if _, err = VerifyTrace(state, "alice", newID, summary.MAC); err == nil {
		t.Fatal("signed trace relabeled as another task/attempt")
	}
}
