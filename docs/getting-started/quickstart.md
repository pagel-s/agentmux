# Start a native work session

Use your existing Claude Code and Codex subscriptions. This page covers ordinary
setup; the [optional verification walkthrough](native-walkthrough.md) tests every
permission and recovery boundary in detail.

Task pairing and automatic installed-provider selection require
[Agentmux v0.15.0](installation.md). An existing workspace retains its pinned
controller until explicitly upgraded; a fresh workspace uses the installed binary.

## 1. Install and sign in once

[Install Agentmux](installation.md). Native sessions require tmux 3.2+ and the
native CLIs you want to use. Open Claude Code and Codex directly and complete
their normal subscription login and project-trust prompts.

You keep the native tools' configuration, skills, history and approval behavior.
Ordinary sessions use trusted local execution. Strict Linux isolation is an
[explicit advanced option](../managed-setup.md).

## 2. Start your project and pair for tasks

From the project directory:

```sh
amx start --detach
amx pair claude codex --delivery auto
```

The pairing command is a preview. It shows both identities, command permissions,
delivery modes, returned results, existing queues and limits. `--apply` checks
and prints the current review again before its atomic update:

```sh
amx pair claude codex --delivery auto --apply
amx attach claude
```

Ask Claude to delegate a specific task to Codex, for example running a check or
reviewing a file. Instructions arrive as agent-attributed turns, and returned
results need no separate reverse message permission.

Pairing enables mutual Commands and own-result delivery in one atomic update.
It leaves message/ping grants, shared boards/memory and existing limits alone.
Auto can start eligible existing queued work and future work from **all permitted
senders**, not just this pair. Use `--delivery review` if you want to approve
each exact queued item. Native readiness and limits still govern when turns run.

For another workspace, add `-s NAME` to every command. Existing workspaces keep
their controller version; start a fresh name or follow
[deliberate upgrade/recovery](../traces-and-recovery.md).

## 3. Work and inspect

**F1** returns to the workbench. **Ctrl+P** finds sessions and **Ctrl+K** finds
actions. In Connections, **[** chooses sender focus and **]** chooses recipient
focus; **↑/↓** changes that identity. **Shift+C** reviews task pairing,
**Shift+D** reviews delivery settings and **Shift+A** approves an exact item.

From an owner shell:

```sh
amx observe
amx tasks
amx task TASK_ID
```

Tasks show what was queued, delivered and verified separately. Use the exact
printed task ID; [observability](../observability.md) explains uncertainty and
stale reporting. For a checked coding request in a separate worktree, use
[`amx work`](../working.md); its preview selects installed providers without
starting a model.

Detach with **Ctrl-b**, release, then **d** to keep the workspace running.
`amx down` stops its owned processes and retains history. See
[everyday commands](everyday-commands.md) for adding and relaunching sessions.

## Existing workspace mode

The original YAML layout compiler is a separate workflow. Its
[optional walkthrough](native-walkthrough.md#existing-workspace-mode) covers
`templates`, `init`, `render`, `load` and `reload`.
