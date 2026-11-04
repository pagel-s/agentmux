package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentmux/internal/managed"
	"agentmux/internal/managedconfig"
	"agentmux/internal/managedlaunch"
)

type workOptions struct {
	state, session, config, project, sandbox string
	group, board                             string
	mode, to, resume, legacyPolicy           string
	nativeLauncher                           string
	detach                                   bool
	sandboxExplicit                          bool
	args                                     []string
	view                                     string
	popup                                    bool
	selectionSource                          string
}

func hasWorkSessionFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--state" || arg == "--session" || arg == "--view" || arg == "--popup" || arg == "--mode" || strings.HasPrefix(arg, "--mode=") || strings.HasPrefix(arg, "--view=") || strings.HasPrefix(arg, "--state=") || strings.HasPrefix(arg, "--session=") {
			return true
		}
	}
	return false
}

// A single recipient before -- is the human message form. Two positionals
// before -- remain the legacy SESSION PANE raw-input form.
func workSendRequested(args []string) bool {
	return len(args) == 2 || hasWorkSessionFlag(args) || workHelpRequested(args) || (len(args) == 3 && args[1] == "--")
}
func defaultWorkState(session string) (string, error) {
	if !managedconfig.ValidID(session) {
		return "", errors.New("invalid session name")
	}
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("XDG_STATE_HOME must be absolute")
	}
	return filepath.Join(root, "agentmux", session), nil
}
func parseWorkOptions(args []string) (workOptions, error) {
	return parseWorkOptionsWithStorage(args, false)
}

// Starting a workspace may name it and choose its storage location together.
// Selection commands require one selector so a stale copied flag cannot route
// an action to a different workspace.
func parseWorkOptionsWithStorage(args []string, allowNamedState bool) (workOptions, error) {
	o := workOptions{session: "work", sandbox: "local"}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			o.args = append(o.args, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") {
			o.args = append(o.args, arg)
			continue
		}
		name, value, inline := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if seen[name] {
			return o, fmt.Errorf("duplicate --%s", name)
		}
		seen[name] = true
		if name == "detach" {
			if inline {
				return o, errors.New("--detach takes no value")
			}
			o.detach = true
			continue
		}
		if name == "popup" {
			if inline {
				return o, errors.New("--popup takes no value")
			}
			o.popup = true
			continue
		}
		if name != "state" && name != "session" && name != "config" && name != "project" && name != "sandbox" && name != "view" && name != "group" && name != "board" && name != "mode" && name != "to" && name != "resume" && name != "legacy-policy" && name != "native-launcher" {
			return o, fmt.Errorf("unknown flag --%s", name)
		}
		if !inline {
			i++
			if i >= len(args) {
				return o, fmt.Errorf("--%s needs a value", name)
			}
			value = args[i]
		}
		if value == "" {
			return o, fmt.Errorf("--%s needs a nonempty value", name)
		}
		switch name {
		case "native-launcher":
			if !filepath.IsAbs(value) {
				return o, errors.New("--native-launcher requires an absolute executable path")
			}
			o.nativeLauncher = value
		case "legacy-policy":
			if value != "preserve" && value != "group" {
				return o, errors.New("--legacy-policy must be preserve or group")
			}
			o.legacyPolicy = value
		case "mode":
			o.mode = value
		case "to":
			o.to = value
		case "resume":
			o.resume = value
		case "group":
			o.group = value
		case "board":
			o.board = value
		case "view":
			o.view = value
		case "state":
			o.state = value
		case "session":
			o.session = value
		case "config":
			o.config = value
		case "project":
			o.project = value
		case "sandbox":
			o.sandbox = value
			o.sandboxExplicit = true
		}
	}
	if !managedconfig.ValidID(o.session) {
		return o, errors.New("invalid session name")
	}
	if !allowNamedState && seen["session"] && seen["state"] {
		return o, errors.New("choose either --session or --state to select a workspace, not both; no action was taken")
	}
	return resolveWorkSelection(o, seen["session"], seen["state"], !allowNamedState)
}

