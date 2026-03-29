# Agentmux Runtime Guide

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

This mechanism allows isolated terminal agents to signal progress, share file paths, or manage state without a background daemon.

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
