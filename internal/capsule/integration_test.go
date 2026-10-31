package capsule

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmux/internal/harness"
)

// This exercises real kernel mounts and the trusted runner, not a fake quota.
// It is opt-in because enclosing containers may deny user namespaces.
func TestRealCapsuleENOSPCAndHistoryCheckpoint(t *testing.T) {
	binary := os.Getenv("AGENTMUX_INTEGRATION_BINARY")
	if binary == "" {
		t.Skip("set AGENTMUX_INTEGRATION_BINARY to a freshly built agentmux binary")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, spec, opts := fixture(t, ctx)
	c.opts.Executable = binary
	const script = `import errno, os
def capped(directory):
    path = os.path.join(directory, 'overflow')
    try:
        with open(path, 'wb', buffering=0) as f:
            while f.tell() < 3 * 1024 * 1024:
                f.write(b'x' * 65536)
        raise AssertionError('tmpfs quota did not enforce ENOSPC')
    except OSError as error:
        assert error.errno == errno.ENOSPC, error.errno
    finally:
        if os.path.exists(path): os.unlink(path)
for directory in ['/workspace', '/home/agent', '/tmp']: capped(directory)
try:
    open('/run/agentmux/input/0/probe.py', 'ab').write(b'bad')
    raise AssertionError('input snapshot was writable')
except OSError as error:
    assert error.errno == errno.EROFS, error.errno
with open('/workspace/proof.txt', 'w') as f: f.write('three tmpfs ENOSPC and readonly seed verified')
os.makedirs('/home/agent/.codex/sessions', exist_ok=True)
with open('/home/agent/.codex/sessions/turn.jsonl', 'w') as f: f.write('{"native_history":"checkpointed"}\n')
print('CAPSULE_OK')
`
	if err = os.WriteFile(filepath.Join(c.opts.Workspace, "probe.py"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(c.opts.StateDir, "sessions", "agent", "g-test")
	os.MkdirAll(session, 0700)
	spec.Binary = "/usr/bin/python3"
	spec.Args = []string{"/workspace/probe.py"}
	spec.SessionDirs = []harness.SessionDir{{Source: session, Target: "/home/agent/.codex/sessions"}}
	wrapped, err := c.Wrap(spec, opts)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, wrapped.Binary, wrapped.Args...)
	cmd.Env = wrapped.Env
	cmd.Dir = wrapped.Workdir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real capsule process failed: %v: %s", err, output)
	}
	if !strings.Contains(string(output), "CAPSULE_OK") {
		t.Fatal("native probe did not execute")
	}
	if err = c.Finish(); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(c.opts.Workspace, "proof.txt")); err != nil || !strings.Contains(string(b), "ENOSPC") {
		t.Fatal("workspace was not checkpointed")
	}
	if b, err := os.ReadFile(filepath.Join(session, "turn.jsonl")); err != nil || !strings.Contains(string(b), "checkpointed") {
		t.Fatal("native history unavailable before verification")
	}
}
