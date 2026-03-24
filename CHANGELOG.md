# Changelog

## Unreleased

- Added `templates` and `init` so new configs can be created from built-in starter templates instead of copied manually.
- Added `edit <path-or-session>` with session config-path metadata, so live sessions can reopen the config file they came from.
- Added a friendly `default` starter alias for the `ai-dev` template and clearer help/output for the starter flow.
- Added `remember <session> <config>` so existing live sessions can backfill config-path metadata without a reload.

## v0.4.0 - 2026-03-24

- Added `status --pane <name>` for targeted single-pane inspection in both human-readable and JSON output.
- Added `capture --tail N` for smaller, automation-friendly pane reads.
- Added `capture --strip-empty` for cleaner script-facing reads from shell panes with blank terminal filler rows.
- Added `send --no-enter` for queueing text into a live pane without executing it yet.
- Added explicit JSON `schema_version` and `kind` fields for runtime automation output.
- Added structured JSON error output for runtime commands when `--json` is set, while preserving the existing exit codes.
- Added live pane dead/alive status to `status` so finished `remain` panes and similar runtime states are visible in both human-readable and JSON output.
- Expanded runtime tests and README guidance, including concrete JSON envelope examples for the automation-oriented pane control loop.

## v0.3.0 - 2026-03-23

Third private release of Agentmux, focused on session reload and richer pane lifecycle behavior.

- Added `reload` as the workflow command for recreating an existing session from config, with guards against self-replacing the current tmux session.
- Added configurable pane `on_exit` behavior with `shell`, `remain`, `restart`, and `kill`.
- Preserved `on_exit` as live pane metadata so `status`, `restart`, `rerun`, and `stop` stay aligned with configured pane behavior.
- Fixed live `status` and named-pane lookup so runtime commands operate across all windows in a session instead of only the active window.
- Added an `on-exit` example config and expanded runtime coverage around multi-window sessions and pane lifecycle behavior.

## v0.2.0 - 2026-03-23

Second private release of Agentmux, focused on live pane-oriented workspace management.

- Stable pane names as first-class runtime identifiers.
- Live pane commands: `status`, `focus`, `restart`, `rerun`, `stop`, `kill`, `capture`, and `send`.
- `status --json` and `capture --json` for scriptable runtime automation.
- Richer live status metadata including pane source, agent name, and current foreground command.
- Stronger runtime error messages and troubleshooting docs for missing sessions, missing pane names, and unnamed sessions.
- Strategy, roadmap, and README documentation aligned with the current product direction.

## v0.1.0 - 2026-03-23

Initial private release of Agentmux.

- Declarative YAML config for tmux sessions, windows, panes, params, hooks, and reusable agents.
- Validation, `render`, `dry-run`, `doctor`, and strict-mode warnings for safer config authoring.
- Predictable resolution rules for params, cwd, focus, titles, and env inheritance.
- Exact grid layouts with `layout: grid`, `grid_rows`, and `grid_columns`.
- Window-level env overrides with precedence `pane > window > agent > session`.
- Unit, fixture, and tmux integration coverage plus CI for build and test checks.
