package main

import (
	"context"
	"fmt"
	"io"

	"agentmux/internal/managed"
)

func runWorkRole(args []string, out, diagnostics io.Writer) int {
	if workHelpRequested(args) {
		fmt.Fprintln(out, "Usage: agentmux role AGENT_ID \"new role\" [--session NAME | --state DIR]\nUpdates owner role with version checking. Native sessions receive an inbox notice; no terminal input is injected. Managed workers use the current role at their next claim.")
		return exitSuccess
	}
	fail := func(err error) int { fmt.Fprintln(diagnostics, "error:", err); return exitValidation }
	if err := validateWorkFlags("role", args); err != nil {
		return fail(err)
	}
	o, err := parseWorkOptions(args)
	if err != nil {
		return fail(err)
	}
	if len(o.args) != 2 {
		return fail(fmt.Errorf("role requires one stable agent ID and one quoted role; use an empty role to clear it"))
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(o.state, ""))
	if err != nil {
		return fail(err)
	}
	client := managed.NewClient(credential)
	info, err := workAgentForLabel(context.Background(), client, o.args[0])
	if err != nil {
		return fail(err)
	}
	_, err = workCall(context.Background(), client, "agent.role", map[string]any{"id": info.ID, "role": o.args[1], "version": info.RoleVersion})
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(out, "Role updated for %s · workspace %q.\nNative sessions: inbox notice only; read identity for the current role. Managed workers: applies at the next task claim.\n", info.ID, o.state)
	return exitSuccess
}
