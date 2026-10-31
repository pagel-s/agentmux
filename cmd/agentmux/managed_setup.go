package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"agentmux/internal/managed"
	"agentmux/internal/managedcheck"
	"agentmux/internal/managedsetup"
)

func runManagedSetup(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runManagedSetupIO(ctx, args, os.Stdin, stdout, stderr)
}

func runManagedSetupIO(ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("managed setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "agentmux-managed.yaml", "new private YAML file (never overwritten)")
	specFile := flags.String("spec", "", "noninteractive JSON setup specification; - reads stdin")
	initGit := flags.Bool("init-git", false, "initialize Git only in newly created dedicated workspaces")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: agentmux managed setup [--spec setup.json] --output team.yaml [--init-git]\nWithout --spec, asks for agent roles, runtimes, isolation and explicit authentication choices.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitSuccess
		}
		return managedCLIError(stdout, "setup", "", exitValidation, err)
	}
	if flags.NArg() != 0 {
		return managedCLIError(stdout, "setup", "", exitValidation, errors.New("setup accepts flags only"))
	}
	var spec managedsetup.Spec
	var err error
	if *specFile != "" {
		reader := input
		if *specFile != "-" {
			f, openErr := os.Open(expandUserPath(*specFile))
			if openErr != nil {
				return managedCLIError(stdout, "setup", "", exitConfigParse, errors.New("setup spec cannot be opened"))
			}
			defer f.Close()
			reader = f
		}
		spec, err = managedsetup.Decode(reader)
	} else {
		spec, err = askManagedSetup(input, stderr)
	}
	if err != nil {
		return managedCLIError(stdout, "setup", "", exitValidation, err)
	}
	if *initGit {
		spec.InitGit = true
	}
	plan, err := managedsetup.Build(spec)
	if err != nil {
		return managedCLIError(stdout, "setup", "", exitValidation, err)
	}
	path, err := filepath.Abs(expandUserPath(*output))
	if err != nil {
		return managedCLIError(stdout, "setup", "", exitValidation, err)
	}
	// Refuse an existing output before creating workspaces or running checks.
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return managedCLIError(stdout, "setup", "", exitValidation, errors.New("setup output already exists or cannot be inspected"))
	}
	executable, _ := os.Executable()
	var discoveries = map[string]managedcheck.ModelDiscovery{}
	for _, agent := range plan.Config.Agents {
		if agent.Harness != "pi" || agent.Connection == "" {
			continue
		}
		connection := plan.Config.Connections[agent.Connection]
		if connection.BaseURL == "" {
			continue
		}
		discovery := managedcheck.DiscoverModelMetadata(ctx, connection, agent.Model, os.Getenv(connection.APIKeyEnv))
		connection.ModelMetadata = discovery.Metadata
		plan.Config.Connections[agent.Connection] = connection
		discoveries[agent.Connection] = discovery
	}
	report := managedcheck.Check(ctx, plan.Config, managedcheck.Options{Executable: executable, BrokerSandbox: plan.BrokerSandbox, CheckAuth: true})
	if !report.OK {
		_ = writeManagedEnvelope(stdout, managedEnvelope{Operation: "setup", OK: false, Result: map[string]any{"preflight": report, "written": false}, Error: &managed.Fault{Code: "preflight", Message: "setup preflight failed; no configuration or workspace was created"}})
		return exitValidation
	}
	notes, err := managedsetup.InitWorkspaces(ctx, plan, spec.InitGit)
	if err != nil {
		return managedCLIError(stdout, "setup", "", exitRuntime, err)
	}
	if err := managedsetup.Write(path, plan.Config); err != nil {
		return managedCLIError(stdout, "setup", "", exitRuntime, err)
	}
	return writeManagedEnvelope(stdout, managedEnvelope{Operation: "setup", OK: true, Result: map[string]any{"config": path, "session": spec.Session, "agents": len(spec.Agents), "broker_sandbox": plan.BrokerSandbox, "preflight": report, "model_metadata": discoveries, "warnings": plan.Warnings, "workspace_notes": notes, "next": []string{"agentmux managed doctor " + shellQuote(path), "agentmux managed up " + shellQuote(path) + " --broker-sandbox " + plan.BrokerSandbox}}})
}

