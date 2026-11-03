package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"agentmux/internal/cli"
	"agentmux/internal/config"
	"agentmux/internal/doctor"
	"agentmux/internal/envcheck"
	"agentmux/internal/hooks"
	"agentmux/internal/managedlaunch"
	"agentmux/internal/mcp"
	"agentmux/internal/pack"
	"agentmux/internal/provider"
	"agentmux/internal/remote"
	"agentmux/internal/resolve"
	"agentmux/internal/scaffold"
	"agentmux/internal/tmux"
	"agentmux/internal/toolruntime"
	"agentmux/internal/tools"
	"agentmux/internal/tui"
	"agentmux/internal/validate"
)

const (
	exitSuccess     = 0
	exitRuntime     = 1
	exitValidation  = 2
	exitConfigParse = 3
	exitDependency  = 4
	jsonSchemaV1    = 1
)

var (
	version             = "dev"
	commit              = "none"
	date                = "unknown"
	toolExecutorFactory = func() toolruntime.Executor {
		return toolruntime.New(requireTMux, resolveScaleConfigForTools)
	}
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 && strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == "amx" {
		return runWorkHome(os.Stdin, os.Stdout, os.Stderr)
	}
	if len(args) == 1 && args[0] == "ui" {
		return runWorkHome(os.Stdin, os.Stdout, os.Stderr)
	}
	var err error
	args, err = normalizeWorkCLI(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitValidation
	}
	if len(args) == 0 {
		printUsage()
		return exitValidation
	}
	if len(args) == 1 && args[0] == "attach" {
		return runWorkHomeOperation("attach", os.Stdin, os.Stdout, os.Stderr)
	}

	switch args[0] {
	case "split", "unsplit":
		return runWorkLayout(args[0], args[1:], os.Stdout, os.Stderr)
	case "allow", "deny":
		return runWorkPermission(args[0], args[1:], os.Stdout, os.Stderr)
	case "start", "attach", "reply", "inbox", "board", "down", "remove", "retire", "collaborate", "disconnect", "isolate", "label":
		return runWorkSession(args[0], args[1:], os.Stdin, os.Stdout, os.Stderr)
	case "team":
		return runWorkTeam(args[1:], os.Stdout, os.Stderr)
	case "sessions":
		return runWorkInventory(args[1:], os.Stdout, os.Stderr)
	case "workspaces":
		return runWorkspaceInventory(args[1:], os.Stdout, os.Stderr)
	case "relaunch":
		return runWorkRelaunch(args[1:], os.Stdout, os.Stderr)
	case "delivery":
		return runWorkDelivery(args[1:], os.Stdout, os.Stderr)
	case "connect":
		return runWorkConnect(args[1:], os.Stdout, os.Stderr)
	case "add":
		return runWorkAdd(args[1:], os.Stdout, os.Stderr)
	case "role":
		return runWorkRole(args[1:], os.Stdout, os.Stderr)
	case "web":
		return runWorkWeb(args[1:], os.Stdout, os.Stderr)
	case "load":
		return runLoad(args[1:])
	case "reload":
		return runReload(args[1:])
	case "templates":
		return runTemplates(args[1:])
	case "init":
		return runInit(args[1:])
	case "edit":
		return runEdit(args[1:])
	case "remember":
		return runRemember(args[1:])
	case "pack":
		return runPack(args[1:])
	case "tools":
		return runTools(args[1:])
	case "prompt":
		return runPrompt(args[1:])
	case "tool-call":
		return runToolCall(args[1:])
	case "mcp-server":
		return runMCPServer(args[1:])
	case "managed":
		return runManaged(args[1:])
	case "internal-egress":
		return runInternalEgress(args[1:])
	case "internal-capsule":
		return runInternalCapsule(args[1:])
	case "experiment":
		return runExperiment(args[1:])
	case "flow":
		return runWorkflow(args[1:])
	case "work":
		return runGuidedWork(args[1:])
	case "demo":
		return runDemo(args[1:], os.Stdout, os.Stderr)
	case "research":
		return runResearch(args[1:])
	case "provider":
		return runProvider(args[1:], os.Stdout, os.Stderr)
	case "validate":
		return runValidate(args[1:])
	case "render":
		return runRender(args[1:])
	case "list":
		return runList(args[1:])
	case "dashboard":
		if len(args) == 1 || hasWorkSessionFlag(args[1:]) || workHelpRequested(args[1:]) {
			return runWorkSession("dashboard", args[1:], os.Stdin, os.Stdout, os.Stderr)
		}
		return runDashboard(args[1:])
	case "status":
		return runStatus(args[1:])
	case "inspect":
		return runInspect(args[1:])
	case "kv-set":
		return runKVSet(args[1:])
	case "kv-get":
		return runKVGet(args[1:])
	case "kv-list":
		return runKVList(args[1:])
	case "kv-del":
		return runKVDel(args[1:])
	case "wait":
		return runWait(args[1:])
	case "scale":
		return runScale(args[1:])
	case "focus":
		return runFocus(args[1:])
	case "restart":
		return runRestart(args[1:])
	case "rerun":
		return runRerun(args[1:])
	case "stop":
		return runStop(args[1:])
	case "kill":
		return runKill(args[1:])
	case "capture":
		return runCapture(args[1:])
	case "send":
		if workSendRequested(args[1:]) {
			return runWorkSession("send", args[1:], os.Stdin, os.Stdout, os.Stderr)
		}
		return runSend(args[1:])
	case "doctor":
		return runDoctor(args[1:])
	case "version", "-v", "--version":
		fmt.Println(versionString())
		return exitSuccess
	case "help", "-h", "--help":
		return runHelp(args[1:], os.Stdout, os.Stderr)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n\n", args[0])
		printUsage()
		return exitValidation
	}
}

func runLoad(args []string) int {
	opts, err := cli.ParseLoad(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	return runSessionLaunch("load", opts, false, opts.Replace)
}

func runReload(args []string) int {
	opts, err := cli.ParseReload(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	return runSessionLaunch("reload", opts, true, true)
}

func runTemplates(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "error: templates does not accept arguments")
		return exitValidation
	}

	templates, err := scaffold.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	fmt.Println("templates:")
	for _, template := range templates {
		sourceStr := ""
		if template.Source != scaffold.SourceBuiltin {
			sourceStr = fmt.Sprintf(" [%s]", template.Source)
		}

		if template.Description != "" {
			fmt.Printf("  %s: %s%s\n", template.Name, template.Description, sourceStr)
			continue
		}
		fmt.Printf("  %s%s\n", template.Name, sourceStr)
	}
	aliases := scaffold.Aliases()
	if len(aliases) > 0 {
		fmt.Println("aliases:")
		aliasNames := make([]string, 0, len(aliases))
		for alias := range aliases {
			aliasNames = append(aliasNames, alias)
		}
		sort.Strings(aliasNames)
		for _, alias := range aliasNames {
			fmt.Printf("  %s -> %s\n", alias, aliases[alias])
		}
	}
	fmt.Println("examples:")
	fmt.Println("  amx init default my-workflow.yaml")
	fmt.Println("  amx init coding-starter coding.yaml")
	fmt.Println("  amx init minimal scratch.yaml")
	return exitSuccess
}

