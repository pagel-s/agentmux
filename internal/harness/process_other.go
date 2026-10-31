//go:build !unix

package harness

import "os/exec"

func configureProcess(cmd *exec.Cmd) {
	// exec.CommandContext cancels the direct process on these platforms. A
	// sandbox backend must own descendant cleanup where process groups are
	// unavailable.
}
