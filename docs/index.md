# Welcome to Agentmux

**Agentmux** is the fastest way to spin up and control coordinated AI-heavy terminal workspaces.

It's not just a window manager—it's a **stateless orchestrator** for terminal-based agents.

!!! info "The Paradigm"
    Agentmux sits exactly between two extremes: manual tmux splits and complex, black-box agent daemons. It gives you the power of declarative configuration with the transparency of the Unix terminal.

---

## Why Agentmux?

*   **🚀 One-Command Launch:** Spin up complex multi-window, multi-pane layouts with specialized AI agents in seconds.
*   **🧠 Coordinated Teams:** Agents talk to each other via a shared **Blackboard** (shared memory) and **Wait** (synchronization) primitives.
*   **📦 Portable Bundles:** Package your entire agentic team—YAML, scripts, and prompts—into a single `.amx` file that runs anywhere.
*   **🔍 Visible Agency:** Don't let your agents run in the dark. Monitor everything in real-time with the **Mission Control Dashboard**.
*   **⚡ Stateless & Resilient:** No background daemons. Agentmux "compiles" your config into tmux commands. If Agentmux crashes, your agents keep working.

---

## Getting Started

Check out these guides to get up and running:

*   **[Installation](getting-started/installation.md)**: How to get Agentmux on your machine.
*   **[Quickstart Guide](getting-started/quickstart.md)**: Launch your first session in 60 seconds.
*   **[Your First AI Team](tutorials/hello-team.md)**: A tutorial on building a coordinated 2-agent workflow.
*   **[Architecture Mental Model](architecture/mental-model.md)**: Understand the stateless "compiler" design.
*   **[Portable Bundles](tutorials/portable-bundles.md)**: How to package and share your sessions.

---

## The Agentic Team Protocol

Agentmux provides the core pillars required for professional AI team orchestration in the terminal:

| Pillar | Feature | Purpose |
| :--- | :--- | :--- |
| **Memory** | [The Blackboard](architecture/coordination.md#1-the-blackboard-kv-store) | Shared state via tmux session options. |
| **Signals** | [Synchronization](architecture/coordination.md#2-synchronization-wait) | Block execution until output patterns are matched. |
| **Elasticity** | [Dynamic Scaling](architecture/coordination.md#3-dynamic-scaling) | Surgically add/remove workers based on workload. |
| **Reliability** | [Environment Guards](config-guide.md#requires-environment-guards) | Pre-flight dependency and API key verification. |

---

## Current Status

This repository builds on the v1 foundation with:

- **Agentic Orchestration:** `wait`, `kv-set`, and `scale`.
- **Remote Loading:** Fetch and launch configurations directly from URLs.
- **Declarative Workspaces:** YAML configs with `include` for modular reuse.
- **Non-destructive Management:** `reload` surgically updates panes without killing sessions.
- **Mission Control:** A full-screen TUI for real-time monitoring.
