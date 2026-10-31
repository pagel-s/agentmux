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
	detach                                   bool
	args                                     []string
	view                                     string
	popup                                    bool
}

func hasWorkSessionFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--state" || arg == "--session" || arg == "--view" || arg == "--popup" || strings.HasPrefix(arg, "--view=") || strings.HasPrefix(arg, "--state=") || strings.HasPrefix(arg, "--session=") {
			return true
		}
	}
	return false
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
		if name != "state" && name != "session" && name != "config" && name != "project" && name != "sandbox" && name != "view" && name != "group" && name != "board" {
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
		}
	}
	if !managedconfig.ValidID(o.session) {
		return o, errors.New("invalid session name")
	}
	var err error
	if o.state == "" {
		o.state, err = defaultWorkState(o.session)
	} else {
		o.state, err = filepath.Abs(expandUserPath(o.state))
	}
	return o, err
}

func workCall(ctx context.Context, client *managed.Client, op string, args any) (json.RawMessage, error) {
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	query, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, problem := client.Call(query, managed.Request{Operation: op, RequestID: requestIDForWork(), Args: data})
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
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			fmt.Fprintln(out, `Human work sessions:
  agentmux start [--project DIR] [--session work] [--detach]
                 [--config team.yaml] [--sandbox bubblewrap|local] [--state DIR]
  agentmux attach [AGENT] [--session work | --state DIR]
  agentmux dashboard [--session work | --state DIR]
  agentmux web [--session work | --state DIR]
  agentmux send AGENT "message" [--session work | --state DIR]
  agentmux reply TASK_OR_MESSAGE_ID "message" [--session work | --state DIR]
  agentmux inbox [--session work | --state DIR]
  agentmux board "note" [--group ID | --board NAMESPACE] [--session work | --state DIR]
  agentmux add [NAME] --harness claude|codex|pi --project DIR [--title "..."] [--role "..."]
  agentmux label ID "Fix login redirect"
  agentmux remove NAME
  agentmux sessions
  agentmux connect OBSERVED_ID --name NAME
  agentmux collaborate NAME NAME [...]
  agentmux isolate NAME [...]
  agentmux down [--session work | --state DIR]

start creates a coding preset with real interactive Claude and Codex terminals.
By default both use normal local native configuration in the current project.
Collaboration, delegation and autonomous wakeups start disabled. Use collaborate
for a dedicated board/memory group, or --sandbox bubblewrap for isolated profiles.
send/reply queue broker messages for interactive agents and instruction tasks for
managed workers. Use attach to type directly into a native agent terminal.
Legacy raw pane input remains: agentmux send SESSION PANE -- COMMAND.`)
			return exitSuccess
		}
	}
	o, err := parseWorkOptions(args)
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
				project, e = filepath.Abs(project)
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
						fmt.Fprintf(out, "Team %s is already running.\n", live.Session)
						if o.detach {
							return exitSuccess
						}
						return runWorkSession("attach", []string{"--state", o.state}, in, out, diagnostics)
					}
				}
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
			return fail(e)
		}
		agent := ""
		if len(o.args) == 1 {
			agent = o.args[0]
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
		return fail(errors.New("work session not found; use agentmux start or select --state / --session"))
	}
	client := managed.NewClient(credential)
	if operation == "remove" || operation == "collaborate" || operation == "isolate" || operation == "label" {
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
		fmt.Fprintln(out, "Posted to team board.")
		return exitSuccess
	}
	if operation == "send" || operation == "reply" {
		if len(o.args) != 2 {
			return fail(errors.New(operation + " requires a recipient/reference and one quoted message"))
		}
		target, text := o.args[0], o.args[1]
		if operation == "reply" {
			target, err = workReplyTarget(ctx, client, target)
			if err != nil {
				return fail(err)
			}
			text = "Follow-up to " + o.args[0] + ":\n" + text
		}
		id, kind, e := workSend(ctx, client, o.state, target, text)
		if e != nil {
			return fail(e)
		}
		fmt.Fprintf(out, "%s queued for %s: %s\n", kind, target, id)
		if kind == "Message" {
			fmt.Fprintf(out, "Interactive agent: read it from the agent's broker inbox, or use agentmux attach %s --state %s to talk directly.\n", target, o.state)
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
	if result.Message.From != "$owner" {
		return result.Message.From, nil
	}
	if len(result.Message.To) == 1 {
		return result.Message.To[0], nil
	}
	return "", errors.New("board/group messages need an explicit recipient; use send")
}
func workSend(ctx context.Context, client *managed.Client, state, target, text string) (string, string, error) {
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
	if agent.Interaction == "interactive" {
		op, kind = "message.send", "Message"
	}
	raw, err := workCall(ctx, client, op, map[string]any{"to": []string{target}, "text": text})
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
