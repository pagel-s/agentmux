package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agentmux/internal/managed"
	"agentmux/internal/managedcheck"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
	"agentmux/internal/managedsetup"
)

func runWorkAdd(args []string, out, diagnostics io.Writer) int {
	code, _ := runWorkAddWithID(args, out, diagnostics)
	return code
}

func runWorkAddWithID(args []string, out, diagnostics io.Writer) (int, string) {
	id := ""
	code := runWorkAddParsed(args, out, diagnostics, &id)
	return code, id
}

func workAddName(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

func newWorkAgentID(harness string) string {
	switch harness {
	case "claude", "codex", "pi", "antigravity":
	default:
		harness = "agent"
	}
	return harness + "_" + strings.ToLower(rand.Text()[:12])
}

func runWorkAddParsed(args []string, out, diagnostics io.Writer, resolvedID *string) int {
	fail := func(err error) int { fmt.Fprintln(diagnostics, "error:", err); return exitValidation }
	id, flagArgs := workAddName(args)
	flags := flag.NewFlagSet("add", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	provider := flags.String("harness", "claude", "native agent interface: claude, codex or pi")
	project := flags.String("project", ".", "project directory (may differ from other sessions)")
	role := flags.String("role", "", "optional role context; native tools and customizations remain available")
	title := flags.String("title", "", "optional display title; does not change agent instructions or stable ID")
	model := flags.String("model", "", "optional native model choice")
	isolation := flags.String("sandbox", "local", "local preserves native configuration; bubblewrap uses an isolated profile")
	state := flags.String("state", "", "workspace state directory")
	session := flags.String("session", "work", "workspace session")
	authFile := flags.String("auth-file", "", "isolated Pi credential snapshot")
	authProvider := flags.String("auth-provider", "", "isolated Pi provider")
	if err := flags.Parse(flagArgs); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return exitValidation
	}
	if flags.NArg() != 0 {
		return fail(errors.New("unexpected arguments"))
	}
	options, err := parseWorkOptions([]string{"--session", *session})
	if err != nil {
		return fail(err)
	}
	if *state != "" {
		options, err = parseWorkOptions([]string{"--state", *state})
		if err != nil {
			return fail(err)
		}
	}
	if *provider != "claude" && *provider != "codex" && *provider != "pi" {
		return fail(errors.New("choose claude, codex or pi; use managed configurations for other harnesses"))
	}
	if id == "" {
		id = newWorkAgentID(*provider)
	}
	*resolvedID = id
	if !managedconfig.ValidID(id) {
		return fail(errors.New("invalid agent ID"))
	}
	if err := managed.ValidateSessionTitle(*title); err != nil {
		return fail(err)
	}
	directory, err := filepath.Abs(expandUserPath(*project))
	if err != nil {
		return fail(err)
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return fail(err)
	}
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return fail(errors.New("project must be an existing directory"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fail(err)
	}
	agent := managedsetup.Agent{ID: id, Harness: *provider, Role: *role, Interaction: "interactive", Workdir: directory, Model: *model, Auth: "native-local"}
	if *isolation == "bubblewrap" {
		agent.Auth = "file"
		switch *provider {
		case "claude":
			agent.AuthFile = filepath.Join(home, ".claude", ".credentials.json")
		case "codex":
			agent.AuthFile = filepath.Join(home, ".codex", "auth.json")
			agent.CodexSandbox = "external"
		case "pi":
			agent.AuthFile = *authFile
			agent.AuthProvider = *authProvider
		}
	}
	plan, err := managedsetup.Build(managedsetup.Spec{Session: "addition", WorkspaceRoot: filepath.Join(options.state, "workspaces"), Sandbox: *isolation, BrokerSandbox: "local", Agents: []managedsetup.Agent{agent}})
	if err != nil {
		return fail(err)
	}
	exe, _ := os.Executable()
	report := managedcheck.Check(context.Background(), plan.Config, managedcheck.Options{Executable: exe, CheckAuth: true})
	if !report.OK {
		data, _ := json.MarshalIndent(report, "", "  ")
		return fail(fmt.Errorf("native startup preflight failed: %s", data))
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(options.state, ""))
	if err != nil {
		return fail(errors.New("workspace not found; use agentmux start first"))
	}
	client := managed.NewClient(credential)
	if _, err = workCall(context.Background(), client, "agent.add", map[string]any{"id": id, "title": *title, "config": plan.Config.Agents[id]}); err != nil {
		return fail(err)
	}
	fmt.Fprintf(out, "Added %s · %s · %s\n", id, *provider, directory)
	fmt.Fprintln(out, "Collaboration is off. Open:", managedlaunch.ShellCommand([]string{"agentmux", "attach", id, "--state", options.state}))
	if *title != "" {
		fmt.Fprintln(out, "Title:", *title)
	}
	return exitSuccess
}

func workCollaboration(ctx context.Context, client *managed.Client, ids []string, independent bool) (json.RawMessage, error) {
	raw, err := workCall(ctx, client, "control.get", map[string]any{})
	if err != nil {
		return nil, err
	}
	var control managed.ControlState
	if err = json.Unmarshal(raw, &control); err != nil {
		return nil, err
	}
	kind := "collaboration"
	if independent {
		kind = "independent"
	}
	return workCall(ctx, client, "control.update", map[string]any{"version": control.Version, "kind": kind, "agents": ids, "enabled": !independent})
}

func workAgentOperation(operation string, o workOptions, client *managed.Client, out io.Writer) error {
	ctx := context.Background()
	switch operation {
	case "label":
		if len(o.args) != 2 {
			return errors.New("label requires a stable agent ID and display title; use an empty title to clear it")
		}
		if err := managed.ValidateSessionTitle(o.args[1]); err != nil {
			return err
		}
		info, err := workAgentForLabel(ctx, client, o.args[0])
		if err != nil {
			return err
		}
		if _, err = workCall(ctx, client, "agent.label", map[string]any{"id": info.ID, "title": o.args[1], "version": info.TitleVersion}); err != nil {
			return err
		}
		fmt.Fprintf(out, "Labelled %s: %s\n", info.ID, o.args[1])
	case "remove":
		if len(o.args) != 1 {
			return errors.New("remove requires one agent name")
		}
		raw, err := workCall(ctx, client, "agent.stop", map[string]string{"id": o.args[0]})
		if err != nil {
			return err
		}
		var result struct {
			RevokedAgents int `json:"revoked_agents"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
		if result.RevokedAgents > 0 {
			fmt.Fprintf(out, "Revoked coordination for %s and its active descendants (%d session(s)).\n", o.args[0], result.RevokedAgents)
		} else {
			fmt.Fprintf(out, "%s is already revoked.\n", o.args[0])
		}
		fmt.Fprintln(out, "Saved history remains. The supervisor closes owned terminals after observing revocation; external native terminals stay open. Process termination is not yet confirmed.")
	case "collaborate", "isolate":
		if len(o.args) == 0 {
			return errors.New("select agent names")
		}
		raw, err := workCollaboration(ctx, client, o.args, operation == "isolate")
		if err != nil {
			return err
		}
		var control managed.ControlState
		if err = json.Unmarshal(raw, &control); err != nil {
			return err
		}
		if operation == "isolate" {
			fmt.Fprintf(out, "Disconnected %s from peer coordination and shared memory. Native terminals keep running; accepted tasks and previously read information remain.\n", strings.Join(o.args, ", "))
		} else {
			fmt.Fprintf(out, "Connected %s: notes/pings; commands remain disabled.\n", strings.Join(o.args, ", "))
			if control.Group != nil {
				fmt.Fprintf(out, "Group %s\nBoard %s\nMemory %s\n", control.Group.ID, control.Group.Board, control.Group.Memory)
			}
			fmt.Fprintln(out, "Ask native agents to read their updated boundaries/inbox.")
		}
	}
	return nil
}

func workAgentForLabel(ctx context.Context, client *managed.Client, id string) (managed.AgentInfo, error) {
	cursor := ""
	for {
		raw, err := workCall(ctx, client, "agent.list", map[string]any{"limit": 200, "after": cursor})
		if err != nil {
			return managed.AgentInfo{}, err
		}
		var page struct {
			Agents []managed.AgentInfo `json:"agents"`
			Cursor string              `json:"cursor"`
		}
		if err = json.Unmarshal(raw, &page); err != nil {
			return managed.AgentInfo{}, err
		}
		for _, a := range page.Agents {
			if a.ID == id {
				return a, nil
			}
		}
		if len(page.Agents) == 0 || page.Cursor == "" || page.Cursor == cursor {
			return managed.AgentInfo{}, errors.New("agent ID not found")
		}
		cursor = page.Cursor
	}
}
