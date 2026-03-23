# Agentmux

Agentmux is the fastest way to spin up and control AI-heavy tmux workspaces.

Agentmux is a tmux-native CLI for reproducible multi-agent terminal workspaces. It reads a declarative YAML config, validates it, resolves params and environment inheritance, and launches the corresponding tmux session.

Current `main` also supports named-pane runtime controls, so the product is now best understood as a pane-oriented workspace manager built on reusable agent definitions.

## Status

This repository builds on the v1 foundation with:

- declarative local YAML configs
- schema and semantic validation
- predictable param, env, cwd, and title resolution
- dry-run and render output for inspection
- tmux session creation with hooks, focus, and replace handling
- unit, fixture, and tmux integration coverage
- named-pane runtime controls for live sessions

## Docs

- `README.md`: current capabilities and usage
- `technical_spec.md`: frozen v1 baseline contract
- `project_plan.md`: product strategy and direction
- `roadmap.md`: immediate, mid-term, and long-term next steps

## Current Scope

- Local YAML configs
- Schema and semantic validation
- Param interpolation with `{{param_name}}`
- Session-, window-, agent-, and pane-level env merging
- tmux session, window, and pane creation
- Commands: `load`, `validate`, `render`, `list`, `status`, `focus`, `restart`, `rerun`, `stop`, `kill`, `capture`, `send`, `doctor`

Current model:

- `agents` are first-class config objects that define reusable execution defaults
- `windows` provide layout and context
- `panes` are the runtime instantiations and control unit

Still intentionally out of scope:

- remote config fetching
- daemons and agent-to-agent messaging
- provider SDKs and plugins
- orchestration logic beyond tmux session creation

## Build And Run

Agentmux targets Go 1.21 or newer. If a repo-local toolchain exists under `.tools/`, the `Makefile` uses it automatically.

```bash
make build
./bin/agentmux version
./bin/agentmux doctor
```

If you want to call `agentmux` directly in the current shell:

```bash
export PATH="$PWD/bin:$PATH"
```

## Commands

```bash
./bin/agentmux load <config> [--param key=value] [--attach|--no-attach] [--dry-run] [--replace]
./bin/agentmux validate <config> [--param key=value]
./bin/agentmux render <config> [--param key=value]
./bin/agentmux list
./bin/agentmux status <session> [--json]
./bin/agentmux focus <session> <pane-name>
./bin/agentmux restart <session> <pane-name>
./bin/agentmux rerun <session> <pane-name>
./bin/agentmux stop <session> <pane-name>
./bin/agentmux kill <session> <pane-name>
./bin/agentmux capture <session> <pane-name> [--json]
./bin/agentmux send <session> <pane-name> <command...>
./bin/agentmux doctor
./bin/agentmux version
```

`load` attaches by default unless you pass `--no-attach` or `--dry-run`.

## Config Shape

```yaml
version: 1
session: ai-dev

params:
  project_dir: "."

env:
  PROJECT_DIR: "{{project_dir}}"

agents:
  coder:
    provider: command
    command: "ollama run codellama"
    cwd: "{{project_dir}}"

layout:
  - window: editor
    root: "{{project_dir}}"
    layout: even-horizontal
    env:
      WINDOW_ROLE: "coding"
    panes:
      - name: editor
        command: "nvim"
        title: "Editor"
        focus: true
      - name: coder
        agent: coder
```

Resolution rules:

- params: CLI `--param` overrides config defaults
- env: pane env > window env > agent env > session env
- cwd: pane cwd > agent cwd > window root > current working directory
- title: pane title > agent title > pane name > agent name > command-derived fallback
- focus: set `focus: true` on the pane you want selected after startup
- name: optional stable pane ID, unique across the session, for pane-targeted runtime commands

`render` prints the fully resolved config plus a readable execution plan. `validate --strict` turns warnings such as unknown top-level fields into failures.

## Exact Grids

