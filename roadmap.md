# Agentmux Roadmap

For a sharper product-direction document focused on the rest of 2026, see `roadmap_2026.md`.

## Current Frame

Current `main` is beyond the original `v0.1.0` launcher baseline.

The product now has:

- declarative tmux workspace creation
- reusable agent templates
- exact grid layouts
- stable pane names
- live pane commands such as `status`, `focus`, `restart`, `rerun`, `stop`, `kill`, `capture`, and `send`

That means the next milestone should be treated as a workspace-management release, not more v1 cleanup.

## Immediate

Target: `v0.4.x`

- keep dogfooding on real sessions and fix rough edges quickly
- improve runtime automation surfaces beyond the basic pane loop
- consider JSON output for `list` or additional runtime inspection only if a real automation need appears
- consider small lifecycle refinements only when real use shows a gap
- cut small follow-up releases from the current runtime loop instead of broadening the command surface too quickly

## Mid-Term

Target: `v0.4.x`

- add richer inspection and diagnostics for running sessions
- add local config reuse such as includes or shared templates
- improve machine-friendly automation and scripting surfaces

## Long-Term

Target: decide product direction after real usage proves it

Option 1: workspace manager

- deeper tmux lifecycle management
- reproducible team workflows
- better observability and automation

Option 2: agent runtime

- provider-aware agent definitions
- task and handoff semantics
- event-driven workflows
- optional daemon only if justified by real runtime needs

## Not Yet

These remain intentionally out of scope:

- remote config fetching or marketplace features
- plugin SDK
- agent-to-agent messaging
- provider-specific API clients in core
- distributed execution
