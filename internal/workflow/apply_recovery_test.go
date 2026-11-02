package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedApplicationIntent(t *testing.T, dir string, r Report) string {
	t.Helper()
	c, err := selectResult(r, "fix")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "applied-1000000000000000000.json")
	if err := writePrivateJSON(path, ApplyResult{Run: r.ID, Candidate: c.ID, Repository: r.Repository, PatchSHA256: c.PatchSHA256, Uncertain: true, Phase: "intent", Receipt: path, Note: "Simulated hard interruption before final receipt"}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestApplicationIntentBlocksReplayUntilExplicitRecovery(t *testing.T) {
	dir, r := evidenceRun(t)
	path := seedApplicationIntent(t, dir, r)
	inspection, err := InspectApply(context.Background(), dir, "fix")
	if err != nil || inspection.State != "uncertain" || inspection.SourceObservation != "matches_start" || !inspection.Recoverable {
		t.Fatalf("interrupted admission not discoverable: %+v %v", inspection, err)
	}
	if result, err := Apply(context.Background(), dir, "fix"); err == nil || result.Applied || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("unresolved application replayed: %+v %v", result, err)
	}
	data, _ := os.ReadFile(filepath.Join(r.Repository, "file.txt"))
	if string(data) != "base\n" {
		t.Fatal("blocked application changed source")
	}
	inspection, err = RecoverApply(context.Background(), dir, "fix")
	if err != nil || inspection.State != "not_applied" || inspection.Recoverable {
		t.Fatalf("exact starting source did not recover without replay: %+v %v", inspection, err)
	}
	var receipt ApplyResult
	if err := DecodeFile(path, &receipt); err != nil || receipt.Phase != "recovered" || receipt.Uncertain || receipt.Applied {
		t.Fatalf("recovered receipt: %+v %v", receipt, err)
	}
	data, _ = os.ReadFile(filepath.Join(r.Repository, "file.txt"))
	if string(data) != "base\n" {
		t.Fatal("recovery replayed a patch")
	}
}

func TestApplicationRecoveryRetainsDivergedSourceAndUnresolvedReceipt(t *testing.T) {
	dir, r := evidenceRun(t)
	path := seedApplicationIntent(t, dir, r)
	before, _ := os.ReadFile(path)
	fixtureWrite(t, filepath.Join(r.Repository, "file.txt"), []byte("partially changed\n"))
	inspection, err := RecoverApply(context.Background(), dir, "fix")
	if err == nil || inspection.State != "uncertain" || inspection.SourceObservation != "diverged" || inspection.Recoverable {
		t.Fatalf("diverged source called recovered: %+v %v", inspection, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("uncertain receipt changed despite diverged source")
	}
	data, _ := os.ReadFile(filepath.Join(r.Repository, "file.txt"))
	if string(data) != "partially changed\n" {
		t.Fatal("recovery changed unrelated source content")
	}
}

func TestApplicationRefusesDamagedHistoryBeforeSourceMutation(t *testing.T) {
	dir, r := evidenceRun(t)
	fixtureWrite(t, filepath.Join(dir, "applied-999.json"), []byte("{damaged"))
	if result, err := Apply(context.Background(), dir, "fix"); err == nil || result.Applied || !strings.Contains(err.Error(), "history needs inspection") {
		t.Fatalf("damaged history permitted application: %+v %v", result, err)
	}
	inspection, err := RecoverApply(context.Background(), dir, "fix")
	if err == nil || inspection.State != "unknown" {
		t.Fatalf("damaged history silently recovered: %+v %v", inspection, err)
	}
	data, _ := os.ReadFile(filepath.Join(r.Repository, "file.txt"))
	if string(data) != "base\n" {
		t.Fatal("damaged history led to source changes")
	}
}

func TestApplicationFinalizesItsDurableIntentInsteadOfAppendingAnotherReceipt(t *testing.T) {
	dir, r := evidenceRun(t)
	result, err := Apply(context.Background(), dir, "fix")
	if err != nil || !result.Applied || result.Uncertain || result.Phase != "finished" || result.Receipt == "" {
		t.Fatalf("apply failed: %+v %v", result, err)
	}
	receipts, err := readApplicationReceipts(r)
	if err != nil || len(receipts) != 1 || receipts[0].path != result.Receipt || receipts[0].result.Phase != "finished" || receipts[0].result.Uncertain {
		t.Fatalf("intent was not finalized atomically: %+v %v", receipts, err)
	}
	inspection, err := InspectApply(context.Background(), dir, "fix")
	if err != nil || inspection.State != "applied" || inspection.SourceObservation != "matches_final" {
		t.Fatalf("completed application not inspectable: %+v %v", inspection, err)
	}
}
