# Agentmux Roadmap

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

Target: `v0.3.0`

- keep dogfooding on real sessions and fix rough edges quickly
- add configurable pane exit behavior such as `on_exit` or optional auto-restart
- improve runtime automation surfaces such as `capture --tail` or `send --no-enter`
- cut `v0.3.0` once reload and pane lifecycle behavior feel stable in real use

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
