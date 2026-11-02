# Agentmux

Run agent teams in tmux with explicit communication and memory permissions.
Use native Claude/Codex/Pi/Antigravity terminals for human-led work, managed workers for
bounded autonomous workflows, or the research runners for controlled experiments.

## Start here

1. [Install the v0.11.0 release or build from source](getting-started/installation.md).
2. [Explore the illustrated workflow guide](visual-guide.md): navigate the
   terminal and web app, connect everyday sessions, and follow teams, automation
   and research with screenshots and examples.
3. [Try the fresh-install walkthrough](getting-started/quickstart.md): use two
   native sessions, connect them, exchange context, and disconnect them again.
4. Keep [work sessions](work-sessions.md) open for terminal keys, session
   discovery, workspace switching and individual-session recovery. Use the local
   web app for activity, tasks, shared-memory editing and permission controls.

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
- [Live permissions and admission controls](live-permissions.md)
- [Hardening](hardening.md)
- [Trace inspection and recovery](traces-and-recovery.md)
- [Remote workers](remote-workers.md)
- [Provider validation](provider-validation.md)
- [Dated provider acceptance](provider-acceptance.md)
- [Research](research.md) and [controlled studies](research-study.md)
- [UAT gap closure and limits](uat-gap-closure.md)

## Existing workspace mode

The original YAML compiler creates tmux layouts with `load` and `reload`. Pane
capture, raw input, synchronization, session KV and portable bundles remain
available. This mode does not provide the managed broker's per-agent ACLs.

[Workspace quickstart](getting-started/quickstart.md#existing-workspace-mode)
· [Layout architecture](architecture/mental-model.md)
· [Portable bundles](tutorials/portable-bundles.md)
