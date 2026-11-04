# Agentmux

Run coding agents together, control what they share, and inspect checked changes
before applying them to your project.

## Start with your native subscriptions

[Install Agentmux](getting-started/installation.md) and sign in to Claude Code
and Codex. From your project, start a workspace and review task pairing:

```sh
amx start --detach
amx pair claude codex --delivery auto
```

Pairing previews the mutual command grants, both delivery modes, returned
results and existing queues. Add `--apply` after reviewing it, then
`amx attach claude`. Auto can start eligible existing work and future work from
all permitted senders; choose `--delivery review` for exact item approval.
Task pairing and installed-provider selection require v0.15.0 or later.
The [short quickstart](getting-started/quickstart.md) covers ordinary setup.

For a checked coding request in a separate worktree, preview `amx work "Change" -- CHECK`.
To inspect the mechanics without provider setup, run `amx demo`; it changes only
its own disposable Git fixture. See [your first checked result](getting-started/first-result.md).

## Choose your starting point

| Goal | Guide | Execution starts when |
| --- | --- | --- |
| Inspect a checked fixture without configuring providers | [Your first checked result](getting-started/first-result.md) | Run `amx demo`; it changes only its generated repository |
| Ask for one checked coding change | [Work on a task](working.md) | Add `--allow-models` after inspecting the preview |
| Type directly in several native conversations | [Short native quickstart](getting-started/quickstart.md) | Start a workspace and use its native terminals |
| Inspect reporting, pending work or uncertain delivery | [Observability](observability.md) | Inspection starts no execution; choose the workspace explicitly |
| Compare alternatives, build a mission or review changed snapshots | [Advanced workflows](workflows.md) | Run an owner-authored plan; provider turns require `--allow-models` |
| Queue tasks or enable bounded autonomous teams | [Managed setup](managed-setup.md) | Launch a reviewed team configuration |
| Run controlled cooperation or competition studies | [Research](research.md) | Run a declared study; model participants require explicit opt-in |

The [visual guide](visual-guide.md) illustrates the coding journey and the native
terminal/web controls. [Work sessions](work-sessions.md) covers session discovery,
workspace switching and individual-session recovery. A
[source build](getting-started/installation.md#build-from-source) remains an
alternative to release binaries.

v0.15.0 includes the daily power-user controls: `amx context` explains the selected workspace, `amx tasks`
searches retained delegation history, and `amx task ID --open -s NAME` opens
the exact task's evidence and recovery controls. `amx observe` summarizes fresh
broker/worker reporting and complete retained task counts, with bounded attention
and optional JSONL watching. Both terminal and web Actions
offer **Tasks for selected session**. Upgrade existing workspace controllers explicitly;
see [everyday commands](getting-started/everyday-commands.md) for the five daily loops.

Keep the [everyday command reference](commands.md#everyday-work-sessions) handy
for selecting named workspaces, adding or connecting sessions, and choosing a
collaboration group's board. For a repeated-round study, start with the
[research planning and observation workflow](research.md).

The managed runtime and workbench are experimental. Broker permissions, native
approvals, provider networking and filesystem isolation are separate boundaries.
The [trust model](security-model.md) and [validation guide](workflow-validation.md)
describe enforced boundaries, reproducible checks and limits.

## Configure and automate

- [Setup and providers](managed-setup.md)
- [Checked coding requests](working.md) and [advanced workflow plans](workflows.md)
- [Workflow validation and boundaries](workflow-validation.md)
- [Managed runtime](managed-runtime.md)
- [Persistent conversations and autonomous wakeups](autonomous-workflows.md)
- [Dynamic teams and bounded spawning](dynamic-teams.md)
- [Private/shared memory](memory-lifecycle.md)
- [Live permissions and admission controls](live-permissions.md)
- [Hardening](hardening.md)
- [Trace inspection and recovery](traces-and-recovery.md)
- [Operational observation and reporting](observability.md)
- [Remote workers](remote-workers.md)
- [Provider compatibility](provider-compatibility.md)
- [Reproduce a mixed-harness scenario](mixed-harness-acceptance.md)
- [Research](research.md) and [controlled studies](research-study.md)

## Evaluate and contribute

[Quality and evidence](quality.md) describes what the checks establish and what
remains unproven. Use [matched workflow evaluations](evaluation.md) to measure
task quality, time and cost.
[Release validation](releasing.md) covers installable artifacts and release checks.

## Existing workspace mode

The original YAML compiler creates tmux layouts with `load` and `reload`. Pane
capture, raw input, synchronization, session KV and portable bundles remain
available. This mode does not provide the managed broker's per-agent ACLs.

[Workspace quickstart](getting-started/quickstart.md#existing-workspace-mode)
· [Layout architecture](architecture/mental-model.md)
· [Portable bundles](tutorials/portable-bundles.md)