func askManagedSetup(input io.Reader, output io.Writer) (managedsetup.Spec, error) {
	reader := bufio.NewReader(input)
	ask := func(label, fallback string) (string, error) {
		fmt.Fprintf(output, "%s [%s]: ", label, fallback)
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", errors.New("interactive setup input ended; use --spec for noninteractive setup")
		}
		line = strings.TrimSpace(line)
		if line == "" {
			line = fallback
		}
		return line, nil
	}
	var spec managedsetup.Spec
	var err error
	if spec.Session, err = ask("Session name", "team"); err != nil {
		return spec, err
	}
	if spec.WorkspaceRoot, err = ask("Dedicated workspace root", "./agentmux-workspaces"); err != nil {
		return spec, err
	}
	if spec.Sandbox, err = ask("Agent sandbox (bubblewrap or trusted local)", "bubblewrap"); err != nil {
		return spec, err
	}
	if spec.BrokerSandbox, err = ask("Broker sandbox (bubblewrap or trusted local)", "bubblewrap"); err != nil {
		return spec, err
	}
	countText, err := ask("Number of agents", "2")
	if err != nil {
		return spec, err
	}
	count, err := strconv.Atoi(countText)
	if err != nil || count < 1 || count > 32 {
		return spec, errors.New("agent count must be between 1 and 32")
	}
	for i := 0; i < count; i++ {
		fmt.Fprintf(output, "Agent %d (no credential values are requested):\n", i+1)
		var a managedsetup.Agent
		if a.ID, err = ask("  ID", fmt.Sprintf("agent%d", i+1)); err != nil {
			return spec, err
		}
		if a.Role, err = ask("  Role", "developer"); err != nil {
			return spec, err
		}
		if a.Harness, err = ask("  Harness (claude, codex, pi, antigravity, antigravity-bridge)", "codex"); err != nil {
			return spec, err
		}
		modelDefault := ""
		if a.Harness == "antigravity-bridge" {
			modelDefault = "gemini-3.8-flash-low"
		}
		if a.Model, err = ask("  Model ID (empty uses native default)", modelDefault); err != nil {
			return spec, err
		}
		if a.Binary, err = ask("  Executable (empty discovers installed runtime)", ""); err != nil {
			return spec, err
		}
		authDefault := "file"
		if a.Harness == "antigravity-bridge" {
			authDefault = "api-env"
		}
		if a.Auth, err = ask("  Auth (file, api-env, native-local, none)", authDefault); err != nil {
			return spec, err
		}
		if a.Auth == "file" {
			if a.AuthFile, err = ask("  Explicit private credential file", ""); err != nil {
				return spec, err
			}
			a.AuthFile = expandUserPath(a.AuthFile)
			if a.Harness == "pi" {
				if a.AuthProvider, err = ask("  Pi credential provider ID", ""); err != nil {
					return spec, err
				}
			}
		}
		if a.Auth == "api-env" {
			keyDefault := ""
			if a.Harness == "antigravity-bridge" {
				keyDefault = "AGENTMUX_AGY_BRIDGE_KEY"
			}
			if a.APIKeyEnv, err = ask("  API key environment VARIABLE NAME", keyDefault); err != nil {
				return spec, err
			}
		}
		baseDefault := ""
		if a.Harness == "antigravity-bridge" {
			baseDefault = "http://127.0.0.1:8097/v1"
		}
		if a.BaseURL, err = ask("  Custom API base URL (Pi/Claude; include /v1 when required)", baseDefault); err != nil {
			return spec, err
		}
		if a.Harness == "claude" {
			value, e := ask("  Allow coding tools Read,Write,Edit,Bash (yes/no)", "no")
			if e != nil {
				return spec, e
			}
			a.CodingTools = value == "yes"
		}
		if a.Harness == "codex" && spec.Sandbox == "bubblewrap" {
			if a.CodexSandbox, err = ask("  Codex isolation (native or explicit external outer sandbox)", "native"); err != nil {
				return spec, err
			}
		}
		if spec.Sandbox == "bubblewrap" && a.Harness != "antigravity" {
			if a.Conversation, err = ask("  Conversation (fresh/persistent)", "persistent"); err != nil {
				return spec, err
			}
		}
		spec.Agents = append(spec.Agents, a)
	}
	shared, err := ask("Shared team board and project memory (yes/no)", "yes")
	if err != nil {
		return spec, err
	}
	spec.SharedBoard = shared == "yes"
	git, err := ask("Initialize Git in newly created workspaces (yes/no)", "yes")
	if err != nil {
		return spec, err
	}
	spec.InitGit = git == "yes"
	return spec, nil
}
