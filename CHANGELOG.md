# Changelog

## Unreleased

- Added configurable pane `on_exit` behavior with `shell`, `remain`, `restart`, and `kill`.
- Preserved `on_exit` as live pane metadata so `status`, `restart`, `rerun`, and `stop` stay aligned with configured pane behavior.

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
