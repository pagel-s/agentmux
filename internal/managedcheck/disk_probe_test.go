//go:build linux

package managedcheck

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmux/internal/managedconfig"
)

func TestDoctorChecksCappedTmpfsForAgentsAndEnabledTemplates(t *testing.T) {
	tools := t.TempDir()
	t.Setenv("PATH", tools+":/usr/bin:/bin")
	agent := managedconfig.Agent{Harness: "command", Command: []string{"/bin/cat"}, Sandbox: "bubblewrap", Network: "none", Disk: managedconfig.Disk{WorkspaceBytes: 1 << 20, HistoryBytes: 1 << 20, TempBytes: 1 << 20, MaxEntries: 32}}
	cfg := managedconfig.Config{Version: 2, Mode: "managed", Session: "disk-doctor", Agents: map[string]managedconfig.Agent{"worker": agent}, Spawn: managedconfig.Spawn{Enabled: true, Templates: map[string]managedconfig.Agent{"helper": agent}}}
	for _, supported := range []bool{false, true} {
		body := "printf 'Unknown option --size: private diagnostic' >&2; exit 1"
		want := StatusError
		if supported {
			body = "printf '256:4096'; exit 0"
			want = StatusOK
		}
		script := "#!/bin/sh\nfor arg do\nif [ \"$arg\" = --size ]; then " + body + "; fi\ndone\nexit 0\n"
		if err := os.WriteFile(filepath.Join(tools, "bwrap"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		report := Check(context.Background(), cfg, Options{Executable: "/bin/true"})
		for _, id := range []string{"worker", "template:helper"} {
			item := findCheck(report, id, "sandbox.disk")
			if item.Status != want || !strings.Contains(item.Message, "--size") || strings.Contains(item.Message, "private diagnostic") {
				t.Fatalf("supported=%t disk check for %s: %+v", supported, id, item)
			}
		}
		if report.OK != supported {
			t.Fatalf("supported=%t doctor result: %+v", supported, report)
		}
	}
}
