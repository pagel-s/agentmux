//go:build !unix

package harness

import (
	"errors"
	"os/exec"
)

func configureProcess(cmd *exec.Cmd) {
	// exec.CommandContext cancels the direct process on these platforms. A
	// sandbox backend must own descendant cleanup where process groups are
	// unavailable.
}

func supportsDescendantReaping() bool { return false }

func reapDescendants(cmd *exec.Cmd) error {
	return errors.New("bounded turn descendant cleanup is unsupported on this platform")
}
