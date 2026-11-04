# Everyday commands

`amx` is the Agentmux command. `amx` alone opens the current terminal dashboard,
the sole running workspace, or a workspace chooser when several are running; it
starts no agents. `amx a` restores the last live native terminal in the selected
workspace. If no terminal has been selected or it has closed, the workbench opens.
With several running workspaces, choose one or pass `-s NAME`.

The searchable choosers, `context`, `observe`, `task`/`tasks`, exact-task workbench opening,
bounded run lists and report opening below are included in v0.14.0.

## Daily power-user loop

| Goal | Short route |
| --- | --- |
| Resume | `amx a`; filter the workspace chooser by name, path or status |
| Launch | **Shift+N** inherits the selected provider and project; review before starting |
| Peer delegation | **Shift+C** in Connections reviews Commands, both delivery modes and returned results together |
| Owner task | `amx send ID "Do this" --mode instruction -s NAME`; copy its printed inspection command |
| Inspect | `amx task TASK_ID -s NAME`, or **Ctrl+K → Tasks for selected session** |
| Observe | `amx observe -s NAME`; add `--watch` for repeated fresh evidence |
| Recover | `amx task TASK_ID --open -s NAME`; inspect its evidence and review the exact task/version |

Use `amx context` to see which workspace an ordinary command selects and why.
It never starts or switches a workspace. Outside an attachment, bare `amx` and
`amx a` offer running-workspace selection; commands with IDs still default to
`work` unless you supply a selector. Keep `-s NAME` on scripts and copied commands.

**Ctrl+W** opens the same workspace filter inside the workbench without losing
an open draft or review. Press `/` or Ctrl+F, then filter by name, path or status;
arrows or Ctrl+N/P choose, Ctrl+U clears, Enter opens the exact displayed state,
and Esc returns. While filtering, `j`, `k` and `q` are literal query text.
Use `amx send --help`, `amx attach --help` or another command's help for focused
options. `amx help all` retains the full reference.

In Connections, `[` focuses **Sender** and `]` focuses **Recipient**. Tab or
Shift+Tab switches fields; Up/Down changes only the highlighted identity.
Left/Right still changes views. Focus changes neither permissions nor the
other field's selected identity.

[`amx observe`](../observability.md) separates broker connectivity, worker
check-ins, native events, effective delivery uncertainty and retained outcomes.
It counts complete task history and prints exact task inspection commands;
quiet reporting alone does not establish a stalled or failed model.

## Ask for a checked coding change

The checked workflow, `--limit` and `--open` below are included in
[v0.14.0](installation.md). For a first checked fixture
with no provider setup, run [`amx demo`](first-result.md).
From a Git project with an initial commit and the selected native CLIs installed:

```sh
amx work "Fix the login redirect" -- go test ./...
amx work "Fix the login redirect" --allow-models -- go test ./...
amx work status
amx work show latest
amx work list --limit 10
amx work show RUN_ID --open
amx work apply latest
```

The first command previews without model calls or run files. The second starts
bounded coding, fresh review and checks in a retained private worktree. Inspect
`status`, `show` and the saved HTML report before choosing `apply`; application
rechecks freshness and changes your project without committing or pushing it.
After an interrupted application, inspect and use `amx work recover latest` to
resolve its journal without replaying the patch. See [work on a task](../working.md)
for provider, isolation, deadlines and recovery details, or
[advanced workflows](../workflows.md) for command-only comparisons and custom plans.

A clean first review of the exact independently checked revision can finish
with two native CLI turns, skipping the response and second review. Final
acceptance checks still run; requested changes keep the full loop. Saved reports
show skipped phases explicitly.

Copy a stable run ID from `work list` when following up on older work. `--open`
requires an explicit ID, directory or `latest`; it renders a fresh private HTML
report from saved evidence and opens it with the system browser. It prints and
retains the generated file if the opener fails. It never starts another run.

`work` selects the Git project in the current directory (or `--repo DIR`). The
interactive commands below select a running workspace with `-s NAME`; these are
separate selectors.

## Open and manage sessions

```sh
amx up -s work -p /path/to/project -d
amx ui -s work
amx ls -s work
amx a claude -s work
amx new reviewer --harness codex -p /path/to/project -s work
amx name reviewer "Review login changes" -s work
```

Use stable session IDs from `amx ls` when attaching, naming, or connecting
agents. Display names describe the task and can repeat. Select a workspace
explicitly with `-s NAME` when switching between several workspaces; `amx ws`
lists saved workspaces. Attaching opens the normal native terminal so you can
keep typing, approve tools, and interrupt work yourself.
Within an owned tmux attachment, an omitted workspace selector uses that exact
attachment after checking its attempt identity. Outside, commands with an explicit agent ID default to
`work`; there is no global last-used workspace setting. `up` / `start` always
defaults to `work` when no selector is given, even inside another workspace.
`amx -s NAME ui` also works.