func runInit(args []string) int {
	if workHelpRequested(args) {
		fmt.Println("Usage: amx init TEMPLATE PATH [--force]\nCreates a legacy pane-layout configuration. List templates with amx templates.\nFor everyday native work sessions use amx start; for managed presets use amx managed setup --help.")
		return exitSuccess
	}
	opts, err := cli.ParseInit(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	path, session, err := scaffold.Init(opts.Template, opts.Path, scaffold.InitOptions{Force: opts.Force})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	fmt.Printf("created %s from template %q\n", path, opts.Template)
	fmt.Printf("session: %s\n", session)
	fmt.Printf("next:\n")
	fmt.Printf("  amx validate %s\n", path)
	fmt.Printf("  amx render %s\n", path)
	fmt.Printf("  amx load %s --replace --no-attach\n", path)
	fmt.Printf("  amx edit %s\n", path)
	return exitSuccess
}

func runEdit(args []string) int {
	opts, err := cli.ParseEdit(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	targetPath, err := resolveEditTarget(opts.Target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	editorShell, editorCommand, err := editorInvocation(targetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitDependency
	}

	cmd := exec.Command(editorShell, "-lc", editorCommand)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: edit %s: %v\n", targetPath, err)
		return exitRuntime
	}
	return exitSuccess
}

func runRemember(args []string) int {
	opts, err := cli.ParseRemember(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	if err := requireTMux(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitDependency
	}

	configPath, err := resolveExistingConfigPath(opts.Path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	if err := tmux.SetSessionConfigPath(opts.Session, configPath, false); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	fmt.Printf("remembered config path %s for session %q\n", configPath, opts.Session)
	return exitSuccess
}

func runPack(args []string) int {
	opts, err := cli.ParsePack(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	path, err := pack.CreateBundle(opts.ConfigPath, opts.OutputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: pack %s: %v\n", opts.ConfigPath, err)
		return exitRuntime
	}

	fmt.Printf("created session bundle: %s\n", path)
	return exitSuccess
}

func runTools(args []string) int {
	opts, err := cli.ParseTools(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	out, err := tools.Definitions(opts.Format)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: generate tools: %v\n", err)
		return exitRuntime
	}

	fmt.Println(out)
	return exitSuccess
}

func runPrompt(args []string) int {
	opts, err := cli.ParsePrompt(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	out, err := tools.Prompt(tools.PromptOptions{
		Provider: opts.Provider,
		Mode:     opts.Mode,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: generate prompt: %v\n", err)
		return exitRuntime
	}

	fmt.Println(out)
	return exitSuccess
}

func runToolCall(args []string) int {
	opts, err := cli.ParseToolCall(args)
	if err != nil {
		return printToolCallError("", exitValidation, err, nil)
	}

	spec, ok := tools.Lookup(opts.Tool)
	if !ok {
		return printToolCallError(opts.Tool, exitValidation, fmt.Errorf("unsupported tool %q", opts.Tool), nil)
	}

	raw, source, err := loadToolCallPayload(opts)
	if err != nil {
		return printToolCallError(spec.Name, exitValidation, err, nil)
	}

	result, execErr := toolExecutorFactory().Execute(spec.Name, []byte(raw), source)
	if execErr != nil {
		return printToolCallError(spec.Name, execErr.ExitCode, errors.New(execErr.Message), execErr.DetailsJSON())
	}

	out, err := renderToolCallJSON(spec.Name, true, result, nil)
	if err != nil {
		return printToolCallError(spec.Name, exitRuntime, fmt.Errorf("render tool-call json: %w", err), nil)
	}
	fmt.Print(out)
	return exitSuccess
}

func runMCPServer(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "error: mcp-server does not accept arguments")
		return exitValidation
	}

	server := mcp.New(versionString(), toolExecutorFactory())
	if err := server.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: mcp-server: %v\n", err)
		return exitRuntime
	}
	return exitSuccess
}

func loadToolCallPayload(opts cli.ToolCallOptions) (string, string, error) {
	switch {
	case strings.TrimSpace(opts.FromJSON) != "":
		return opts.FromJSON, "--from-json", nil
	case opts.FromFile != "":
		data, err := os.ReadFile(opts.FromFile)
		if err != nil {
			return "", "--from-file", fmt.Errorf("read --from-file %q: %w", opts.FromFile, err)
		}
		return string(data), "--from-file", nil
	case opts.FromStdin:
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", "--from-stdin", fmt.Errorf("read --from-stdin payload: %w", err)
		}
		return string(data), "--from-stdin", nil
	default:
		return "", "", errors.New("tool-call requires an input mode")
	}
}

type toolCallError struct {
	Code     string          `json:"code"`
	ExitCode int             `json:"exit_code"`
	Message  string          `json:"message"`
	Details  json.RawMessage `json:"details,omitempty"`
}

func renderToolCallJSON(tool string, ok bool, result json.RawMessage, callErr *toolCallError) (string, error) {
	return renderJSON(struct {
		SchemaVersion int             `json:"schema_version"`
		Kind          string          `json:"kind"`
		Ok            bool            `json:"ok"`
		Tool          string          `json:"tool"`
		Result        json.RawMessage `json:"result,omitempty"`
		Error         *toolCallError  `json:"error,omitempty"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          "tool-call",
		Ok:            ok,
		Tool:          tool,
		Result:        result,
		Error:         callErr,
	})
}

func printToolCallError(tool string, exitCode int, err error, details json.RawMessage) int {
	code := exitCodeName(exitCode)
	if code == "unknown" {
		code = "runtime"
	}
	out, renderErr := renderToolCallJSON(tool, false, nil, &toolCallError{
		Code:     code,
		ExitCode: exitCode,
		Message:  err.Error(),
		Details:  details,
	})
	if renderErr == nil {
		fmt.Print(out)
		return exitCode
	}
	fmt.Fprintf(os.Stderr, "error: render tool-call error json: %v\n", renderErr)
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	return exitCode
}

func runSessionLaunch(mode string, opts cli.LoadOptions, requireExisting bool, replace bool) int {
	jsonOut := hasFlag(os.Args[1:], "--json")

	// 1. Resolve and Validate (handles .amx automatically)
	resolved, warnings, cleanup, code := resolveCommand(opts.CommonOptions)
	if code != exitSuccess {
		return code
	}
	cleanupFn := cleanup
	defer func() { cleanupFn() }()
	printWarnings(warnings)

	// 2. Setup Wizard (Interactive fulfillment)
	// We check for missing items in the resolved config requirements.
	report := envcheck.Check(resolved.Requires, opts.Params)
	if err := fulfillRequirements(resolved.Session, report, opts.Params, jsonOut); err != nil {
		return exitValidation
	}

	// 3. Re-resolve if setup wizard modified params
	if report.HasMissing() && !jsonOut {
		cleanupFn()
		resolved, warnings, cleanup, code = resolveCommand(opts.CommonOptions)
		cleanupFn = cleanup
		if code != exitSuccess {
			return code
		}
		printWarnings(warnings)
	}

	if opts.DryRun {
		fmt.Print(renderPlan(*resolved, opts.Attach, replace))
		return exitSuccess
	}

	if err := requireTMux(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitDependency
	}

	exists, err := tmux.SessionExists(resolved.Session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}
	if requireExisting && !exists {
		fmt.Fprintf(os.Stderr, "error: %s requires existing session %q; use `amx load %s` to create it\n", mode, resolved.Session, opts.ConfigPath)
		return exitRuntime
	}

	// Final pre-flight check (in case some env vars were missed or implicit)
	if missing := envcheck.Check(resolved.Requires, opts.Params); missing.HasMissing() {
		fmt.Fprintf(os.Stderr, "error: pre-flight check failed for session %q:\n", resolved.Session)
		if len(missing.MissingBinaries) > 0 {
			for _, m := range missing.MissingBinaries {
				fmt.Fprintf(os.Stderr, "  - binary %q not found in PATH\n", m)
			}
		}
		if len(missing.MissingEnv) > 0 {
			for _, m := range missing.MissingEnv {
				fmt.Fprintf(os.Stderr, "  - environment variable %q is not set\n", m)
			}
		}
		if len(missing.MissingParams) > 0 {
			for _, m := range missing.MissingParams {
				fmt.Fprintf(os.Stderr, "  - parameter %q is missing\n", m)
			}
		}
		return exitValidation
	}

	if ok := preflightProviders(*resolved); !ok {
		return exitDependency
	}

	if err := hooks.RunPre(resolved.Hooks.PreSession, opts.Verbose); err != nil {
		fmt.Fprintf(os.Stderr, "error: pre_session hook failed: %v\n", err)
		return exitRuntime
	}

	execOpts := tmux.Options{
		Attach:  opts.Attach,
		Replace: replace,
		Verbose: opts.Verbose,
	}

	if mode == "reload" {
		err = tmux.Reconcile(*resolved, execOpts)
		if errors.Is(err, tmux.ErrStructuralChange) {
			fmt.Fprintf(os.Stderr, "warning: %v. Re-creating entire session.\n", err)
			err = tmux.Execute(*resolved, execOpts)
		}
	} else {
		err = tmux.Execute(*resolved, execOpts)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	if absConfigPath, err := filepath.Abs(opts.ConfigPath); err == nil {
		if err := tmux.SetSessionConfigPath(resolved.Session, absConfigPath, opts.Verbose); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to persist config path metadata for session %q: %v\n", resolved.Session, err)
		}
	}

	if err := hooks.RunPost(resolved.Hooks.PostSession, opts.Verbose); err != nil {
		fmt.Fprintf(os.Stderr, "warning: post_session hook failed: %v\n", err)
	}

	return exitSuccess
}

func runValidate(args []string) int {
	if workHelpRequested(args) {
		fmt.Println("Usage: amx validate CONFIG [--param key=value] [--strict]\nValidates a pane-layout configuration without launching it.\nFor managed team definitions use amx managed doctor CONFIG --help.")
		return exitSuccess
	}
	opts, err := cli.ParseValidate(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	_, warnings, cleanup, code := resolveCommand(opts)
	defer cleanup()
	if code != exitSuccess {
		return code
	}
	printWarnings(warnings)
	fmt.Println("config is valid")
	return exitSuccess
}

func runRender(args []string) int {
	opts, err := cli.ParseRender(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	resolved, warnings, cleanup, code := resolveCommand(opts)
	defer cleanup()
	if code != exitSuccess {
		return code
	}
	printWarnings(warnings)

	out, err := json.MarshalIndent(resolved, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: render failed: %v\n", err)
		return exitRuntime
	}

	fmt.Println(string(out))
	fmt.Println()
	fmt.Print(renderPlan(*resolved, false, false))
	return exitSuccess
}

func runList(args []string) int {
	if workHelpRequested(args) {
		fmt.Println("Usage: amx list\nLists legacy sessions on the default tmux socket.\nEveryday work sessions: amx sessions [--session NAME | --state DIR].")
		return exitSuccess
	}
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "error: list does not accept arguments")
		return exitValidation
	}

	fmt.Fprintln(os.Stderr, "This lists legacy tmux sessions. For everyday work sessions use amx sessions [--session NAME | --state DIR].")
	sessions, err := tmux.ListSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	for _, session := range sessions {
		fmt.Println(session)
	}

	return exitSuccess
}

func runDashboard(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "error: dashboard requires exactly one session name")
		return exitValidation
	}

	session := args[0]
	if err := requireTMux(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitDependency
	}

	if err := tui.RunDashboard(session); err != nil {
		fmt.Fprintf(os.Stderr, "error: dashboard: %v\n", err)
		return exitRuntime
	}

	return exitSuccess
}

func runStatus(args []string) int {
	if workHelpRequested(args) {
		fmt.Println("Usage: amx status SESSION [--json] [--pane PANE] [--logs [N]]\nInspects a legacy pane layout on the default tmux socket.\nEveryday work sessions: amx sessions [--session NAME | --state DIR].\nManaged runtime: amx managed status --state DIR [--watch].")
		return exitSuccess
	}
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseStatus(args)
	if err != nil {
		return printCommandError("status", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("status", opts.JSON, exitDependency, err, opts.Session, opts.Pane)
	}

	panes := []tmux.PaneStatus{}
	if opts.Pane != "" {
		pane, err := tmux.StatusPaneByName(opts.Session, opts.Pane)
		if err != nil {
			return printCommandError("status", opts.JSON, exitRuntime, err, opts.Session, opts.Pane)
		}
		panes = append(panes, pane)
	} else {
		allPanes, err := tmux.Status(opts.Session)
		if err != nil {
			return printCommandError("status", opts.JSON, exitRuntime, err, opts.Session, opts.Pane)
		}
		panes = allPanes
	}

	if opts.JSON {
		out, err := renderStatusJSON(opts.Session, opts.Pane, panes)
		if err != nil {
			return printCommandError("status", true, exitRuntime, fmt.Errorf("render status json: %w", err), opts.Session, opts.Pane)
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Print(renderStatus(opts.Session, panes))

	if opts.Logs > 0 {
		for _, pane := range panes {
			out, err := tmux.CapturePaneByName(opts.Session, pane.Name, opts.Logs, true, false)
			if err == nil && strings.TrimSpace(out) != "" {
				fmt.Printf("\n--- logs: %s ---\n%s", pane.Name, out)
				if !strings.HasSuffix(out, "\n") {
					fmt.Println()
				}
			}
		}
	}

	return exitSuccess
}

func runInspect(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseInspect(args)
	if err != nil {
		return printCommandError("inspect", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("inspect", opts.JSON, exitDependency, err, opts.Session, opts.Pane)
	}

	pane, err := tmux.StatusPaneByName(opts.Session, opts.Pane)
	if err != nil {
		return printCommandError("inspect", opts.JSON, exitRuntime, err, opts.Session, opts.Pane)
	}

	logs, err := tmux.CapturePaneByName(opts.Session, opts.Pane, 20, true, false)
	if err != nil {
		logs = fmt.Sprintf("error capturing logs: %v", err)
	}

	if opts.JSON {
		out, err := renderInspectJSON(opts.Session, pane, logs)
		if err != nil {
			return printCommandError("inspect", true, exitRuntime, fmt.Errorf("render inspect json: %w", err), opts.Session, opts.Pane)
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Print(renderInspect(opts.Session, pane, logs))
	return exitSuccess
}

func runKVSet(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseKVSet(args)
	if err != nil {
		return printCommandError("kv-set", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("kv-set", opts.JSON, exitDependency, err, opts.Session, "")
	}

	if err := tmux.SetKV(opts.Session, opts.Key, opts.Value, false); err != nil {
		return printCommandError("kv-set", opts.JSON, exitRuntime, err, opts.Session, "")
	}

	message := fmt.Sprintf("set blackboard key %q to %q in session %q", opts.Key, opts.Value, opts.Session)
	if opts.JSON {
		out, err := renderKVActionJSON("kv-set", opts.Session, opts.Key, opts.Value, message)
		if err != nil {
			return printCommandError("kv-set", true, exitRuntime, fmt.Errorf("render kv-set json: %w", err), opts.Session, "")
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Println(message)
	return exitSuccess
}

func runKVGet(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseKVGet(args)
	if err != nil {
		return printCommandError("kv-get", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("kv-get", opts.JSON, exitDependency, err, opts.Session, "")
	}

	value, err := tmux.GetKV(opts.Session, opts.Key, false)
	if err != nil {
		return printCommandError("kv-get", opts.JSON, exitRuntime, err, opts.Session, "")
	}

	if opts.JSON {
		out, err := renderKVJSON(opts.Session, opts.Key, value)
		if err != nil {
			return printCommandError("kv-get", true, exitRuntime, fmt.Errorf("render kv-get json: %w", err), opts.Session, "")
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Println(value)
	return exitSuccess
}

func runKVList(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseKVList(args)
	if err != nil {
		return printCommandError("kv-list", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("kv-list", opts.JSON, exitDependency, err, opts.Session, "")
	}

	kv, err := tmux.ListKV(opts.Session, false)
	if err != nil {
		return printCommandError("kv-list", opts.JSON, exitRuntime, err, opts.Session, "")
	}

	if opts.JSON {
		out, err := renderKVListJSON(opts.Session, kv)
		if err != nil {
			return printCommandError("kv-list", true, exitRuntime, fmt.Errorf("render kv-list json: %w", err), opts.Session, "")
		}
		fmt.Print(out)
		return exitSuccess
	}

	if len(kv) == 0 {
		fmt.Printf("blackboard for session %q is empty\n", opts.Session)
		return exitSuccess
	}

	fmt.Printf("blackboard for session %q:\n", opts.Session)
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %s: %s\n", k, kv[k])
	}
	return exitSuccess
}

func runKVDel(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseKVDel(args)
	if err != nil {
		return printCommandError("kv-del", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("kv-del", opts.JSON, exitDependency, err, opts.Session, "")
	}

	if err := tmux.DelKV(opts.Session, opts.Key, false); err != nil {
		return printCommandError("kv-del", opts.JSON, exitRuntime, err, opts.Session, "")
	}

	message := fmt.Sprintf("deleted blackboard key %q from session %q", opts.Key, opts.Session)
	if opts.JSON {
		out, err := renderKVActionJSON("kv-del", opts.Session, opts.Key, "", message)
		if err != nil {
			return printCommandError("kv-del", true, exitRuntime, fmt.Errorf("render kv-del json: %w", err), opts.Session, "")
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Println(message)
	return exitSuccess
}

func runWait(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseWait(args)
	if err != nil {
		return printCommandError("wait", jsonOut, exitValidation, err, "", "")
	}

	timeout := time.Duration(0)
	if opts.Timeout != "" {
		timeout, err = time.ParseDuration(opts.Timeout)
		if err != nil {
			return printCommandError("wait", opts.JSON, exitValidation, fmt.Errorf("invalid timeout %q: %w", opts.Timeout, err), opts.Session, opts.Pane)
		}
	}
	scope, err := tmux.ParseWaitScope(opts.Scope)
	if err != nil {
		return printCommandError("wait", opts.JSON, exitValidation, err, opts.Session, opts.Pane)
	}

	if err := requireTMux(); err != nil {
		return printCommandError("wait", opts.JSON, exitDependency, err, opts.Session, opts.Pane)
	}

	if !opts.JSON {
		fmt.Fprintf(os.Stderr, "waiting for pattern %q in pane %q (%s scope)...\n", opts.Pattern, opts.Pane, scope)
	}

	match, err := tmux.WaitForPattern(opts.Session, opts.Pane, opts.Pattern, timeout, scope, false)
	if err != nil {
		return printCommandError("wait", opts.JSON, exitRuntime, err, opts.Session, opts.Pane)
	}

	if opts.JSON {
		out, err := renderWaitJSON(opts.Session, opts.Pane, opts.Pattern, string(scope), match)
		if err != nil {
			return printCommandError("wait", true, exitRuntime, fmt.Errorf("render wait json: %w", err), opts.Session, opts.Pane)
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Printf("matched pattern %q in pane %q (%s scope): %s\n", opts.Pattern, opts.Pane, scope, match)
	return exitSuccess
}

func runScale(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseScale(args)
	if err != nil {
		return printCommandError("scale", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("scale", opts.JSON, exitDependency, err, opts.Session, "")
	}

	resolved, execErr := resolveScaleConfigForTools(opts.Session)
	if execErr != nil {
		return printCommandError("scale", opts.JSON, execErr.ExitCode, execErr, opts.Session, "")
	}

	if err := tmux.ScaleAgent(opts.Session, opts.Agent, opts.Count, opts.Window, resolved, tmux.Options{Verbose: false}); err != nil {
		return printCommandError("scale", opts.JSON, exitRuntime, err, opts.Session, "")
	}

	message := fmt.Sprintf("scaled agent %q to %d instances in session %q", opts.Agent, opts.Count, opts.Session)
	if opts.JSON {
		out, err := renderScaleJSON(opts.Session, opts.Agent, opts.Count, message)
		if err != nil {
			return printCommandError("scale", true, exitRuntime, fmt.Errorf("render scale json: %w", err), opts.Session, "")
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Println(message)
	return exitSuccess
}

func fulfillRequirements(session string, report envcheck.Report, params map[string]string, jsonOut bool) error {
	if !report.HasMissing() {
		return nil
	}

	// 1. Hard fail on missing binaries (cannot be fulfilled interactively)
	if len(report.MissingBinaries) > 0 {
		fmt.Fprintf(os.Stderr, "error: pre-flight check failed for session %q (missing binaries):\n", session)
		for _, bin := range report.MissingBinaries {
			fmt.Fprintf(os.Stderr, "  - binary %q not found in PATH\n", bin)
		}
		return errors.New("missing mandatory binaries")
	}

	// 2. Interactive fulfillment for Env and Params
	// If jsonOut is requested, we are in automation mode; do not prompt.
	if jsonOut {
		return nil // Caller will handle the remaining missing items via resolve/validate errors
	}

	// Only prompt if we have something to prompt for
	if len(report.MissingEnv) == 0 && len(report.MissingParams) == 0 {
		return nil
	}

	fmt.Printf("\n--- Setup Wizard for session %q ---\n", session)
	fmt.Println("Some mandatory requirements are missing. Please provide them below:")

	reader := bufio.NewReader(os.Stdin)

	for _, env := range report.MissingEnv {
		fmt.Printf("  Enter value for environment variable %q: ", env)
		val, _ := reader.ReadString('\n')
		val = strings.TrimSpace(val)
		if val != "" {
			os.Setenv(env, val)
		}
	}

	for _, p := range report.MissingParams {
		fmt.Printf("  Enter value for parameter %q: ", p)
		val, _ := reader.ReadString('\n')
		val = strings.TrimSpace(val)
		if val != "" {
			params[p] = val
		}
	}

	fmt.Println("--- Setup Complete ---")
	fmt.Println()
	return nil
}

func runFocus(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseFocus(args)
	if err != nil {
		return printCommandError("focus", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("focus", opts.JSON, exitDependency, err, opts.Session, opts.Pane)
	}

	if err := tmux.FocusPaneByName(opts.Session, opts.Pane, false); err != nil {
		return printCommandError("focus", opts.JSON, exitRuntime, err, opts.Session, opts.Pane)
	}

	message := fmt.Sprintf("focused pane %q in session %q", opts.Pane, opts.Session)
	if opts.JSON {
		out, err := renderActionJSON("focus", opts.Session, opts.Pane, message, false)
		if err != nil {
			return printCommandError("focus", true, exitRuntime, fmt.Errorf("render focus json: %w", err), opts.Session, opts.Pane)
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Println(message)
	return exitSuccess
}

func runRestart(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseRestart(args)
	if err != nil {
		return printCommandError("restart", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("restart", opts.JSON, exitDependency, err, opts.Session, opts.Pane)
	}

	if err := tmux.RestartPaneByName(opts.Session, opts.Pane, false); err != nil {
		return printCommandError("restart", opts.JSON, exitRuntime, err, opts.Session, opts.Pane)
	}

	message := fmt.Sprintf("restarted pane %q in session %q", opts.Pane, opts.Session)
	if opts.JSON {
		out, err := renderActionJSON("restart", opts.Session, opts.Pane, message, false)
		if err != nil {
			return printCommandError("restart", true, exitRuntime, fmt.Errorf("render restart json: %w", err), opts.Session, opts.Pane)
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Println(message)
	return exitSuccess
}

func runRerun(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseRerun(args)
	if err != nil {
		return printCommandError("rerun", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("rerun", opts.JSON, exitDependency, err, opts.Session, opts.Pane)
	}

	if err := tmux.RerunPaneByName(opts.Session, opts.Pane, false); err != nil {
		return printCommandError("rerun", opts.JSON, exitRuntime, err, opts.Session, opts.Pane)
	}

	message := fmt.Sprintf("reran pane %q in session %q", opts.Pane, opts.Session)
	if opts.JSON {
		out, err := renderActionJSON("rerun", opts.Session, opts.Pane, message, false)
		if err != nil {
			return printCommandError("rerun", true, exitRuntime, fmt.Errorf("render rerun json: %w", err), opts.Session, opts.Pane)
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Println(message)
	return exitSuccess
}

func runStop(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseStop(args)
	if err != nil {
		return printCommandError("stop", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("stop", opts.JSON, exitDependency, err, opts.Session, opts.Pane)
	}

	if err := tmux.StopPaneByName(opts.Session, opts.Pane, false); err != nil {
		return printCommandError("stop", opts.JSON, exitRuntime, err, opts.Session, opts.Pane)
	}

	message := fmt.Sprintf("stopped pane %q in session %q", opts.Pane, opts.Session)
	if opts.JSON {
		out, err := renderActionJSON("stop", opts.Session, opts.Pane, message, false)
		if err != nil {
			return printCommandError("stop", true, exitRuntime, fmt.Errorf("render stop json: %w", err), opts.Session, opts.Pane)
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Println(message)
	return exitSuccess
}

func runKill(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseKill(args)
	if err != nil {
		return printCommandError("kill", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("kill", opts.JSON, exitDependency, err, opts.Session, opts.Pane)
	}

	if err := tmux.KillPaneByName(opts.Session, opts.Pane, false); err != nil {
		return printCommandError("kill", opts.JSON, exitRuntime, err, opts.Session, opts.Pane)
	}

	message := fmt.Sprintf("killed pane %q in session %q", opts.Pane, opts.Session)
	if opts.JSON {
		out, err := renderActionJSON("kill", opts.Session, opts.Pane, message, false)
		if err != nil {
			return printCommandError("kill", true, exitRuntime, fmt.Errorf("render kill json: %w", err), opts.Session, opts.Pane)
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Println(message)
	return exitSuccess
}

func runCapture(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseCapture(args)
	if err != nil {
		return printCommandError("capture", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("capture", opts.JSON, exitDependency, err, opts.Session, opts.Pane)
	}

	out, err := tmux.CapturePaneByName(opts.Session, opts.Pane, opts.Tail, opts.StripEmpty, false)
	if err != nil {
		return printCommandError("capture", opts.JSON, exitRuntime, err, opts.Session, opts.Pane)
	}

	if opts.JSON {
		jsonOut, err := renderCaptureJSON(opts.Session, opts.Pane, opts.Tail, opts.StripEmpty, out)
		if err != nil {
			return printCommandError("capture", true, exitRuntime, fmt.Errorf("render capture json: %w", err), opts.Session, opts.Pane)
		}
		fmt.Print(jsonOut)
		return exitSuccess
	}

	fmt.Print(out)
	if out == "" || !strings.HasSuffix(out, "\n") {
		fmt.Println()
	}
	return exitSuccess
}

func runSend(args []string) int {
	jsonOut := hasFlag(args, "--json")
	opts, err := cli.ParseSend(args)
	if err != nil {
		return printCommandError("send", jsonOut, exitValidation, err, "", "")
	}

	if err := requireTMux(); err != nil {
		return printCommandError("send", opts.JSON, exitDependency, err, opts.Session, opts.Pane)
	}

	if err := tmux.SendCommandToPaneByName(opts.Session, opts.Pane, opts.Command, !opts.NoEnter, false); err != nil {
		return printCommandError("send", opts.JSON, exitRuntime, err, opts.Session, opts.Pane)
	}

	if opts.NoEnter {
		message := fmt.Sprintf("sent command text to pane %q in session %q without pressing Enter", opts.Pane, opts.Session)
		if opts.JSON {
			out, err := renderActionJSON("send", opts.Session, opts.Pane, message, true)
			if err != nil {
				return printCommandError("send", true, exitRuntime, fmt.Errorf("render send json: %w", err), opts.Session, opts.Pane)
			}
			fmt.Print(out)
			return exitSuccess
		}
		fmt.Println(message)
		return exitSuccess
	}
	message := fmt.Sprintf("sent command to pane %q in session %q", opts.Pane, opts.Session)
	if opts.JSON {
		out, err := renderActionJSON("send", opts.Session, opts.Pane, message, false)
		if err != nil {
			return printCommandError("send", true, exitRuntime, fmt.Errorf("render send json: %w", err), opts.Session, opts.Pane)
		}
		fmt.Print(out)
		return exitSuccess
	}
	fmt.Println(message)
	return exitSuccess
}

func runDoctor(args []string) int {
	configPath := ""
	if len(args) == 1 {
		configPath = args[0]
	} else if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "error: doctor accepts at most one config path")
		return exitValidation
	}

	input := doctor.Input{
		AgentmuxVersion: versionString(),
		ConfigPath:      configPath,
	}
	if configPath != "" {
		opts := cli.CommonOptions{ConfigPath: configPath}
		resolved, issues, cleanup, code := resolveCommand(opts)
		defer cleanup()
		input.Issues = issues
		input.ResolveExitCode = code
		if code == exitSuccess {
			input.Resolved = resolved
		}
	}

	fmt.Print(doctor.RenderText(doctor.Collect(input, doctor.DefaultProbes())))
	return exitSuccess
}

func resolveCommand(opts cli.CommonOptions) (*resolve.Config, []validate.Issue, func(), int) {
	configPath := opts.ConfigPath
	isRemote := remote.IsRemote(configPath)

	cleanup := func() {}

	if isRemote {
		localPath, err := remote.Fetch(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: fetch remote config %s: %v\n", configPath, err)
			return nil, nil, cleanup, exitRuntime
		}
		configPath = localPath
		cleanup = func() { os.Remove(localPath) }
	}

	if strings.HasSuffix(configPath, ".amx") {
		tmpDir, mainYaml, err := pack.UnpackBundle(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: unpack bundle %s: %v\n", configPath, err)
			return nil, nil, cleanup, exitRuntime
		}
		configPath = mainYaml
		prevCleanup := cleanup
		cleanup = func() {
			os.RemoveAll(tmpDir)
			prevCleanup()
		}
	}

	doc, err := config.LoadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, nil, cleanup, exitConfigParse
	}

	issues := validate.Document(doc, opts.Params)
	printErrors(issues)

	if validate.HasErrors(issues) {
		printWarnings(issues)
		return nil, issues, cleanup, exitValidation
	}
	if opts.Strict && validate.HasWarnings(issues) {
		printWarnings(issues)
		if opts.Strict && validate.HasWarnings(issues) && !validate.HasErrors(issues) {
			fmt.Fprintln(os.Stderr, "error: strict mode enabled; warnings treated as errors")
		}
		return nil, issues, cleanup, exitValidation
	}

	resolved, err := resolve.Project(doc, opts.Params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, issues, cleanup, exitValidation
	}

	return resolved, issues, cleanup, exitSuccess
}

func printErrors(issues []validate.Issue) {
	for _, issue := range issues {
		if issue.Severity == validate.SeverityError {
			fmt.Fprintf(os.Stderr, "error: %s\n", issue.Message)
		}
	}
}

func printWarnings(issues []validate.Issue) {
	for _, issue := range issues {
		if issue.Severity == validate.SeverityWarning {
			fmt.Fprintf(os.Stderr, "warning: %s\n", issue.Message)
		}
	}
}

func resolveScaleConfigForTools(session string) (resolve.Config, *toolruntime.Error) {
	configPath, err := tmux.SessionConfigPath(session, false)
	if err != nil {
		return resolve.Config{}, &toolruntime.Error{
			Code:     exitCodeName(exitRuntime),
			ExitCode: exitRuntime,
			Message:  err.Error(),
			Command:  "scale",
			Session:  session,
		}
	}
	if configPath == "" {
		return resolve.Config{}, &toolruntime.Error{
			Code:     exitCodeName(exitRuntime),
			ExitCode: exitRuntime,
			Message:  fmt.Sprintf("session %q has no config path metadata; run 'amx remember %s <config>' to fix this", session, session),
			Command:  "scale",
			Session:  session,
		}
	}

	doc, err := config.LoadFile(configPath)
	if err != nil {
		return resolve.Config{}, &toolruntime.Error{
			Code:     exitCodeName(exitConfigParse),
			ExitCode: exitConfigParse,
			Message:  err.Error(),
			Command:  "scale",
			Session:  session,
		}
	}

	issues := validate.Document(doc, nil)
	if validate.HasErrors(issues) {
		messages := make([]string, 0, len(issues))
		for _, issue := range issues {
			if issue.Severity == validate.SeverityError {
				messages = append(messages, issue.Message)
			}
		}
		if len(messages) == 0 {
			messages = append(messages, "config validation failed")
		}
		return resolve.Config{}, &toolruntime.Error{
			Code:     exitCodeName(exitValidation),
			ExitCode: exitValidation,
			Message:  strings.Join(messages, "; "),
			Command:  "scale",
			Session:  session,
		}
	}

	resolved, err := resolve.Project(doc, nil)
	if err != nil {
		return resolve.Config{}, &toolruntime.Error{
			Code:     exitCodeName(exitValidation),
			ExitCode: exitValidation,
			Message:  err.Error(),
			Command:  "scale",
			Session:  session,
		}
	}
	return *resolved, nil
}

func preflightProviders(resolved resolve.Config) bool {
	reports := make([]provider.HealthReport, 0)
	for _, name := range collectNativeProviders(resolved) {
		report := provider.CheckCLI(name)
		reports = append(reports, report)
	}

	ok := true
	for _, report := range reports {
		for _, issue := range report.Issues {
			line := fmt.Sprintf("provider %q: %s", report.Spec.ID, issue.Message)
			if issue.Severity == "error" {
				fmt.Fprintf(os.Stderr, "error: %s\n", line)
				ok = false
				continue
			}
			fmt.Fprintf(os.Stderr, "warning: %s\n", line)
		}
	}
	return ok
}

func collectNativeProviders(resolved resolve.Config) []string {
	seen := make(map[string]struct{})
	add := func(name string) {
		name = provider.Normalize(name)
		if name == "" || !provider.UsesNativeIntegration(name) {
			return
		}
		seen[name] = struct{}{}
	}

	for _, agent := range resolved.Agents {
		add(agent.Provider)
	}
	for _, window := range resolved.Windows {
		for _, pane := range window.Panes {
			add(pane.Provider)
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func launchProfileForRender(name string) provider.LaunchProfile {
	return provider.ResolveLaunchWith(name, exec.LookPath, func(path string, args ...string) (string, error) {
		cmd := exec.Command(path, args...)
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		return buf.String(), err
	})
}

func renderPlan(resolved resolve.Config, attach bool, replace bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "plan:\n")
	fmt.Fprintf(&b, "  session: %s\n", resolved.Session)
	if replace {
		fmt.Fprintf(&b, "  replace_existing: true\n")
	}
	if resolved.Hooks.PreSession != "" {
		fmt.Fprintf(&b, "  pre_session: %s\n", resolved.Hooks.PreSession)
	}
	for _, window := range resolved.Windows {
		fmt.Fprintf(&b, "  window %s", window.Name)
		if window.Root != "" {
			fmt.Fprintf(&b, " (root=%s)", window.Root)
		}
		if window.Layout != "" {
			fmt.Fprintf(&b, " layout=%s", window.Layout)
			if window.Layout == "grid" && window.GridRows > 0 && window.GridColumns > 0 {
				fmt.Fprintf(&b, " %dx%d", window.GridRows, window.GridColumns)
			}
		}
		fmt.Fprintln(&b)

		for idx, pane := range window.Panes {
			fmt.Fprintf(&b, "    pane %d: %s", idx+1, pane.SourceType)
			if pane.AgentName != "" {
				fmt.Fprintf(&b, "=%s", pane.AgentName)
			}
			if pane.Name != "" {
				fmt.Fprintf(&b, " name=%q", pane.Name)
			}
			fmt.Fprintf(&b, " title=%q", pane.Title)
			if pane.Cwd != "" {
				fmt.Fprintf(&b, " cwd=%s", pane.Cwd)
			}
			if len(pane.Env) > 0 {
				keys := make([]string, 0, len(pane.Env))
				for key := range pane.Env {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				fmt.Fprintf(&b, " env=%s", strings.Join(keys, ","))
			}
			if pane.OnExit != "" {
				fmt.Fprintf(&b, " on_exit=%s", pane.OnExit)
			}
			if pane.Provider != "" {
				fmt.Fprintf(&b, " provider=%s", pane.Provider)
			}
			if pane.Model != "" {
				fmt.Fprintf(&b, " model=%s", pane.Model)
			}
			if pane.ReasoningEffort != "" {
				fmt.Fprintf(&b, " reasoning_effort=%s", pane.ReasoningEffort)
			}
			if profile := launchProfileForRender(pane.Provider); profile.Mode != "" {
				fmt.Fprintf(&b, " integration=%s", profile.Mode)
				if profile.PersonaChannel != "" {
					fmt.Fprintf(&b, " persona_channel=%s", profile.PersonaChannel)
				}
				if profile.PersonaSource != "" && pane.Persona != "" {
					fmt.Fprintf(&b, " persona_source=%s", profile.PersonaSource)
				}
				if profile.ToolChannel != "" {
					fmt.Fprintf(&b, " tool_channel=%s", profile.ToolChannel)
				}
				if profile.PreferredInput != "" {
					fmt.Fprintf(&b, " tool_input=%s", profile.PreferredInput)
				}
				if profile.FallbackReason != "" {
					fmt.Fprintf(&b, " integration_fallback=%q", profile.FallbackReason)
				}
			}
			if runtime := tmux.DescribeProviderRuntime(resolved.Session, pane); runtime.PersonaArtifact != "" {
				fmt.Fprintf(&b, " persona_artifact=%s", runtime.PersonaArtifact)
			}
			if pane.Focus {
				fmt.Fprintf(&b, " focus=true")
			}
			fmt.Fprintf(&b, "\n      command: %s\n", tmux.BuildPaneCommand(resolved.Session, pane))
		}
	}
	if resolved.Hooks.PostSession != "" {
		fmt.Fprintf(&b, "  post_session: %s\n", resolved.Hooks.PostSession)
	}
	if attach {
		fmt.Fprintf(&b, "  attach: true\n")
	}
	return b.String()
}

func renderStatus(session string, panes []tmux.PaneStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "status:\n")
	fmt.Fprintf(&b, "  session: %s\n", session)

	currentWindow := -1
	for _, pane := range panes {
		if pane.WindowIndex != currentWindow {
			currentWindow = pane.WindowIndex
			fmt.Fprintf(&b, "  window %d %s", pane.WindowIndex, pane.WindowName)
			if pane.WindowActive {
				fmt.Fprintf(&b, " [active]")
			}
			fmt.Fprintln(&b)
		}

		fmt.Fprintf(&b, "    pane %d", pane.PaneIndex)
		if pane.Name != "" {
			fmt.Fprintf(&b, " %s", pane.Name)
		}
		fmt.Fprintf(&b, " id=%s", pane.PaneID)

		if pane.Dead {
			fmt.Fprintf(&b, " [DEAD (status %d)]", pane.ExitStatus)
		} else {
			fmt.Fprintf(&b, " [RUNNING (pid %d)]", pane.Pid)
		}

		if pane.PaneActive {
			fmt.Fprintf(&b, " [active]")
		}
		fmt.Fprintln(&b)

		fmt.Fprintf(&b, "      title: %q\n", pane.Title)
		if pane.SourceType != "" {
			fmt.Fprintf(&b, "      source: %s", pane.SourceType)
			if pane.AgentName != "" {
				fmt.Fprintf(&b, " (%s)", pane.AgentName)
			}
			if pane.Provider != "" {
				fmt.Fprintf(&b, " provider=%s", pane.Provider)
				if pane.IntegrationMode != "" {
					fmt.Fprintf(&b, " integration=%s", pane.IntegrationMode)
				}
				if pane.PersonaChannel != "" {
					fmt.Fprintf(&b, " persona_channel=%s", pane.PersonaChannel)
				}
				if pane.PersonaSource != "" {
					fmt.Fprintf(&b, " persona_source=%s", pane.PersonaSource)
				}
				if pane.PersonaArtifact != "" {
					fmt.Fprintf(&b, " persona_artifact=%s", pane.PersonaArtifact)
				}
				if pane.ToolChannel != "" {
					fmt.Fprintf(&b, " tool_channel=%s", pane.ToolChannel)
				}
				if pane.ToolInput != "" {
					fmt.Fprintf(&b, " tool_input=%s", pane.ToolInput)
				}
				if pane.IntegrationFallback != "" {
					fmt.Fprintf(&b, " integration_fallback=%q", pane.IntegrationFallback)
				}
			}
			if pane.Model != "" {
				fmt.Fprintf(&b, " model=%s", pane.Model)
			}
			if pane.ReasoningEffort != "" {
				fmt.Fprintf(&b, " reasoning_effort=%s", pane.ReasoningEffort)
			}
			fmt.Fprintln(&b)
		}
		if pane.OnExit != "" {
			fmt.Fprintf(&b, "      on_exit: %s\n", pane.OnExit)
		}
		if pane.Dead && pane.StartupCmd != "" {
			fmt.Fprintf(&b, "      startup: %s\n", pane.StartupCmd)
		} else if pane.CurrentCmd != "" {
			fmt.Fprintf(&b, "      command: %s\n", pane.CurrentCmd)
		}
	}

	return b.String()
}

func renderInspect(session string, pane tmux.PaneStatus, logs string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "inspect:\n")
	fmt.Fprintf(&b, "  session: %s\n", session)
	fmt.Fprintf(&b, "  pane:    %s\n", pane.Name)
	fmt.Fprintf(&b, "  id:      %s\n", pane.PaneID)
	fmt.Fprintf(&b, "  window:  %d (%s)\n", pane.WindowIndex, pane.WindowName)
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "  state:   ")
	if pane.Dead {
		fmt.Fprintf(&b, "DEAD (exit code %d)\n", pane.ExitStatus)
	} else {
		fmt.Fprintf(&b, "RUNNING (pid %d, cpu %.1f%%, mem %.1f%%)\n", pane.Pid, pane.CPU, pane.Memory)
	}

	if pane.StartTime != "" {
		fmt.Fprintf(&b, "  started: %s\n", pane.StartTime)
	}
	fmt.Fprintf(&b, "  active:  %v\n", pane.PaneActive)
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "  config:\n")
	fmt.Fprintf(&b, "    title:   %q\n", pane.Title)
	fmt.Fprintf(&b, "    source:  %s", pane.SourceType)
	if pane.AgentName != "" {
		fmt.Fprintf(&b, " (%s)", pane.AgentName)
	}
	fmt.Fprintf(&b, "\n")
	if pane.Provider != "" {
		fmt.Fprintf(&b, "    provider: %s\n", pane.Provider)
	}
	if pane.Model != "" {
		fmt.Fprintf(&b, "    model:    %s\n", pane.Model)
	}
	if pane.IntegrationMode != "" {
		fmt.Fprintf(&b, "    integration: %s\n", pane.IntegrationMode)
	}
	if pane.PersonaChannel != "" {
		fmt.Fprintf(&b, "    persona_channel: %s\n", pane.PersonaChannel)
	}
	if pane.PersonaSource != "" {
		fmt.Fprintf(&b, "    persona_source: %s\n", pane.PersonaSource)
	}
	if pane.PersonaArtifact != "" {
		fmt.Fprintf(&b, "    persona_artifact: %s\n", pane.PersonaArtifact)
	}
	if pane.ToolChannel != "" {
		fmt.Fprintf(&b, "    tool_channel: %s\n", pane.ToolChannel)
	}
	if pane.ToolInput != "" {
		fmt.Fprintf(&b, "    tool_input: %s\n", pane.ToolInput)
	}
	if pane.IntegrationFallback != "" {
		fmt.Fprintf(&b, "    integration_fallback: %s\n", pane.IntegrationFallback)
	}
	if pane.ReasoningEffort != "" {
		fmt.Fprintf(&b, "    reasoning_effort: %s\n", pane.ReasoningEffort)
	}
	fmt.Fprintf(&b, "    on_exit: %s\n", pane.OnExit)
	fmt.Fprintf(&b, "\n")

	if pane.Dead {
		fmt.Fprintf(&b, "  startup:\n    %s\n", pane.StartupCmd)
	} else {
		fmt.Fprintf(&b, "  command:\n    %s\n", pane.CurrentCmd)
	}
	fmt.Fprintf(&b, "\n")

	if strings.TrimSpace(logs) != "" {
		fmt.Fprintf(&b, "  logs:\n")
		lines := strings.Split(strings.TrimSpace(logs), "\n")
		for _, line := range lines {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}

	return b.String()
}

func renderInspectJSON(session string, pane tmux.PaneStatus, logs string) (string, error) {
	return renderJSON(struct {
		SchemaVersion int             `json:"schema_version"`
		Kind          string          `json:"kind"`
		Session       string          `json:"session"`
		Pane          tmux.PaneStatus `json:"pane"`
		Logs          string          `json:"logs"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          "inspect",
		Session:       session,
		Pane:          pane,
		Logs:          logs,
	})
}

func renderKVJSON(session string, key string, value string) (string, error) {
	return renderJSON(struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Session       string `json:"session"`
		Key           string `json:"key"`
		Value         string `json:"value"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          "kv-get",
		Session:       session,
		Key:           key,
		Value:         value,
	})
}

func renderKVListJSON(session string, kv map[string]string) (string, error) {
	return renderJSON(struct {
		SchemaVersion int               `json:"schema_version"`
		Kind          string            `json:"kind"`
		Session       string            `json:"session"`
		KV            map[string]string `json:"kv"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          "kv-list",
		Session:       session,
		KV:            kv,
	})
}

func renderKVActionJSON(kind string, session string, key string, value string, message string) (string, error) {
	return renderJSON(struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Session       string `json:"session"`
		Key           string `json:"key"`
		Value         string `json:"value,omitempty"`
		Message       string `json:"message"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          kind,
		Session:       session,
		Key:           key,
		Value:         value,
		Message:       message,
	})
}

func renderWaitJSON(session string, pane string, pattern string, scope string, match string) (string, error) {
	return renderJSON(struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Session       string `json:"session"`
		Pane          string `json:"pane"`
		Pattern       string `json:"pattern"`
		Scope         string `json:"scope"`
		Match         string `json:"match"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          "wait",
		Session:       session,
		Pane:          pane,
		Pattern:       pattern,
		Scope:         scope,
		Match:         match,
	})
}

func renderScaleJSON(session string, agent string, count int, message string) (string, error) {
	return renderJSON(struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Session       string `json:"session"`
		Agent         string `json:"agent"`
		Count         int    `json:"count"`
		Message       string `json:"message"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          "scale",
		Session:       session,
		Agent:         agent,
		Count:         count,
		Message:       message,
	})
}

func renderStatusJSON(session string, pane string, panes []tmux.PaneStatus) (string, error) {
	return renderJSON(struct {
		SchemaVersion int               `json:"schema_version"`
		Kind          string            `json:"kind"`
		Session       string            `json:"session"`
		Pane          string            `json:"pane,omitempty"`
		Panes         []tmux.PaneStatus `json:"panes"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          "status",
		Session:       session,
		Pane:          pane,
		Panes:         panes,
	})
}

