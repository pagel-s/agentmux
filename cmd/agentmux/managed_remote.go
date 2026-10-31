package main

import (
	"agentmux/internal/managed"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
)

func runManagedRemote(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("managed remote", flag.ContinueOnError)
	f.SetOutput(stderr)
	state := f.String("state", "", "local private broker state")
	agent := f.String("agent", "", "configured worker ID")
	host := f.String("host", "", "SSH alias or host")
	directory := f.String("remote-dir", "", "new absolute private deployment directory")
	layoutFile := f.String("layout", "", "optional explicit remote paths/command JSON")
	binary := f.String("binary", "", "local Linux amd64 Agentmux binary")
	once := f.Bool("once", false, "stop after one task")
	resume := f.Bool("resume", false, "reuse an identical verified private deployment")
	if err := f.Parse(args); err != nil {
		return exitValidation
	}
	if f.NArg() != 0 || *state == "" || *agent == "" || *host == "" || *directory == "" {
		return managedCLIError(stdout, "remote", "", exitValidation, errors.New("--state --agent --host --remote-dir are required"))
	}
	var layout managed.RemoteLayout
	if *layoutFile != "" {
		info, err := os.Lstat(*layoutFile)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return managedCLIError(stdout, "remote", "", exitValidation, errors.New("layout must be a regular JSON file under 1 MiB"))
		}
		data, err := os.ReadFile(*layoutFile)
		if err != nil {
			return managedCLIError(stdout, "remote", "", exitValidation, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var extra any
		if err := decoder.Decode(&layout); err != nil {
			return managedCLIError(stdout, "remote", "", exitValidation, err)
		}
		if decoder.Decode(&extra) != io.EOF {
			return managedCLIError(stdout, "remote", "", exitValidation, errors.New("layout must contain exactly one JSON object"))
		}
	}
	if err := managed.RunRemote(ctx, managed.RemoteOptions{StateDir: expandUserPath(*state), AgentID: *agent, Host: *host, Directory: *directory, Layout: layout, Executable: *binary, Once: *once, Resume: *resume, Stdout: stdout, Stderr: stderr}); err != nil {
		return managedCLIError(stdout, "remote", "", exitRuntime, err)
	}
	return exitSuccess
}
