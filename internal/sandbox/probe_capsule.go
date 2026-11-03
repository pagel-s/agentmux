package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const probeOutputLimit = 4096
const capsuleProbeBytes = 1 << 20

// ProbeCapsule verifies that the installed Bubblewrap can create a kernel-
// capped tmpfs. A successful namespace probe alone does not establish --size
// support. This is separate from Wrap, which only constructs an argv.
func ProbeCapsule(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return errors.New("capped tmpfs requires Linux and Bubblewrap --size support")
	}
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return errors.New("capped tmpfs requires Bubblewrap with --size support")
	}
	stat, err := exec.LookPath("stat")
	if err != nil {
		return errors.New("capped tmpfs preflight requires a system stat executable")
	}
	stat, err = filepath.EvalSymlinks(stat)
	if err != nil || !coveredByRuntime(stat) {
		return errors.New("capped tmpfs preflight requires a system stat executable")
	}
	args := []string{"--die-with-parent", "--new-session", "--unshare-all", "--cap-drop", "ALL"}
	args = append(args, runtimeMounts()...)
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--size", strconv.Itoa(capsuleProbeBytes), "--tmpfs", "/run/agentmux-disk-probe", "--", stat, "-f", "-c", "%b:%S", "/run/agentmux-disk-probe")
	output, err := runSandboxProbe(ctx, bwrap, args)
	if err != nil {
		return errors.New("capped tmpfs preflight failed: install Bubblewrap with working --size support and permit its tmpfs/user namespaces; no task was claimed")
	}
	fields := strings.Split(strings.TrimSpace(string(output)), ":")
	if len(fields) != 2 {
		return errors.New("capped tmpfs preflight could not confirm its kernel capacity; no task was claimed")
	}
	blocks, blocksErr := strconv.ParseUint(fields[0], 10, 64)
	blockBytes, bytesErr := strconv.ParseUint(fields[1], 10, 64)
	if blocksErr != nil || bytesErr != nil || blocks == 0 || blockBytes == 0 || blocks > capsuleProbeBytes/blockBytes {
		return errors.New("capped tmpfs preflight did not enforce the requested kernel capacity; no task was claimed")
	}
	return nil
}

type probeOutput struct {
	mu       sync.Mutex
	data     []byte
	overflow bool
	cancel   context.CancelFunc
}

func (w *probeOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := probeOutputLimit - len(w.data)
	if len(p) > remaining {
		w.data = append(w.data, p[:remaining]...)
		w.overflow = true
		w.cancel()
	} else {
		w.data = append(w.data, p...)
	}
	return len(p), nil
}

// No probe may inherit provider credentials, produce unbounded diagnostics, or
// hold startup indefinitely through a child or an inherited output pipe.
func runSandboxProbe(ctx context.Context, binary string, args []string) ([]byte, error) {
	return runSandboxProbeFiles(ctx, binary, args, nil)
}

func runSandboxProbeFiles(ctx context.Context, binary string, args []string, files []*os.File) ([]byte, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output := &probeOutput{cancel: cancel}
	cmd := exec.CommandContext(probeCtx, binary, args...)
	cmd.ExtraFiles = append([]*os.File(nil), files...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	cmd.Stdout, cmd.Stderr = output, output
	cmd.WaitDelay = 100 * time.Millisecond
	err := cmd.Run()
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.overflow {
		return output.data, fmt.Errorf("sandbox probe output exceeds %d bytes", probeOutputLimit)
	}
	if err == nil && probeCtx.Err() != nil {
		err = probeCtx.Err()
	}
	return output.data, err
}
