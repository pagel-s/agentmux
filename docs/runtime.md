# Legacy pane runtime guide

These commands address original v1 layouts on the default tmux server. For
current authenticated workspaces use [work sessions](work-sessions.md):
`dashboard --session NAME`, `attach ID --session NAME`, `relaunch ID`, and
`down --session NAME`. Native session IDs, layout pane names and provider-native
conversation IDs are different identifiers.

Agentmux runtime commands operate on live tmux sessions through stable named panes.

## Runtime Model

- sessions are tmux sessions
- panes are the runtime control unit
- pane `name` is the stable identifier
- tmux metadata stores runtime details like source type, startup command, and config path

If you want live control, give panes explicit `name:` fields.

## Core Runtime Commands

Status:

```bash
./bin/agentmux status my-workflow
./bin/agentmux status my-workflow --json
./bin/agentmux status my-workflow --pane review
```

Focus:

```bash
./bin/agentmux focus my-workflow review
```

Restart and rerun:

```bash
./bin/agentmux restart my-workflow review
./bin/agentmux rerun my-workflow review
```

Stop and kill:

```bash
./bin/agentmux stop my-workflow review
./bin/agentmux kill my-workflow build
```

Capture output:

```bash
./bin/agentmux capture my-workflow review --tail 20 --strip-empty
```

Send input:

```bash
./bin/agentmux send my-workflow review "git status --short"
./bin/agentmux send my-workflow review --no-enter "printf 'queued\n'"
```

## Status Data

`status` exposes live pane metadata including:

- window and pane indexes
- pane ID
- pane `name`
- pane `title`
- `source_type`
- `agent_name`
- configured `on_exit`
- current foreground command
- `dead` state

That makes it possible to inspect finished `remain` panes, restart loops, and normal interactive panes through Agentmux instead of raw tmux commands.

## JSON Mode

Runtime JSON output uses stable envelope fields:

- `schema_version`
- `kind`

Important commands:

- `status --json`
- `capture --json`
- action commands like `focus --json` or `send --json`

Runtime JSON errors are also structured and keep the same exit codes as plain-text mode.

## Config Path Metadata

When you `load` or `reload` a config from disk, Agentmux remembers the config path on the live tmux session.

That enables:

```bash
./bin/agentmux edit my-workflow
```

If a session is older and missing that metadata, backfill it:

```bash
./bin/agentmux remember my-workflow ./my-workflow.yaml
./bin/agentmux edit my-workflow
```

## Blackboard (Shared KV-Store)

The "Blackboard" is a shared memory layer for agent coordination. It uses tmux session options to store key-value pairs that are accessible from any pane in the session.

Set a value:

```bash
./bin/agentmux kv-set my-workflow current_task "fixing-auth-bug"
```

Get a value:

```bash
./bin/agentmux kv-get my-workflow current_task
```

List all values:

```bash
./bin/agentmux kv-list my-workflow
./bin/agentmux kv-list my-workflow --json
```

Delete a value:

```bash
./bin/agentmux kv-del my-workflow current_task
```

This lets tmux pane processes signal progress and share values without a
broker. It provides neither per-agent authorization nor filesystem isolation.

## Synchronization (Wait)

The `wait` command blocks until a specific pattern (regex) appears in a pane's output. This is useful for coordinating handoffs between agents.

```bash
./bin/agentmux wait my-workflow reviewer "REVIEW COMPLETE" --timeout 5m
```

If the pattern is found, the command exits with code `0`. If the timeout is reached, it exits with code `1`.

## Dynamic Scaling (Scale)

The `scale` command allows you to dynamically adjust the number of instances of a specific agent in a running session.

```bash
# Increase or decrease the number of 'researcher' workers to 3
./bin/agentmux scale my-workflow researcher 3
```

This command will:
- **Scale Up:** Create new panes using the agent's original configuration (command, env, cwd) from the session's config file.
- **Scale Down:** Terminate instances of the agent until the target count is reached.
- **Naming:** Automatically assign unique names like `researcher-2`, `researcher-3`.
- **Layout:** Re-apply the window's defined layout (e.g., `even-horizontal`) to keep the workspace tidy.

## Troubleshooting

Missing session:

```bash
./bin/agentmux list
./bin/agentmux load my-workflow.yaml --replace --no-attach
```

Missing pane:

```bash
./bin/agentmux status my-workflow
```

Missing config-path metadata:

```bash
./bin/agentmux remember my-workflow ./my-workflow.yaml
```

Useful debugging commands:

```bash
./bin/agentmux doctor
./bin/agentmux status my-workflow --json
./bin/agentmux capture my-workflow review --json --tail 20 --strip-empty
```
