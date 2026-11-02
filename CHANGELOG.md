# Changelog

## v0.10.0 - 2026-11-02

Terminal-first workspaces, native collaboration and an observable control center.
This release includes the managed runtime introduced in `v0.10.0-rc.1` and all
subsequent workbench and hardening changes. The managed runtime and workbench
remain experimental; this version does not imply a production security or
availability guarantee.

- Managed roles and provider adapters for Claude, Codex, Pi and Antigravity,
  scoped boards and memory, hierarchy, bounded spawning, opt-in Linux Bubblewrap
  isolation, explicit SSH workers and controlled research workflows.

- Illustrated workflow guide with terminal and web screenshots, everyday session
  navigation, a worked poem collaboration, dynamic teams, research and scaling.

- A further usability sweep fixes loading-modal input capture, stale native
  focus, old-controller history compatibility, large-list navigation, exact
  permanent-team impact reviews, recovery of expired deliveries, memory
  replacement/import races, and Pi transport receipt truth. Final-phase research
  controls now govern trial transitions; study observation verifies frozen plans.

- Reviewed, target-bound control center actions, quick native additions,
  cross-workspace handoff, attention and scoped history; exact group disconnect,
  safe uncertainty recovery, session retirement and metadata-preserving memory
  edits. Continuous study observation, archived evidence and richer prompt-free
  behavior exports. See [the control center guide](docs/usability-control-center.md).

- An installed `amx` shortcut, shorter everyday commands and workspace flags,
  directional `allow`/`deny`, and attachment-local workspace selection. A visible
  terminal session list and Ctrl+P switcher make selecting native sessions clear.
- Opt-in native collaboration for Claude, Codex and Pi: independent
  Manual/Review/Auto receiver modes, exact approvals, scoped observations and
  correlated results, with terminal/web controls and bounded broker recovery.
  See [native delivery](docs/native-delivery.md) for provider guarantees and
  the explicit upgrade path for existing pinned sessions.
- Terminal-first workspaces with native interactive sessions, recognizable
  titles and stable IDs, existing-session discovery/bridges, prompt-free
  additions, versioned roles, workspace inventory and individual relaunch.
- A black/purple local web workbench for observation and reviewed live control,
  with searchable task history, scoped memory and a navigable interaction graph.
- Independent contributions from overlapping collaboration groups, explicit
  historical-policy migration, effective-boundary explanations and override resets.
- Explicit note/instruction intent, audience-checked reply ancestry, imported
  memory source claims and owner-signed portable trace bundles.
- Consented dynamic-team ownership/moves, capacity explanations, live admission
  controls, observable research steering, role/profile treatments and supported
  primary outcomes/counterbalancing.
- Worker/native-completion, recovery, scale and asynchronous-editor fixes from
  bounded UAT. See the [closure record](docs/uat-gap-closure.md) and
  [dated provider acceptance](docs/provider-acceptance.md) for measured evidence
  and limits.
- A complete fresh-install acceptance walkthrough, synchronized command help,
  current sample launches, and strict documentation builds in CI.

For installation and a complete first-run test, use the
[fresh-install walkthrough](docs/getting-started/quickstart.md).

## v0.9.0 - 2026-03-31

Ninth private release, introducing **Native LLM Providers** and the **JIT Shell Compiler**.

- Implemented **Native Provider Support**: Agentmux now natively understands `claude`, `gemini`, `openai`, and `gptme`.
- Added **JIT Shell Compiler**: Automatically compiles optimized CLI commands for specific LLM providers, injecting personas and tool schemas.
- Added **Persona Support**: New `persona` field allows linking to Markdown files for agent instructions, optimizing for prompt caching and clarity.
- Added **Model Selection**: New `model` field for specifying LLM model identifiers directly in the agent or pane config.
- Refactored `internal/resolve` to handle deep provider and persona resolution across agents and panes.

## v0.8.0 - 2026-03-31

Eighth private release, enabling **Native LLM Awareness** and coordination tool-calling.

- Added **`agentmux tools`**: Generates JSON Schemas for all coordination primitives (kv-store, wait, scale), enabling native LLM function-calling.
- Added **`agentmux prompt`**: Generates a standardized environment-awareness system prompt for AI agents.
- Implemented **Automatic Self-Awareness**: Injects `$AGENTMUX_SESSION` and `$AGENTMUX_PANE` environment variables into every spawned pane automatically.
- Hardened integration tests against CI race conditions and shell expansion quirks.

## v0.7.0 - 2026-03-29

Seventh private release of Agentmux, introducing the **Agentic Team Protocol** for coordination and portable session sharing.

- Added **Mission Control (TUI Dashboard)**: a real-time, full-screen dashboard for monitoring agents and the shared Blackboard.
- Implemented **Blackboard (KV-Store)**: a shared memory layer for agent coordination using tmux session options.
- Added **Synchronization (`wait`)**: a primitive to block execution until a specific regex pattern appears in a pane's output.
- Implemented **Dynamic Scaling (`scale`)**: surgically add or remove agent instances in a running session.
- Added **Environment Guards (`requires:`)**: pre-flight checks for binaries, env vars, and mandatory params.
- Introduced **Portable Bundles**: the `{{config_dir}}` variable and `pack` command for self-contained session distribution.
- Added **Remote Loading**: fetch and launch configurations or `.amx` bundles directly from URLs.
- Added `supervisor-demo` and `research-team` examples showcasing multi-agent orchestration.

## v0.6.0 - 2026-03-29
...
Sixth private release of Agentmux, focused on config reuse, team workflows, and professional packaging.

- Added `include:` top-level field for splitting configurations and sharing common agents, params, and environment variables across files.
- Implemented recursive config loading with Parent-Wins merging precedence and circular inclusion detection.
- Added support for repo-local (`.agentmux/templates/`) and user-global (`~/.config/agentmux/templates/`) template discovery.
- Updated `templates` command to display template sources ([project], [global], or built-in).
- Enhanced `doctor` with actionable diagnostics, including tmux version checks and PATH verification.
- Implemented professional versioning with embedded Git commit hash and build date.
- Added automated binary distribution via GoReleaser and GitHub Actions.
- Added `team-standard` starter template and layered config examples.

## v0.5.0 - 2026-03-29

Fifth private release of Agentmux, focused on non-destructive session management and deep live inspection.

- Implemented a non-destructive reconciliation engine for `reload`, surgically updating panes and metadata instead of killing the entire session.
- Added support for window renaming and layout re-syncing during session reconciliation.
- Enhanced `status` with a `--logs [N]` dashboard view, showing recent activity for all panes at once.
- Added deep diagnostics to `status` and new `inspect` command, including PID, CPU/Memory usage, and start time metadata.
- Implemented exit status tracking for dead panes using `#{pane_dead_status}`.
- Refined `status` output formatting for better readability and detail.

## v0.4.0 - 2026-03-24
...
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
