# Agentmux Commands

This guide explains what each command is for, when to use it, and the most useful examples.

For a guided native-terminal workflow, see [work sessions](work-sessions.md).
For original YAML pane-layout examples, see [recipes](recipes.md).

## Everyday work sessions

After building, put `bin/` on your PATH as shown in the [installation
guide](getting-started/installation.md). With Claude Code and Codex installed and
logged in, run `agentmux start` from your project directory. It creates the
default workspace `work` and opens its workbench.

| Command | Use it to |
| --- | --- |
| `agentmux start` | Start the default native Claude/Codex team, or attach when that same team is running |
| `agentmux attach AGENT_ID` | Open one workspace-owned native conversation for direct typing |
| `agentmux dashboard` | Return to the owner workbench |
| `agentmux web --open` | Open the local owner app |
| `agentmux add --harness claude --project DIR --title "Task"` | Start another native session; copy its printed stable ID |
| `agentmux sessions` | List registered sessions and separately observed external terminals |
| `agentmux connect OBSERVED_ID --title "Task"` | Prepare a helper for an existing external session; paste the printed setup into its original terminal |
| `agentmux label AGENT_ID "Task"` | Rename its display title while keeping its ID and conversation |
| `agentmux collaborate AGENT_ID OTHER_ID` | Give those sessions a dedicated board/memory group and allow notes/pings |
| `agentmux send AGENT_ID "Message"` | Queue a native inbox note, or a managed worker instruction task |
| `agentmux inbox` | Inspect recent coordination and task activity as the owner |
| `agentmux board "Note" --group GROUP_ID` | Post to a particular collaboration group's board |
| `agentmux reply TASK_OR_MESSAGE_ID "Follow-up"` | Follow up to the task recipient or original message sender |
| `agentmux isolate AGENT_ID` | Revoke its peer coordination and memberships across groups; native input stays open |
| `agentmux remove AGENT_ID` | Close an owned terminal or disconnect an external bridge; retain saved history |
| `agentmux down` | Stop the workspace's owned processes while keeping saved data |

Replace uppercase placeholders with actual paths or IDs. `DIR` must already
exist. `--harness` chooses the native CLI (`claude`, `codex`, or `pi`);
`--title` is a display label and `--role` supplies optional agent instructions.
Session titles can repeat; commands address the stable ID shown under the title.
An observed terminal must be connected before its registered ID can be used for
collaboration. A prepared bridge does not mean the agent read the pasted setup;
external sessions keep their original terminals for typing.

These commands select workspace `work` by default, regardless of your current
directory. For another workspace use `--session NAME` on each command, or select
its exact directory with `--state DIR`. For example:

```sh
agentmux start --session review --project /path/to/project
# In another terminal:
agentmux attach codex --session review
agentmux web --session review --open
agentmux down --session review
```

`send` queues coordination; it does not type into a native terminal or start a
native model turn. Use `attach` to talk directly. With several groups, copy the
group ID printed by `collaborate` or shown under **Teams → Owner collaboration
groups** in the web app. `board` can choose the only active group automatically;
with multiple groups select `--group ID` or `--board NAMESPACE` explicitly.
For terminal shortcuts, recovery and native history, see [work
sessions](work-sessions.md). For bounded headless studies see
[research](research.md).

## Existing workspace commands

The reference below covers the original YAML layout mode (`init` / `load`).
Its pane IDs, raw terminal input and shared key/value store are separate from
the work-session broker's messages, memory and permissions. In particular,
`dashboard SESSION` opens the legacy pane dashboard; use
`dashboard --session NAME` for a managed workbench. Legacy `send SESSION PANE --
COMMAND` types into a pane, unlike the queued message form above.

### The short version

If you only remember a few commands, remember these:

- `templates`: show built-in starter configs
- `init`: create a new config from a starter
- `load`: create a tmux session from a config
- `reload`: recreate an existing session from the config
- `status`: inspect a live session
- `capture`: read output from a named pane
- `send`: send text into a named pane
- `edit`: reopen the config file

## Starter Commands

### `templates`

What it does:
- lists the built-in starter templates that ship with Agentmux
- shows friendly aliases like `default`

When to use it:
- when starting a new config
- when you forget the built-in template names

Examples:

```bash
./bin/agentmux templates
./bin/agentmux init coding-starter coding.yaml
./bin/agentmux init review-starter review.yaml
```

### `init`

What it does:
- writes a starter template to a target path
- rewrites the top-level `session` field from the target filename by default

