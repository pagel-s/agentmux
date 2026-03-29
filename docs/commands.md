# Agentmux Commands

This guide explains what each command is for, when to use it, and the most useful examples.

If you want workflow-shaped examples instead of command reference, see [`recipes.md`](recipes.md).

## The Short Version

If you only remember a few commands, remember these:

- `templates`: show built-in starter configs
- `init`: create a new config from a starter
- `load`: create a tmux session from a config
- `reload`: recreate an existing session from the config
- `status`: inspect a live session
- `capture`: read output from a named pane
- `send`: send text into a named pane
- `edit`: reopen the config file

## Starter Commands

### `templates`

What it does:
- lists the built-in starter templates that ship with Agentmux
- shows friendly aliases like `default`

When to use it:
- when starting a new config
- when you forget the built-in template names

Examples:

```bash
./bin/agentmux templates
./bin/agentmux init coding-starter coding.yaml
./bin/agentmux init review-starter review.yaml
```

### `init`

What it does:
- writes a starter template to a target path
- rewrites the top-level `session` field from the target filename by default

When to use it:
- when creating a new config
- when you want a starting point instead of hand-writing YAML from scratch

Examples:

```bash
./bin/agentmux init coding-starter coding.yaml
./bin/agentmux init default my-workflow.yaml
./bin/agentmux init minimal scratch.yaml
./bin/agentmux init ai-dev work/review.yaml --force
```

Useful notes:
- `default` is an alias for `ai-dev`
- `coding-starter` and `review-starter` are built-in minimal copy-and-tweak starters
- `--force` overwrites an existing file

### `edit`

What it does:
- opens a config path directly, or
- opens the config path remembered for a live session

When to use it:
- when editing a config you already have on disk
- when you know the session name but do not want to remember where the file lives

Examples:

```bash
./bin/agentmux edit my-workflow.yaml
./bin/agentmux edit my-workflow
```

Useful notes:
- `edit <session>` works best for sessions created or reloaded through current Agentmux
- if metadata is missing, use `remember`

### `remember`

What it does:
- attaches a config path to an already-running session without reloading it

When to use it:
- when a session is older and missing config-path metadata
- when you want `edit <session>` to work immediately

Examples:

```bash
./bin/agentmux remember my-workflow ./my-workflow.yaml
./bin/agentmux edit my-workflow
```

## Config Commands

### `validate`

What it does:
- checks YAML shape, references, and config rules
- prints errors and warnings without launching tmux

When to use it:
- after editing a config
- before `load` if you want a fast safety check

Examples:

```bash
./bin/agentmux validate my-workflow.yaml
./bin/agentmux validate my-workflow.yaml --strict
```

Useful flags:
- `--param key=value`: override config params
- `--strict`: treat warnings as errors

### `render`

What it does:
- prints the fully resolved config
- prints the launch plan Agentmux would execute

When to use it:
- when debugging params, env, cwd, or layout resolution
- when you want to inspect what will actually run

Examples:

```bash
./bin/agentmux render my-workflow.yaml
./bin/agentmux render my-workflow.yaml --param project_dir=.
```

## Session Commands

### `load`

What it does:
- creates a tmux session from a config

When to use it:
- the first time you launch a config
- when replacing an existing session from scratch

Examples:

```bash
./bin/agentmux load my-workflow.yaml
./bin/agentmux load my-workflow.yaml --replace --no-attach
./bin/agentmux load my-workflow.yaml --dry-run
```

Useful flags:
- `--replace`: replace an existing session
- `--no-attach`: do not attach after launch
- `--dry-run`: print the launch plan only
- `--verbose`: print underlying tmux commands

### `reload`

What it does:
- reconciles an existing session from a config without destroying the whole session
- surgically updates pane metadata or respawns individual panes if their config changed
- preserves history and layout for untouched panes

When to use it:
- after editing a config for a session that already exists

Examples:

```bash
./bin/agentmux reload my-workflow.yaml --no-attach
```

Useful note:
- unlike `load`, `reload` expects the session to already exist
- if the session structure (window or pane count) has changed significantly, it falls back to a destructive replace with a warning
- if you are running `reload` from inside one of the session's panes, and that pane's config changed, it will be respawned last