func workCall(ctx context.Context, client *managed.Client, op string, args any) (json.RawMessage, error) {
	return workCallRequest(ctx, client, op, args, "")
}

func workCallRequest(ctx context.Context, client *managed.Client, op string, args any, requestID string) (json.RawMessage, error) {
	if requestID == "" {
		requestID = requestIDForWork()
	}
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	query, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, problem := client.Call(query, managed.Request{Operation: op, RequestID: requestID, Args: data})
	if problem != nil {
		return nil, problem
	}
	return raw, nil
}
func requestIDForWork() string {
	// Request IDs stay stable for a submitted UI operation. UI mutations are
	// never automatically retried on transport loss.
	return "human_" + rand.Text()
}

func runWorkSession(operation string, args []string, in io.Reader, out, diagnostics io.Writer) int {
	code := runWorkSessionInner(operation, args, in, out, diagnostics)
	if code == exitSuccess && (operation == "start" || operation == "attach") && !workHelpRequested(args) {
		if o, err := parseWorkOptionsWithStorage(args, operation == "start"); err == nil {
			if err := rememberWorkState(o.state); err != nil {
				fmt.Fprintln(diagnostics, "Workspace remains available, but could not update workspace inventory:", err)
			}
		}
	}
	return code
}

func runWorkSessionInner(operation string, args []string, in io.Reader, out, diagnostics io.Writer) int {
	if workHelpRequested(args) {
		fmt.Fprintln(out, workSessionUsage(operation))
		return exitSuccess
	}
	if err := validateWorkFlags(operation, args); err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitValidation
	}
	o, err := parseWorkOptionsWithStorage(args, operation == "start")
	if err == nil && o.legacyPolicy != "" && operation != "collaborate" {
		err = errors.New("--legacy-policy applies only to collaborate")
	}
	if err == nil && o.nativeLauncher != "" {
		err = errors.New("--native-launcher applies only to relaunch")
	}
	if err != nil {
		fmt.Fprintln(diagnostics, "error:", err)
		return exitValidation
	}
	fail := func(err error) int { fmt.Fprintln(diagnostics, "error:", err); return exitRuntime }
	ctx := context.Background()
	if operation == "start" {
		if len(o.args) != 0 {
			return fail(errors.New("start accepts flags only"))
		}
		if o.sandbox != "bubblewrap" && o.sandbox != "local" {
			return fail(errors.New("--sandbox must be bubblewrap or local"))
		}
		config := o.config
		if config == "" {
			config = o.state + ".yaml"
			if _, e := os.Lstat(config); os.IsNotExist(e) {
				if e = os.MkdirAll(filepath.Dir(config), 0700); e != nil {
					return fail(e)
				}
				project := o.project
				if project == "" {
					project = "."
				}
				project, e = filepath.Abs(expandUserPath(project))
				if e != nil {
					return fail(e)
				}
				var result bytes.Buffer
				code := runManagedSetupIO(ctx, []string{"--preset", "coding", "--session", o.session, "--project", project, "--sandbox", o.sandbox, "--output", config}, in, &result, diagnostics)
				if code != exitSuccess {
					fmt.Fprint(out, result.String())
					return code
				}
				fmt.Fprintf(out, "Created coding team %s in %s\n", o.session, project)
			} else if e != nil {
				return fail(e)
			} else {
				project := o.project
				if project == "" {
					project = "."
				}
				if e = checkSavedWorkProject(config, project); e != nil {
					return fail(e)
				}
			}
		}
		if runtime, e := managedlaunch.Snapshot(o.state); e == nil && runtime.State == "running" {
			exe, e := os.Executable()
			if e != nil {
				return fail(e)
			}
			launcher, e := managedlaunch.New(managedlaunch.Options{StateDir: o.state, Executable: exe})
			if e != nil {
				return fail(e)
			}
			live, e := launcher.Describe(ctx)
			if e != nil {
				return fail(e)
			}
			if live.State == "running" {
				if o.sandboxExplicit && o.sandbox != live.BrokerSandbox {
					return fail(fmt.Errorf("broker sandbox mismatch: requested %q, running workspace uses %q; choose a new workspace for the requested mode; no action was taken", o.sandbox, live.BrokerSandbox))
				}
				cfg, e := loadManagedConfig(config)
				if e != nil {
					return fail(e)
				}
				if e = launcher.CheckConfiguration(cfg); e != nil {
					return fail(e)
				}
				fmt.Fprintf(out, "Team %s is already running.\n", live.Session)
				if err := rememberWorkState(o.state); err != nil {
					fmt.Fprintln(diagnostics, "Could not update workspace inventory:", err)
				}
				if o.detach {
					return exitSuccess
				}
				return runWorkSession("attach", []string{"--state", o.state}, in, out, diagnostics)
			}
		}
		up := []string{"up", config, "--state", o.state, "--broker-sandbox", o.sandbox}
		if !o.detach {
			up = append(up, "--attach")
		}
		return runManagedIO(up, in, out, diagnostics)
	}
	if operation == "down" {
		if len(o.args) != 0 {
			return fail(errors.New("down accepts no positional arguments"))
		}
		return runManagedIO([]string{"down", "--state", o.state}, in, out, diagnostics)
	}
	if operation == "dashboard" {
		if len(o.args) != 0 {
			return fail(errors.New("use dashboard --session NAME for a managed work session"))
		}
		return runManagedConsoleView(o.state, o.view, o.popup, in, out, diagnostics)
	}
	if operation == "attach" {
		if len(o.args) > 1 {
			return fail(errors.New("attach accepts at most one agent ID"))
		}
		exe, e := os.Executable()
		if e != nil {
			return fail(e)
		}
		launcher, e := managedlaunch.New(managedlaunch.Options{StateDir: o.state, Executable: exe})
		if e != nil {
			return fail(e)
		}
		runtime, e := launcher.Describe(ctx)
		if e != nil {
			if os.IsNotExist(e) {
				return fail(fmt.Errorf("work session not found; start this workspace with %s, or select an existing --state / --session", managedlaunch.ShellCommand([]string{"amx", "start", "--session", o.session, "--state", o.state})))
			}
			return fail(e)
		}
		agent := ""
		if len(o.args) == 1 {
			agent = o.args[0]
		} else {
			agent, e = launcher.LastAgent(ctx, runtime)
			if e != nil {
				return fail(e)
			}
		}
		if strings.HasPrefix(os.Getenv("TMUX"), managedlaunch.SocketPath(o.state)+",") {
			if agent != "" {
				if e = launcher.Focus(ctx, runtime, agent); e != nil {
					return fail(e)
				}
			}
			return exitSuccess
		}
		if e = launcher.AttachAgent(ctx, runtime, agent, in, out, diagnostics); e != nil {
			return fail(e)
		}
		return exitSuccess
	}
	credential, err := managed.LoadCredential(managed.CredentialPath(o.state, ""))
	if err != nil {
		return fail(errors.New("work session not found; use amx start or select --state / --session"))
	}
	client := managed.NewClient(credential)
	fmt.Fprintf(out, "Workspace: %s\n", activityText(o.state, 300))
	if operation == "remove" || operation == "retire" || operation == "collaborate" || operation == "disconnect" || operation == "isolate" || operation == "label" {
		if err := workAgentOperation(operation, o, client, out); err != nil {
			return fail(err)
		}
		return exitSuccess
	}
	if operation == "inbox" {
		if len(o.args) != 0 {
			return fail(errors.New("inbox accepts no positional arguments"))
		}
		raw, e := workCall(ctx, client, "activity", map[string]any{"limit": 20})
		if e != nil {
			return fail(e)
		}
		var snapshot managedActivitySnapshot
		if e = json.Unmarshal(raw, &snapshot); e != nil {
			return fail(e)
		}
		fmt.Fprint(out, formatManagedActivity(snapshot))
		return exitSuccess
	}
	if operation == "board" {
		if len(o.args) != 1 {
			return fail(errors.New("board requires one quoted note"))
		}
		if o.board != "" && o.group != "" {
			return fail(errors.New("choose either --group or --board for the destination, not both; no message was posted"))
		}
		namespace := o.board
		if namespace == "" {
			raw, e := workCall(ctx, client, "control.get", map[string]any{})
			if e != nil {
				return fail(e)
			}
			var controls managed.ControlState
			if e = json.Unmarshal(raw, &controls); e != nil {
				return fail(e)
			}
			var active []managed.WorkspaceGroup
			for _, g := range controls.Groups {
				if g.State == "active" && len(g.Members) > 0 {
					active = append(active, g)
				}
			}
			if o.group != "" {
				for _, g := range active {
					if g.ID == o.group {
						namespace = g.Board
					}
				}
				if namespace == "" {
					return fail(errors.New("active group not found"))
				}
			} else if len(active) == 1 {
				namespace = active[0].Board
			} else if len(active) > 1 {
				return fail(errors.New("multiple collaboration groups; select --group ID or --board NAMESPACE"))
			} else {
				namespace = "team"
			}
		}
		_, e := workCall(ctx, client, "board.post", map[string]any{"board": namespace, "text": o.args[0]})
		if e != nil {
			return fail(e)
		}
		fmt.Fprintf(out, "Posted to board %s.\n", namespace)
		return exitSuccess
	}
	if operation == "send" || operation == "reply" {
		if len(o.args) != 2 {
			return fail(errors.New(operation + " requires a recipient/reference and one quoted message"))
		}
		target, text := o.args[0], o.args[1]
		replyTo := ""
		if strings.TrimSpace(text) == "" {
			return fail(errors.New("message text must not be empty; no message or instruction was queued"))
		}
		if operation == "reply" {
			replyTo = target
			if o.to != "" || o.board != "" {
				target = o.to
			} else {
				target, err = workReplyTarget(ctx, client, target)
				if err != nil {
					return fail(err)
				}
			}
		} else if o.to != "" || o.board != "" {
			return fail(errors.New("send uses its positional recipient; --to/--board select an explicit reply audience"))
		}
		id, kind, e := workSendToAudience(ctx, client, o.state, target, o.board, text, o.mode, replyTo)
		if e != nil {
			return fail(e)
		}
		audience := target
		if o.board != "" {
			audience = "board " + o.board
		}
		fmt.Fprintf(out, "%s queued for %s: %s\n", kind, audience, id)
		if kind == "Instruction" {
			printWorkTaskCommand(out, "Inspect", []string{"amx", "task", id, "--state", o.state})
		}
		if kind == "Message" {
			fmt.Fprintln(out, "Interactive agent: read its broker inbox, or talk directly with:", managedlaunch.ShellCommand([]string{"amx", "attach", target, "--state", o.state}))
		}
		if target != "" {
			if raw, err := workCall(ctx, client, "native.status", map[string]any{"agent": target}); err == nil {
				var v struct {
					Status managed.NativeStatus `json:"status"`
				}
				if json.Unmarshal(raw, &v) == nil {
					fmt.Fprintf(out, "Native receiver: %s · %s. %s\n", v.Status.Mode, v.Status.State, consoleContent(v.Status.Reason, 600))
					if kind == "Message" && !v.Status.Policy.Notes {
						fmt.Fprintln(out, "Automatic note delivery is off; this note stays in the broker inbox.")
					}
					if kind == "Instruction" && v.Status.Mode == "review" {
						printWorkTaskCommand(out, "Approve the exact item in Agents (Shift+A) or with", []string{"amx", "delivery", target, "--approve", id, "--state", o.state})
					}
				}
			}
		}
		return exitSuccess
	}
	return fail(errors.New("unknown work-session command"))
}

