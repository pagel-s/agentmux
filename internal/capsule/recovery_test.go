package capsule

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentmux/internal/harness"
)

func quarantined(t *testing.T, history bool) (*Capsule, string) {
	t.Helper()
	c, spec, opts := fixture(t, context.Background())
	os.WriteFile(filepath.Join(c.opts.Workspace, "file"), []byte("before"), 0600)
	if history {
		session := filepath.Join(c.opts.StateDir, "sessions", "agent", "g-current")
		os.MkdirAll(session, 0700)
		os.WriteFile(filepath.Join(session, "turn"), []byte("old history"), 0600)
		spec.SessionDirs = []harness.SessionDir{{Source: session, Target: "/home/agent/.codex/sessions"}}
	}
	if _, err := c.Wrap(spec, opts); err != nil {
		t.Fatal(err)
	}
	output := privateDir(t)
	os.WriteFile(filepath.Join(output, "file"), []byte("after"), 0600)
	volumes := []volume{{Name: "workspace", Source: output, Bytes: c.opts.WorkspaceBytes}}
	if history {
		newHistory := privateDir(t)
		os.WriteFile(filepath.Join(newHistory, "turn"), []byte("new history"), 0600)
		volumes = append(volumes, volume{Name: "history/0", Source: newHistory, Bytes: c.opts.HistoryBytes})
	}
	if err := sendCheckpoint(t, c, volumes); err != nil {
		t.Fatal(err)
	}
	c.Close()
	return c, output
}
func actions(report RecoveryReport, action string) bool {
	for _, value := range report.Actions {
		if value == action {
			return true
		}
	}
	return false
}
func writeJournal(t *testing.T, c *Capsule) {
	t.Helper()
	var receipt map[string]string
	data, _ := os.ReadFile(filepath.Join(c.dir, "received.json"))
	json.Unmarshal(data, &receipt)
	j := struct {
		State   string              `json:"state"`
		Volumes []map[string]string `json:"volumes"`
	}{State: "publishing"}
	for _, v := range c.volumes {
		j.Volumes = append(j.Volumes, map[string]string{"name": v.Name, "source": v.Source, "before_sha256": c.baseline[v.Source], "after_sha256": receipt[v.Name]})
	}
	data, _ = json.Marshal(j)
	if err := writeDurable(filepath.Join(c.dir, "publish.json"), data); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryRequiresStoppedWorkerAndController(t *testing.T) {
	c, spec, opts := fixture(t, context.Background())
	if _, err := c.Wrap(spec, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(context.Background(), c.opts); err == nil {
		t.Fatal("active controller inspection allowed")
	}
	c.Close()
	lock, err := takeLock(filepath.Join(c.opts.StateDir, "worker-agent.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err = Inspect(context.Background(), c.opts); err == nil {
		t.Fatal("active worker recovery allowed")
	}
}

func TestRecoveryPublishesAcceptedCheckpointWithCAS(t *testing.T) {
	c, _ := quarantined(t, false)
	report, err := Inspect(context.Background(), c.opts)
	if err != nil {
		t.Fatal(err)
	}
	if !actions(report, "publish") || !actions(report, "discard") || !validDigest(report.Digest) {
		t.Fatalf("bad prepared recovery %+v", report)
	}
	if _, err = Recover(context.Background(), c.opts, "publish", report.Digest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(c.opts.Workspace, "file")); string(b) != "after" {
		t.Fatal("accepted checkpoint not published")
	}
	if _, err = os.Stat(c.dir); !os.IsNotExist(err) {
		t.Fatal("completed recovery left staging")
	}
}

func TestRecoveryCASPreservesEditsAndUnknownBackups(t *testing.T) {
	c, _ := quarantined(t, false)
	report, err := Inspect(context.Background(), c.opts)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(c.opts.Workspace, "file"), []byte("owner edit"), 0600)
	if _, err = Recover(context.Background(), c.opts, "publish", report.Digest); err == nil {
		t.Fatal("stale inspect digest was accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(c.opts.Workspace, "file")); string(b) != "owner edit" {
		t.Fatal("owner edit overwritten")
	}
	os.Mkdir(filepath.Join(c.dir, "previous-1"), 0700)
	os.WriteFile(filepath.Join(c.dir, "previous-1", "unique"), []byte("preserve"), 0600)
	if _, err = Inspect(context.Background(), c.opts); err == nil {
		t.Fatal("unrecognized previous snapshot can be deleted")
	}
}

func TestRecoveryNeverDiscardsOnlyOldSnapshotAfterPartialPublish(t *testing.T) {
	for _, action := range []string{"restore", "publish"} {
		t.Run(action, func(t *testing.T) {
			c, _ := quarantined(t, false)
			writeJournal(t, c)
			if err := os.Rename(c.opts.Workspace, filepath.Join(c.dir, "previous-0")); err != nil {
				t.Fatal(err)
			}
			report, err := Inspect(context.Background(), c.opts)
			if err != nil {
				t.Fatal(err)
			}
			if actions(report, "discard") || !actions(report, "restore") || !actions(report, "publish") {
				t.Fatalf("unsafe actions %+v", report)
			}
			if _, err = Recover(context.Background(), c.opts, "discard", report.Digest); err == nil {
				t.Fatal("discard deleted only previous workspace")
			}
			if _, err = Recover(context.Background(), c.opts, action, report.Digest); err != nil {
				t.Fatal(err)
			}
			want := "before"
			if action == "publish" {
				want = "after"
			}
			if b, _ := os.ReadFile(filepath.Join(c.opts.Workspace, "file")); string(b) != want {
				t.Fatal("wrong snapshot selected")
			}
		})
	}
}

func TestRecoveryRestoresBothVolumesAfterFirstPublished(t *testing.T) {
	c, _ := quarantined(t, true)
	writeJournal(t, c)
	if err := os.Rename(c.opts.Workspace, filepath.Join(c.dir, "previous-0")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(c.dir, "output", "workspace"), c.opts.Workspace); err != nil {
		t.Fatal(err)
	}
	report, err := Inspect(context.Background(), c.opts)
	if err != nil {
		t.Fatal(err)
	}
	if !actions(report, "restore") || actions(report, "discard") {
		t.Fatalf("unsafe mixed-volume recovery %+v", report)
	}
	if _, err = Recover(context.Background(), c.opts, "restore", report.Digest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(c.opts.Workspace, "file")); string(b) != "before" {
		t.Fatal("workspace not restored")
	}
	if b, _ := os.ReadFile(filepath.Join(c.volumes[1].Source, "turn")); string(b) != "old history" {
		t.Fatal("native history not restored")
	}
}

func TestRecoveryDiscardsIncompleteCheckpointWithoutTouchingSource(t *testing.T) {
	c, spec, opts := fixture(t, context.Background())
	os.WriteFile(filepath.Join(c.opts.Workspace, "old"), []byte("retain"), 0600)
	if _, err := c.Wrap(spec, opts); err != nil {
		t.Fatal(err)
	}
	c.Close()
	report, err := Inspect(context.Background(), c.opts)
	if err != nil {
		t.Fatal(err)
	}
	if !actions(report, "discard") || actions(report, "publish") {
		t.Fatal("incomplete output offered publication")
	}
	if _, err = Recover(context.Background(), c.opts, "discard", report.Digest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(c.opts.Workspace, "old")); string(b) != "retain" {
		t.Fatal("discard deleted existing workspace")
	}
}
