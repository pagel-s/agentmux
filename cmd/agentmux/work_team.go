package main

import (
	"agentmux/internal/managed"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const teamHelp = `Consented teams (native terminals keep running):
  agentmux team list
  agentmux team create NAME [--description TEXT]
  agentmux team edit TEAM --name NAME [--description TEXT]
  agentmux team invite TEAM AGENT
  agentmux team remove TEAM AGENT
  agentmux team transfer TEAM AGENT
  agentmux team dissolve TEAM
  agentmux team join TEAM --credential-file AGENT_CREDENTIAL
  agentmux team leave TEAM --credential-file AGENT_CREDENTIAL
  agentmux team accept-owner TEAM --credential-file NEW_OWNER_CREDENTIAL
  agentmux team move SOURCE DESTINATION --credential-file AGENT_CREDENTIAL

Options: --session NAME | --state DIR; --version N; move also --to-version N.
Omitted versions are read once immediately before submitting; conflicts are never
retried. Invitations, ownership proposals and moves require the agent's own
credential and consent; managers cannot force these acceptances. Transfer first
proposes ownership, then the proposed owner runs accept-owner. Existing owners
must transfer ownership before leaving. All identifiers are stable team/agent IDs.`

func parseTeamArgs(operation string, args []string) (workOptions, map[string]string, []string, error) {
	values := map[string]string{}
	positionals := []string{}
	selectors := []string{}
	allowed := map[string]bool{"session": true, "state": true, "version": true, "credential-file": true}
	switch operation {
	case "create", "edit":
		allowed["description"] = true
	case "move":
		allowed["to-version"] = true
	}
	if operation == "edit" {
		allowed["name"] = true
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") {
			positionals = append(positionals, arg)
			continue
		}
		name, value, inline := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if !allowed[name] {
			return workOptions{}, nil, nil, fmt.Errorf("unknown --%s for team %s", name, operation)
		}
		if _, seen := values[name]; seen {
			return workOptions{}, nil, nil, fmt.Errorf("duplicate --%s", name)
		}
		if !inline {
			i++
			if i >= len(args) {
				return workOptions{}, nil, nil, fmt.Errorf("--%s requires a value", name)
			}
			value = args[i]
		}
		if value == "" && name != "description" {
			return workOptions{}, nil, nil, fmt.Errorf("--%s requires a nonempty value", name)
		}
		values[name] = value
		if name == "state" || name == "session" {
			selectors = append(selectors, "--"+name, value)
		}
	}
	o, err := parseWorkOptions(selectors)
	return o, values, positionals, err
}
func listVisibleTeams(ctx context.Context, client *managed.Client) ([]managed.Team, error) {
	teams := []managed.Team{}
	cursor := ""
	for {
		raw, err := workCall(ctx, client, "team.list", map[string]any{"after": cursor, "limit": 200})
		if err != nil {
			return nil, err
		}
		var page struct {
			Teams  []managed.Team `json:"teams"`
			Cursor string         `json:"cursor"`
		}
		if err = json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		teams = append(teams, page.Teams...)
		if len(page.Teams) == 0 || page.Cursor == "" || page.Cursor == cursor {
			break
		}
		cursor = page.Cursor
	}
	return teams, nil
}
func runWorkTeam(args []string, out, diagnostics io.Writer) int {
	if len(args) == 0 || helpArgument(args[0]) {
		fmt.Fprintln(out, teamHelp)
		return exitSuccess
	}
	op := args[0]
	known := map[string]bool{"list": true, "create": true, "edit": true, "invite": true, "join": true, "leave": true, "remove": true, "dissolve": true, "transfer": true, "accept-owner": true, "move": true}
	if !known[op] {
		fmt.Fprintln(diagnostics, "error: unknown team operation", op)
		return exitValidation
	}
	for _, arg := range args[1:] {
		if helpArgument(arg) {
			fmt.Fprintln(out, teamHelp)
			return exitSuccess
		}
	}
	o, values, pos, err := parseTeamArgs(op, args[1:])
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitValidation
	}
	want := 1
	if op == "list" {
		want = 0
	}
	if op == "invite" || op == "remove" || op == "transfer" || op == "move" {
		want = 2
	}
	if len(pos) != want {
		fmt.Fprintf(diagnostics, "error: team %s requires %d positional arguments; see agentmux team --help\n", op, want)
		return exitValidation
	}
	consent := op == "join" || op == "leave" || op == "accept-owner" || op == "move"
	path := values["credential-file"]
	if consent && path == "" {
		fmt.Fprintln(diagnostics, "error: consent operations require --credential-file for the acting agent (the proposed owner for accept-owner)")
		return exitValidation
	}
	if path == "" {
		path = managed.CredentialPath(o.state, "")
	}
	credential, err := managed.LoadCredential(expandUserPath(path))
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitRuntime
	}
	if _, stateSelected := values["state"]; stateSelected && credential.Socket != managed.SocketPath(o.state) {
		fmt.Fprintln(diagnostics, "error: credential belongs to a different workspace socket")
		return exitValidation
	}
	if _, sessionSelected := values["session"]; sessionSelected && credential.Socket != managed.SocketPath(o.state) {
		fmt.Fprintln(diagnostics, "error: credential belongs to a different workspace socket")
		return exitValidation
	}
	client := managed.NewClient(credential)
	ctx := context.Background()
	input := map[string]any{}
	operation := "team." + op
	if op == "edit" {
		operation = "team.update"
	}
	if op == "create" {
		input["name"] = pos[0]
		input["description"] = values["description"]
	} else if op != "list" {
		teams, e := listVisibleTeams(ctx, client)
		if e != nil {
			fmt.Fprintln(diagnostics, "error:", e)
			return exitRuntime
		}
		lookup := func(id string) (managed.Team, error) {
			for _, team := range teams {
				if team.ID == id {
					return team, nil
				}
			}
			return managed.Team{}, errors.New("visible team not found: " + id)
		}
		team, e := lookup(pos[0])
		if e != nil {
			fmt.Fprintln(diagnostics, "error:", e)
			return exitValidation
		}
		version := team.Version
		if raw, ok := values["version"]; ok {
			version, e = strconv.Atoi(raw)
			if e != nil || version < 1 {
				fmt.Fprintln(diagnostics, "error: --version must be positive")
				return exitValidation
			}
		}
		input["id"], input["version"] = team.ID, version
		switch op {
		case "edit":
			name, exists := values["name"]
			if !exists || strings.TrimSpace(name) == "" {
				fmt.Fprintln(diagnostics, "error: edit requires --name")
				return exitValidation
			}
			description := team.Description
			if value, exists := values["description"]; exists {
				description = value
			}
			input["name"], input["description"] = name, description
		case "invite", "remove", "transfer":
			input["agent"] = pos[1]
		case "move":
			target, e := lookup(pos[1])
			if e != nil {
				fmt.Fprintln(diagnostics, "error:", e)
				return exitValidation
			}
			version := target.Version
			if raw, ok := values["to-version"]; ok {
				version, e = strconv.Atoi(raw)
				if e != nil || version < 1 {
					fmt.Fprintln(diagnostics, "error: --to-version must be positive")
					return exitValidation
				}
			}
			input["to_id"], input["to_version"] = target.ID, version
		}
	}
	raw, err := workCall(ctx, client, operation, input)
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitRuntime
	}
	var value any
	if err = json.Unmarshal(raw, &value); err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitRuntime
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(value); err != nil {
		return exitRuntime
	}
	return exitSuccess
}
