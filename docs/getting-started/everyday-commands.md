# Everyday commands

Install with `make install` to get both `agentmux` and `amx`. `amx` alone opens
the current terminal dashboard, the sole running workspace, or a workspace
chooser when several are running; it starts no agents. `agentmux` alone shows help.
Every example below also
works with the longer executable name.

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
attachment after checking its attempt identity. Outside, commands default to
`work`; there is no global last-used workspace setting. `up` / `start` always
defaults to `work` when no selector is given, even inside another workspace.
`amx -s NAME ui` also works.

In the workbench, ↑/↓ chooses a session. Ctrl+P finds sessions by task, stable
ID, provider or project: Enter opens the native terminal, Tab selects it in the
workbench, and Esc returns. In Links, ↑/↓ chooses the sender and h/l chooses
the recipient. Left/Right switches views; F1 returns from a native terminal.

## Connect two agents

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
```

Copy the exact pending candidate ID from the status. A queued or submitted
item does not prove the model executed it; inspect its receipt and native
terminal. Provider-specific guarantees are in [native collaboration](../native-delivery.md).

```sh
amx mode reviewer manual -s work
amx deny claude reviewer commands -s work
amx unlink claude reviewer -s work
```

Manual stops future native dispatch. `deny` removes the chosen directional
permission. `unlink` removes the selected agents from collaboration groups;
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
| `unlink` | `isolate` |
| `name` | `label` |
| `mode` | `delivery` |

Everyday commands accept `-s` for `--session`, `-p` for `--project`, `-d` for
`--detach`, and `-j` for `--json` where that option applies. Text after a literal
`--` keeps its original meaning. Long commands and flags remain available.
`allow FROM TO commands|messages|pings` and the matching `deny` change only the
selected permission; they do not enable receiver delivery or grant new memory
access. Use [the complete command reference](../commands.md) for advanced options.
