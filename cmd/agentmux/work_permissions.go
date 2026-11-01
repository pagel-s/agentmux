package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
)

// Permission edits always use owner authentication. FROM describes the edge,
// never the identity whose credential should authorize the change.
func runWorkPermission(operation string, args []string, out, diagnostics io.Writer) int {
	fail := func(err error) int { fmt.Fprintln(diagnostics, "error:", err); return exitValidation }
	if operation != "allow" && operation != "deny" {
		return fail(errors.New("choose allow or deny"))
	}
	if workHelpRequested(args) {
		fmt.Fprintln(out, `Usage: amx allow FROM TO commands|messages|pings [-s WORKSPACE | --state DIR]
       amx deny  FROM TO commands|messages|pings [-s WORKSPACE | --state DIR]

Set one directional owner permission. Use stable agent IDs from amx ls.
Exact action names instruct, message.send and ping.send also work.
The reverse direction and shared board/memory permissions stay independent.
Native delivery is separate: amx mode TO review|auto enables eligible turns.
Deny applies to future operations; cancel accepted tasks separately if needed.`)
		return exitSuccess
	}
	// The shared parser accepts options used by other workspace commands. This
	// command accepts selectors only, so unrelated flags cannot be silently ignored.
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name, _, inline := strings.Cut(arg, "=")
		if name != "--session" && name != "--state" {
			return fail(fmt.Errorf("unknown permission flag %s; use --session or --state", name))
		}
		if !inline {
			i++
		}
	}
	options, err := parseWorkOptions(args)
	if err != nil {
		return fail(err)
	}
	if len(options.args) != 3 {
		return fail(fmt.Errorf("%s requires FROM TO commands|messages|pings; see %s --help", operation, operation))
	}
	from, to := options.args[0], options.args[1]
	if !managedconfig.ValidID(from) || !managedconfig.ValidID(to) {
		return fail(errors.New("FROM and TO must be stable agent IDs from amx ls"))
	}
	if from == to {
		return fail(errors.New("FROM and TO must be different agents"))
	}
	var action, label string
	switch options.args[2] {
	case "commands", "instruct":
		action, label = "instruct", "commands"
	case "messages", "message.send":
		action, label = "message.send", "messages"
	case "pings", "ping.send":
		action, label = "ping.send", "pings"
	default:
		return fail(errors.New("choose commands, messages or pings (exact actions: instruct, message.send, ping.send)"))
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(options.state, ""))
	if err != nil {
		return fail(errors.New("workspace not found; start a workspace first"))
	}
	client := managed.NewClient(credential)
	call := func(ctx context.Context, op string, args any) (json.RawMessage, error) {
		return workCall(ctx, client, op, args)
	}
	if err := changeWorkPermission(context.Background(), call, from, to, action, operation == "allow"); err != nil {
		return fail(err)
	}
	fmt.Fprintf(out, "Saved %s: %s → %s · %s (%s).\n", operation, from, to, label, action)
	fmt.Fprintf(out, "Native delivery is separate: amx mode %s review|auto.\n", to)
	if operation == "deny" {
		fmt.Fprintln(out, "Future operations are denied; cancel accepted tasks separately if needed.")
	}
	return exitSuccess
}

// One snapshot authorizes one CAS edit. A conflict or uncertain transport result
// is returned to the owner; refreshing and retrying is always an explicit action.
func changeWorkPermission(ctx context.Context, call func(context.Context, string, any) (json.RawMessage, error), from, to, action string, allow bool) error {
	raw, err := call(ctx, "control.get", map[string]any{})
	if err != nil {
		return err
	}
	var control managed.ControlState
	if err := json.Unmarshal(raw, &control); err != nil {
		return err
	}
	_, err = call(ctx, "control.update", map[string]any{
		"version": control.Version, "kind": "edge", "from": from, "to": to, "action": action, "allow": allow,
	})
	return err
}