In the workbench, Tab switches between the session sidebar and the current
view’s content. The purple header shows focus; ↑/↓ navigates that area.
Left/Right or 1–8 changes views. Esc returns focus to the sidebar after closing
an open reader or editor. Ctrl+P finds sessions by task, stable
ID, provider or project: Enter opens the native terminal, Tab selects it in the
workbench, and Esc returns. In Connections (Links on narrow terminals), use the sidebar to choose the sender, then Tab to choose a recipient
with ↑/↓; h/l also changes the recipient. F1 returns from a native terminal.

See [terminal folders and layouts](../terminal-workflow.md) for scratch panes,
multiple agents in one window and reattachment.

## Pair native agents for tasks

```sh
amx pair claude codex --delivery auto -s work
amx pair claude codex --delivery auto --apply -s work
```

The first command previews; each `--apply` invocation reads and prints the
current review before making one atomic update. It grants Commands both ways,
sets both receivers and enables returned results. In Connections, **Shift+C**
opens one frozen review: **1** chooses Review, **2** chooses Auto, **Enter** applies.

Auto affects eligible existing queues and future work from all permitted senders,
not just this pair. New grants/results can make previously ineligible items
eligible. Limits and spent turns remain saved. Use `--delivery review` for exact
item approval. Pairing leaves notes, pings, shared boards and memory alone.
Older pinned controllers need an explicit upgrade or a fresh workspace.

## Share chat and memory, or edit one direction

```sh
amx link claude reviewer -s work
amx allow claude reviewer commands -s work
amx mode reviewer review -s work
```

`link` provides a shared board and memory scope. `allow` grants one direction
of command permission. `mode` decides whether the receiver accepts a native
turn: Manual stays human-led, Review needs approval of each exact item, and
Auto delivers eligible turns within the configured limits.

```sh
amx send reviewer "Check the login proposal." --mode instruction -s work
amx mode reviewer -j -s work
amx mode reviewer --approve CANDIDATE_ID -s work
amx tasks --to reviewer -s work
amx task TASK_ID -s work
amx task TASK_ID --open -s work
```

Copy the exact pending candidate ID from the status. A queued or submitted
item does not prove the model executed it; inspect its receipt and native
terminal. Provider-specific guarantees are in [native collaboration](../native-delivery.md).

Start with the unfiltered recipient history above so pending approval and
submitted results stay visible. Add `--status running` only when you want to
limit the view to running tasks.

`tasks` searches retained history at the broker, newest first, with `--query`,
`--status`, `--to` and `--limit` (1–200). Copy the printed **More** command for
the next page, or add `-j` for JSON. `task` shows the exact instruction, result,
version and available receipt/recovery evidence, then prints a command to open
that exact task. `task TASK_ID --open` goes straight to its Tasks view without
searching the history; old tasks and retired recipients remain addressable.
Opening changes no task state. Any recovery action still reviews the exact
task/version and requires explicit confirmation. `--open` applies only to `task`
and cannot be combined with `--json`.

In the workbench or web app, **Tasks for selected session** clears prior
filters and opens that recipient's complete paginated history. Web attention
rows also offer **Inspect tasks**, including for a retired recipient.

```sh
amx mode reviewer manual -s work
amx deny claude reviewer commands -s work
amx unlink claude reviewer -s work
```

Manual pauses future native dispatch and requests cancellation of an active
peer turn where the adapter supports it. Inspect the receipt and native terminal:
the request does not confirm that work stopped, and it does not interrupt an
unrelated human turn. `deny` removes the chosen directional permission. `unlink`
disables only the exact selected pair/group; `isolate ID` disconnects that session
from all peers. Other groups or explicit grants can still provide access;
inspect active tasks separately because already read context and completed
effects cannot be undone.

## Short names

| Short command | Full command |
| --- | --- |
| `up` | `start` |
| `a`, `open` | `attach` |
| `ui` | `dashboard` |
| `ls` | `sessions` |
| `ws` | `workspaces` |
| `new` | `add` |
| `link` | `collaborate` |
| `unlink` | `disconnect` |
| `name` | `label` |
| `mode` | `delivery` |

Everyday commands accept `-s` for `--session`, `-p` for `--project`, `-d` for
`--detach`, and `-j` for `--json` where that option applies. `web` and `task`
also accept `-o` for `--open`. Text after a literal
`--` keeps its original meaning. Long commands and flags remain available.
`allow FROM TO commands|messages|pings` and the matching `deny` change only the
selected permission; they do not enable receiver delivery or grant new memory
access. In terminal Connections, inspect the recipient's delivery summary and
press **Shift+D** for its settings or **Shift+A** to review a pending item.
`amx delivery ID auto -s work` can dispatch existing eligible queued items and
preserves saved subscriptions and limits. Choose Review first to inspect an
exact item. Results of your own instructions use the correlated task channel,
without a reverse message grant; receiving them as native turns still needs your
own Results subscription and delivery readiness. Use `amx task TASK_ID` to inspect
the recorded result directly. See [native collaboration](../native-delivery.md)
and [the complete command reference](../commands.md) for advanced options.