Use `layout: grid` with `grid_rows` and `grid_columns` when you want an exact pane matrix instead of tmux's best-effort presets.

```yaml
layout:
  - window: workspace
    root: "{{project_dir}}"
    layout: grid
    grid_rows: 2
    grid_columns: 2
    panes:
      - command: "nvim"
        title: "Editor"
      - command: "go test ./... -count=1"
        title: "Tests"
      - command: "git status"
        title: "Git"
      - agent: coder
        focus: true
```

For exact grids, the pane count must equal `grid_rows * grid_columns`.

Window-level `env` applies to every pane in that window, including panes created from shared agents.

## Pane Names

Use `name` when you want a stable pane identity that is distinct from the visible title.

```yaml
layout:
  - window: review
    panes:
      - name: editor
        command: "nvim"
      - name: reviewer
        agent: coder
```

Pane names:

- are optional
- must be unique across the session
- may use letters, numbers, dot, dash, and underscore
- become the default pane title when there is no higher-precedence title already available

Named panes can now be targeted at runtime:

```bash
./bin/agentmux status dogfood
./bin/agentmux status dogfood --json
./bin/agentmux focus dogfood resolve
./bin/agentmux restart dogfood resolve
./bin/agentmux rerun dogfood resolve
./bin/agentmux stop dogfood resolve
./bin/agentmux kill dogfood build
./bin/agentmux capture dogfood review
./bin/agentmux capture dogfood review --json
./bin/agentmux send dogfood review "git status --short"
```

`restart` and `rerun` both re-execute the pane's stored startup command in place using tmux pane respawn. In the current version, they are intentionally equivalent so the behavior stays predictable for interactive panes.

`stop` kills the pane's current process and respawns a fresh interactive shell in place. This is the safe live-control option because it preserves the pane and its layout instead of destroying it.

`kill` is the explicitly destructive option. It removes the target pane from tmux entirely, which can collapse the layout and may remove the window or session if that pane was the last one.

`capture` prints the current joined pane contents for the named pane, which makes it possible to read back output from a live workflow endpoint.

`send` writes a literal command into the target pane and presses Enter, which makes named panes usable as live workflow endpoints after startup.

`status --json` and `capture --json` expose machine-friendly output for scripting and automation while preserving the existing human-readable defaults.

`status` now also surfaces the pane source, agent name when relevant, and tmux's current foreground command so running sessions are easier to inspect.

## Runtime Troubleshooting

Start with:

```bash
./bin/agentmux status <session>
```

Common runtime errors:

- `session "..." not found`: the tmux session does not exist yet or has already been removed. Run `./bin/agentmux list` to inspect sessions, or `./bin/agentmux load <config>` to recreate it.
- `pane "..." not found ... available named panes: ...`: the session exists, but that pane name is wrong for the current live session. Run `./bin/agentmux status <session>` and use one of the listed pane names.
- `session has no named panes`: the live session was created from panes without `name:` fields. Add `name:` to the panes you want to control and reload the session.

## Examples

Canonical examples:

- [testdata/examples/minimal.yaml](testdata/examples/minimal.yaml)
- [testdata/examples/ai-dev.yaml](testdata/examples/ai-dev.yaml)
- [testdata/examples/review-loop.yaml](testdata/examples/review-loop.yaml)
- [testdata/examples/ssh-ops.yaml](testdata/examples/ssh-ops.yaml)

## Development

```bash
make fmt
make test
make build
```

The repo includes:

- fixture-driven validation tests in `testdata/fixtures`
- isolated tmux integration tests that use a dedicated socket
- a GitHub Actions workflow in `.github/workflows/ci.yml`

## Operational Notes

- Configs are trusted code. Agentmux executes shell commands exactly as defined.
- Values like `${OPENAI_API_KEY}` are preserved for shell expansion at runtime.
- Pane titles are best-effort. Some shells overwrite the visible pane title after startup, so titles should be treated as a helpful hint rather than a hard guarantee.
