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
	usage := func() {
		fmt.Fprintln(stdout, `Usage:
  amx managed trace list --state DIR --agent ID
  amx managed trace verify|show --state DIR --agent ID --id TRACE_ID [--mac TASK_MAC]
  amx managed trace export --state DIR --agent ID --id TRACE_ID
      [--mac TASK_MAC] --output NEW_PRIVATE_FILE
  amx managed trace verify-bundle --input FILE --trust OWNER_PUBLIC_KEY_SHA256

Local list/verify/show/export require owner state access. Export signs a locally
verified trace without exporting credentials or secret keys. Offline verify-bundle
requires an independently obtained owner-key fingerprint; an embedded key alone
is not trusted. This authenticates an owner exporter, not a model provider.`)
	}
	if len(args) > 0 && (helpArgument(args[0]) || args[0] == "help") {
		usage()
		return exitSuccess
	}
	if len(args) == 0 {
		return managedCLIError(stdout, "trace", "", exitValidation, errors.New("trace list|verify|show|export|verify-bundle required"))
	}
	operation := args[0]
	f := flag.NewFlagSet("trace", flag.ContinueOnError)
	f.SetOutput(stderr)
	f.Usage = usage
	state := f.String("state", "", "private local broker state")
	agent := f.String("agent", "", "agent ID")
	id := f.String("id", "", "trace ID")
	anchor := f.String("mac", "", "optional task-completion anchor")
	output := f.String("output", "", "new private signed trace bundle")
	input := f.String("input", "", "portable signed trace bundle")
	trust := f.String("trust", "", "independently obtained owner signing-key SHA256 fingerprint")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitSuccess
		}
		return exitValidation
	}
	if operation == "verify-bundle" {
		if f.NArg() != 0 || *input == "" || *trust == "" || *state != "" || *agent != "" || *id != "" || *anchor != "" || *output != "" {
			return managedCLIError(stdout, "trace.verify-bundle", "", exitValidation, errors.New("verify-bundle requires --input and --trust only; it never reads local state or secrets"))
		}
		verified, err := managed.VerifyTraceBundle(expandUserPath(*input), *trust)
		if err != nil {
			return managedCLIError(stdout, "trace.verify-bundle", "", exitRuntime, err)
		}
		return writeManagedEnvelope(stdout, managedEnvelope{Operation: "trace.verify-bundle", OK: true, Result: verified})
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
	if operation == "export" {
		if *output == "" {
			return managedCLIError(stdout, "trace.export", "", exitValidation, errors.New("trace export requires --output for a new private bundle"))
		}
		verified, err := managed.ExportTraceBundle(*state, *agent, *id, *anchor, expandUserPath(*output))
		if err != nil {
			return managedCLIError(stdout, "trace.export", "", exitRuntime, err)
		}
		return writeManagedEnvelope(stdout, managedEnvelope{Operation: "trace.export", OK: true, Result: verified})
	}
	if operation != "verify" && operation != "show" {
		return managedCLIError(stdout, "trace", "", exitValidation, errors.New("trace list|verify|show|export|verify-bundle required"))
	}
	if operation == "show" {
		if _, err := managed.WriteVerifiedTrace(*state, *agent, *id, *anchor, stdout); err != nil {
			return managedCLIError(stdout, "trace.show", "", exitRuntime, err)
		}
		return exitSuccess
	}
	sum, err := managed.VerifyTrace(*state, *agent, *id, *anchor)
	if err != nil {
		return managedCLIError(stdout, "trace.verify", "", exitRuntime, err)
	}
	return writeManagedEnvelope(stdout, managedEnvelope{Operation: "trace.verify", OK: true, Result: sum})
}
