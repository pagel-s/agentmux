# Agentmux 2026 Roadmap

## Purpose

This document sharpens the next stage of the project after `v0.4.0`.

It is more concrete than `project_plan.md` and more directional than `roadmap.md`.
Use it to answer:

- where the product is right now
- what the 2026 goal is
- which path to prioritize next
- what evidence should change that path

## Current Position

Agentmux is currently a pane-oriented tmux workspace manager.

That means:

- configs define sessions, windows, panes, env, params, hooks, and reusable agents
- panes are the runtime unit
- named panes are the control surface
- tmux remains the underlying execution and inspection model

Today, Agentmux is strongest at:

- reproducible workspace startup
- predictable config resolution
- live pane lifecycle control
- scriptable runtime inspection

Today, Agentmux is not yet:

- a true multi-agent runtime
- provider-aware in a deep way
- a system with task, handoff, or coordination semantics

## 2026 North Star

By the end of 2026, Agentmux should be the default way a tmux-heavy user spins up and controls AI-heavy terminal workspaces.

That is the main goal.

If the product proves real demand beyond that, Agentmux can become the foundation for a true terminal agent runtime later. But that should be earned by usage, not assumed up front.

## Core Product Stance

For 2026, the default path should be:

1. strengthen Agentmux as a workspace manager
2. make runtime control and automation excellent
3. improve real workflow reuse and team sharing
4. only then decide whether deeper agent-runtime behavior is justified

This keeps the product honest and keeps the architecture clean.

## Product Principles

Every 2026 feature should be judged against these rules:

- local-first by default
- tmux-native and inspectable
- explicit rather than magical
- pane-first runtime model
- automation-friendly output and exit behavior
- reusable configs without indirection-heavy abstractions

## 2026 Plan

### Phase 1: `v0.4.x` Stabilization

Goal: make the current runtime loop feel dependable in real work.

Focus:

- dogfood the tool on real sessions
- fix rough edges in `status`, `capture`, `send`, `reload`, and `on_exit`
- tighten output clarity for both humans and scripts
- keep release increments small and evidence-driven

Success signs:

- daily use no longer reveals obvious runtime surprises
- scripts can reliably target named panes without ad hoc tmux fallbacks
- live inspection feels sufficient for normal debugging

### Phase 2: Session Management

Candidate milestone: `v0.5.x`

Goal: make Agentmux better at managing existing sessions, not just creating them.

Likely focus:

- better `reload` / apply semantics
- clearer live reconciliation when config changes
- richer status and diagnostics for running panes
- better failure visibility around pane exit behavior

This is the highest-value next layer because it deepens the current product identity instead of broadening it.

### Phase 3: Config Reuse And Team Workflows

Candidate milestone: `v0.6.x`

Goal: make real workflows easier to share and maintain.

Likely focus:

- local includes or shared templates
- stronger examples for common AI-heavy workflows
- better packaging and install story
- clearer conventions for team-owned configs

This is where Agentmux becomes more than a personal tool without needing a remote marketplace or registry.

### Phase 4: Product Decision Point

Candidate milestone: late 2026

At this point, make an explicit choice based on real usage:

#### Path A: Best-in-class workspace manager

Double down on:

- deeper session lifecycle management
- observability
- automation
- team workflow reproducibility

#### Path B: True terminal agent runtime

Only move here if real usage shows that reusable pane templates are not enough.

Evidence that would justify this path:

- repeated demand for provider-aware agent behavior
- repeated demand for task/handoff semantics
- repeated demand for persistent runtime state beyond tmux metadata
- strong evidence that users want Agentmux to manage agent behavior, not just pane behavior

If that evidence is weak, stay on Path A.

## What Not To Build In 2026

Do not default into:

- plugin SDK
- remote config marketplace
- remote config loading as a core dependency
- provider-specific API clients in core
- agent-to-agent messaging
- mandatory daemon architecture
- orchestration that hides tmux semantics

These are expensive, identity-blurring features unless real usage clearly demands them.

## Decision Gates

Before expanding scope, check these questions:

- Are users still escaping to raw tmux for tasks Agentmux should own?
- Are runtime commands missing essential inspection or control behavior?
- Are configs becoming painful to maintain without local reuse?
- Is demand for provider-native behavior real, repeated, and specific?

If the answer is mostly "no", keep strengthening the workspace-manager path.

## Success Criteria

By the end of 2026, success should look like this:

- Agentmux is the default launcher and controller for at least a few real repeatable workflows
- runtime commands cover most day-to-day pane control without dropping to custom tmux scripts
- JSON output is stable enough for small automation loops
- shared configs feel readable and maintainable
- the product direction is clearer, not muddier, than it was at `v0.4.0`

## Bottom Line

The right 2026 strategy is not "build an agent runtime as fast as possible."

The right 2026 strategy is:

- become excellent at tmux-native workspace management
- let real usage reveal the next missing layer
- only cross into deeper agent-runtime behavior when the evidence is strong
