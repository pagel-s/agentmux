package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"agentmux/internal/managed"
)

func runWorkDelivery(args []string, out, diagnostics io.Writer) int {
	if workHelpRequested(args) {
		fmt.Fprintln(out, `Usage: amx delivery ID [manual|review|auto] [--session NAME | --state DIR]
       [--approve CANDIDATE] [--max-turns N] [--max-hops N] [--cooldown N]
       [--notes=true|false] [--pings=true|false] [--boards=true|false]
       [--memory-updates=true|false] [--results=true|false] [--json]

Show or control delivery into one native terminal. Default: manual.
Review requires approval of each exact queued item; auto delivers eligible turns.
Sender command permissions are configured separately in Connections.
Manual pauses future deliveries and requests cancellation of an active peer turn
where supported. It never types into or interrupts unrelated human input.`)
		return exitSuccess
	}
	fail := func(err error) int { fmt.Fprintln(diagnostics, "error:", err); return exitValidation }
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fail(errors.New("delivery requires a stable agent ID; see delivery --help"))
	}
	id := args[0]
	args = args[1:]
	mode := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode = args[0]
		args = args[1:]
		if mode != "manual" && mode != "review" && mode != "auto" {
			return fail(errors.New("choose manual, review or auto"))
		}
	}
	flags := flag.NewFlagSet("delivery", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	session := flags.String("session", "work", "workspace name")
	state := flags.String("state", "", "workspace state")
	approve := flags.String("approve", "", "exact pending candidate ID")
	maxTurns := flags.Int("max-turns", 20, "cumulative native turn limit")
	maxHops := flags.Int("max-hops", 4, "causal hop limit")
	cooldown := flags.Int("cooldown", 0, "seconds between native turns")
	notes := flags.Bool("notes", false, "deliver direct notes as observations")
	pings := flags.Bool("pings", false, "deliver pings as observations")
	boards := flags.Bool("boards", false, "deliver readable board updates")
	memory := flags.Bool("memory-updates", false, "deliver readable shared memory updates")
	results := flags.Bool("results", true, "return results of this agent's instructions")
	asJSON := flags.Bool("json", false, "print the complete status as JSON")
	if err := flags.Parse(args); err != nil {
		return exitValidation
	}
	if flags.NArg() != 0 {
		return fail(errors.New("unexpected delivery arguments"))
	}
	selectors := []string{}
	changes := map[string]any{}
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "session":
			selectors = append(selectors, "--session", *session)
		case "state":
			selectors = append(selectors, "--state", *state)
		case "max-turns":
			changes["max_turns"] = *maxTurns
		case "max-hops":
			changes["max_hops"] = *maxHops
		case "cooldown":
			changes["cooldown_seconds"] = *cooldown
		case "notes":
			changes["notes"] = *notes
		case "pings":
			changes["pings"] = *pings
		case "boards":
			changes["boards"] = *boards
		case "memory-updates":
			changes["memory_updates"] = *memory
		case "results":
			changes["results"] = *results
		}
	})
	options, err := parseWorkOptions(selectors)
	if err != nil {
		return fail(err)
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(options.state, ""))
	if err != nil {
		return fail(errors.New("workspace not found; start a workspace first"))
	}
	client := managed.NewClient(credential)
	ctx := context.Background()
	load := func() (managed.NativeStatus, error) {
		raw, err := workCall(ctx, client, "native.status", map[string]any{"agent": id})
		var v struct {
			Status managed.NativeStatus `json:"status"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &v)
		}
		return v.Status, err
	}
	status, err := load()
	if err != nil {
		return fail(err)
	}
	if mode != "" {
		changes["mode"] = mode
	}
	if len(changes) > 0 {
		changes["agent"] = id
		changes["version"] = status.Policy.Version
		if _, err = workCall(ctx, client, "native.configure", changes); err != nil {
			return fail(err)
		}
		status, err = load()
		if err != nil {
			return fail(err)
		}
	}
	if *approve != "" {
		if _, err = workCall(ctx, client, "native.approve", map[string]any{"agent": id, "version": status.Policy.Version, "candidate": *approve}); err != nil {
			return fail(err)
		}
		if !*asJSON {
			fmt.Fprintln(out, "Approved", consoleContent(*approve, 150)+"; native delivery still waits for receiver readiness.")
		}
		status, err = load()
		if err != nil {
			return fail(err)
		}
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(status); err != nil {
			fmt.Fprintln(diagnostics, "error:", err)
			return exitRuntime
		}
		return exitSuccess
	}
	fmt.Fprintf(out, "%s · %s · %s\n%s\n", consoleContent(id, 100), status.Mode, status.State, consoleContent(status.Reason, 700))
	pending := fmt.Sprint(len(status.Pending))
	if !nativePendingKnown(status) {
		pending = "unknown"
		fmt.Fprintln(out, consoleContent(status.Coverage, 700))
	}
	fmt.Fprintf(out, "Native turns %d/%d · %s pending on this page · cooldown %ds · max hops %d\n", status.Turns, status.Policy.MaxTurns, pending, status.Policy.CooldownSeconds, status.Policy.MaxHops)
	if status.PendingMore {
		fmt.Fprintln(out, "More pending items follow as this queue advances; message bodies are never truncated for approval.")
	}
	if status.Session != "" {
		fmt.Fprintf(out, "Adapter %s · session %s · preserves draft: %t · cancellation: %t\n", status.Adapter, consoleContent(status.Session, 160), status.Capabilities.PreservesDraft, status.Capabilities.Cancellation)
	}
	if status.Capabilities.CompletionEvidence != "" {
		fmt.Fprintln(out, "Completion evidence:", status.Capabilities.CompletionEvidence)
	}
	if status.Active != nil {
		fmt.Fprintf(out, "Active %s · %s\n", consoleContent(status.Active.ID, 128), status.Active.Phase)
	}
	for i, d := range status.Pending {
		if i >= 8 {
			fmt.Fprintln(out, "Use --json to inspect the remaining pending items.")
			break
		}
		fmt.Fprintf(out, "  %s · %s from %s · %s\n", consoleContent(d.ID, 128), d.Kind, consoleContent(d.From, 100), consoleContent(d.Text, 100))
	}
	if len(status.Pending) > 0 {
		fmt.Fprintln(out, "Previews only; inspect exact full bodies with --json before approving a candidate.")
	}
	return exitSuccess
}

func nativePendingKnown(status managed.NativeStatus) bool {
	// Older controllers did not advertise coverage; their returned queue keeps
	// its legacy meaning. Current unavailable/summary responses are explicit.
	return status.PendingKnown || status.Coverage == ""
}

func nativePendingCountText(status managed.NativeStatus) string {
	if !nativePendingKnown(status) {
		return "unknown"
	}
	return fmt.Sprint(max(status.PendingCount, len(status.Pending)))
}