### `list`

What it does:
- lists tmux session names

When to use it:
- when checking whether a session exists
- when troubleshooting runtime commands

Examples:

```bash
./bin/agentmux list
```

## Runtime Commands

These commands operate on live sessions and mostly target named panes.

### `status`

What it does:
- shows live panes in a session
- reports metadata like pane name, title, source, `on_exit`, current command, and `dead`
- includes PID for running panes and exit status for dead panes

When to use it:
- as the first runtime debugging step
- to discover pane names
- to inspect a single pane with `--pane`

Examples:

```bash
./bin/agentmux status my-workflow
./bin/agentmux status my-workflow --json
./bin/agentmux status my-workflow --pane review
./bin/agentmux status my-workflow --pane review --json
```

### `focus`

What it does:
- switches the active tmux client focus to a named pane

When to use it:
- when jumping directly to a pane by name instead of navigating manually

Examples:

```bash
./bin/agentmux focus my-workflow review
```

### `restart`

What it does:
- respawns a named pane with its stored startup command

When to use it:
- when a pane is stuck or you want to restart its workflow endpoint

Examples:

```bash
./bin/agentmux restart my-workflow review
```

### `rerun`

What it does:
- re-executes the stored startup command for a named pane

When to use it:
- when you want to re-run the pane’s original command in place

Examples:

```bash
./bin/agentmux rerun my-workflow review
```

Useful note:
- in the current version, `restart` and `rerun` are intentionally equivalent

### `stop`

What it does:
- stops the current pane process
- leaves a usable interactive shell in the same pane

When to use it:
- when you want to stop a running pane without destroying the layout

Examples:

```bash
./bin/agentmux stop my-workflow review
```

### `kill`

What it does:
- destroys the target tmux pane entirely

When to use it:
- when you really want the pane removed from the layout

Examples:

```bash
./bin/agentmux kill my-workflow build
```

Useful note:
- this is destructive and can collapse the layout

### `capture`

What it does:
- reads the current captured output of a named pane

When to use it:
- for quick inspection
- for small polling scripts
- for automation that needs to read pane output

Examples:

```bash
./bin/agentmux capture my-workflow review
./bin/agentmux capture my-workflow review --tail 20
./bin/agentmux capture my-workflow review --tail 20 --strip-empty
./bin/agentmux capture my-workflow review --json --tail 20 --strip-empty
```

Useful flags:
- `--tail N`: keep only the last `N` lines
- `--strip-empty`: remove blank and whitespace-only lines
- `--json`: structured output

### `send`

What it does:
- writes text into a named pane
- presses Enter by default

When to use it:
- when treating a pane like a live workflow endpoint

Examples:

```bash
./bin/agentmux send my-workflow review "git status --short"
./bin/agentmux send my-workflow review --no-enter "printf 'queued\n'"
```

Useful note:
- `--no-enter` stages the text without executing it yet

## Utility Commands

### `doctor`

What it does:
- prints environment and dependency information

When to use it:
- when tmux or environment issues look suspicious

Examples:

```bash
./bin/agentmux doctor
```

### `version`

What it does:
- prints the current Agentmux version string

Examples:

```bash
./bin/agentmux version
./bin/agentmux --version
```

### `--help`

What it does:
- prints built-in CLI usage

Examples:

```bash
./bin/agentmux --help
```

## Common Flows

### New workflow

```bash
./bin/agentmux templates
./bin/agentmux init default my-workflow.yaml
./bin/agentmux validate my-workflow.yaml
./bin/agentmux load my-workflow.yaml --replace --no-attach
```

### Edit and reapply

```bash
./bin/agentmux edit my-workflow
./bin/agentmux validate my-workflow.yaml
./bin/agentmux reload my-workflow.yaml --no-attach
```

### Inspect and interact with a live pane

```bash
./bin/agentmux status my-workflow --pane review
./bin/agentmux capture my-workflow review --tail 20 --strip-empty
./bin/agentmux send my-workflow review "git status --short"
```
