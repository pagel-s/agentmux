# Agentmux

Agentmux is the fastest way to spin up and control AI-heavy tmux workspaces.

Agentmux is a tmux-native CLI for reproducible multi-agent terminal workspaces. It reads a declarative YAML config, validates it, resolves params and environment inheritance, and launches the corresponding tmux session.

Current `main` also supports named-pane runtime controls, so the product is now best understood as a pane-oriented workspace manager built on reusable agent definitions.

## Status

This repository builds on the v1 foundation with:

- declarative local YAML configs
- schema and semantic validation
- predictable param, env, cwd, and title resolution
- configurable pane exit behavior with `on_exit`
- dry-run and render output for inspection
- tmux session creation with hooks, focus, and replace handling
- unit, fixture, and tmux integration coverage
- named-pane runtime controls for live sessions

## Docs

- `README.md`: current capabilities and usage
- `technical_spec.md`: frozen v1 baseline contract
- `project_plan.md`: product strategy and direction
- `roadmap.md`: immediate, mid-term, and long-term next steps
- `roadmap_2026.md`: sharper product direction for the rest of 2026
- `milestones_2026.md`: concrete milestone and issue-bucket board for 2026
- `docs/commands.md`: command guide with explanations and examples
- `docs/config-guide.md`: config authoring guide
- `docs/recipes.md`: real-world workflow recipes and starter patterns
- `docs/runtime.md`: live session and runtime control guide

## Current Scope

- Local YAML configs
- Schema and semantic validation
- Param interpolation with `{{param_name}}`
- Session-, window-, agent-, and pane-level env merging
- tmux session, window, and pane creation
- Commands: `templates`, `init`, `edit`, `remember`, `load`, `reload`, `validate`, `render`, `list`, `status`, `focus`, `restart`, `rerun`, `stop`, `kill`, `capture`, `send`, `doctor`

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

For command-by-command explanations and usage examples, see [`docs/commands.md`](docs/commands.md).

```bash
./bin/agentmux templates
./bin/agentmux init <template> <path> [--force]
./bin/agentmux edit <path-or-session>
./bin/agentmux remember <session> <config>
./bin/agentmux load <config> [--param key=value] [--attach|--no-attach] [--dry-run] [--replace]
./bin/agentmux reload <config> [--param key=value] [--attach|--no-attach] [--dry-run]
./bin/agentmux validate <config> [--param key=value]
./bin/agentmux render <config> [--param key=value]
./bin/agentmux list
./bin/agentmux status <session> [--json] [--pane <pane-name>]
./bin/agentmux focus <session> <pane-name> [--json]
./bin/agentmux restart <session> <pane-name> [--json]
./bin/agentmux rerun <session> <pane-name> [--json]
./bin/agentmux stop <session> <pane-name> [--json]
./bin/agentmux kill <session> <pane-name> [--json]
./bin/agentmux capture <session> <pane-name> [--json] [--tail N] [--strip-empty]
./bin/agentmux send <session> <pane-name> [--json] [--no-enter] <command...>
./bin/agentmux doctor
./bin/agentmux version
```

`load` attaches by default unless you pass `--no-attach` or `--dry-run`.

`reload` recreates an existing session from config using replace semantics. It is the workflow command for applying config changes onto a live session, and it fails if the session does not exist yet.

`templates` lists the built-in starter configs that ship with Agentmux.

`default` is a friendly alias for the `ai-dev` starter template.

`init` writes one of those built-in starter configs to a target path and rewrites the top-level `session` field from the target filename by default.

`edit` opens either a config path directly or the config path remembered for a live session that was loaded or reloaded from disk.

`remember` backfills that config-path metadata for an already-running session, so `edit <session>` can work without forcing a reload.

Starter flow:

```bash
./bin/agentmux templates
./bin/agentmux init coding-starter coding.yaml
./bin/agentmux validate coding.yaml
./bin/agentmux load coding.yaml --replace --no-attach
```

Full-featured starter flow:

```bash
./bin/agentmux templates
./bin/agentmux init default my-workflow.yaml
./bin/agentmux validate my-workflow.yaml
./bin/agentmux load my-workflow.yaml --replace --no-attach
```