When to use it:
- when creating a new config
- when you want a starting point instead of hand-writing YAML from scratch

Examples:

```bash
./bin/agentmux init coding-starter coding.yaml
./bin/agentmux init default my-workflow.yaml
./bin/agentmux init minimal scratch.yaml
./bin/agentmux init ai-dev work/review.yaml --force
```

Useful notes:
- `default` is an alias for `ai-dev`
- `coding-starter` and `review-starter` are built-in minimal copy-and-tweak starters
- `--force` overwrites an existing file

### `edit`

What it does:
- opens a config path directly, or
- opens the config path remembered for a live session

When to use it:
- when editing a config you already have on disk
- when you know the session name but do not want to remember where the file lives

Examples:

```bash
./bin/agentmux edit my-workflow.yaml
./bin/agentmux edit my-workflow
```

Useful notes:
- `edit <session>` works best for sessions created or reloaded through current Agentmux
- if metadata is missing, use `remember`

### `remember`

What it does:
- attaches a config path to an already-running session without reloading it

When to use it:
- when a session is older and missing config-path metadata
- when you want `edit <session>` to work immediately

Examples:

```bash
./bin/agentmux remember my-workflow ./my-workflow.yaml
./bin/agentmux edit my-workflow
```

## Config Commands

### `validate`

What it does:
- checks YAML shape, references, and config rules
- prints errors and warnings without launching tmux

When to use it:
- after editing a config
- before `load` if you want a fast safety check

Examples:

```bash
./bin/agentmux validate my-workflow.yaml
./bin/agentmux validate my-workflow.yaml --strict
```

Useful flags:
- `--param key=value`: override config params
- `--strict`: treat warnings as errors

### `render`

What it does:
- prints the fully resolved config
- prints the launch plan Agentmux would execute

When to use it:
- when debugging params, env, cwd, or layout resolution
- when you want to inspect what will actually run

Examples:

```bash
./bin/agentmux render my-workflow.yaml
./bin/agentmux render my-workflow.yaml --param project_dir=.
```

### `pack`

What it does:
- creates a shareable, self-contained `.amx` bundle (a ZIP archive)
- bundles the configuration file, any local `include` files, and local scripts

When to use it:
- when you want to share a complex agentic team setup with someone else
- to distribute workflows that rely on local assets via `{{config_dir}}`

Examples:

```bash
./bin/agentmux pack my-workflow.yaml
./bin/agentmux pack my-workflow.yaml my-team.amx
```

Useful note:
- `.amx` bundles can be loaded directly with `agentmux load my-team.amx`
- you can host `.amx` bundles online and load them directly from a URL

### `tools`

What it does:
- generates a JSON Schema export of Agentmux coordination tools
- output can be rendered in provider-friendly shapes such as `openai`, `codex`, `gemini`, or `claude`

When to use it:
- when you need a compatibility/debug export of the coordination tool schemas
- when comparing Agentmux tool shapes across provider formats

Examples:

```bash
./bin/agentmux tools --format openai
./bin/agentmux tools --format claude
```

### `prompt`

What it does:
- generates the Agentmux operational runtime contract for AI agents
- describes either the native MCP tool path or the CLI bridge path, depending on `--mode`

When to use it:
- as the operational system/bootstrap prompt prefix for LLM agents that run inside an Agentmux pane
- when you want to inspect the exact coordination contract a pane will receive

Useful note:
- this command describes coordination behavior, not necessarily persona delivery
- persona may be delivered through a provider channel such as Claude's appended system prompt or a Gemini workspace context file

Examples:

```bash
./bin/agentmux prompt
./bin/agentmux prompt --provider claude
./bin/agentmux prompt --provider claude --mode native-mcp
./bin/agentmux prompt --provider gemini --mode native-mcp
```

### `tool-call`

What it does:
- executes a real Agentmux coordination action through a stable JSON envelope
- maps canonical tool names like `kv_set`, `wait`, or `send` onto the existing CLI subcommands
- accepts payloads from `--from-json`, `--from-file`, or `--from-stdin`

When to use it:
- from LLM/provider runtimes that need machine-stable coordination behavior
- when you want structured JSON success/error output without scraping human text

Examples:

```bash
./bin/agentmux tool-call kv_set --from-json '{"session":"demo","key":"task","value":"review"}'
./bin/agentmux tool-call kv_get --from-stdin <<EOF
{"session":"demo","key":"task"}
EOF
./bin/agentmux tool-call kv_set --from-file /tmp/agentmux-tool.json
./bin/agentmux tool-call wait --from-json '{"session":"demo","pane":"planner","pattern":"PLAN_READY","timeout":"30s"}'
```