func renderCaptureJSON(session string, pane string, tailLines int, stripEmpty bool, content string) (string, error) {
	return renderJSON(struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Session       string `json:"session"`
		Pane          string `json:"pane"`
		TailLines     int    `json:"tail_lines,omitempty"`
		StripEmpty    bool   `json:"strip_empty,omitempty"`
		Content       string `json:"content"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          "capture",
		Session:       session,
		Pane:          pane,
		TailLines:     tailLines,
		StripEmpty:    stripEmpty,
		Content:       content,
	})
}

func renderActionJSON(kind string, session string, pane string, message string, noEnter bool) (string, error) {
	return renderJSON(struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Session       string `json:"session"`
		Pane          string `json:"pane,omitempty"`
		Message       string `json:"message,omitempty"`
		NoEnter       bool   `json:"no_enter,omitempty"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          kind,
		Session:       session,
		Pane:          pane,
		Message:       message,
		NoEnter:       noEnter,
	})
}

func renderErrorJSON(command string, exitCode int, message string, session string, pane string) (string, error) {
	return renderJSON(struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Command       string `json:"command"`
		ErrorCode     string `json:"error_code"`
		ExitCode      int    `json:"exit_code"`
		Message       string `json:"message"`
		Session       string `json:"session,omitempty"`
		Pane          string `json:"pane,omitempty"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          "error",
		Command:       command,
		ErrorCode:     exitCodeName(exitCode),
		ExitCode:      exitCode,
		Message:       message,
		Session:       session,
		Pane:          pane,
	})
}

func renderJSON(v any) (string, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}

func printCommandError(command string, jsonOut bool, exitCode int, err error, session string, pane string) int {
	if jsonOut {
		out, renderErr := renderErrorJSON(command, exitCode, err.Error(), session, pane)
		if renderErr == nil {
			fmt.Print(out)
			return exitCode
		}
		fmt.Fprintf(os.Stderr, "error: render %s error json: %v\n", command, renderErr)
	}
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	if command == "status" {
		hint := []string{"amx", "sessions"}
		if session != "" {
			hint = append(hint, "--session", session)
		}
		fmt.Fprintln(os.Stderr, "This status command inspects legacy pane layouts. For an everyday workspace use:", managedlaunch.ShellCommand(hint))
	}
	return exitCode
}

func exitCodeName(code int) string {
	switch code {
	case exitSuccess:
		return "success"
	case exitRuntime:
		return "runtime"
	case exitValidation:
		return "validation"
	case exitConfigParse:
		return "config_parse"
	case exitDependency:
		return "dependency"
	default:
		return "unknown"
	}
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func versionString() string {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" && commit == "none" {
					commit = setting.Value
				}
				if setting.Key == "vcs.time" && date == "unknown" {
					date = setting.Value
				}
			}
			if info.Main.Version != "" && info.Main.Version != "(devel)" {
				version = info.Main.Version
			}
		}
	}

	if commit != "none" {
		return fmt.Sprintf("amx %s (commit: %s, built: %s)", version, commit, date)
	}
	return fmt.Sprintf("amx %s", version)
}

func requireTMux() error {
	if _, err := exec.LookPath(tmux.Binary()); err != nil {
		return fmt.Errorf("%s is not installed or not in PATH", tmux.Binary())
	}
	return nil
}

func resolveEditTarget(target string) (string, error) {
	expanded := expandUserPath(target)
	if pathLikeTarget(target) {
		absPath, err := filepath.Abs(expanded)
		if err != nil {
			return "", fmt.Errorf("resolve edit target %q: %w", target, err)
		}
		if info, err := os.Stat(absPath); err == nil && info.IsDir() {
			return "", fmt.Errorf("edit target %q is a directory", absPath)
		} else if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect edit target %q: %w", absPath, err)
		}
		return absPath, nil
	}

	if info, err := os.Stat(expanded); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("edit target %q is a directory", expanded)
		}
		absPath, err := filepath.Abs(expanded)
		if err != nil {
			return "", fmt.Errorf("resolve edit target %q: %w", target, err)
		}
		return absPath, nil
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect edit target %q: %w", expanded, err)
	}

	if err := requireTMux(); err != nil {
		return "", fmt.Errorf("edit target %q is not a file path, and tmux is unavailable to resolve it as a session", target)
	}

	path, err := tmux.SessionConfigPath(target, false)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("session %q is missing config path metadata; use `amx remember %s ./path/to/config.yaml`, `amx edit ./path/to/config.yaml`, or reload the session from a config path first", target, target)
	}
	return path, nil
}

func resolveExistingConfigPath(target string) (string, error) {
	expanded := expandUserPath(target)
	absPath, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve config path %q: %w", target, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("config path %q does not exist", absPath)
		}
		return "", fmt.Errorf("inspect config path %q: %w", absPath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("config path %q is a directory", absPath)
	}
	return absPath, nil
}

func editorInvocation(path string) (string, string, error) {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		editor = "vi"
	}

	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/sh"
	}
	if _, err := exec.LookPath(shell); err != nil {
		return "", "", fmt.Errorf("shell %q is not installed or not in PATH", shell)
	}
	editorFields := strings.Fields(editor)
	if len(editorFields) == 0 {
		return "", "", fmt.Errorf("editor command is empty")
	}
	if _, err := exec.LookPath(editorFields[0]); err != nil {
		return "", "", fmt.Errorf("editor %q is not installed or not in PATH", editorFields[0])
	}

	command := editor + " " + shellQuote(path)
	return shell, command, nil
}

func pathLikeTarget(target string) bool {
	if strings.HasPrefix(target, "~") || strings.HasPrefix(target, ".") {
		return true
	}
	if strings.Contains(target, string(os.PathSeparator)) {
		return true
	}
	ext := strings.ToLower(filepath.Ext(target))
	return ext == ".yaml" || ext == ".yml"
}

func expandUserPath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
