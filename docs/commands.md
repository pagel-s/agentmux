# Agentmux Commands

This is the fastest command reference for day-to-day Agentmux usage.

## Starters

List built-in starter templates:

```bash
./bin/agentmux templates
```

Create a new config from a starter template:

```bash
./bin/agentmux init default my-workflow.yaml
./bin/agentmux init minimal scratch.yaml
```

Open a config path or the config remembered for a live session:

```bash
./bin/agentmux edit my-workflow.yaml
./bin/agentmux edit my-workflow
```

Backfill config-path metadata for an already-running session:

```bash
./bin/agentmux remember my-workflow ./my-workflow.yaml
```

## Config Commands

Validate a config:

```bash
./bin/agentmux validate my-workflow.yaml
```

Render the fully resolved config and launch plan:

```bash
./bin/agentmux render my-workflow.yaml
```

Common config flags:

- `--param key=value`: override config params
- `--strict`: treat warnings as errors

## Session Commands

Load a config into tmux:

```bash
./bin/agentmux load my-workflow.yaml --replace --no-attach
```

Reload an existing session from config:

```bash
./bin/agentmux reload my-workflow.yaml --no-attach
```

List tmux sessions:

```bash
./bin/agentmux list
```

Useful launch flags:

- `--replace`: replace an existing session on `load`
- `--no-attach`: create or reload without attaching
- `--dry-run`: print the launch plan without touching tmux
- `--verbose`: print underlying tmux commands

## Runtime Commands

Show live pane status:

```bash
./bin/agentmux status my-workflow
./bin/agentmux status my-workflow --json
./bin/agentmux status my-workflow --pane review
```

Focus a named pane:

```bash
./bin/agentmux focus my-workflow review
```

Restart or rerun a named pane:

```bash
./bin/agentmux restart my-workflow review
./bin/agentmux rerun my-workflow review
```

Stop a pane safely or kill it destructively:

```bash
./bin/agentmux stop my-workflow review
./bin/agentmux kill my-workflow build
```

Read pane output:

```bash
./bin/agentmux capture my-workflow review
./bin/agentmux capture my-workflow review --tail 20 --strip-empty
./bin/agentmux capture my-workflow review --json --tail 20 --strip-empty
```

Send input to a pane:

```bash
./bin/agentmux send my-workflow review "git status --short"
./bin/agentmux send my-workflow review --no-enter "printf 'queued\n'"
```

Runtime flags:

- `--json`: structured output
- `--pane <name>`: filter `status` to one pane
- `--tail N`: limit `capture` output to the last `N` lines
- `--strip-empty`: remove blank and whitespace-only lines from `capture`
- `--no-enter`: send text without pressing Enter

## Utility Commands

Environment and dependency check:

```bash
./bin/agentmux doctor
```

Version:

```bash
./bin/agentmux version
./bin/agentmux --version
```

Built-in help:

```bash
./bin/agentmux --help
```
