# Agentmux

Run Claude Code, Codex and Pi in tmux with a shared communication board,
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

## Start a work session

Install and log in to Claude Code and Codex first, then run this from your
project directory:

```sh
agentmux start
```

This creates workspace `work` with native Claude and Codex sessions in your
project. Your local tools, skills, MCPs, login and native history remain available.
Each session has a stable Agentmux ID. Collaboration and autonomous wakeups start
off; you decide which sessions can work together. The native-only preset runs until you close it; managed automation retains
explicit runtime budgets.

Give sessions task titles such as “Fix login redirect”; the title can change
while its ID and native conversation stay the same. The overview shows project,
provider and ID, with creation/start time and terminal location in session details.
Press **/** to find a session and **L** to edit its title. The web app has the same
search and title controls. New sessions can generate their IDs automatically:

```sh
agentmux add --harness claude --project ~/projects/api --title "Fix login redirect"
agentmux label SESSION_ID "Investigate cookie expiry"
```

Select an agent with ↑/↓ and press **Enter** for its native CLI. **F1** returns to
the workbench; **Ctrl-b b** opens the board as a popup; **Ctrl-b d** detaches.
Open another terminal window with `attach`: each attachment switches agents
independently while sharing the same underlying sessions.

```sh
agentmux add billing --harness claude --project ~/projects/billing
agentmux add review --harness codex --project ~/projects/api --role "Review changes"
agentmux attach billing
agentmux collaborate claude review
agentmux send review "Please read the proposal when we discuss it."
agentmux isolate review
agentmux remove billing
agentmux web --open
```

`collaborate` creates a dedicated board and shared memory for the selected IDs.
Two unrelated groups do not silently share context. `isolate` revokes peer
coordination and group membership while keeping native interaction running.
Accepted tasks need separate cancellation; previously read information remains.
`send` queues an inbox note for native sessions and an instruction task for managed
workers. Messages never become unsolicited terminal keystrokes.

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
coordination, task results, follow-ups, cancellation and live permissions.
`agentmux web` serves a compact local owner app with the same communication
controls, shared-memory inspection/editing, session setup/discovery, a relationship
graph and read-only terminal previews. Native
terminal input stays in tmux.

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

Claude, Codex and Pi have managed adapters. Pi connects to configured compatible
endpoints, including Ollama/vLLM. The Antigravity bridge combines native local
inference with isolated Pi tools/history. Provider and tool coverage vary:
consult the [validation matrix](docs/provider-validation.md).

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