func workReplyTarget(ctx context.Context, client *managed.Client, id string) (string, error) {
	if strings.HasPrefix(id, "task_") {
		raw, err := workCall(ctx, client, "task.get", map[string]string{"id": id})
		if err != nil {
			return "", err
		}
		var result struct {
			Task managed.Task `json:"task"`
		}
		if err = json.Unmarshal(raw, &result); err != nil {
			return "", err
		}
		return result.Task.To, nil
	}
	raw, err := workCall(ctx, client, "message.get", map[string]string{"id": id})
	if err != nil {
		return "", err
	}
	var result struct {
		Message managed.Message `json:"message"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.Message.Board != "" {
		return "", fmt.Errorf("shared board messages need an explicit destination; use board --board %s with the same workspace selector, or send AGENT for a private reply", result.Message.Board)
	}
	if result.Message.From != "$owner" {
		return result.Message.From, nil
	}
	if len(result.Message.To) == 1 {
		return result.Message.To[0], nil
	}
	return "", errors.New("board/group messages need an explicit recipient; use send")
}
func workSend(ctx context.Context, client *managed.Client, state, target, text string) (string, string, error) {
	return workSendIntent(ctx, client, state, target, text, "auto", "")
}

// workSendIntent preserves the native-inbox default while allowing an owner to
// choose whether a new message is informational or an executable task.
func workSendIntent(ctx context.Context, client *managed.Client, state, target, text, mode, replyTo string) (string, string, error) {
	return workSendToAudience(ctx, client, state, target, "", text, mode, replyTo)
}

func workSendToAudience(ctx context.Context, client *managed.Client, state, target, board, text, mode, replyTo string) (string, string, error) {
	if strings.TrimSpace(text) == "" {
		return "", "", errors.New("message text must not be empty")
	}
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "note" && mode != "instruction" {
		return "", "", errors.New("--mode must be auto, note, or instruction")
	}
	if board != "" {
		if target != "" || mode == "instruction" {
			return "", "", errors.New("board replies require no direct recipient and cannot be instructions")
		}
		raw, err := workCall(ctx, client, "board.post", map[string]any{"board": board, "text": text, "reply_to": replyTo})
		if err != nil {
			return "", "", err
		}
		var result struct {
			Message managed.Message `json:"message"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", "", err
		}
		return result.Message.ID, "Board note", nil
	}
	store, err := managed.Open(state)
	if err != nil {
		return "", "", err
	}
	agent, err := store.Agent(target)
	_ = store.Close()
	if err != nil {
		return "", "", err
	}
	op, kind := "instruct", "Instruction"
	if mode == "note" || mode == "auto" && agent.Interaction == "interactive" {
		op, kind = "message.send", "Message"
	}
	raw, err := workCall(ctx, client, op, map[string]any{"to": []string{target}, "text": text, "reply_to": replyTo})
	if err != nil {
		return "", "", err
	}
	var result struct {
		Task    managed.Task    `json:"task"`
		Message managed.Message `json:"message"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return "", "", err
	}
	if result.Task.ID != "" {
		return result.Task.ID, kind, nil
	}
	return result.Message.ID, kind, nil
}

// A saved default session must never silently open another project's agents.
func checkSavedWorkProject(config, project string) error {
	cfg, err := managedconfig.Load(config)
	if err != nil {
		return err
	}
	requested, err := filepath.Abs(expandUserPath(project))
	if err != nil {
		return err
	}
	requested, err = filepath.EvalSymlinks(requested)
	if err != nil {
		return err
	}
	for _, agent := range cfg.Agents {
		saved, e := filepath.EvalSymlinks(agent.Workdir)
		if e != nil || saved != requested {
			return errors.New("saved team uses a different project; use attach to open it, or start --session NAME for a new team")
		}
	}
	return nil
}
