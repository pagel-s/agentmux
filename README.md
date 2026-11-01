# Agentmux

Run Claude Code, Codex, Pi and native Antigravity in tmux with a shared communication board,
private/shared memory, and explicit permissions between agents.

Use native interactive terminals for a human-led workday, managed workers for
queued tasks and bounded autonomous workflows, or a mixture of both. Terminal
and local web dashboards expose activity and the same live permission controls.
The managed runtime and workbench are experimental.

## Build

Requirements: Go 1.24+, Make and tmux 3.2+. Isolated agents and an isolated broker
require Linux with working Bubblewrap namespaces. Everyday native sessions use trusted local execution to preserve your normal
configuration; it provides no filesystem isolation. Isolated profiles are opt-in.

```sh
git clone https://github.com/pagel-s/agentmux.git
cd agentmux
make build
./bin/agentmux doctor
export PATH="$PWD/bin:$PATH"
```

Already in a checkout? Run `make build` from its root. The Makefile selects a
compatible repo-local Go toolchain when available. See
[installation](docs/getting-started/installation.md) for binary releases and PATH
setup. Remote `go install …@latest` is currently unsupported by this module.

For the complete first-run test, follow the [fresh-install
walkthrough](docs/getting-started/quickstart.md). It uses a separate workspace
and covers direct native interaction, opt-in collaboration, shared memory,
revocation, web controls and cleanup with expected results at each step.

## Start a work session

Install and log in to Claude Code and Codex first, then run this from your
project directory:

```sh
agentmux start
```

This creates workspace `work` with native Claude and Codex sessions in your
project. Your local tools, skills, MCPs, login and native history remain available.
Each session has a stable Agentmux ID. Collaboration and autonomous wakeups start
off; you decide which sessions can work together. An interactive workspace runs
until you close it; managed automation retains explicit runtime budgets.

The workspace name (`work`) selects the running team; a session ID (`claude`,
`codex`, or an ID printed by `add`) selects one conversation inside it. Commands
default to workspace `work`, including when run from another project directory.
Use the same `--session NAME` on later commands when you start a named workspace.

Give sessions task titles such as “Fix login redirect”; the title can change
while its ID and native conversation stay the same. The overview shows project,
provider and ID, with creation/start time and terminal location in session details.
Press **/** to find a session and **Shift+L** to edit its title. The web app has the same
search and title controls. New sessions can generate their IDs automatically:

```sh
agentmux add --harness claude --project ~/projects/api --title "Fix login redirect"
agentmux label SESSION_ID "Investigate cookie expiry"
agentmux role SESSION_ID "Review the authentication changes"
agentmux workspaces
```

Press **Shift+V** to edit the selected session's role with version checking and **Shift+W**
to inspect other workspaces. Native sessions receive role changes as inbox
notices; managed workers use the current role at their next task claim. For a
prompt-free launch, use `add --native-only`; it appends no Agentmux context or
role. Native Antigravity uses `--harness antigravity --native-only`.

Select an agent with ↑/↓ and press **Enter** for its native CLI. **F1** returns to
the workbench; **Ctrl-b b** opens the board as a popup; **Ctrl-b d** detaches.
For a Ctrl-b shortcut, press Ctrl-b, release both keys, then press the final key.
Open another terminal window with `attach`: each attachment switches agents
independently and has its own dashboard and drafts, while sharing the same
underlying agent terminals. **Ctrl-b**, release, then **Shift+D** opens read-only
runtime service logs.
In the workbench itself, use **1–8** to switch views; popup shortcuts show the
corresponding view key instead of opening a second dashboard over it.

```sh
agentmux add billing --harness claude --project ~/projects/billing
agentmux add review --harness codex --project ~/projects/api --role "Review changes"
agentmux attach billing
agentmux collaborate claude review
agentmux send review "Please read the proposal when we discuss it." --mode note
agentmux isolate review
agentmux remove billing
agentmux web --open
```

`collaborate` creates a dedicated board and shared memory for the selected IDs.
Two unrelated groups do not silently share context. `isolate` revokes peer
coordination and group membership while keeping native interaction running.
Accepted tasks need separate cancellation; previously read information remains.
Keep the printed group ID: with several groups, post using
`agentmux board "Note for this group" --group GROUP_ID`. The web app's Teams view
lists owner collaboration groups and their boards/memory.
`send` defaults to an inbox note for native sessions and an instruction task for
managed workers. Choose `--mode note` or `--mode instruction` explicitly; the
terminal editor uses **Ctrl-T** and the web composer shows the same intent.
Replies retain broker-checked ancestry and an explicit shared or private
audience. Messages never become unsolicited terminal keystrokes. An exited owned
native session can be relaunched under its existing ID without restarting peers:
`agentmux relaunch review --resume last` selects native history deliberately.

Existing sessions can join without restarting their conversation:

```sh
agentmux sessions
agentmux connect OBSERVATION_ID --title "Review API changes"
# Paste the printed setup once into that existing native session.
```

Discovery reads live terminal/process metadata, not prompts or transcripts.
Observed sessions and prepared bridges are labeled separately: preparing a helper
does not prove the agent has read its setup. Generic interpreter processes may
not be identifiable. Existing external terminals remain under your direct control.

Use `start --sandbox bubblewrap` for isolated profiles, `--session NAME` for a
separate workspace, or `start --config team.yaml` for a configured mixed team.
Configuration and state live under `$XDG_STATE_HOME/agentmux` (default
`~/.local/state/agentmux`), outside the writable projects. Run `agentmux down` to
stop the workspace's owned processes. External bridged terminals are never killed.

See [work sessions](docs/work-sessions.md) for dashboard keys, mixed teams,
native history and current limits.

## Observe and control

The terminal dashboard provides agent selection, terminal previews, recent
coordination, searchable complete task history, follow-ups, cancellation and live
permissions. In Tasks, **f** searches and **PgUp/PgDn** pages; **?** opens context
help, including **Ctrl-G** while editing without consuming printable text.
Teams and Events expose consented membership, pending invitations, spawn lineage,
worker admission state and recent lifecycle changes. Team boards and memory appear
in the same namespace controls as configured shared knowledge.
`agentmux web` serves a compact local owner app with the same communication
controls, shared-memory inspection/editing, session setup/discovery, a relationship
graph and read-only terminal previews. Native
terminal input stays in tmux.

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
[validation matrix](docs/provider-validation.md) and the [dated live provider
acceptance](docs/provider-acceptance.md).

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

The [UAT gap closure record](docs/uat-gap-closure.md) maps the previous open points
to implementation, regression coverage and explicit limits. Memory imports retain
structured source claims; portable trace bundles verify an explicitly trusted
owner exporter's signature. Neither establishes provider attestation or the
truth of an agent's output.

The [trust model](docs/security-model.md) and
[validation record](docs/release-validation.md) describe enforced boundaries,
measured coverage and remaining evidence. Coordination permissions do not isolate
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
