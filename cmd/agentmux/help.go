package main

import (
	"fmt"
	"io"
	"os"
)

const everydayUsage = `amx · Agentmux
Run native coding agents together, with communication you control.

Start here:
  amx demo                     Try a checked change in a throwaway repository
  amx start                    Start Claude and Codex in the current project
  amx a [ID]                   Open a native terminal (or restore the last one)
  amx add --harness codex --project DIR
  amx sessions                 List sessions; amx opens the workbench
  amx context                  Show the selected workspace and why
  amx task ID                  Inspect a delegated task and its delivery
  amx collaborate claude codex  Connect selected agents
  amx web --open               Open the local dashboard
  amx down                     Stop the current workspace

Checked coding:
  amx work "Describe the change" -- CHECK [ARG...]
  Preview first; add --allow-models to run. Inspect, then explicitly apply.

Use -s NAME to choose a workspace. Ctrl+K opens Actions; Ctrl+P switches sessions.
Native sessions require tmux and installed, authenticated provider CLIs.
amx demo needs only Git and a POSIX shell; it makes no model calls.

More help:
  amx COMMAND --help           Options for one command
  amx help automation         Managed workers, workflows and research
  amx help legacy             YAML pane layouts and older tmux commands
  amx help all                Complete command reference
  amx doctor                  Check your installation`

func printUsage() { fmt.Fprintln(os.Stdout, everydayUsage) }

func runHelp(args []string, out, diagnostics io.Writer) int {
	if len(args) == 0 || len(args) == 1 && helpArgument(args[0]) {
		fmt.Fprintln(out, everydayUsage)
		return exitSuccess
	}
	if len(args) != 1 {
		fmt.Fprintln(diagnostics, "error: use amx help [all|automation|legacy]")
		return exitValidation
	}
	switch args[0] {
	case "all":
		printAllUsage(out)
	case "automation":
		fmt.Fprintln(out, `Agentmux automation

  amx demo                         Model-free checked change in a fixture repo
  amx work "Change" -- CHECK        Preview a coding and fresh-review workflow
  amx work status [RUN_DIR|latest]  Inspect saved progress and evidence
  amx work list --limit 10          Find recent stable run IDs
  amx work show RUN_ID --open       Open a freshly rendered local report
  amx work apply [RUN_DIR|latest]   Explicitly apply a checked result
  amx flow --help                   Plans, comparisons, missions and recovery
  amx managed setup --help          Configure a managed team
  amx managed --help                Tasks, permissions, memory and worker control
  amx experiment --help             Repeatable declared experiment plans
  amx research --help               Controlled studies and analysis

Provider-backed execution requires explicit --allow-models for work/flow.
Choose containment and isolation explicitly; private Git storage alone does not
isolate processes, the filesystem or networking. Managed automation is experimental.
Use amx help all for the complete command reference.`)
	case "legacy":
		fmt.Fprintln(out, `Agentmux legacy pane layouts

  amx templates
  amx init TEMPLATE CONFIG.yaml
  amx validate CONFIG.yaml
  amx load CONFIG.yaml --no-attach
  amx reload CONFIG.yaml
  amx list
  amx status SESSION
  amx inspect SESSION PANE
  amx capture SESSION PANE

These commands address legacy layouts on the default tmux socket. Everyday work
sessions use amx start, amx sessions and amx a on dedicated workspace sockets.
Use amx COMMAND --help for options, or amx help all for every legacy command.`)
	default:
		fmt.Fprintf(diagnostics, "error: unknown help topic %q; use all, automation or legacy\n", args[0])
		return exitValidation
	}
	return exitSuccess
}

