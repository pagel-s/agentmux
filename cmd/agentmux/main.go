package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"agentmux/internal/cli"
	"agentmux/internal/config"
	"agentmux/internal/hooks"
	"agentmux/internal/resolve"
	"agentmux/internal/tmux"
	"agentmux/internal/validate"
)

const (
	exitSuccess     = 0
	exitRuntime     = 1
	exitValidation  = 2
	exitConfigParse = 3
	exitDependency  = 4
)

var version = "dev"

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
	case "validate":
		return runValidate(args[1:])
	case "render":
		return runRender(args[1:])
	case "list":
		return runList(args[1:])
	case "status":
		return runStatus(args[1:])
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

	if err := tmux.Execute(*resolved, tmux.Options{
		Attach:  opts.Attach,
		Replace: replace,
		Verbose: opts.Verbose,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
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
	opts, err := cli.ParseStatus(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	if err := requireTMux(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitDependency
	}

	panes, err := tmux.Status(opts.Session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	if opts.JSON {
		out, err := renderStatusJSON(opts.Session, panes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: render status json: %v\n", err)
			return exitRuntime
		}
		fmt.Print(out)
		return exitSuccess
	}

	fmt.Print(renderStatus(opts.Session, panes))
	return exitSuccess
}

func runFocus(args []string) int {
	opts, err := cli.ParseFocus(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	if err := requireTMux(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitDependency
	}

	if err := tmux.FocusPaneByName(opts.Session, opts.Pane, false); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	fmt.Printf("focused pane %q in session %q\n", opts.Pane, opts.Session)
	return exitSuccess
}

func runRestart(args []string) int {
	opts, err := cli.ParseRestart(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	if err := requireTMux(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitDependency
	}

	if err := tmux.RestartPaneByName(opts.Session, opts.Pane, false); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	fmt.Printf("restarted pane %q in session %q\n", opts.Pane, opts.Session)
	return exitSuccess
}

func runRerun(args []string) int {
	opts, err := cli.ParseRerun(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	if err := requireTMux(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitDependency
	}

	if err := tmux.RerunPaneByName(opts.Session, opts.Pane, false); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	fmt.Printf("reran pane %q in session %q\n", opts.Pane, opts.Session)
	return exitSuccess
}

func runStop(args []string) int {
	opts, err := cli.ParseStop(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	if err := requireTMux(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitDependency
	}

	if err := tmux.StopPaneByName(opts.Session, opts.Pane, false); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	fmt.Printf("stopped pane %q in session %q\n", opts.Pane, opts.Session)
	return exitSuccess
}

func runKill(args []string) int {
	opts, err := cli.ParseKill(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	if err := requireTMux(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitDependency
	}

	if err := tmux.KillPaneByName(opts.Session, opts.Pane, false); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	fmt.Printf("killed pane %q in session %q\n", opts.Pane, opts.Session)
	return exitSuccess
}

func runCapture(args []string) int {
	opts, err := cli.ParseCapture(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	if err := requireTMux(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitDependency
	}

	out, err := tmux.CapturePaneByName(opts.Session, opts.Pane, opts.Tail, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	if opts.JSON {
		jsonOut, err := renderCaptureJSON(opts.Session, opts.Pane, opts.Tail, out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: render capture json: %v\n", err)
			return exitRuntime
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
	opts, err := cli.ParseSend(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitValidation
	}

	if err := requireTMux(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitDependency
	}

	if err := tmux.SendCommandToPaneByName(opts.Session, opts.Pane, opts.Command, !opts.NoEnter, false); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitRuntime
	}

	if opts.NoEnter {
		fmt.Printf("sent command text to pane %q in session %q without pressing Enter\n", opts.Pane, opts.Session)
	} else {
		fmt.Printf("sent command to pane %q in session %q\n", opts.Pane, opts.Session)
	}
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

	tmuxVersion := "not found"
	tmuxErr := error(nil)
	if path, err := exec.LookPath(tmux.Binary()); err == nil {
		tmuxVersion = path
		cmd := exec.Command(tmux.Binary(), "-V")
		out, err := cmd.Output()
		if err == nil {
			tmuxVersion = strings.TrimSpace(string(out))
		} else {
			tmuxErr = err
		}
	} else {
		tmuxErr = err
	}

	fmt.Printf("shell: %s\n", shell)
	fmt.Printf("agentmux: %s\n", versionString())
	fmt.Printf("tmux_binary: %s\n", tmux.Binary())
	if tmux.Socket() != "" {
		fmt.Printf("tmux_socket: %s\n", tmux.Socket())
	}
	fmt.Printf("tmux: %s\n", tmuxVersion)
	if tmuxErr != nil {
		fmt.Printf("tmux_status: %v\n", tmuxErr)
		return exitDependency
	}
	fmt.Printf("cwd: %s\n", mustGetwd())

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
				fmt.Fprintf(&b, " active=true")
			}
			fmt.Fprintln(&b)
		}

		fmt.Fprintf(&b, "    pane %d", pane.PaneIndex)
		if pane.Name != "" {
			fmt.Fprintf(&b, " name=%q", pane.Name)
		}
		fmt.Fprintf(&b, " title=%q id=%s", pane.Title, pane.PaneID)
		if pane.SourceType != "" {
			fmt.Fprintf(&b, " source=%s", pane.SourceType)
		}
		if pane.AgentName != "" {
			fmt.Fprintf(&b, " agent=%q", pane.AgentName)
		}
		if pane.OnExit != "" {
			fmt.Fprintf(&b, " on_exit=%q", pane.OnExit)
		}
		if pane.CurrentCmd != "" {
			fmt.Fprintf(&b, " current=%q", pane.CurrentCmd)
		}
		if pane.PaneActive {
			fmt.Fprintf(&b, " active=true")
		}
		fmt.Fprintln(&b)
	}

	return b.String()
}

func renderStatusJSON(session string, panes []tmux.PaneStatus) (string, error) {
	return renderJSON(struct {
		Session string            `json:"session"`
		Panes   []tmux.PaneStatus `json:"panes"`
	}{
		Session: session,
		Panes:   panes,
	})
}

func renderCaptureJSON(session string, pane string, tailLines int, content string) (string, error) {
	return renderJSON(struct {
		Session   string `json:"session"`
		Pane      string `json:"pane"`
		TailLines int    `json:"tail_lines,omitempty"`
		Content   string `json:"content"`
	}{
		Session:   session,
		Pane:      pane,
		TailLines: tailLines,
		Content:   content,
	})
}

func renderJSON(v any) (string, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out) + "\n", nil
}

func versionString() string {
	return fmt.Sprintf("agentmux %s", version)
}

func requireTMux() error {
	if _, err := exec.LookPath(tmux.Binary()); err != nil {
		return fmt.Errorf("%s is not installed or not in PATH", tmux.Binary())
	}
	return nil
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
  agentmux load <config> [--param key=value] [--attach|--no-attach] [--dry-run] [--replace] [--strict] [--verbose]
  agentmux reload <config> [--param key=value] [--attach|--no-attach] [--dry-run] [--strict] [--verbose]
  agentmux validate <config> [--param key=value] [--strict]
  agentmux render <config> [--param key=value] [--strict]
  agentmux list
  agentmux status <session> [--json]
  agentmux focus <session> <pane-name>
  agentmux restart <session> <pane-name>
  agentmux rerun <session> <pane-name>
  agentmux stop <session> <pane-name>
  agentmux kill <session> <pane-name>
  agentmux capture <session> <pane-name> [--json] [--tail N]
  agentmux send <session> <pane-name> [--no-enter] <command...>
  agentmux doctor
  agentmux version`)
}
