# Agentmux Config Guide

This guide covers the legacy v1 workspace compiler. Its provider launch flags
and raw pane commands differ from v2 managed work sessions. For native
interactive teams with authenticated coordination and live boundaries, use
[work sessions](work-sessions.md) and the [managed configuration](managed-runtime.md).


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
    provider: codex
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
- `provider`: native CLI harness (e.g., `claude`, `antigravity`, `pi`, `codex`)

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

- `provider`: `command` (default), `shell`, `ssh`, `claude`, `antigravity`, `pi`, `gemini`, `codex`, `gptme`, or `aichat`
- aliases: `agy` -> `antigravity`, `claude-cli` -> `claude`, `gemini-cli` -> `gemini`, `openai` -> `codex`
- `command`: raw shell command
- `persona`: path to a Markdown file containing agent instructions
- `model`: specific LLM model ID
- `reasoning_effort`: optional provider-specific reasoning control for providers that support it
- `cwd`
- `env`
- `title`
- `on_exit`

If two panes reference the same agent, you still get two separate pane processes.

### Native LLM Providers

When you select a native pane provider such as `claude`, `antigravity`, `pi`, or
`codex`, the v1 launcher constructs that CLI's shell command, supplying:
1. Your `persona` file content through the strongest provider-native surface that CLI actually supports.
2. The `amx prompt` runtime contract, which carries the operational coordination instructions separately from the persona.
3. Provider-specific launch wiring so persona lands in the strongest available instruction channel for that CLI without pretending every provider has the same system-prompt model.
4. Provider-specific coordination details such as resolved integration mode, tool channel, preferred tool-call input mode, and optional reasoning controls.

Current persona delivery defaults:

- `claude`: `--append-system-prompt`, combining the persona with Agentmux instructions while preserving Claude Code's default system prompt
- `antigravity`: interactive bootstrap prompt using the local `agy` executable
- `pi`: `--append-system-prompt`, preserving Pi's default system prompt
- `gemini`: native workspace context file from a session-scoped runtime artifact
- `codex`: bootstrap prompt
- `gptme`: bootstrap prompt

`amx tools --format ...` still exists, but it is now an export/debug surface rather than the primary runtime path.

Current integration defaults:

- `claude`: prefers `native-mcp`, falls back to `cli-bridge`
- `codex`: prefers `native-mcp`, falls back to `cli-bridge`
- `gemini`: prefers `native-mcp`, falls back to `cli-bridge`
- `gptme`: uses `cli-bridge`
- `antigravity` and `pi`: use `cli-bridge`; Agentmux does not change global or workspace MCP settings for these harnesses

Antigravity accepts `reasoning_effort: low|medium|high|xhigh|max` through `--effort`. Pi accepts `off|minimal|low|medium|high|xhigh|max` through `--thinking`; support depends on the selected model and installed harness version. Pi's `model` can include a provider prefix, such as `anthropic/<model-id>`, to select a configured endpoint. Agentmux uses each harness's existing authentication and endpoint configuration and does not copy credentials.

Antigravity and Pi remain ordinary interactive pane processes in this launch mode. Their CLI bridge runs coordination commands through the harness's shell tool, subject to its permissions. A launcher is not a structured input channel or an isolation boundary. See [harness integration](harnesses.md) for capabilities and limits.

Codex-specific runtime notes:

- Agentmux launches Codex with `--no-alt-screen` to behave better inside tmux panes and preserve scrollback.
- The v1 launcher uses `--sandbox workspace-write --ask-for-approval never`.
  This is specific to the original pane mode. Everyday v2 local interactive
  work sessions preserve native approval/sandbox configuration; see
  [work sessions](work-sessions.md).

Direct pane `provider:` usage is intended for native providers. Command-style providers like `ssh` should be defined on an agent alongside `command:`.

Example:
```yaml
agents:
  expert:
    provider: claude
    persona: "prompts/expert.md"
    model: "sonnet"
    reasoning_effort: "high"
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
./bin/amx init default my-workflow.yaml
```

Then iterate:

```bash
./bin/amx validate my-workflow.yaml
./bin/amx render my-workflow.yaml
./bin/amx load my-workflow.yaml --replace --no-attach
./bin/amx reload my-workflow.yaml --no-attach
```

## Examples

See:

- [`testdata/examples/minimal.yaml`](https://github.com/pagel-s/agentmux/blob/main/testdata/examples/minimal.yaml)
- [`testdata/examples/ai-dev.yaml`](https://github.com/pagel-s/agentmux/blob/main/testdata/examples/ai-dev.yaml)
- [`testdata/examples/on-exit.yaml`](https://github.com/pagel-s/agentmux/blob/main/testdata/examples/on-exit.yaml)
- [`testdata/examples/review-loop.yaml`](https://github.com/pagel-s/agentmux/blob/main/testdata/examples/review-loop.yaml)
- [`testdata/examples/ssh-ops.yaml`](https://github.com/pagel-s/agentmux/blob/main/testdata/examples/ssh-ops.yaml)
