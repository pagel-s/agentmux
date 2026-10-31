//go:build !unix

package main

import "os/exec"

func configureManagedBrokerProcess(cmd *exec.Cmd) {
	// Bubblewrap validation rejects unsupported platforms before this helper.
}
