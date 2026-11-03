# Agentmux Commands

This guide explains what each command is for, when to use it, and the most useful examples.

For a guided native-terminal workflow, see [work sessions](work-sessions.md).
For original YAML pane-layout examples, see [recipes](recipes.md).

`context`, `task`/`tasks`, `work list --limit` and `work show --open` are
source-build additions after v0.12.0.

## Checked coding requests

`demo`, `work` and `flow` are included in
[Agentmux v0.12.0](getting-started/installation.md). `work` and `flow` select a Git project,
independently of an interactive workspace's `-s NAME` selector.

| Command | Use it to |
| --- | --- |
| `amx demo [--output NEW_DIR]` | Run a deterministic checked fixture, retain evidence, and apply its patch only to a generated disposable repository; no models |
| `amx work "Change description" -- CHECK [ARG...]` | Preview implementation, fresh review and checks without models or run files |
| `amx work "Change description" --allow-models -- CHECK [ARG...]` | Execute the bounded journey with the explicitly selected provider profiles |
| `amx work status [RUN\|latest]` | Inspect recorded phases, checks, review and next action for the project |
| `amx work show [RUN\|latest]` | Inspect the checked patch preview and retained HTML report path |
| `amx work list [--limit N]` | List retained runs and stable IDs for the selected project; omitted limit retains all history |
| `amx work show RUN --open` | Render a fresh private HTML report from saved evidence and open it in the system browser |
| `amx work apply [RUN\|latest]` | Revalidate and explicitly apply an approved result to the original project |
| `amx work recover [RUN\|latest]` | Resolve an interrupted application's journal against exact source content without replaying its patch |

