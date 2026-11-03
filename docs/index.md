# Agentmux

Run agent teams in tmux with explicit communication and memory permissions.
Use native Claude/Codex/Pi/Antigravity terminals for human-led work, managed workers for
bounded autonomous workflows, or the research runners for controlled experiments.
For a coding request with implementation, fresh review and executable checks,
use [`amx work`](working.md); inspect the retained result before explicitly
applying it to your project.

## Choose your starting point

| Goal | Guide | Execution starts when |
| --- | --- | --- |
| Ask for one checked coding change | [Work on a task](working.md) | Add `--allow-models` after inspecting the preview |
| Type directly in several native conversations | [Fresh-install walkthrough](getting-started/quickstart.md) | Start a workspace and use its native terminals |
| Compare alternatives, build a mission or review changed snapshots | [Advanced workflows](workflows.md) | Run an owner-authored plan; provider turns require `--allow-models` |
| Queue tasks or enable bounded autonomous teams | [Managed setup](managed-setup.md) | Launch a reviewed team configuration |
| Run controlled cooperation or competition studies | [Research](research.md) | Run a declared study; model participants require explicit opt-in |

`work` and `flow` require the current source build; the published v0.11.0 binary
predates them. Follow [build from source](getting-started/installation.md#build-from-source)
and run `make install` to use these commands. The
[visual guide](visual-guide.md#ask-for-a-checked-coding-change) illustrates their
preview, review, evidence and application boundaries.

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
- [Remote workers](remote-workers.md)
- [Provider compatibility](provider-compatibility.md)
- [Reproduce a mixed-harness scenario](mixed-harness-acceptance.md)
- [Research](research.md) and [controlled studies](research-study.md)

## Existing workspace mode

The original YAML compiler creates tmux layouts with `load` and `reload`. Pane
capture, raw input, synchronization, session KV and portable bundles remain
available. This mode does not provide the managed broker's per-agent ACLs.

[Workspace quickstart](getting-started/quickstart.md#existing-workspace-mode)
· [Layout architecture](architecture/mental-model.md)
· [Portable bundles](tutorials/portable-bundles.md)
