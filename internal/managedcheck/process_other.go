//go:build !unix

package managedcheck

import "os/exec"

func configureMetadataProcess(*exec.Cmd) {}
