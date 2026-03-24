# Agentmux Config Guide

Agentmux configs are local YAML files that define one tmux workspace.

## Mental Model

- `agents`: reusable execution defaults
- `windows`: layout and context
- `panes`: actual runtime units

Panes are what really run. Agents help reduce duplication. Windows group panes and provide shared context.

## Minimal Shape

```yaml
version: 1
session: my-workflow

layout:
  - window: main
    panes:
      - command: "printf 'ready\n'; exec $SHELL"
```

## Common Structure

```yaml
version: 1
name: My Workspace
session: my-workflow

params:
  project_dir: "."

env:
  PROJECT_DIR: "{{project_dir}}"

agents:
  reviewer:
    provider: command
    command: "openai api chat.completions.create -m gpt-5"
    cwd: "{{project_dir}}"
    title: "Reviewer"

layout:
  - window: editor
    root: "{{project_dir}}"
    layout: even-horizontal
    panes:
      - name: editor
        command: "nvim"
        title: "Editor"
        focus: true
      - name: review
        agent: reviewer
```

## Resolution Rules

### Params

- config `params` provide defaults
- CLI `--param key=value` overrides them

### Env

Env precedence is:

- pane env
- window env
- agent env
- session env

### Working Directory

Working directory precedence is:

- pane `cwd`
- agent `cwd`
- window `root`
- current working directory where Agentmux was launched

## Panes

Each pane must specify exactly one of:

- `command`
- `agent`

Useful pane fields:

- `name`: stable runtime identifier
- `title`: best-effort pane title
- `focus: true`: start focused here
- `env`
- `cwd`
- `on_exit`

## Agents

Agents are reusable pane templates, not shared background runtimes.

Current useful agent fields:

- `provider`
- `command`
- `cwd`
- `env`
- `title`
- `on_exit`

If two panes reference the same agent, you still get two separate pane processes.

## Layouts

Supported layouts:

- `even-horizontal`
- `even-vertical`
- `main-horizontal`
- `main-vertical`
- `tiled`
- `grid`

Exact grids use:

```yaml
layout:
  - window: workspace
    layout: grid
    grid_rows: 2
    grid_columns: 2
    panes:
      - command: "nvim"
      - command: "go test ./..."
      - command: "git status"
      - command: "printf 'review\n'; exec $SHELL"
```

## Exit Behavior

Supported `on_exit` values:

- `shell`
- `remain`
- `restart`
- `kill`

Typical use:

- `shell`: default interactive behavior
- `remain`: keep finished output visible
- `restart`: auto-restart a watcher-like pane
- `kill`: remove the pane when the command exits

## Good Workflow

Create from a starter:

```bash
./bin/agentmux init default my-workflow.yaml
```

Then iterate:

```bash
./bin/agentmux validate my-workflow.yaml
./bin/agentmux render my-workflow.yaml
./bin/agentmux load my-workflow.yaml --replace --no-attach
./bin/agentmux reload my-workflow.yaml --no-attach
```

## Examples

See:

- [`testdata/examples/minimal.yaml`](../testdata/examples/minimal.yaml)
- [`testdata/examples/ai-dev.yaml`](../testdata/examples/ai-dev.yaml)
- [`testdata/examples/on-exit.yaml`](../testdata/examples/on-exit.yaml)
- [`testdata/examples/review-loop.yaml`](../testdata/examples/review-loop.yaml)
- [`testdata/examples/ssh-ops.yaml`](../testdata/examples/ssh-ops.yaml)
