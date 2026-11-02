# Folders, terminal panes and reattachment

`amx` is the Agentmux command. Workspaces contain registered agent sessions;
each session has a stable ID, project directory and native process. A tmux
window can show one agent, several agents, or ordinary scratch shells.

## Navigate the dashboard

**Tab** or **Shift+Tab** switches focus between the session sidebar and the
current view's content. The purple header says where **Up/Down** acts.

| Focus | Up/Down | Enter |
| --- | --- | --- |
| Sessions sidebar | Select another agent | Open its native terminal |
| Board | Select a message | Read the full message |
| Links | Select a recipient; sidebar selects the sender | Open the selected sender's native terminal |
| Memory | Select a namespace or entry | Open entries or read the selected entry |
| Tasks | Select a task | Read the task and its result |
| Existing terminals | Select an observed terminal | Show its original terminal details |
| Teams | Select a consented team | Read the team details |
| Events | Select and pin an event | Read the event details |

**Left/Right** and **1–8** change views. **Ctrl+P** searches sessions from
anywhere: Enter opens the native terminal, Tab selects it in the workbench,
and Esc cancels. **Ctrl+K** opens searchable Actions. Esc closes an editor,
reader or namespace first; from ordinary content it focuses the sidebar.
**F1** returns from a native terminal to your dashboard.

## Choose a session's folder

Start the default Claude/Codex pair in a project:

```sh
amx up -s daily -p ~/projects/api
```

Add agents in the same or another project:

```sh
amx new review --harness codex -p ~/projects/api -s daily
amx new notes --harness claude -p ~/projects/research -s daily
amx a review -s daily
```

`-p` is short for `--project`. It must name an existing directory. Without
`-p`, `up` and `new` use the shell's current directory. One workspace can
contain agents from unrelated projects; sharing a workspace grants no peer
communication or shared-memory access by itself.

In the dashboard, **Shift+N** inherits the selected session's provider and
project. Its final review shows both. **Ctrl+O** while entering the optional
title opens advanced settings, including the project directory. Cancelling
the review starts no provider. A running conversation's project is not changed
by relabeling it: add a new session when you want a different project.

## Use normal scratch shells and windows

Press Ctrl-b, release both keys, then press the final key:

| Keys | Result |
| --- | --- |
| Ctrl-b `%` | Shell beside the current pane |
| Ctrl-b `"` | Shell below the current pane |
| Ctrl-b arrow | Focus another pane |
| Ctrl-b `z` | Zoom/unzoom the current pane |
| Ctrl-b `c` | New scratch window |
| Ctrl-b `!` | Move the current pane into its own window |
| Ctrl-b `w` | Choose a workspace window |
| Ctrl-b `n` / `p` | Next/previous window |
| Ctrl-b `d` | Detach; keep the workspace running |

New shells/windows inherit the selected agent's project. From a scratch shell,
they inherit that shell's current directory, including a later `cd`. Scratch
windows and the normal breakout key are anchored to the workspace, so disposing
an attachment does not kill them. They appear again on reattachment.

Scratch shells run on the host. Splitting beside a sandboxed agent does not
create another sandbox. Starting `claude` or `codex` manually in such a shell
creates an ordinary external native session. It can appear in **Existing
terminals** where recognized, but it does not inherit the neighboring agent's
identity, role or permissions. Use `amx new` to create a registered session,
or `amx connect OBSERVED_ID` and the printed manual setup to coordinate an
existing conversation.

## Put multiple agents in one tmux window

For two already-running registered sessions:

```sh
amx split claude review -s daily
amx split claude review --vertical -s daily
```

The first ID is the destination; the second terminal moves beside it. The
default is side by side; `--vertical` stacks panes. If they already share a
window, the command focuses the second terminal without moving it again.
The status label lists the agent IDs, and pane borders identify each agent.
Their processes, conversations and coordination permissions stay the same.

From an agent or scratch shell inside the workspace, one ID is enough:

```sh
amx split review
```

It moves `review` beside the current pane. Dashboard and runtime-service
windows are excluded. To return an agent to a separate window:

```sh
amx unsplit review -s daily
```

For a new agent, run `amx new ...` first, then `amx split ID` using its printed
ID. Agentmux does not silently launch a duplicate conversation to arrange panes.

Native windows are shared across attachments, like normal linked tmux windows.
Their layout and active pane are shared too. Each attachment retains its own
dashboard, drafts and selected window. Two attachments focused on different
windows remain independent; two viewing the same combined window see its pane
changes. Closing a native pane/window ends its affected terminal processes;
use `amx remove ID` when you also want to revoke that registered identity.

## Reopen a work session

```sh
amx                  # Workbench
amx a                # Last live native terminal
amx a review -s daily # Exact registered conversation
amx ui -s daily      # Workbench explicitly
```

Inside an owned attachment, an omitted workspace selector uses that exact
workspace. Outside, bare `amx` or `amx a` uses the sole running workspace or
shows a chooser when several are running. Commands with an explicit agent ID
still use workspace `work` outside an attachment unless you provide `-s`.

`amx a` restores an attachment's last live agent when available, otherwise the
workspace's last live selection. If none is available, it opens the workbench.
It neither starts a provider nor chooses a provider-history conversation. An
exited agent needs the explicit relaunch flow, such as
`amx relaunch review --resume picker -s daily`.

Installing a newer `amx` does not restart live agents. Reattach with the newer
command for the updated dashboard and attachment controls. A native window
manually moved to an unrelated tmux server/session is outside the tracked
workspace; use `amx split` / `amx unsplit` for registered agent layouts.
