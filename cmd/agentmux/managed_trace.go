package main

import (
	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func runManagedTrace(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return managedCLIError(stdout, "trace", "", exitValidation, errors.New("trace list|verify|show required"))
	}
	operation := args[0]
	f := flag.NewFlagSet("trace", flag.ContinueOnError)
	f.SetOutput(stderr)
	state := f.String("state", "", "private local broker state")
	agent := f.String("agent", "", "agent ID")
	id := f.String("id", "", "trace ID")
	anchor := f.String("mac", "", "optional task-completion anchor")
	if err := f.Parse(args[1:]); err != nil {
		return exitValidation
	}
	if f.NArg() != 0 || *state == "" || !managedconfig.ValidID(*agent) {
		return managedCLIError(stdout, "trace", "", exitValidation, errors.New("--state and valid --agent are required"))
	}
	// This is an owner filesystem operation, never an agent-facing broker API.
	owner, err := managed.LoadCredential(managed.CredentialPath(*state, ""))
	if err != nil {
		return managedCLIError(stdout, "trace", "", exitRuntime, err)
	}
	store, err := managed.Open(*state)
	if err != nil {
		return managedCLIError(stdout, "trace", "", exitRuntime, err)
	}
	defer store.Close()
	identityRaw, problem := store.Call(ctx, owner.Token, managed.Request{Operation: "identity", Args: json.RawMessage(`{}`)})
	var identity struct {
		Owner bool `json:"owner"`
	}
	if problem != nil || json.Unmarshal(identityRaw, &identity) != nil || !identity.Owner {
		return managedCLIError(stdout, "trace", "", exitValidation, errors.New("owner credential required"))
	}
	if operation == "list" {
		files, err := os.ReadDir(filepath.Join(*state, "traces", *agent))
		if err != nil && !os.IsNotExist(err) {
			return managedCLIError(stdout, "trace", "", exitRuntime, err)
		}
		type item struct {
			ID      string               `json:"id"`
			Summary managed.TraceSummary `json:"summary"`
			Valid   bool                 `json:"valid"`
		}
		out := []item{}
		for _, file := range files {
			if !file.IsDir() && strings.HasSuffix(file.Name(), ".jsonl") {
				name := strings.TrimSuffix(file.Name(), ".jsonl")
				sum, err := managed.VerifyTrace(*state, *agent, name, "")
				out = append(out, item{name, sum, err == nil})
			}
		}
		return writeManagedEnvelope(stdout, managedEnvelope{Operation: "trace.list", OK: true, Result: out})
	}
	if operation != "verify" && operation != "show" {
		return managedCLIError(stdout, "trace", "", exitValidation, errors.New("trace list|verify|show required"))
	}
	sum, err := managed.VerifyTrace(*state, *agent, *id, *anchor)
	if err != nil {
		return managedCLIError(stdout, "trace.verify", "", exitRuntime, err)
	}
	if operation == "show" {
		file, err := os.Open(filepath.Join(*state, "traces", *agent, *id+".jsonl"))
		if err != nil {
			return managedCLIError(stdout, "trace.show", "", exitRuntime, err)
		}
		defer file.Close()
		if _, err := io.Copy(stdout, file); err != nil {
			fmt.Fprintln(stderr, err)
			return exitRuntime
		}
		return exitSuccess
	}
	return writeManagedEnvelope(stdout, managedEnvelope{Operation: "trace.verify", OK: true, Result: sum})
}
