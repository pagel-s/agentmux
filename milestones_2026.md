# Agentmux 2026 Milestone Board

This document turns `roadmap_2026.md` into a concrete working board.

It is intentionally more execution-oriented than the strategy docs.
Use it to decide what should go into the next releases and what should stay out.

## How To Use This Board

- treat each bucket as a group of related issues, not a promise that every item must ship
- use `Must Ship` to protect milestone identity
- use `Should Ship` as the default working set after the must-have layer is stable
- use `Nice To Have` only when real usage creates pressure
- move items down, not up, when time gets tight

## `v0.4.x` Stabilization

Theme: make the runtime loop feel dependable in daily use.

### Must Ship

#### Runtime polish

- tighten awkward command output in `status`, `capture`, `send`, and `reload`
- improve any remaining noisy or unclear error messages
- fix shell or tmux edge cases found during real dogfooding

#### Automation contract hardening

- confirm JSON envelopes stay stable across real usage
- document any output fields that scripts should treat as stable
- tighten exit-code behavior anywhere it still feels ambiguous

#### Dogfood-driven fixes

- keep one real workflow on Agentmux full-time
- convert actual friction into small follow-up fixes instead of speculative feature work

### Should Ship

#### Inspection improvements

- richer pane metadata only if it solves a real debugging need
- targeted improvements to `status --pane` or `capture --tail`
- clearer display of pane exit behavior where helpful

#### Packaging and install polish

- tighten install docs
- make `go install` and local build flows clearer
- reduce confusion around stale binaries and path setup
- keep config creation and reopening ergonomic through starter templates and session-aware editing

### Nice To Have

#### Minor ergonomics

- small command aliases only if repeated usage proves they matter
- extra examples for common day-to-day workflows

### Not In This Milestone

- new command families without real pain behind them
- deep config abstractions
- provider-specific agent logic
- daemon work

## `v0.5.x` Session Management

Theme: make Agentmux better at managing existing sessions, not just creating them.

### Must Ship

#### Better reload and apply behavior

- clarify what `reload` actually guarantees
- improve behavior when config changes materially affect a running session
- surface reconciliation failures clearly instead of silently replacing too much or too little

#### Stronger live inspection

- better runtime diagnostics for running and finished panes
- more useful live metadata when debugging exit or restart behavior
- session-wide inspection that reduces the need to fall back to raw tmux

#### Failure visibility

- make pane exit behavior easier to understand in practice
- improve visibility around restart loops and dead panes
- expose the right metadata before users need manual tmux inspection

### Should Ship

#### Safer reconciliation

- guard rails for destructive reload situations
- clearer messaging around what happens to existing panes and windows
- predictable behavior when the user runs Agentmux from inside tmux

#### Better status targeting

- additional status filters only if real workflows need them
- lightweight scripting improvements that avoid broadening the CLI too much

### Nice To Have

#### Operational quality of life

- better doctor or troubleshooting guidance
- more examples showing real runtime control loops

### Not In This Milestone

- remote workspace management
- full history or persistence layer
- background daemon

## `v0.6.x` Config Reuse And Team Workflows

Theme: make shared workflows easier to maintain without losing clarity.

### Must Ship

#### Local config reuse

- local includes or shared templates
- reuse mechanisms that stay readable and do not hide what runs
- clear precedence rules when reused config layers combine

#### Team workflow readability

- examples for real AI-heavy team workflows
- conventions for naming panes, agents, and windows
- docs that make shared configs serve as workflow documentation

#### Packaging and onboarding

- cleaner install story
- reduce friction for first-time setup
- make the repo feel easier to adopt without turning it into a platform

### Should Ship

#### Config ergonomics

- reduce duplication in large configs
- keep parameterization portable across machines and repos
- improve discoverability of common patterns through examples

#### Team-friendly automation

- clarify which runtime JSON fields are stable for shared scripts
- make small automation loops easier to share inside a team

### Nice To Have

#### Distribution improvements

- release artifacts beyond source builds
- optional packaging integrations if adoption justifies them

### Not In This Milestone

- remote config registry
- cloud-hosted sharing layer
- plugin marketplace

## Cross-Cutting Backlog

These items may span multiple milestones, but should be pulled in only when real usage justifies them.

### Observability

- richer status metadata
- clearer pane health signals
- better visibility into restart and exit behavior

### Automation

- stable JSON contract documentation
- small scripting-oriented flags that reduce wrapper code
- better examples of shell and CI usage

### Documentation

- real workflow examples
- troubleshooting for tmux and shell edge cases
- sharper explanation of the product model: agents, windows, panes

## Decision Rules

When deciding whether a candidate issue belongs in a milestone, ask:

- does this strengthen the workspace-manager identity?
- does this remove real friction seen in actual sessions?
- does this improve inspectability or automation without hiding tmux?
- does this keep the pane-first runtime model clean?

If the answer is mostly "no", it probably does not belong yet.

## Triggers For A Larger Direction Change

Do not start planning a deeper agent-runtime push until there is repeated evidence of:

- demand for provider-aware agent behavior
- demand for task and handoff semantics
- demand for shared state that tmux metadata cannot cover
- repeated frustration with pane-level control as the main abstraction

Until then, the board should stay biased toward workspace management.
