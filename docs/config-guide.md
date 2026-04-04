# Agentmux Config Guide

Agentmux configs are local YAML files that define one tmux workspace.

If you want use-case-driven starting points instead of field-by-field reference, see [`recipes.md`](recipes.md).

## Mental Model

- `agents`: reusable execution defaults
- `windows`: layout and context
- `panes`: actual runtime units
- `include`: reuse parts of other configs

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

## Reuse and Includes

You can split your configuration across multiple files using the `include` field. This is useful for sharing common `agents`, `params`, or `env` across different projects.

```yaml
version: 1
session: project-a

include:
  - shared/base-agents.yaml
  - shared/standard-env.yaml

layout:
  - window: main
    panes:
      - agent: shared-agent
```

### Merging Rules

When a file includes another, they are merged with **Parent-Wins** precedence:

- `params`, `env`, `agents`: Values in the main file override values in included files.
- `layout`: Windows from included files are appended **before** the windows in the main file.
- `include` paths are resolved relative to the file they are defined in.
- Circular includes are detected and rejected.

## Requires (Environment Guards)

You can specify mandatory dependencies that must be present before a session is launched. If any `env` or `params` are missing, Agentmux will launch an **Interactive Setup Wizard** to prompt the user for them. If a binary is missing, it will fail fast with a helpful error.

```yaml
requires:
  binaries:
    - gh
    - nvim
  env:
    - OPENAI_API_KEY
  params:
    - project_name
```

## Resolution Rules

### Params

- config `params` provide defaults
- CLI `--param key=value` overrides them
- **Special `config_dir`**: The absolute path to the directory containing the root configuration file is automatically available as `{{config_dir}}`. Use this to reference local scripts and assets portably.

```yaml
agents:
  researcher:
    command: "python3 {{config_dir}}/scripts/research.py"
```

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

- `command`: raw shell command
- `agent`: reference to a named agent
- `provider`: native LLM provider (e.g., `claude`, `gemini`)

Useful pane fields:

- `name`: stable runtime identifier
- `title`: best-effort pane title
- `persona`: path to a Markdown file containing agent instructions
- `model`: specific LLM model ID
- `focus: true`: start focused here
- `env`
- `cwd`
- `on_exit`

## Agents

Agents are reusable pane templates, not shared background runtimes.

Current useful agent fields:

- `provider`: `command` (default), `claude`, `gemini`, `openai`, or `gptme`
- `command`: raw shell command
- `persona`: path to a Markdown file containing agent instructions
- `model`: specific LLM model ID
- `cwd`
- `env`
- `title`
- `on_exit`

If two panes reference the same agent, you still get two separate pane processes.

### Native LLM Providers

When you use a native provider like `claude` or `gemini`, Agentmux acts as a **JIT Shell Compiler**. It automatically constructs the optimized shell command for that CLI, injecting:
1. Your `persona` file content.
2. The `agentmux prompt` (environmental awareness).
3. The `agentmux tools` JSON schema (native function calling).

Example:
```yaml
agents:
  expert:
    provider: claude
    persona: "prompts/expert.md"
    model: "claude-3-5-sonnet"
```

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