Use `--repo DIR` for another Git project and `--catalog DIR` for a custom private
history. `--output DIR` selects a new result directory outside the project; its
parent must already exist. Run `amx work --help` for profile, deadline, attempt,
storage, containment and isolation controls. Checks after `--` are literal argv;
use a check script when shell logic is required. See [work on a task](working.md)
for the complete journey and [its visual overview](visual-guide.md#ask-for-a-checked-coding-change).

## Advanced workflow plans

| Command | Use it to |
| --- | --- |
| `amx flow run PLAN.json --repo DIR --output RUN` | Compare independent candidates from one captured revision |
| `amx flow mission PLAN.json --repo DIR --output RUN` | Execute bounded dependency steps and final acceptance checks |
| `amx flow watch PLAN.json --repo DIR --output RUN` | Review stable changed snapshots within duration and run-count limits |
| `amx flow lab PLAN.json --repo DIR --output RUN --repetitions 3` | Repeat comparisons against a pinned starting snapshot with rotated order |
| `amx flow check --repo DIR --checks CHECKS.json --output FILE` | Record revision-bound executable checks and a private sibling `FILE.git` fingerprint store |
| `amx flow status RUN`, `amx flow explain RUN` | Read the saved report, or explain evidence with patch integrity and current freshness |
| `amx flow report RUN --output FILE.html` | Render a private offline report to a new file |
| `amx flow handoff RUN [--candidate ID] --output FILE.md` | Save a private revision-bound handoff brief |
| `amx flow apply RUN [--candidate ID]` | Explicitly apply the selected checked result |
| `amx flow recover RUN [--candidate ID]` | Inspect and resolve an interrupted application |

Provider turns require `--allow-models`; command-only plans and checks can execute
owner-selected code without that flag. Plans are strict JSON and choose
`"execution": "local"`. Private Git storage is the default and does not confine
trusted host execution. Select `--isolation bubblewrap` for `work`, or a private
plan's `isolation` object for `flow`, to require filesystem, PID and network
boundaries on Linux. Required boundaries fail closed. Application is separate
from execution and never commits or pushes. See [advanced workflows](workflows.md)
for model-free examples, plan fields, exact saved-brief delivery and limitations.

## Everyday work sessions

Install `amx` using the [release installer or source build](getting-started/installation.md).
The [short command reference](getting-started/everyday-commands.md)
covers `amx a`, `ui`, `ls`, `link`, `mode`, and directional `allow`/`deny`.
Everyday commands accept `-s NAME` for the workspace, before or after the command.
Inside an owned tmux attachment the workspace is inferred from that exact
attachment; explicit selectors take precedence. Outside, it defaults to `work`.
`start` / `up` is the creation exception: its default is always `work`.

After installation, check `command -v amx`. With Claude Code and Codex installed and
logged in, run `amx start` from your project directory. It creates the
default workspace `work` and opens its workbench.

| Command | Use it to |
| --- | --- |
| `amx start` | Start the default native Claude/Codex team, or attach when that same team is running |
| `amx a` | Restore the last live native terminal, or open the workspace chooser |
| `amx split FIRST SECOND` | Combine existing native terminals in one tmux window |
| `amx unsplit ID` | Return one agent to its own window |
| `amx attach AGENT_ID` | Open one workspace-owned native conversation for direct typing |
| `amx dashboard` | Return to the owner workbench |
| `amx web --open` | Open the local owner app |
| `amx add --harness claude --project DIR --title "Task"` | Start another native session; copy its printed stable ID |
| `amx add --harness antigravity --native-only --project DIR` | Start native Antigravity without an injected Agentmux prompt |
| `amx sessions` | List registered sessions and separately observed external terminals |
| `amx context [--json]` | Explain the ordinary command target, selection source and runtime status without starting anything |
| `amx task TASK_ID [--json]` | Read one delegated instruction, its result, version, delivery and recovery evidence |
| `amx tasks [--to ID] [--status STATE] [--query TEXT] [--limit N] [--after TASK_ID] [--json]` | Search retained delegation history, newest first; copy the continuation command to read more |
| `amx workspaces [--register DIR]` | Find named and remembered custom workspaces; register an older custom location |
| `amx relaunch AGENT_ID [--resume ID\|last\|picker] [--native-launcher ABSOLUTE_PATH]` | Relaunch one exited native session, retaining its stable ID and healthy peers; optional launcher recovery applies only to trusted-local interactive sessions |
| `amx delivery AGENT_ID [manual\|review\|auto] [--approve CANDIDATE_ID]` | Inspect native delivery evidence, set receiver mode, or approve an exact queued item; `--help` lists limits and subscriptions |
| `amx connect OBSERVED_ID --title "Task"` | Prepare a helper for an existing external session; paste the printed setup into its original terminal |
| `amx label AGENT_ID "Task"` | Rename its display title while keeping its ID and conversation |
| `amx role AGENT_ID "Role"` | Change the owner role with CAS; managed workers use it at their next claim, native agents receive an inbox notice |
| `amx collaborate AGENT_ID OTHER_ID` | Give those sessions a dedicated board/memory group and allow notes/pings |
| `amx send AGENT_ID "Message" [--mode note\|instruction\|auto]` | Choose an informational inbox note or an instruction task; auto follows the agent's interaction mode |
| `amx inbox` | Inspect recent coordination and task activity as the owner |
| `amx board "Note" --group GROUP_ID` | Post to a particular collaboration group's board |
| `amx reply TASK_OR_MESSAGE_ID "Follow-up" [--mode auto\|note\|instruction]` | Follow up with broker-validated structured ancestry; shared board posts require `--board NAMESPACE` or `--to AGENT_ID` |
| `amx isolate AGENT_ID` | Revoke its peer coordination and memberships across groups; native input stays open |
| `amx remove AGENT_ID` | Close an owned terminal or disconnect an external bridge; retain saved history |
| `amx down` | Stop the workspace's owned processes while keeping saved data |

Replace uppercase placeholders with actual paths or IDs. `DIR` must already
exist. `--harness` chooses the native CLI (`claude`, `codex`, `pi`, or native-only `antigravity`);
`--title` is a display label and `--role` supplies optional agent instructions.
Choose `--native-only` to append no Agentmux prompt; it cannot be combined with
a nonempty role. Native Antigravity requires this option. Roles never grant
coordination permissions. Use `role ID ""` to clear a coordinated session's role.
Session titles can repeat; commands address the stable ID shown under the title.
An observed terminal must be connected before its registered ID can be used for
collaboration. A prepared bridge does not mean the agent read the pasted setup;
external sessions keep their original terminals for typing.

Outside an owned attachment, these interactive commands select workspace `work`
by default, regardless of your current directory. Inside an owned attachment,
they infer its checked workspace identity as described above. For another
workspace use `--session NAME` on each command, or select its exact directory
with `--state DIR`. For example:

```sh
amx start --session review --project /path/to/project
# In another terminal:
amx attach codex --session review
amx web --session review --open
amx down --session review
```

`send` queues coordination. Integrated native sessions can receive attributed
turns when their receiver policy is Review/Auto and current permissions allow;
Manual remains the default. It never types terminal keystrokes. See
[native delivery](native-delivery.md). Use `attach` to talk directly. With several groups, copy the
group ID printed by `collaborate` or shown under **Teams → Owner collaboration
groups** in the web app. `board` can choose the only active group automatically;
with multiple groups select `--group ID` or `--board NAMESPACE` explicitly.
Board replies use `reply ID "Text" --board NAMESPACE`; a private follow-up uses
`--to AGENT_ID`. Direct recipients must already be able to read the referenced
message. Broker-inferred `thread_id` and `reply_to` retain ancestry without
adding a prose prefix. There is no user-supplied thread-root override.
For terminal shortcuts, recovery and native history, see [work
sessions](work-sessions.md). For bounded headless studies see
[research](research.md).

## Consented teams and admission controls

Owner collaboration groups created by `collaborate` are distinct from dynamic
consented teams. The latter require `teams.enabled` and delegated capabilities
in the selected managed configuration; the everyday coding preset leaves them
disabled. Use `amx team --help` for the complete command syntax:

| Command | Behavior |
| --- | --- |
| `amx team list`, `amx team create NAME` | Inspect visible teams or create one within configured authority and lifetime limits |
| `amx team edit ID --name NAME --description TEXT` | Change live metadata, retaining the team's ID and namespaces |
| `amx team invite ID AGENT`, `amx team remove ID AGENT`, `amx team dissolve ID` | Review membership invitations, revocation or closure as a manager |
| `amx team transfer ID AGENT` | Propose ownership; the proposed owner must accept separately |
| `amx team join ID`, `amx team leave ID`, `amx team accept-owner ID` | Decide using `--credential-file FILE` for the actual acting participant |
| `amx team move SOURCE DESTINATION` | Use the participant's credential and an existing destination invitation to move atomically |

Add `--session NAME` or `--state DIR` to select the workspace. `--version N`
pins a reviewed version; moves also accept `--to-version N`. Omitted versions
are read once, never retried after a conflict or uncertain response. A human
owner cannot impersonate an agent's consent. See [dynamic teams](dynamic-teams.md).

`managed call` exposes effective boundaries and capacity through the selected
credential. For example, an agent's own shell can run:

```sh
"$AGENTMUX_CLI" managed call boundaries.get --json '{}'
"$AGENTMUX_CLI" managed call capacity.get --json '{}'
"$AGENTMUX_CLI" managed call budget.status --json '{}'
```

These bridge variables are supplied to workspace-native agents. An owner outside
their terminal selects `--credential-file STATE/owner.json` explicitly and can
inspect a particular agent with `capacity.get --json '{"agent":"AGENT_ID"}'`.
`capacity.update` is an owner-only CAS edit of complete admission-budget objects
and a shorter admission deadline; it does not rewrite launch configuration,
refund past reservations or impose a hard provider bill cap. The terminal
**B** editor and web capacity controls use the same broker rules. See [live
permissions and resources](live-permissions.md) for fields and constraints.

## Memory transfers and portable traces

Use `managed memory export|import|search|index|prune` for explicitly credential-
scoped memory operations; [memory lifecycle](memory-lifecycle.md) covers CAS,
TTL, replacement and asserted import provenance. These are separate from native
conversation history.

`managed trace list|verify|show` inspects owner-accessible local traces.
`managed trace export --state STATE --agent ID --id TRACE_ID --output NEW_FILE`
signs a locally verified trace without exporting secret keys. On another machine,
`managed trace verify-bundle --input FILE --trust OWNER_PUBLIC_KEY_SHA256`
requires an independently obtained trust fingerprint and no broker state.
Use `--mac TASK_MAC` during local verification/export to pin an existing task
anchor. These are owner-exporter attestations, not provider attestations; see
[traces and recovery](traces-and-recovery.md).

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
./bin/amx templates
./bin/amx init coding-starter coding.yaml
./bin/amx init review-starter review.yaml
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
./bin/amx init coding-starter coding.yaml
./bin/amx init default my-workflow.yaml
./bin/amx init minimal scratch.yaml
./bin/amx init ai-dev work/review.yaml --force
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
./bin/amx edit my-workflow.yaml
./bin/amx edit my-workflow
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
./bin/amx remember my-workflow ./my-workflow.yaml
./bin/amx edit my-workflow
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
./bin/amx validate my-workflow.yaml
./bin/amx validate my-workflow.yaml --strict
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
./bin/amx render my-workflow.yaml
./bin/amx render my-workflow.yaml --param project_dir=.
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
./bin/amx pack my-workflow.yaml
./bin/amx pack my-workflow.yaml my-team.amx
```

Useful note:
- `.amx` bundles can be loaded directly with `amx load my-team.amx`
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
./bin/amx tools --format openai
./bin/amx tools --format claude
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
./bin/amx prompt
./bin/amx prompt --provider claude
./bin/amx prompt --provider claude --mode native-mcp
./bin/amx prompt --provider gemini --mode native-mcp
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
./bin/amx tool-call kv_set --from-json '{"session":"demo","key":"task","value":"review"}'
./bin/amx tool-call kv_get --from-stdin <<EOF
{"session":"demo","key":"task"}
EOF
./bin/amx tool-call kv_set --from-file /tmp/agentmux-tool.json
./bin/amx tool-call wait --from-json '{"session":"demo","pane":"planner","pattern":"PLAN_READY","timeout":"30s"}'
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
./bin/amx load my-workflow.yaml
./bin/amx load https://example.com/team.amx
./bin/amx load my-workflow.yaml --replace --no-attach
./bin/amx load my-workflow.yaml --dry-run
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
./bin/amx reload my-workflow.yaml --no-attach
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
./bin/amx list
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
./bin/amx status my-workflow
./bin/amx status my-workflow --json
./bin/amx status my-workflow --pane review
./bin/amx status my-workflow --pane review --json
```

### `focus`

What it does:
- switches the active tmux client focus to a named pane

When to use it:
- when jumping directly to a pane by name instead of navigating manually

Examples:

```bash
./bin/amx focus my-workflow review
```

### `restart`

What it does:
- respawns a named pane with its stored startup command

When to use it:
- when a pane is stuck or you want to restart its workflow endpoint

Examples:

```bash
./bin/amx restart my-workflow review
```

### `rerun`

What it does:
- re-executes the stored startup command for a named pane

When to use it:
- when you want to re-run the pane’s original command in place

Examples:

```bash
./bin/amx rerun my-workflow review
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
./bin/amx stop my-workflow review
```

### `kill`

What it does:
- destroys the target tmux pane entirely

When to use it:
- when you really want the pane removed from the layout

Examples:

```bash
./bin/amx kill my-workflow build
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
./bin/amx capture my-workflow review
./bin/amx capture my-workflow review --tail 20
./bin/amx capture my-workflow review --tail 20 --strip-empty
./bin/amx capture my-workflow review --json --tail 20 --strip-empty
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
./bin/amx send my-workflow review -- "git status --short"
./bin/amx send my-workflow review --no-enter -- "printf 'queued\n'"
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
./bin/amx dashboard my-workflow
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
./bin/amx inspect my-workflow reviewer
./bin/amx inspect my-workflow reviewer --json
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
./bin/amx kv-set my-team current_task "researching-agent-scaling"
```

### `kv-get`

What it does:
- retrieves a value from the session's Blackboard

Examples:

```bash
./bin/amx kv-get my-team current_task
```

### `kv-list`

What it does:
- lists all keys and values currently stored in the Blackboard

Examples:

```bash
./bin/amx kv-list my-team
./bin/amx kv-list my-team --json
```

### `kv-del`

What it does:
- removes a key from the Blackboard

Examples:

```bash
./bin/amx kv-del my-team current_task
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
./bin/amx wait my-team builder "SUCCESS" --timeout 5m
./bin/amx wait my-team reviewer '^• REVIEW_DONE$' --timeout 2m
./bin/amx wait my-team builder "SUCCESS" --scope visible --timeout 5m
```

### `scale`

What it does:
- dynamically adds or removes instances of a specific agent

When to use it:
- to adjust workforce based on load (e.g., spawn 3 more "Researchers")

Examples:

```bash
./bin/amx scale my-team researcher 5
./bin/amx scale my-team researcher 1
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
./bin/amx doctor
./bin/amx doctor ./shared-team-config.yaml
```

### `version`

What it does:
- prints the current Agentmux version string

Examples:

```bash
./bin/amx version
./bin/amx --version
```

### `--help`

What it does:
- prints built-in CLI usage

Examples:

```bash
./bin/amx --help
```

## Common Flows

### New workflow

```bash
./bin/amx templates
./bin/amx init default my-workflow.yaml
./bin/amx validate my-workflow.yaml
./bin/amx load my-workflow.yaml --replace --no-attach
```

### Edit and reapply

```bash
./bin/amx edit my-workflow
./bin/amx validate my-workflow.yaml
./bin/amx reload my-workflow.yaml --no-attach
```

### Inspect and interact with a live pane

```bash
./bin/amx status my-workflow --pane review
./bin/amx capture my-workflow review --tail 20 --strip-empty
./bin/amx send my-workflow review "git status --short"
```
