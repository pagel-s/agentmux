# Agentmux

Run Claude Code, Codex and other native agents together. Delegate work, control
who can instruct whom, and inspect checked changes before applying them.
Agentmux is [MIT licensed](LICENSE); [dependency notices](docs/third-party-notices.txt)
ship with its binaries. The managed runtime and workbench are experimental.

## Start with your existing subscriptions

[Install Agentmux](docs/getting-started/installation.md), and sign in to your
native Claude Code and Codex CLIs once. From your project:

```sh
amx start --detach
amx pair claude codex --delivery auto
```

The second command previews task pairing without changing anything. Add
`--apply` when ready; that invocation checks and prints the current review again:

```sh
amx pair claude codex --delivery auto --apply
amx attach claude
```

You can now ask Claude to delegate a task to Codex and receive its result.
The pair permits commands in both directions and enables returned results.
Instructions arrive as agent-attributed turns. Auto applies to each receiver's
eligible existing queue and future work from all permitted senders; its existing
limits remain in force. Choose `--delivery review` for approval of each exact item.
Ordinary `amx link` still enables scoped chat and shared memory separately.

Task pairing and installed-provider selection require **v0.15.0 or later**.
An existing workspace keeps its pinned controller; use a fresh workspace or
[upgrade it deliberately](docs/traces-and-recovery.md).

The [short quickstart](docs/getting-started/quickstart.md) covers the complete
setup. The [optional verification walkthrough](docs/getting-started/native-walkthrough.md)
checks permissions and recovery step by step.

## Daily controls

| Want to… | Use |
| --- | --- |
| Return to the workbench | `amx` |
| Open a native conversation | `amx attach SESSION_ID` |
| Find a session or action | **Ctrl+P** or **Ctrl+K** |
| Change sender / recipient in Connections | **[** / **]**, then **↑/↓** |
| Pair the selected sessions for tasks | **Shift+C** in Connections |
| Inspect delivery or approve queued work | **Shift+D** / **Shift+A** |
| Inspect one workspace's health and attention | `amx observe` |
| Inspect delegated work | `amx tasks`, then `amx task TASK_ID` |
| Inspect a checked coding run | `amx work status`, `amx work show latest` |
| Stop the workspace | `amx down` |

Commands target workspace `work` outside an Agentmux attachment. Use `-s NAME`
for another workspace; `amx context` explains the selected target. Detaching
with **Ctrl-b**, release, then **d** keeps agents running. The
[everyday command reference](docs/getting-started/everyday-commands.md) covers
adding sessions, shared boards and individual recovery.

## Ask for a checked coding change

In a Git project with an initial commit:

```sh
amx work "Fix the login redirect" -- go test ./...
```

The preview shows the selected installed providers, check and deadline without
starting a model or writing run files. With Codex and Claude available, it uses
Codex for coding and Claude for fresh review. One installed CLI can serve both
roles in separate fresh contexts. Discovery checks executables, not login.
Use `--coder` and `--reviewer` to pin explicit profiles.
Turns and checks share `--timeout`; the preview shows each role's turn limit.

When the preview is suitable:

```sh
amx work "Fix the login redirect" --allow-models -- go test ./...
amx work show latest
amx work apply latest
```

The result stays in a separate private worktree until explicit application.
A clean initial approval can skip redundant response/review turns; final checks
always run. Replace `go test ./...` with your project's acceptance check.
See [work on a task](docs/working.md) for isolated profiles and recovery.


To inspect that workflow without provider setup, run `amx demo`. It makes and
checks a deterministic change only in its own disposable project; it measures
workflow mechanics, not model quality.

## Observe what happened

```sh
amx observe
amx tasks
amx task TASK_ID
```

Observation separates reporting, delivery uncertainty and verified results.
`amx observe --watch --json` records JSONL. Bundled Python tools summarize saved
[observation](docs/observability.md) and [matched-comparison](docs/evaluation.md)
evidence offline, preserving gaps, failures and unknowns.

## Build from source

A published binary needs no Go toolchain. Source builds require Git, Make and
Go 1.24+; current distributions use Go 1.27.3. Native sessions need tmux 3.2+.

```sh
git clone https://github.com/pagel-s/agentmux.git
cd agentmux
make build
./bin/amx demo
export PATH="$PWD/bin:$PATH"
```

Native sessions use trusted local execution with your normal tools, login,
skills and history. Linux filesystem/network isolation is an explicit option
requiring working Bubblewrap namespaces; worktrees alone are not a sandbox.
See [installation and upgrades](docs/getting-started/installation.md).

## More when you need it

- [Illustrated workflow guide](docs/visual-guide.md) · [control center](docs/usability-control-center.md)
- [Providers and managed setup](docs/managed-setup.md) · [advanced workflows](docs/workflows.md)
- [Permissions](docs/live-permissions.md) · [native delivery](docs/native-delivery.md)
- [Observability](docs/observability.md) · [traces and recovery](docs/traces-and-recovery.md)
- [Autonomous teams](docs/autonomous-workflows.md) · [research](docs/research.md)
- [Quality and evidence](docs/quality.md) · [evaluation](docs/evaluation.md)
- [Contributing](CONTRIBUTING.md) · [security reporting](SECURITY.md)

The original YAML/tmux layout compiler remains available through `templates`,
`init`, `render`, `load` and `reload`. See the
[layout walkthrough](docs/getting-started/native-walkthrough.md#existing-workspace-mode)
and [portable bundles](docs/tutorials/portable-bundles.md). It has separate semantics
from the managed runtime's authenticated permissions.
