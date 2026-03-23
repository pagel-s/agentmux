# Agentmux Project Plan

## Vision

Agentmux is a tmux-native tool for defining, launching, and reusing AI-heavy terminal workspaces with one command.

The long-term ambition is still the same: make multi-agent terminal workflows reproducible, shareable, and easy to inspect. The difference now is that the product should grow from a strong tmux workspace manager into anything more ambitious, not skip that step.

## Current Product Position

Current `main` is best described as a pane-oriented workspace manager built on reusable agent templates.

- `agents` are reusable pane templates
- `windows` provide layout and context
- `panes` are the runtime unit

That framing is important because it keeps the product honest. Today, Agentmux is not yet a true multi-agent runtime with coordination semantics, provider-native behavior, or shared state. It is a reliable config-and-runtime layer for tmux workspaces.

## Product Thesis

Agentmux should win on:

- reproducibility: one config can recreate a real working session
- transparency: tmux behavior stays explicit and debuggable
- ergonomics: repeated tmux workflows become easy to launch and operate
- composability: reusable agent templates reduce duplication without hiding what runs

## Users

Primary users:

- heavy tmux users
- AI-heavy terminal developers
- engineers comfortable with CLI and YAML
- people who value explicit control over hidden automation

## Near-Term Direction

The product should continue moving from "workspace compiler" to "workspace manager".

That means prioritizing:

- stable pane identity
- live pane lifecycle operations
- machine-friendly output for automation
- better observability of running sessions

The core rule is simple: every new capability should build on tmux and named panes in a way that remains inspectable and predictable.

## Immediate Priorities

- finish the pane-runtime control loop with an explicitly destructive `kill` command
- add `--json` output for `status` and `capture`
- improve runtime metadata and error clarity
- keep dogfooding the tool on real sessions
- tag the next milestone as `v0.2.0`

## Mid-Term Priorities

- `reload` or `apply` to reconcile config changes onto an existing session
- richer pane lifecycle controls such as `on_exit` policy or optional auto-restart
- better automation surfaces, including stable metadata and scriptable output
- local config reuse mechanisms such as includes or shared templates

## Long-Term Decision

Agentmux will eventually need a deliberate product choice:

### Path A: Best-in-class tmux workspace manager

Double down on:

- lifecycle management
- observability
- reproducibility
- automation
- team-shared local workflows

### Path B: True multi-agent runtime

Only after the workspace-manager layer is proven, consider adding:

- provider-aware agents
- task and handoff semantics
- event hooks and workflow steps
- optional daemon or background runtime

The right default is to stay on Path A until real usage proves the need for Path B.

## Explicit Non-Goals For Now

Do not prioritize these yet:

- remote config marketplace or registry
- agent-to-agent messaging
- plugin SDK
- provider-specific API abstractions in core
- orchestration logic that bypasses tmux semantics
- mandatory background daemon

## Success Criteria

Short term:

- a user can launch a real multi-window workspace from one YAML file
- a running session can be controlled reliably by stable pane names
- output is clear enough for both humans and scripts

Medium term:

- Agentmux becomes the default way a tmux-heavy user starts and manages repeatable AI workspaces

Long term:

- Agentmux becomes either the clear terminal-first workspace manager for AI workflows or the foundation of a true multi-agent terminal runtime, based on proven user behavior rather than speculation
