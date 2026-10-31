# Agentmux

Run agent teams in tmux with explicit communication and memory permissions.
Use native Claude/Codex/Pi terminals for human-led work, managed workers for
bounded autonomous workflows, or the research runners for controlled experiments.

## Start here

1. [Build and install](getting-started/installation.md).
2. [Start an interactive work session](work-sessions.md).
3. Use the terminal dashboard for attachment and the local web app for activity,
   tasks, shared-memory editing and live permission controls.

Keep the [everyday command reference](commands.md#everyday-work-sessions) handy
for selecting named workspaces, adding or connecting sessions, and choosing a
collaboration group's board. For a repeated-round study, start with the
[research planning and observation workflow](research.md).

The managed runtime and workbench are experimental. Broker permissions, native
approvals, provider networking and filesystem isolation are separate boundaries.
The [trust model](security-model.md) and [validation record](release-validation.md)
describe measured coverage and limits.

## Configure and automate

- [Setup and providers](managed-setup.md)
- [Managed runtime](managed-runtime.md)
- [Persistent conversations and autonomous wakeups](autonomous-workflows.md)
- [Dynamic teams and bounded spawning](dynamic-teams.md)
- [Private/shared memory](memory-lifecycle.md)
- [Hardening](hardening.md)
- [Trace inspection and recovery](traces-and-recovery.md)
- [Remote workers](remote-workers.md)
- [Provider validation](provider-validation.md)
- [Research](research.md) and [controlled studies](research-study.md)

## Existing workspace mode

The original YAML compiler creates tmux layouts with `load` and `reload`. Pane
capture, raw input, synchronization, session KV and portable bundles remain
available. This mode does not provide the managed broker's per-agent ACLs.

[Workspace quickstart](getting-started/quickstart.md#existing-workspace-mode)
· [Layout architecture](architecture/mental-model.md)
· [Portable bundles](tutorials/portable-bundles.md)
