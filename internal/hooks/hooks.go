package hooks

import (
	"fmt"
	"os"
	"os/exec"
)

func RunPre(command string, verbose bool) error {
	if command == "" {
		return nil
	}
	return run(command, verbose)
}

func RunPost(command string, verbose bool) error {
	if command == "" {
		return nil
	}
	return run(command, verbose)
}

func run(command string, verbose bool) error {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "hook: %s -lc %s\n", shell, command)
	}

	cmd := exec.Command(shell, "-lc", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
