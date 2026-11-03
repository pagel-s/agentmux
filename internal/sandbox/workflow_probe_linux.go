//go:build linux

package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func probeWorkflowCredentialFD(ctx context.Context) error {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return errors.New("workflow isolation requires Bubblewrap")
	}
	cat, err := exec.LookPath("cat")
	if err != nil {
		return errors.New("anonymous credential probe requires system cat")
	}
	cat, err = filepath.EvalSymlinks(cat)
	if err != nil || !coveredByRuntime(cat) {
		return errors.New("anonymous credential probe requires system cat")
	}
	fd, err := unix.MemfdCreate("agentmux-auth-probe", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return errors.New("anonymous native authentication requires Linux sealed memfd")
	}
	f := os.NewFile(uintptr(fd), "agentmux-auth-probe")
	defer f.Close()
	if _, err = f.WriteString("workflow-auth-probe"); err != nil {
		return err
	}
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	if _, err = unix.FcntlInt(f.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		return errors.New("anonymous native authentication could not be sealed")
	}
	args := []string{"--die-with-parent", "--new-session", "--unshare-all", "--cap-drop", "ALL"}
	args = append(args, runtimeMounts()...)
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--ro-bind-data", "3", "/run/agentmux-auth-probe", "--", cat, "/run/agentmux-auth-probe")
	out, err := runSandboxProbeFiles(ctx, bwrap, args, []*os.File{f})
	if err != nil || strings.TrimSpace(string(out)) != "workflow-auth-probe" {
		return errors.New("anonymous credential namespace probe failed; install Bubblewrap with working --ro-bind-data support, no provider was started")
	}
	return nil
}