func printAllUsage(out io.Writer) {
	fmt.Fprintln(out, `amx · Agentmux

Run amx to open the current
workspace or choose a running one. Short options: -s workspace, -p project,
-d detach on up/start, -j JSON on delivery, workspaces, context, task/tasks; -o web browser.
Use -s before or after the command. Inside an owned tmux attachment, omitted
workspace selectors use that attachment; outside, commands default to work.
Creation with up/start always defaults to work unless a selector is supplied.

Everyday shortcuts:
  amx up -s daily -p ./project
  amx a -s daily                 amx ui -s daily
  amx split claude codex -s daily
  amx unsplit codex -s daily
  amx ls -s daily               amx ws
  amx new --harness codex -p ./project -s daily
  amx link claude codex -s daily
  amx allow codex claude commands -s daily
  amx mode claude auto -s daily
Aliases: up=start, a/open=attach, ui=dashboard, ls=sessions, ws=workspaces,
new=add, link=collaborate, unlink=disconnect, name=label, mode=delivery, archive=retire.
Ctrl+K opens Actions; Ctrl+P switches sessions. Tab focuses sidebar/content.
Left/Right switches views; Up/Down navigates the focused area.
amx a restores the last live native terminal; amx ui opens the workbench.

Everyday work sessions use dedicated tmux sockets. Use sessions/dashboard to inspect them;
the legacy list/status commands below address the default tmux socket.

Usage:
  amx demo [--output NEW_DIR]
  amx start [--project DIR] [--session work] [--detach]
  amx add [NAME] --harness claude|codex|pi|antigravity --project DIR [--title "..."] [--role "..." | --native-only]
  amx label ID "Fix login redirect" [--session work | --state DIR]
  amx role ID "Current owner role" [--session work | --state DIR]
  amx sessions [--session work | --state DIR]
  amx context [--json] [--session work | --state DIR]
  amx task TASK_ID [--json] [--session work | --state DIR]
  amx tasks [--to ID] [--status STATE] [--query TEXT] [--limit N] [--after TASK_ID] [--json] [--session work | --state DIR]
  amx workspaces [--json] [--register DIR]
  amx relaunch ID [--resume ID|last|picker] [--session work | --state DIR]
  amx delivery ID [manual|review|auto] [--approve CANDIDATE] [--session work | --state DIR]
  amx allow|deny FROM TO commands|messages|pings [--session work | --state DIR]
  amx connect OBSERVED_ID --name NAME [--session work | --state DIR]
  amx collaborate AGENT AGENT [...] [--legacy-policy preserve|group] [--session work | --state DIR]
  amx disconnect AGENT AGENT [...] [--session work | --state DIR]
  amx retire ID [--session work | --state DIR]
  amx isolate AGENT [...] [--session work | --state DIR]
  amx remove AGENT [--session work | --state DIR]
  amx attach [AGENT] [--session work | --state DIR]
  amx dashboard [--session work | --state DIR]
  amx web [--session work | --state DIR] [--open]
  amx send AGENT "message" [--mode auto|note|instruction] [--session work | --state DIR]
  amx reply TASK_OR_MESSAGE_ID "message" [--mode auto|note|instruction]
      [--to AGENT | --board NAMESPACE] [--session work | --state DIR]
  amx inbox [--session work | --state DIR]
  amx board "note" [--group ID | --board NAMESPACE] [--session work | --state DIR]
  amx team <list|create|edit|invite|remove|transfer|dissolve|join|leave|accept-owner|move> [options]
  amx down [--session work | --state DIR]
  amx templates
  amx init <template> <path> [--force]
  amx edit <path-or-session>
  amx remember <session> <config>
  amx pack <config> [<output.amx>]
  amx tools [--format <openai|codex|gemini|claude>]
  amx prompt [--provider <provider>] [--mode <cli-bridge|native-mcp>]
  amx tool-call <tool-name> (--from-json <json> | --from-file <path> | --from-stdin)
  amx mcp-server
  amx managed <setup|up|down|restart|doctor|status|activity|init|serve|call|worker|remote|memory|trace|capsule|rotate> [options]
  amx experiment <plan|run> <manifest> [options]
  amx flow <run|mission|watch|lab|check|status|explain|handoff|report|apply> [options]
  amx work "Describe the change" [--allow-models] [--repo DIR] -- CHECK [ARG...]
  amx work <status|show|apply|recover> [RUN_ID|RUN_DIR|latest] [--repo DIR]
  amx work list [--limit N] [--repo DIR]
  amx work show RUN_ID|RUN_DIR|latest --open [--repo DIR]
  amx research <plan|run|analyze|status|watch|pause|resume|step|stop> <manifest-output-or-state> [options]
  amx load <config> [--param key=value] [--attach|--no-attach] [--dry-run] [--replace] [--strict] [--verbose]
  amx reload <config> [--param key=value] [--attach|--no-attach] [--dry-run] [--strict] [--verbose]
  amx validate <config> [--param key=value] [--strict]
  amx render <config> [--param key=value] [--strict]
  amx list
  amx dashboard <session>
  amx status <session> [--json] [--pane <pane-name>] [--logs [N]]
  amx inspect <session> <pane-name> [--json]
  amx kv-set <session> <key> <value> [--json]
  amx kv-get <session> <key> [--json]
  amx kv-list <session> [--json]
  amx kv-del <session> <key> [--json]
  amx wait <session> <pane> <pattern> [--scope <output|visible>] [--timeout <duration>] [--json]
  amx scale <session> <agent-name> <count> [--window <name>] [--json]
  amx focus <session> <pane-name> [--json]
  amx restart <session> <pane-name> [--json]
  amx rerun <session> <pane-name> [--json]
  amx stop <session> <pane-name> [--json]
  amx kill <session> <pane-name> [--json]
  amx capture <session> <pane-name> [--json] [--tail N] [--strip-empty]
  amx send <session> <pane-name> [--json] [--no-enter] <command...>
  amx doctor [<config>]
  amx version`)

	fmt.Fprintln(out, `
Starter flow:
  amx start
  amx attach claude
  amx web --open

Legacy workspace flow:
  amx templates
  amx init default my-workflow.yaml
  amx init coding-starter coding.yaml
  amx validate my-workflow.yaml
  amx load my-workflow.yaml --replace --no-attach`)
}