### `mcp-server`

What it does:
- exposes the Agentmux tool registry as an MCP stdio server
- backs the native MCP integrations for supported providers

When to use it:
- indirectly through provider-native launch paths such as Claude, Gemini, or Codex
- directly only when testing MCP integration by hand

## Session Commands

### `load`

What it does:
- creates a tmux session from a config or a remote URL

When to use it:
- the first time you launch a config
- when replacing an existing session from scratch
- when launching a shared session from a URL

Examples:

```bash
./bin/agentmux load my-workflow.yaml
./bin/agentmux load https://example.com/team.amx
./bin/agentmux load my-workflow.yaml --replace --no-attach
./bin/agentmux load my-workflow.yaml --dry-run
```

Useful flags:
- `--replace`: replace an existing session
- `--no-attach`: do not attach after launch
- `--dry-run`: print the launch plan only
- `--verbose`: print underlying tmux commands

### `reload`

What it does:
- reconciles an existing session from a config without destroying the whole session
- surgically updates pane metadata or respawns individual panes if their config changed
- preserves history and layout for untouched panes

When to use it:
- after editing a config for a session that already exists

Examples:

```bash
./bin/agentmux reload my-workflow.yaml --no-attach
```

Useful note:
- unlike `load`, `reload` expects the session to already exist
- if the session structure (window or pane count) has changed significantly, it falls back to a destructive replace with a warning
- if you are running `reload` from inside one of the session's panes, and that pane's config changed, it will be respawned last

### `list`

What it does:
- lists tmux session names

When to use it:
- when checking whether a session exists
- when troubleshooting runtime commands

Examples:

```bash
./bin/agentmux list
```

## Runtime Commands

These commands operate on live sessions and mostly target named panes.

### `status`

What it does:
- shows live panes in a session
- reports metadata like pane name, title, source, `on_exit`, current command, and `dead`
- includes PID for running panes and exit status for dead panes

When to use it:
- as the first runtime debugging step
- to discover pane names
- to inspect a single pane with `--pane`

Examples:

```bash
./bin/agentmux status my-workflow
./bin/agentmux status my-workflow --json
./bin/agentmux status my-workflow --pane review
./bin/agentmux status my-workflow --pane review --json
```

### `focus`

What it does:
- switches the active tmux client focus to a named pane

When to use it:
- when jumping directly to a pane by name instead of navigating manually

Examples:

```bash
./bin/agentmux focus my-workflow review
```

### `restart`

What it does:
- respawns a named pane with its stored startup command

When to use it:
- when a pane is stuck or you want to restart its workflow endpoint

Examples:

```bash
./bin/agentmux restart my-workflow review
```

### `rerun`

What it does:
- re-executes the stored startup command for a named pane

When to use it:
- when you want to re-run the pane’s original command in place

Examples:

```bash
./bin/agentmux rerun my-workflow review
```

Useful note:
- in the current version, `restart` and `rerun` are intentionally equivalent

### `stop`

What it does:
- stops the current pane process
- leaves a usable interactive shell in the same pane

When to use it:
- when you want to stop a running pane without destroying the layout

Examples:

```bash
./bin/agentmux stop my-workflow review
```

### `kill`

What it does:
- destroys the target tmux pane entirely

When to use it:
- when you really want the pane removed from the layout

Examples:

```bash
./bin/agentmux kill my-workflow build
```

Useful note:
- this is destructive and can collapse the layout

### `capture`

What it does:
- reads the current captured output of a named pane

When to use it:
- for quick inspection
- for small polling scripts
- for automation that needs to read pane output

Examples:

```bash
./bin/agentmux capture my-workflow review
./bin/agentmux capture my-workflow review --tail 20
./bin/agentmux capture my-workflow review --tail 20 --strip-empty
./bin/agentmux capture my-workflow review --json --tail 20 --strip-empty
```

Useful flags:
- `--tail N`: keep only the last `N` lines
- `--strip-empty`: remove blank and whitespace-only lines
- `--json`: structured output

### `send SESSION PANE -- COMMAND`

What it does:
- writes text into a named pane
- presses Enter by default

When to use it:
- when treating a pane like a live workflow endpoint

Examples:

```bash
./bin/agentmux send my-workflow review -- "git status --short"
./bin/agentmux send my-workflow review --no-enter -- "printf 'queued\n'"
```

