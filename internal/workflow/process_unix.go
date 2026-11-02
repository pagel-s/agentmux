//go:build unix

package workflow

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Git may launch configured clean/smudge filters. A cancelled snapshot must
// terminate these descendants as well as the direct Git child.
func configureGitProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
