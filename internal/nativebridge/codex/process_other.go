//go:build !unix

package codex

import "os/exec"

func configureServerProcess(cmd *exec.Cmd) {}
