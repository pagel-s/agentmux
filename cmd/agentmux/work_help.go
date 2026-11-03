package main

// Help is an option until --, never the value of a workspace option or literal
// message text after --. Keep this in step with parseWorkOptions.
func workHelpRequested(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			return false
		case "--help", "-h":
			return true
		}
		if workOptionValue(args[i]) {
			i++
		}
	}
	return false
}

func helpArgument(value string) bool { return value == "--help" || value == "-h" }

// Keep help at the operation an owner asked about. The complete command
// reference remains amx help all; help never needs workspace state or a broker.
func workSessionUsage(operation string) string {
	var body string
	switch operation {
	case "start":
		return `Usage: amx start [--project DIR] [--session NAME] [--detach]
                 [--config team.yaml] [--sandbox local|bubblewrap] [--state DIR]

Start a workspace with native Claude and Codex terminals, or use --config.
The default project is the current directory; the default workspace is work,
including inside another attachment. Creation may combine --session and --state.
Local mode uses the providers' installed native configuration and sign-ins.
Collaboration, delegation and autonomous wakeups start disabled.
--detach starts without attaching. Bubblewrap requires supported Linux isolation.
Next: amx sessions --session NAME; amx attach --session NAME.`
	case "attach":
		body = `Usage: amx attach [AGENT] [--session NAME | --state DIR]

Open a native terminal by stable ID. Without an ID, resume the last live terminal
or open the workbench. Bare amx attach offers running-workspace selection.
Type, approve tools and interrupt work in the provider's normal terminal.
F1 returns to the workbench; Ctrl+P finds another session there.
Next: amx sessions with the same selector lists stable IDs.`
	case "dashboard":
		body = `Usage: amx dashboard [--session NAME | --state DIR] [--view VIEW] [--popup]

Open the owner workbench while native sessions continue independently.
Ctrl+K searches Actions; Ctrl+P finds sessions; Ctrl+W finds workspaces.
Tab changes focus; 1–8 chooses a view; ? explains the current controls.
Closing the observer keeps native sessions running.
Next: amx observe with the same selector summarizes fresh operational evidence.`
	case "send":
		body = `Usage: amx send AGENT "message" [--mode auto|note|instruction]
                [--session NAME | --state DIR]

auto sends notes to native sessions and instruction tasks to managed workers.
Use --mode instruction to delegate explicitly; instructions print an exact task ID
and same-workspace inspection command. A queued task is not verified completion.
Native receivers also need compatible readiness and Review or Auto delivery.
Owner send uses owner authority; peer command permission and receiver delivery are separate controls.
Next: amx task TASK_ID with the same selector inspects delivery and recovery.
Legacy raw terminal input: amx send SESSION PANE -- COMMAND.`
	case "reply":
		body = `Usage: amx reply TASK_OR_MESSAGE_ID "message" [--mode auto|note|instruction]
                 [--to AGENT | --board NAMESPACE] [--session NAME | --state DIR]

Reply with broker-checked ancestry; a reply cannot widen a private audience.
Board replies need an explicit destination. auto uses the recipient's session
type; use --mode instruction for a new delegated task.
Next: amx task TASK_ID with the same selector inspects a delegated reply.`
	case "inbox":
		body = `Usage: amx inbox [--session NAME | --state DIR]

Read recent owner-visible messages, tasks, results and lifecycle events.
This is a bounded recent view. amx tasks searches complete retained task history.
Next: amx observe with the same selector summarizes current attention and freshness.`
	case "board":
		body = `Usage: amx board "note" [--group ID | --board NAMESPACE]
                 [--session NAME | --state DIR]

Post an owner note to an explicit shared board or the sole active group.
Choose --group or --board when several groups exist. A board note does not grant
command permission or prove that a native agent received or used its content.
Next: amx inbox with the same selector reads recent activity.`
	case "label":
		body = `Usage: amx label ID "Task title" [--session NAME | --state DIR]

Change the owner's display title. Titles can repeat; operations use stable IDs.
This does not change the native conversation, role or permissions.
Next: amx sessions with the same selector lists titles and stable IDs.`
	case "collaborate":
		body = `Usage: amx collaborate AGENT AGENT [...] [--legacy-policy preserve|group]
                       [--session NAME | --state DIR]

Create a collaboration group with a dedicated shared board and memory scope.
Command permission and native receiver delivery remain separate choices.
Use allow FROM TO commands for a direction, then delivery TO review for reviewed
native turns. Existing owner policy is handled explicitly by --legacy-policy.
Next: amx delivery ID with the same selector explains receiver readiness.`
	case "disconnect":
		body = `Usage: amx disconnect AGENT AGENT [...] [--group ID]
                      [--session NAME | --state DIR]

Disable the exact selected collaboration group. Other groups and explicit grants
can still provide access. Context already read and work already accepted remain.
Next: amx observe with the same selector inspects active and uncertain deliveries.`
	case "isolate":
		body = `Usage: amx isolate AGENT [...] [--session NAME | --state DIR]

Disconnect selected sessions from all broker peers. This changes coordination
authority; it does not sandbox the native process, erase prior knowledge or undo
accepted work. Inspect current tasks and side effects separately.
Next: amx observe with the same selector shows current delivery evidence.`
	case "retire":
		body = `Usage: amx retire ID [--session NAME | --state DIR]

Retire a stopped session from the active roster while retaining task history.
The native process must already be stopped; nothing is silently restarted.
Next: amx sessions --archived with the same selector includes retired sessions;
amx task TASK_ID --open still reaches its exact retained recovery view.`
	case "remove":
		body = `Usage: amx remove ID [--session NAME | --state DIR]

Revoke a session's broker coordination and active descendants. Process exit is
reported separately; revocation alone cannot prove side effects stopped.
Next: amx observe with the same selector inspects workers and uncertain tasks.`
	case "down":
		body = `Usage: amx down [--session NAME | --state DIR]

Stop the selected workspace's dedicated runtime. Other workspaces keep running.
Retained tasks and evidence remain available; an interrupted delivery may require
inspection and exact task/version recovery before another attempt.
Next: amx context with the same selector reports runtime state.`
	default:
		body = "Usage: amx " + operation + " [--session NAME | --state DIR]\n\nUse amx help all for the complete command reference."
	}
	if operation == "send" || operation == "reply" || operation == "attach" {
		body += `

Inspect: amx task TASK_ID [--json | --open]; amx tasks reads retained history.
Opening never retries or resolves a task; recovery is separately reviewed.`
	}
	return body + `

Select one workspace with --session NAME (-s NAME) or --state DIR.
An omitted selector uses a verified attachment; outside it defaults to work.
amx context with the same selector explains the selected target.`
}
