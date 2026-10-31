//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureManagedBrokerProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Stop the wrapper and isolated service together; a surviving broker
		// must never outlive the owner-controlled server command.
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
