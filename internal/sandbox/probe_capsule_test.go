//go:build linux

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProbeCapsuleRejectsUnsupportedSizeWithoutChangingWrap(t *testing.T) {
	f := setup(t)
	if err := os.WriteFile(filepath.Join(f.root, "tools", "bwrap"), []byte("#!/bin/sh\nfor arg do\nif [ \"$arg\" = --size ]; then printf 'bwrap: Unknown option --size' >&2; exit 1; fi\ndone\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Probe(context.Background()); err != nil {
		t.Fatalf("ordinary namespace support should not require --size: %v", err)
	}
	if _, err := Wrap(f.spec, f.opts); err != nil {
		t.Fatalf("argv construction must remain separate from capability probes: %v", err)
	}
	if err := ProbeCapsule(context.Background()); err == nil || !strings.Contains(err.Error(), "--size") || !strings.Contains(err.Error(), "no task was claimed") {
		t.Fatalf("unsupported size was not diagnosed safely: %v", err)
	}
}

func TestProbeCapsuleRequiresConfirmedKernelCapacity(t *testing.T) {
	f := setup(t)
	for _, value := range []struct {
		output string
		ok     bool
	}{{"256:4096", true}, {"257:4096", false}, {"0:4096", false}, {"256:0", false}, {"18446744073709551615:4096", false}, {"unexpected", false}} {
		t.Run(value.output, func(t *testing.T) {
			script := "#!/bin/sh\nprintf '%s' '" + value.output + "'\n"
			if err := os.WriteFile(filepath.Join(f.root, "tools", "bwrap"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err := ProbeCapsule(context.Background()); (err == nil) != value.ok {
				t.Fatalf("capacity %q: %v", value.output, err)
			}
		})
	}
}

func TestSandboxProbeBoundsDiagnosticsAndCallerDeadline(t *testing.T) {
	f := setup(t)
	binary := filepath.Join(f.root, "tools", "bwrap")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nwhile :; do printf 'unbounded probe output'; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	output, err := runSandboxProbe(context.Background(), binary, nil)
	if err == nil || len(output) > probeOutputLimit || time.Since(started) > time.Second {
		t.Fatalf("unbounded diagnostics: bytes=%d duration=%v err=%v", len(output), time.Since(started), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started = time.Now()
	if _, err := runSandboxProbe(ctx, "/bin/sleep", []string{"30"}); err == nil || time.Since(started) > time.Second {
		t.Fatalf("probe ignored caller deadline: duration=%v err=%v", time.Since(started), err)
	}
}

func TestRealProbeCapsuleChecksKernelMountCapacity(t *testing.T) {
	if os.Getenv("AGENTMUX_SANDBOX_INTEGRATION") != "1" {
		t.Skip("set AGENTMUX_SANDBOX_INTEGRATION=1 for actual capped tmpfs probe")
	}
	if err := ProbeCapsule(context.Background()); err != nil {
		t.Fatal(err)
	}
}