Useful note:
- `--no-enter` stages the text without executing it yet

## Agentic Commands

These commands enable coordination and orchestration between multiple agents.

### `dashboard`

What it does:
- launches a full-screen, real-time TUI (Terminal User Interface)
- provides a "Mission Control" view of all panes, the selected pane's live output, and the shared Blackboard (KV-store)

When to use it:
- to monitor an agentic team in real-time
- to see live pane activity, including provider MCP/tool traces, without repeatedly running `capture` and `inspect`
- to see the current state of shared variables without repeatedly running `status`

Examples:

```bash
./bin/agentmux dashboard my-workflow
```

Useful shortcuts inside the dashboard:
- `f`: quit the dashboard and attach/switch to the selected pane
- `r`: restart the selected pane
- `q`: quit the dashboard
- `up/down`: change the selected pane and refresh its live output

### `inspect`

What it does:
- provides a deep, comprehensive diagnostic report for a single pane
- shows real-time metrics (CPU, Memory), lifecycle metadata (start time, exit code), and recent logs

When to use it:
- when a pane is crashing and you need to see its exact configuration and last output
- to verify the environment variables injected into a specific agent

Examples:

```bash
./bin/agentmux inspect my-workflow reviewer
./bin/agentmux inspect my-workflow reviewer --json
```

### `kv-set`

What it does:
- sets a shared key-value pair in the session's "Blackboard"
- uses tmux session options for storage

When to use it:
- to signal state between panes (e.g., `status=ready`)
- to share data like file paths or task IDs

Examples:

```bash
./bin/agentmux kv-set my-team current_task "researching-agent-scaling"
```

### `kv-get`

What it does:
- retrieves a value from the session's Blackboard

Examples:

```bash
./bin/agentmux kv-get my-team current_task
```

### `kv-list`

What it does:
- lists all keys and values currently stored in the Blackboard

Examples:

```bash
./bin/agentmux kv-list my-team
./bin/agentmux kv-list my-team --json
```

### `kv-del`

What it does:
- removes a key from the Blackboard

Examples:

```bash
./bin/agentmux kv-del my-team current_task
```

### `wait`

What it does:
- blocks until a specific regex pattern appears in a pane's emitted output
- by default ignores active input/composer text in interactive panes
- line anchors like `^...$` work against output lines by default
- supports `--scope visible` for legacy raw screen matching

When to use it:
- to synchronize handoffs (e.g., wait for "BUILD SUCCESSFUL")
- to wait for real agent output rather than matching text still sitting in the input composer

Examples:

```bash
./bin/agentmux wait my-team builder "SUCCESS" --timeout 5m
./bin/agentmux wait my-team reviewer '^• REVIEW_DONE$' --timeout 2m
./bin/agentmux wait my-team builder "SUCCESS" --scope visible --timeout 5m
```

### `scale`

What it does:
- dynamically adds or removes instances of a specific agent

When to use it:
- to adjust workforce based on load (e.g., spawn 3 more "Researchers")

Examples:

```bash
./bin/agentmux scale my-team researcher 5
./bin/agentmux scale my-team researcher 1
```

Useful note:
- scaling up creates new panes with unique names like `researcher-2`
- scaling down removes panes with the highest IDs

## Utility Commands

### `doctor`

What it does:
- prints environment and dependency information
- can check requirements for a specific config file
- inspects configured provider CLIs, alias warnings, and likely stale model hints

When to use it:
- when tmux or environment issues look suspicious
- to verify if a shared config will run on your machine

Examples:

```bash
./bin/agentmux doctor
./bin/agentmux doctor ./shared-team-config.yaml
```

### `version`

What it does:
- prints the current Agentmux version string

Examples:

```bash
./bin/agentmux version
./bin/agentmux --version
```

### `--help`

What it does:
- prints built-in CLI usage

Examples:

```bash
./bin/agentmux --help
```

## Common Flows

### New workflow

```bash
./bin/agentmux templates
./bin/agentmux init default my-workflow.yaml
./bin/agentmux validate my-workflow.yaml
./bin/agentmux load my-workflow.yaml --replace --no-attach
```

### Edit and reapply

```bash
./bin/agentmux edit my-workflow
./bin/agentmux validate my-workflow.yaml
./bin/agentmux reload my-workflow.yaml --no-attach
```

### Inspect and interact with a live pane

```bash
./bin/agentmux status my-workflow --pane review
./bin/agentmux capture my-workflow review --tail 20 --strip-empty
./bin/agentmux send my-workflow review "git status --short"
```
