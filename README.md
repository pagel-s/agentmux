# Agentmux

Licensed under the [MIT License](LICENSE). [Dependency notices](docs/third-party-notices.txt) accompany the binaries.

Run coding agents together, control what they share, and inspect checked changes
before applying them to your project. Agentmux works with Claude Code, Codex, Pi
and Antigravity. The managed runtime and workbench are experimental.

## Get your first checked result

[Install v0.14.0](docs/getting-started/installation.md#install-a-published-binary-recommended)
for Linux or macOS 13+, then run:

```sh
amx demo
```

The demo creates a disposable Git project, makes a deterministic change, runs an
independent check, and applies the checked patch **only to that fixture**. It
prints the retained report and patch locations. After installation, it needs Git
and a POSIX shell; no provider account, model calls, Python or tmux is needed.
This demonstrates workflow mechanics, not AI task quality. Follow
[your first checked result](docs/getting-started/first-result.md) to inspect the evidence.

## Use it on your project

Install and sign in to Codex and Antigravity, the default coder and reviewer.
From a Git project with an initial commit, preview a task and its check:

```sh
amx work "Fix the login redirect" -- go test ./...
```

The preview starts no models and creates no run files. Replace `go test ./...`
with your project's check. Add `--allow-models` to run the selected native CLIs:

```sh
amx work "Fix the login redirect" --allow-models -- go test ./...
amx work status
amx work show latest
```

Inspect the saved report and patch, then choose `amx work apply latest` to change
your project. See [work on a task](docs/working.md) for provider choices, fresh
reviews, deadlines and recovery. Each run retains private Git evidence;
filesystem and network isolation require the explicit
[Linux isolation option](docs/workflows.md#private-storage-and-full-isolation).

Agentmux v0.14.0 skips the response and second review when the first fresh
review approves the same checked revision. Without coding retries, that uses
two native turns. Requested changes retain the full four-turn review loop,
and final acceptance checks always run. Skipped phases
remain explicit in the saved evidence. Published v0.13.0 uses the full loop.

[![Actual checked-result report from a model-free fixture, showing retained checks and a reviewable patch](docs/assets/guide/checked-work-report.jpg?v=d3cdf4a8e036)](docs/visual-guide.md#ask-for-a-checked-coding-change)

The report above comes from a command-only fixture. It demonstrates the evidence
and application controls; it does not measure provider task quality.

For several native conversations, use [work sessions](#start-a-work-session).
For custom comparisons, missions and handoffs, see [advanced workflows](docs/workflows.md).

## Install or update

The [installation guide](docs/getting-started/installation.md) provides a
checksum-verified installer for Linux/macOS on x86-64 and ARM64, plus upgrade
instructions. Published binaries need no Go toolchain.

### Build from source

Building requires Git, Make and Go 1.24+; use the maintained Go 1.27.3 toolchain for current builds. `make install` also uses Python 3.
Native sessions require tmux 3.2+. Isolated profiles require Linux with working
Bubblewrap namespaces. Everyday native sessions use trusted local execution to
preserve your normal configuration; isolation is opt-in.

```sh
git clone https://github.com/pagel-s/agentmux.git
cd agentmux
make build
./bin/amx demo
export PATH="$PWD/bin:$PATH"
```

Already in a checkout? Run `make build` from its root. The Makefile selects a
compatible repo-local Go toolchain when available. See
[installation](docs/getting-started/installation.md) for binary releases and PATH
setup. Remote `go install …@latest` is currently unsupported by this module.

v0.13.0 includes workspace targeting with `amx context`, exact task opening
with `amx task ID --open`, searchable workspace selection and direct session
task history. `amx observe` brings broker availability, observed worker state,
live reporting and retained task attention into one read-only view; `--watch --json`
records JSONL. See [observability](docs/observability.md) for fresh/stale evidence
and exact-task drilldown. These controls ship in [v0.13.0](docs/getting-started/installation.md).
Existing workspaces retain their pinned controller until explicitly upgraded.

v0.14.0 also adds fixed diagnostic error codes and local collection
timings to observation JSON. Repository tools summarize saved observation captures
and matched comparison evidence offline:

```sh
python3 scripts/summarize-observations.py /private/observe.jsonl --json
python3 scripts/summarize-comparison.py /private/comparison-evidence --json
```

These Python 3 tools start no models and change no workspace state. Observation
reports preserve gaps and unknown values; comparison reports retain failures and
incomplete attempts. They do not establish uptime, task quality or a competitive
winner. See [observability](docs/observability.md) and
[evaluation](docs/evaluation.md) for capture instructions and evidence limits.

`make install` installs `amx`, the Agentmux command. Use
`amx` to open the workbench, `amx a` to restore the last live terminal, `amx a claude -s work`
to attach, and `amx link claude codex -s work` to connect a pair. See the
[everyday command reference](docs/getting-started/everyday-commands.md).
Use **Ctrl+K** for searchable Actions and **Ctrl+P** for sessions. See the
[control center guide](docs/usability-control-center.md) for quick launch,
attention, exact pair controls, recovery, retirement and study observation.

For screenshots and practical journeys from everyday sessions to dynamic teams,
competition studies and remote workers, read the
[visual workflow guide](docs/visual-guide.md).

For a complete native-session exercise, follow the [fresh-install
walkthrough](docs/getting-started/quickstart.md). It uses a separate workspace
and covers direct native interaction, opt-in collaboration, shared memory,
revocation, web controls and cleanup with expected results at each step.

## Start a work session

Install and log in to Claude Code and Codex first, then run this from your
project directory:

```sh
amx start
```

This creates workspace `work` with native Claude and Codex sessions in your
project. Your local tools, skills, MCPs, login and native history remain available.
Each session has a stable Agentmux ID. Collaboration and autonomous wakeups start
off; you decide which sessions can work together. An interactive workspace runs
until you close it; managed automation retains explicit runtime budgets.

The workspace name (`work`) selects the running team; a session ID (`claude`,
`codex`, or an ID printed by `add`) selects one conversation inside it. Commands
default to workspace `work` outside Agentmux. Inside an owned tmux attachment,
omitting the selector uses that attachment's workspace after checking its identity.
Use `-s NAME` (or `--session NAME`) to select another workspace explicitly.
`start` / `up` creates or starts `work` by default, even inside another workspace.

Give sessions task titles such as “Fix login redirect”; the title can change
while its ID and native conversation stay the same. The overview shows project,
provider and ID, with creation/start time and terminal location in session details.
Press **Ctrl+P** (or **/**) to find a session: **Enter** opens its native terminal,
**Tab** selects it in the workbench, and **Esc** returns. **Shift+L** edits its title. The web app has the same
search and title controls. New sessions can generate their IDs automatically:

```sh
amx add --harness claude --project ~/projects/api --title "Fix login redirect"
amx label SESSION_ID "Investigate cookie expiry"
amx role SESSION_ID "Review the authentication changes"
amx workspaces
```

Press **Shift+V** to edit the selected session's role with version checking and **Shift+W**
to inspect other workspaces. Native sessions receive role changes as inbox
notices; managed workers use the current role at their next task claim. For a
prompt-free launch, use `add --native-only`; it appends no Agentmux context or
role. Native Antigravity uses `--harness antigravity --native-only`.

Use **Tab** to focus the session sidebar, select an agent with ↑/↓ and press
**Enter** for its native CLI. Tab focuses content so ↑/↓ selects messages, tasks,
memory entries and teams. **F1** returns to
the workbench; **Ctrl-b b** opens the board as a popup; **Ctrl-b d** detaches.
For a Ctrl-b shortcut, press Ctrl-b, release both keys, then press the final key.
Open another terminal window with `attach`: each attachment switches agents
independently and has its own dashboard and drafts, while sharing the same
underlying agent terminals. **Ctrl-b**, release, then **Shift+D** opens read-only
runtime service logs.
In the workbench itself, use **←/→** or **1–8** to switch views; popup shortcuts show the
corresponding view key instead of opening a second dashboard over it.

```sh
amx add billing --harness claude --project ~/projects/billing
amx add review --harness codex --project ~/projects/api --role "Review changes"
amx attach billing
amx collaborate claude review
amx send review "Please read the proposal when we discuss it." --mode note
amx isolate review
amx remove billing
amx web --open
```

`collaborate` creates a dedicated board and shared memory for the selected IDs.
Two unrelated groups do not silently share context. `isolate` revokes peer
coordination and group membership while keeping native interaction running.
Accepted tasks need separate cancellation; previously read information remains.
Keep the printed group ID: with several groups, post using
`amx board "Note for this group" --group GROUP_ID`. The web app's **Groups & teams** view
lists owner collaboration groups and their boards/memory.
`send` defaults to an inbox note for native sessions and an instruction task for
managed workers. Choose `--mode note` or `--mode instruction` explicitly; the
terminal editor uses **Ctrl-T** and the web composer shows the same intent.
To receive instructions inside a native conversation, also choose receiver
delivery: `amx delivery codex review` or `amx delivery codex auto`.
Manual is the default. Command permissions and delivery mode are separate;
**Sessions** in the terminal and **Session detail** on the web expose delivery
evidence and approvals. See
[native collaboration](docs/native-delivery.md) for subscriptions, limits,
provider capabilities and upgrade instructions.
Replies retain broker-checked ancestry and an explicit shared or private
audience. Messages never become unsolicited terminal keystrokes. An exited owned
native session can be relaunched under its existing ID without restarting peers:
`amx relaunch review --resume last` selects native history deliberately.

Existing sessions can join without restarting their conversation:

```sh
amx sessions
amx connect OBSERVATION_ID --title "Review API changes"
# Paste the printed setup once into that existing native session.
```

Discovery reads live terminal/process metadata, not prompts or transcripts.
Observed sessions and prepared bridges are labeled separately: preparing a helper
does not prove the agent has read its setup. Generic interpreter processes may
not be identifiable. Existing external terminals remain under your direct control.

Use `start --sandbox bubblewrap` for isolated profiles, `--session NAME` for a
separate workspace, or `start --config team.yaml` for a configured mixed team.
Configuration and state live under `$XDG_STATE_HOME/agentmux` (default
`~/.local/state/agentmux`), outside the writable projects. Run `amx down` to
stop the workspace's owned processes. External bridged terminals are never killed.

See [work sessions](docs/work-sessions.md) for dashboard keys, mixed teams,
native history and current limits.

See [terminal folders and layouts](docs/terminal-workflow.md) for per-session
projects, ordinary shell splits, `amx split A B`, `amx unsplit ID`, scratch-window
persistence and the exact behavior of `amx a`.

## Observe and control

The terminal dashboard provides agent selection, terminal previews, recent
coordination, searchable retained message/event/task history, follow-ups,
cancellation and live permissions. In Tasks, **f** searches and **PgUp/PgDn** pages; **?** opens context
help, including **Ctrl-G** while editing without consuming printable text.
Teams and Events expose consented membership, pending invitations, spawn lineage,
worker admission state and recent lifecycle changes. Team boards and memory appear
in the same namespace controls as configured shared knowledge.
`amx web` serves a compact local owner app with the same communication
controls, shared-memory inspection/editing, session setup/discovery, a relationship
graph and read-only terminal previews. Native
terminal input stays in tmux.

In v0.13.0, use these shortcuts for daily inspection:

```sh
amx context -s work
amx tasks --to codex -s work
amx task TASK_ID --open -s work
```

The last command opens that exact retained task with its evidence and recovery
controls, including older tasks and retired recipients. Recovery still requires
explicit confirmation against the task/version. In terminal and web Actions,
**Tasks for selected session** opens that recipient's complete history; web
attention rows also offer **Inspect tasks**. Board and Events page/search
retained broker history separately from native-provider conversations.

Collaboration groups contribute permissions independently, including overlapping
groups. The controls distinguish stored owner overrides, effective access and
isolation; historical groups require an explicit migration review. Consented
teams support live name/mission changes, ownership proposals and participant-
accepted moves. Capacity views explain remaining lifetime allocations and
inherited denials; owner budget edits govern future admissions.

Messages, pings and commands are separate directional permissions. Board and
shared-memory read/write permissions are also separate. Changes are versioned,
audited and retained across broker restarts. They govern future broker
operations; cancel accepted tasks separately. Removing read access cannot erase
information already received. Agents query their effective permissions with
`managed call boundaries.get`.

The web app binds only to numeric loopback and requires a private owner link
for API access. It is a local control surface, not a hosted multi-user service.

## Managed automation and research

Managed workers support authenticated scoped messages, durable tasks, optional
private persistent conversations and explicitly configured observation wakeups.
Owner-approved templates enable bounded spawning; dynamic teams use consent-based
membership and scoped boards/memory.

`managed setup --preset reviewer-pair` and `--preset research-team` create idle
managed role presets. They queue no tasks or autonomous goal. The research runner
also supports declared role/personality prompt treatments, counterbalanced
condition orders, supported behavioral primary outcomes and small/scripted-
evidence warnings. Prompt treatments do not establish intrinsic agent traits.

Research runs remain observable and steerable: start with `research run …
--start-paused`, open the printed terminal/web console, and inspect or advance one
phase at a time. Pause/resume/step/stop apply at phase barriers; task cancellation
is separate. Owner steering is recorded and assisted runs are excluded from
default research estimates. Research turns are headless; native terminal typing
belongs to the interactive work-session workflow.

Claude, Codex, Pi and Antigravity have managed adapters. Codex supports explicit
Responses endpoints; Pi connects to compatible Chat Completions endpoints,
including Ollama/vLLM. The Antigravity bridge combines native local inference
with isolated Pi tools/history. Provider and tool coverage vary: consult the
[provider compatibility guide](docs/provider-compatibility.md) for supported
routes, setup checks and integration limits.

Try communication and terminal controls without any model calls:

```sh
python3 scripts/smoke-managed.py
python3 scripts/smoke-managed-interactive.py
```

Guides: [setup](docs/managed-setup.md), [managed runtime](docs/managed-runtime.md),
[autonomous workflows](docs/autonomous-workflows.md),
[dynamic teams](docs/dynamic-teams.md), [memory](docs/memory-lifecycle.md),
[hardening](docs/hardening.md), [traces/recovery](docs/traces-and-recovery.md),
[remote workers](docs/remote-workers.md), [research](docs/research.md) and
[controlled studies](docs/research-study.md).

Memory imports retain structured source claims; portable trace bundles verify
an explicitly trusted owner exporter's signature. Neither establishes provider
attestation or the truth of an agent's output.

The [trust model](docs/security-model.md) and
[validation guide](docs/workflow-validation.md) describe enforced boundaries,
reproducible checks and operational limits. Coordination permissions do not isolate
files in a deliberately shared project, guarantee model correctness or provide
a provider spending cap.

## Existing tmux workspace mode

The original YAML layout compiler remains available through `templates`, `init`,
`render`, `load` and `reload`, plus pane capture, raw input and tmux KV tools.
Its `dashboard SESSION` and `send SESSION PANE -- COMMAND` commands retain their
original behavior. These primitives do not provide the managed broker's
per-agent authorization.

[Workspace quickstart](docs/getting-started/quickstart.md#existing-workspace-mode)
· [Portable bundles](docs/tutorials/portable-bundles.md)
· [Documentation](https://pagel-s.github.io/agentmux/)

For development and evaluation, see [contributing](CONTRIBUTING.md),
[security reporting](SECURITY.md), [quality and evidence](docs/quality.md), and
[matched workflow comparisons](docs/evaluation.md).
