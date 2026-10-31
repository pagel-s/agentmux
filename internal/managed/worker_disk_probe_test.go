//go:build linux

package managed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestUnsupportedCappedTmpfsDoesNotClaimTask(t *testing.T) {
	root, err := os.MkdirTemp("", "amx-disk-probe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	tools := filepath.Join(root, "tools")
	if err := os.Mkdir(tools, 0700); err != nil {
		t.Fatal(err)
	}
	// Simulate an installed Bubblewrap that supports ordinary namespaces but
	// rejects capped tmpfs, as older production installations really do.
	script := "#!/bin/sh\nfor arg do\nif [ \"$arg\" = --size ]; then printf 'bwrap: Unknown option --size' >&2; exit 1; fi\ndone\nexit 0\n"
	if err := os.WriteFile(filepath.Join(tools, "bwrap"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+":/usr/bin:/bin")
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "disk-probe", Agents: map[string]managedconfig.Agent{
		"worker": {Harness: "command", Command: []string{"/bin/cat"}, Sandbox: "bubblewrap", Network: "none", Disk: managedconfig.Disk{WorkspaceBytes: 1 << 20, HistoryBytes: 1 << 20, TempBytes: 1 << 20, MaxEntries: 32}},
	}}
	s, err := Init(filepath.Join(root, "s"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task := taskIn(t, call(t, s, "", "instruct", map[string]any{"to": []string{"worker"}, "text": "Must remain unclaimed if the disk boundary is unavailable."}))
	err = RunWorker(context.Background(), WorkerOptions{StateDir: s.dir, AgentID: "worker", Once: true})
	if err == nil || !strings.Contains(err.Error(), "--size") || !strings.Contains(err.Error(), "no task was claimed") {
		t.Fatalf("worker did not report the unavailable disk boundary: %v", err)
	}
	current, err := loadTask(context.Background(), s.db, task.ID)
	if err != nil || current.State != "proposed" || current.Delivery != "pending" || current.Version != task.Version || current.LeaseToken != "" {
		t.Fatalf("unsupported disk isolation claimed or changed work: %+v, %v", current, err)
	}
}
