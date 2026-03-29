package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"agentmux/internal/cli"
	"agentmux/internal/config"
	"agentmux/internal/hooks"
	"agentmux/internal/resolve"
	"agentmux/internal/scaffold"
	"agentmux/internal/tmux"
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
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return exitValidation
	}

	switch args[0] {
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
	case "validate":
		return runValidate(args[1:])
	case "render":
		return runRender(args[1:])
	case "list":
		return runList(args[1:])
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
		return runSend(args[1:])
	case "doctor":
		return runDoctor(args[1:])
	case "version", "-v", "--version":
		fmt.Println(versionString())
		return exitSuccess
	case "help", "-h", "--help":
		printUsage()
		return exitSuccess
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
	fmt.Println("  agentmux init default my-workflow.yaml")
	fmt.Println("  agentmux init coding-starter coding.yaml")
	fmt.Println("  agentmux init minimal scratch.yaml")
	return exitSuccess
}

func runInit(args []string) int {
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
	fmt.Printf("  agentmux validate %s\n", path)
	fmt.Printf("  agentmux render %s\n", path)
	fmt.Printf("  agentmux load %s --replace --no-attach\n", path)
	fmt.Printf("  agentmux edit %s\n", path)
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

func runSessionLaunch(mode string, opts cli.LoadOptions, requireExisting bool, replace bool) int {

	resolved, warnings, code := resolveCommand(opts.CommonOptions)
	if code != exitSuccess {
		return code
	}
	printWarnings(warnings)

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
		fmt.Fprintf(os.Stderr, "error: %s requires existing session %q; use `agentmux load %s` to create it\n", mode, resolved.Session, opts.ConfigPath)
		return exitRuntime
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
	opts, err := cli.ParseValidate(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	_, warnings, code := resolveCommand(opts)
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

	resolved, warnings, code := resolveCommand(opts)
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
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "error: list does not accept arguments")
		return exitValidation
	}

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

func runStatus(args []string) int {
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

	if err := requireTMux(); err != nil {
		return printCommandError("wait", opts.JSON, exitDependency, err, opts.Session, opts.Pane)
	}

	match, err := tmux.WaitForPattern(opts.Session, opts.Pane, opts.Pattern, timeout, false)
	if err != nil {
		return printCommandError("wait", opts.JSON, exitRuntime, err, opts.Session, opts.Pane)
	}

	if opts.JSON {
		out, err := renderWaitJSON(opts.Session, opts.Pane, opts.Pattern, match)
		if err != nil {
			return printCommandError("wait", true, exitRuntime, fmt.Errorf("render wait json: %w", err), opts.Session, opts.Pane)
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Printf("matched pattern %q in pane %q: %s\n", opts.Pattern, opts.Pane, match)
	return exitSuccess
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
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "error: doctor does not accept arguments")
		return exitValidation
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	tmuxPath, _ := exec.LookPath(tmux.Binary())
	tmuxVersion := "not found"
	vNum := 0.0
	if tmuxPath != "" {
		cmd := exec.Command(tmux.Binary(), "-V")
		out, err := cmd.Output()
		if err == nil {
			tmuxVersion = strings.TrimSpace(string(out))
			// Simple version extraction for comparison (e.g., "tmux 3.2a" -> 3.2)
			parts := strings.Fields(tmuxVersion)
			if len(parts) >= 2 {
				fmt.Sscanf(parts[1], "%f", &vNum)
			}
		}
	}

	fmt.Printf("shell:         %s\n", shell)
	fmt.Printf("agentmux:      %s\n", versionString())
	fmt.Printf("tmux_binary:   %s\n", tmuxPath)
	fmt.Printf("tmux_version:  %s\n", tmuxVersion)

	if tmux.Socket() != "" {
		fmt.Printf("tmux_socket:   %s\n", tmux.Socket())
	}

	fmt.Printf("cwd:           %s\n", mustGetwd())

	// Check for local/global templates
	templates, err := scaffold.List()
	if err == nil {
		projectTemplates := 0
		globalTemplates := 0
		for _, t := range templates {
			if t.Source == scaffold.SourceRepo {
				projectTemplates++
			} else if t.Source == scaffold.SourceGlobal {
				globalTemplates++
			}
		}
		if projectTemplates > 0 {
			fmt.Printf("project_templates: %d found in .agentmux/templates/\n", projectTemplates)
		}
		if globalTemplates > 0 {
			fmt.Printf("global_templates:  %d found in ~/.config/agentmux/templates/\n", globalTemplates)
		}
	}

	fmt.Println("\nDiagnostics:")
	if tmuxPath == "" {
		fmt.Println("  [!] CRITICAL: tmux is not installed. agentmux requires tmux to function.")
	} else if vNum < 3.2 {
		fmt.Printf("  [!] WARNING: tmux version %s detected. 3.2 or newer is recommended for full pane title support.\n", tmuxVersion)
	} else {
		fmt.Println("  [✓] tmux version is optimal.")
	}

	selfPath, err := exec.LookPath("agentmux")
	if err != nil {
		fmt.Println("  [!] WARNING: agentmux is not in your PATH. You must call it via absolute path or add its directory to PATH.")
	} else {
		fmt.Printf("  [✓] agentmux is in PATH at %s\n", selfPath)
	}

	return exitSuccess
}

func resolveCommand(opts cli.CommonOptions) (*resolve.Config, []validate.Issue, int) {
	doc, err := config.LoadFile(opts.ConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, nil, exitConfigParse
	}

	issues := validate.Document(doc, opts.Params)
	printErrors(issues)

	if validate.HasErrors(issues) {
		printWarnings(issues)
		return nil, issues, exitValidation
	}
	if opts.Strict && validate.HasWarnings(issues) {
		printWarnings(issues)
		if opts.Strict && validate.HasWarnings(issues) && !validate.HasErrors(issues) {
			fmt.Fprintln(os.Stderr, "error: strict mode enabled; warnings treated as errors")
		}
		return nil, issues, exitValidation
	}

	resolved, err := resolve.Project(&doc.Config, opts.Params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return nil, issues, exitValidation
	}

	return resolved, issues, exitSuccess
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
			if pane.Focus {
				fmt.Fprintf(&b, " focus=true")
			}
			fmt.Fprintf(&b, "\n      command: %s\n", pane.Command)
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

func renderWaitJSON(session string, pane string, pattern string, match string) (string, error) {
	return renderJSON(struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Session       string `json:"session"`
		Pane          string `json:"pane"`
		Pattern       string `json:"pattern"`
		Match         string `json:"match"`
	}{
		SchemaVersion: jsonSchemaV1,
		Kind:          "wait",
		Session:       session,
		Pane:          pane,
		Pattern:       pattern,
		Match:         match,
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
		return fmt.Sprintf("agentmux %s (commit: %s, built: %s)", version, commit, date)
	}
	return fmt.Sprintf("agentmux %s", version)
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
		return "", fmt.Errorf("session %q is missing config path metadata; use `agentmux remember %s ./path/to/config.yaml`, `agentmux edit ./path/to/config.yaml`, or reload the session from a config path first", target, target)
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

func printUsage() {
	fmt.Println(`agentmux

Usage:
  agentmux templates
  agentmux init <template> <path> [--force]
  agentmux edit <path-or-session>
  agentmux remember <session> <config>
  agentmux load <config> [--param key=value] [--attach|--no-attach] [--dry-run] [--replace] [--strict] [--verbose]
  agentmux reload <config> [--param key=value] [--attach|--no-attach] [--dry-run] [--strict] [--verbose]
  agentmux validate <config> [--param key=value] [--strict]
  agentmux render <config> [--param key=value] [--strict]
  agentmux list
  agentmux status <session> [--json] [--pane <pane-name>] [--logs [N]]
  agentmux inspect <session> <pane-name> [--json]
  agentmux kv-set <session> <key> <value> [--json]
  agentmux kv-get <session> <key> [--json]
  agentmux kv-list <session> [--json]
  agentmux kv-del <session> <key> [--json]
  agentmux wait <session> <pane> <pattern> [--timeout <duration>] [--json]
  agentmux focus <session> <pane-name> [--json]
  agentmux restart <session> <pane-name> [--json]
  agentmux rerun <session> <pane-name> [--json]
  agentmux stop <session> <pane-name> [--json]
  agentmux kill <session> <pane-name> [--json]
  agentmux capture <session> <pane-name> [--json] [--tail N] [--strip-empty]
  agentmux send <session> <pane-name> [--json] [--no-enter] <command...>
  agentmux doctor
  agentmux version`)

	fmt.Println(`
Starter flow:
  agentmux templates
  agentmux init default my-workflow.yaml
  agentmux init coding-starter coding.yaml
  agentmux validate my-workflow.yaml
  agentmux load my-workflow.yaml --replace --no-attach`)
}