Backfill flow for older live sessions:

```bash
./bin/agentmux remember dogfood ./dogfood.yaml
./bin/agentmux edit dogfood
```

Typical edit loop:

```bash
./bin/agentmux load dogfood.yaml --replace --no-attach
./bin/agentmux edit dogfood
./bin/agentmux status dogfood
./bin/agentmux reload dogfood.yaml --no-attach
./bin/agentmux status dogfood --json
```

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
- on_exit: pane on_exit > agent on_exit > `shell`

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

## Pane Exit Behavior

Use `on_exit` when you want a pane to do something other than the default drop-into-shell behavior after its startup command exits.

```yaml
agents:
  reviewer:
    provider: command
    command: "review"
    on_exit: restart

layout:
  - window: review
    panes:
      - name: reviewer
        agent: reviewer
      - name: diff
        command: "git diff --stat"
        on_exit: remain
```

Supported values:

- `shell`: default. Run the startup command, then land in an interactive shell.
- `remain`: keep the pane visible after the command exits.
- `restart`: restart the startup command in place after it exits.
- `kill`: remove the pane when the command exits.

`on_exit` can be set on agents as a reusable default and overridden per pane.

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
./bin/agentmux status dogfood --pane review
./bin/agentmux status dogfood --pane review --json
./bin/agentmux focus dogfood resolve
./bin/agentmux focus dogfood resolve --json
./bin/agentmux restart dogfood resolve
./bin/agentmux rerun dogfood resolve
./bin/agentmux stop dogfood resolve
./bin/agentmux kill dogfood build
./bin/agentmux capture dogfood review
./bin/agentmux capture dogfood review --json
./bin/agentmux capture dogfood review --tail 20
./bin/agentmux capture dogfood review --tail 20 --strip-empty
./bin/agentmux send dogfood review "git status --short"
./bin/agentmux send dogfood review --no-enter "git commit -m 'wip'"
```

These runtime commands are session-wide. Pane lookup works across all windows in the target session, not just the currently active window.

`restart` and `rerun` both re-execute the pane's stored startup command in place using tmux pane respawn. In the current version, they are intentionally equivalent so the behavior stays predictable for interactive panes.

Named-pane runtime commands preserve the pane's stored `on_exit` behavior, so `restart`, `rerun`, and `status` stay aligned with the original config semantics.

`stop` kills the pane's current process and respawns a fresh interactive shell in place. This is the safe live-control option because it preserves the pane and its layout instead of destroying it.

`kill` is the explicitly destructive option. It removes the target pane from tmux entirely, which can collapse the layout and may remove the window or session if that pane was the last one.

`capture` prints the current joined pane contents for the named pane, which makes it possible to read back output from a live workflow endpoint.

`capture --tail N` limits output to the last `N` captured lines, which is usually more useful for scripts and polling loops than dumping the full pane every time.

`capture --strip-empty` removes blank and whitespace-only lines before printing or tailing, which is usually a better fit for automation against shell panes that include terminal filler rows.

`send` writes literal text into the target pane and presses Enter by default, which makes named panes usable as live workflow endpoints after startup.

`send --no-enter` writes the text without pressing Enter, which is useful when you want to stage input, edit it in the pane, or combine it with later keystrokes.

`--json` exposes machine-friendly output for scripting and automation across the runtime pane-control commands while preserving the existing human-readable defaults.

`status` now also surfaces the pane source, agent name when relevant, configured `on_exit`, tmux's current foreground command, and whether the pane is currently dead. That makes remain-on-exit panes and other finished runtime states easier to inspect without leaving Agentmux.

`status --pane <name>` narrows inspection to a single named pane while preserving the existing output shapes. This is useful for polling loops and targeted debugging without adding a separate command family.

`capture --json` includes `session`, `pane`, `content`, and optional `tail_lines` / `strip_empty` metadata when those filters were applied.

## Automation Contract

For machine-readable runtime output, Agentmux currently exposes:

- `status --json`: `{ "schema_version": 1, "kind": "status", "session": "...", "pane": "...", "panes": [...] }`
- `capture --json`: `{ "schema_version": 1, "kind": "capture", "session": "...", "pane": "...", "tail_lines": N, "strip_empty": true, "content": "..." }`
- action commands with `--json`, such as `focus --json` or `send --json`: `{ "schema_version": 1, "kind": "focus", ... }`
- runtime failures with `--json`: `{ "schema_version": 1, "kind": "error", "command": "focus", "error_code": "runtime", "exit_code": 1, "message": "..." }`

`schema_version` and `kind` are the stable contract anchors for automation. The pane objects returned by `status --json` match the fields shown in the human-readable status output, including live fields like `dead`, `current_command`, `source_type`, and configured `on_exit`. When `status` is filtered with `--pane`, the top-level `pane` field is included and the `panes` array contains just that one live pane.

Concrete examples:

```json
{
  "schema_version": 1,
  "kind": "status",
  "session": "dogfood",
  "pane": "review",
  "panes": [
    {
      "window_index": 1,
      "window_name": "grid",
      "window_id": "@2",
      "window_active": true,
      "pane_index": 4,
      "pane_id": "%11",
      "pane_active": true,
      "dead": false,
      "name": "review",
      "title": "Review",
      "source_type": "agent",
      "agent_name": "reviewer",
      "on_exit": "restart",
      "current_command": "bash"
    }
  ]
}
```

```json
{
  "schema_version": 1,
  "kind": "capture",
  "session": "dogfood",
  "pane": "review",
  "tail_lines": 20,
  "strip_empty": true,
  "content": "manual-send\nafter-stop\n"
}
```

```json
{
  "schema_version": 1,
  "kind": "send",
  "session": "dogfood",
  "pane": "review",
  "message": "sent command to pane \"review\" in session \"dogfood\"",
  "no_enter": true
}
```

```json
{
  "schema_version": 1,
  "kind": "error",
  "command": "focus",
  "error_code": "runtime",
  "exit_code": 1,
  "message": "pane \"missing\" not found in session \"dogfood\"; available named panes: review, build. Run `agentmux status dogfood` to inspect the live session",
  "session": "dogfood",
  "pane": "missing"
}
```

Runtime command exit codes:

- `0`: success
- `1`: runtime failure, such as tmux errors or missing live sessions/panes
- `2`: usage or validation failure
- `3`: config parse or config load failure
- `4`: missing dependency, such as tmux not being installed

When `--json` is set on a runtime command, structured errors are printed to stdout and the process still exits with the same non-zero exit code.

## Runtime Troubleshooting

Start with:

```bash
./bin/agentmux status <session>
```

Common runtime errors:

- `session "..." not found`: the tmux session does not exist yet or has already been removed. Run `./bin/agentmux list` to inspect sessions, or `./bin/agentmux load <config>` to recreate it.
- `pane "..." not found ... available named panes: ...`: the session exists, but that pane name is wrong for the current live session. Run `./bin/agentmux status <session>` and use one of the listed pane names.
- `session has no named panes`: the live session was created from panes without `name:` fields. Add `name:` to the panes you want to control and reload the session.
- `session "..." is missing config path metadata`: use `./bin/agentmux remember <session> ./path/to/config.yaml`, edit the file path directly, or reload the session from a config path first.
- `cannot replace session "..." from inside the same tmux session`: run `reload` or `load --replace` from another shell or another tmux client so Agentmux does not destroy the pane that is executing it.

## Examples

Canonical examples:

- [testdata/examples/minimal.yaml](testdata/examples/minimal.yaml)
- [testdata/examples/ai-dev.yaml](testdata/examples/ai-dev.yaml)
- [testdata/examples/on-exit.yaml](testdata/examples/on-exit.yaml)
- [testdata/examples/review-loop.yaml](testdata/examples/review-loop.yaml)
- [testdata/examples/ssh-ops.yaml](testdata/examples/ssh-ops.yaml)

Built-in starter templates use the same names:

- `minimal`
- `ai-dev`
- `coding-starter`
- `on-exit`
- `review-loop`
- `review-starter`
- `ssh-ops`

Starter aliases:

- `default` -> `ai-dev`

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
