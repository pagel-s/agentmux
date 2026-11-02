package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"agentmux/internal/capsule"
	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

func runManagedCapsule(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: amx managed capsule inspect|recover --state DIR --agent ID [--action restore|publish|discard --expect DIGEST]")
		return exitValidation
	}
	op := args[0]
	flags := flag.NewFlagSet("managed capsule "+op, flag.ContinueOnError)
	flags.SetOutput(stderr)
	state := flags.String("state", "", "private managed state directory")
	agent := flags.String("agent", "", "agent with a quarantined disk capsule")
	action := flags.String("action", "", "explicit restore, publish or discard")
	expected := flags.String("expect", "", "digest from capsule inspect")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *state == "" || !managedconfig.ValidID(*agent) || (op != "inspect" && op != "recover") {
		return managedCLIError(stdout, "capsule."+op, "", exitValidation, errors.New("select inspect/recover with --state and --agent"))
	}
	abs, err := filepath.Abs(expandUserPath(*state))
	if err != nil {
		return managedCLIError(stdout, "capsule."+op, "", exitValidation, err)
	}
	store, err := managed.Open(abs)
	if err != nil {
		return managedCLIError(stdout, "capsule."+op, "", exitRuntime, err)
	}
	defer store.Close()
	owner, err := managed.LoadCredential(managed.CredentialPath(abs, ""))
	if err != nil {
		return managedCLIError(stdout, "capsule."+op, "", exitRuntime, errors.New("owner credential unavailable"))
	}
	raw, problem := store.Call(ctx, owner.Token, managed.Request{Operation: "identity", Args: json.RawMessage(`{}`)})
	if problem != nil {
		return managedCLIError(stdout, "capsule."+op, "", exitRuntime, errors.New("owner authentication failed"))
	}
	var identity struct {
		Owner bool `json:"owner"`
	}
	if json.Unmarshal(raw, &identity) != nil || !identity.Owner {
		return managedCLIError(stdout, "capsule."+op, "", exitRuntime, errors.New("capsule recovery is owner-only"))
	}
	var definition managedconfig.Agent
	found := false
	cursor := ""
	for {
		data, _ := json.Marshal(map[string]any{"after": cursor, "limit": 200})
		raw, problem = store.Call(ctx, owner.Token, managed.Request{Operation: "agent.list", Args: data})
		if problem != nil {
			return managedCLIError(stdout, "capsule."+op, "", exitRuntime, problem)
		}
		var page struct {
			Agents []managed.AgentInfo `json:"agents"`
			Cursor string              `json:"cursor"`
		}
		if err = json.Unmarshal(raw, &page); err != nil {
			return managedCLIError(stdout, "capsule."+op, "", exitRuntime, err)
		}
		for _, a := range page.Agents {
			if a.ID == *agent {
				definition = a.Config
				found = true
				break
			}
		}
		if found || len(page.Agents) == 0 || page.Cursor == "" || page.Cursor == cursor {
			break
		}
		cursor = page.Cursor
	}
	if !found || !definition.Disk.Enabled() {
		return managedCLIError(stdout, "capsule."+op, "", exitValidation, errors.New("agent has no configured disk capsule"))
	}
	opts := capsule.Options{StateDir: abs, AgentID: *agent, Workspace: definition.Workdir, WorkspaceBytes: definition.Disk.WorkspaceBytes, HistoryBytes: definition.Disk.HistoryBytes, TempBytes: definition.Disk.TempBytes, MaxEntries: definition.Disk.MaxEntries}
	var report capsule.RecoveryReport
	if op == "inspect" {
		if *action != "" || *expected != "" {
			return managedCLIError(stdout, "capsule.inspect", "", exitValidation, errors.New("inspect accepts no recovery action"))
		}
		report, err = capsule.Inspect(ctx, opts)
	} else {
		report, err = capsule.Recover(ctx, opts, *action, *expected)
	}
	if err != nil {
		return managedCLIError(stdout, "capsule."+op, "", exitRuntime, err)
	}
	return writeManagedEnvelope(stdout, managedEnvelope{Operation: "capsule." + op, OK: true, Result: report})
}
