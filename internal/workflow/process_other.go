//go:build !unix

package workflow

import "os/exec"

// Non-Unix platforms retain exec.CommandContext's direct-child cancellation.
func configureGitProcess(cmd *exec.Cmd) {}
