# Agentmux Project Plan

## Vision

Agentmux is the fastest way to spin up and control AI-heavy tmux workspaces.

Agentmux is a tmux-native tool for defining, launching, and reusing AI-heavy terminal workspaces with one command.

The long-term ambition is still the same: make multi-agent terminal workflows reproducible, shareable, and easy to inspect. The difference now is that the product should grow from a strong tmux workspace manager into anything more ambitious, not skip that step.

## Current Product Position

Current `main` is best described as a pane-oriented workspace manager built on reusable agent definitions.

- `agents` are first-class config objects that define reusable execution defaults
- `windows` provide layout and context
- `panes` are the runtime instantiations and control unit

That framing is important because it keeps the product honest. Today, Agentmux is not yet a true multi-agent runtime with coordination semantics, provider-native behavior, or shared state. It is a reliable config-and-runtime layer for tmux workspaces. Agents matter because they capture intent, defaults, and reuse. Panes matter because they are what actually runs.

## Product Thesis

Agentmux should win on:

- reproducibility: one config can recreate a real working session
- transparency: tmux behavior stays explicit and debuggable
- ergonomics: repeated tmux workflows become easy to launch and operate
- composability: reusable agent templates reduce duplication without hiding what runs

## Config Philosophy

Configs should be easy to share, easy to inspect, and easy to move between machines.

That means:

- local-first by default, with no hidden remote dependency
- portable across repos and environments through params and env inheritance
- explicit about what actually runs in each pane
- readable enough to serve as team documentation for a workflow
- structured so reuse does not turn into indirection-heavy magic

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

- keep dogfooding the tool on real sessions
- add configurable pane exit behavior such as `on_exit` or optional auto-restart
- improve runtime automation surfaces and scriptability
- tag the next milestone as `v0.3.0`

## Mid-Term Priorities

- better automation surfaces, including stable metadata and scriptable output
- local config reuse mechanisms such as includes or shared templates
- richer inspection and diagnostics for running sessions

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
