# Agentmux

**Agentmux** is the fastest way to spin up and control coordinated AI-heavy terminal workspaces.

Agentmux is a **tmux-native orchestrator** for multi-agent terminal workflows. It moves beyond simple layout management to provide a complete **Agentic Team Protocol** for building and sharing sophisticated terminal-based AI teams.

---

## 🚀 Vision: The Agentic Terminal

Most AI agent frameworks are "black boxes." Agentmux brings agency to the surface. It gives your agents:
*   **Eyes:** Automated output capture and pattern matching (`wait`).
*   **Hands:** Direct terminal interaction through `send`.
*   **A Shared Brain:** A shared Blackboard (KV-store) for team coordination.
*   **Portability:** Self-contained bundles (`.amx`) that run anywhere.

---

## 📖 Documentation

Technical reference, tutorials, and architectural deep-dives are available on our official site:

👉 **[https://pagel-s.github.io/agentmux/](https://pagel-s.github.io/agentmux/)**

### Core Resources:
*   **[Installation](docs/getting-started/installation.md)**: How to get Agentmux on your machine.
*   **[Quickstart Guide](docs/getting-started/quickstart.md)**: Launch your first session in 60 seconds.
*   **[Your First AI Team](docs/tutorials/hello-team.md)**: A tutorial on building a coordinated 2-agent workflow.
*   **[Architecture Mental Model](docs/architecture/mental-model.md)**: Understand the stateless "compiler" design.
*   **[Portable Bundles](docs/tutorials/portable-bundles.md)**: How to package and share your sessions.
*   **[Managed Runtime](docs/managed-runtime.md)**: Opt-in v2 foundation with authenticated boards, scoped messages, durable tasks, private/shared memory, mixed harnesses, and optional bubblewrap isolation. Start with the no-model `/bin/cat` demo; the existing tmux workflow remains available.
*   **[Dynamic Teams](docs/dynamic-teams.md)**: Bounded sandbox spawning, delegated permissions, invitation-based membership, and team history controls.
*   **[Research Pilots](docs/experiments.md)**: Seeded trial plans, fresh state, independent scoring, and cooperation/competition payoff comparisons.
*   **[Autonomous Workflows](docs/autonomous-workflows.md)**: Private native conversations, durable subscriptions, observation wakeups and bounded execution from one goal.
*   **[Repeated-Round Research](docs/research.md)**: Scarce resources, private information, declared incentives, objective feedback and optional consented teams.
*   **[Mixed-Harness Sandbox Acceptance](docs/mixed-harness-acceptance.md)**: Reproduce the real Claude/Codex/local-Pi handoff with selected native login files, enforced worker resources, scoped artifacts, and independent verification.

---

## 🛠 Status

The established tmux workflow ships with:
- **Agentic Orchestration:** `wait`, `kv-set`, and `scale`.
- **Remote Loading:** Fetch and launch configurations directly from URLs.
- **Declarative Workspaces:** YAML configs with `include` for modular reuse.
- **Reliability:** `requires` block for pre-flight dependency and API key verification.
- **Mission Control:** A real-time TUI dashboard for monitoring your entire team.

---

## 💻 Build and Run

Agentmux requires Go 1.24 or newer and **tmux 3.2+**. `make` will prefer a repo-local toolchain under `.tools/` when it is Go 1.24+; otherwise it uses `go` from your `PATH`.

```bash
# Build locally
make build

# Install globally via Go
go install github.com/pagel-s/agentmux/cmd/agentmux@latest

# Run diagnostics
./bin/agentmux doctor
```

Start a managed team, inspect its published activity, and stop it:

```bash
./bin/agentmux managed up examples/managed/command-demo.yaml
./bin/agentmux managed activity --state .agentmux-state/command-demo --watch
./bin/agentmux managed down --state .agentmux-state/command-demo
```

This demo uses command processes and makes no model calls. Configure Claude Code, Codex, Antigravity, or Pi in [managed team YAML](docs/managed-runtime.md) for model-backed workers. Managed mode and the research runners are opt-in experimental features. Claude, Codex and Pi support private persistent conversations in Bubblewrap; Antigravity uses fresh turns. Message wakeups require explicit autonomy configuration. Interactive terminal steering remains separate from managed task delivery.

---

## 🤝 Contributing

Agentmux is built on the Unix philosophy: simple, composable tools that work together. We welcome contributions that strengthen the **Workspace Manager** identity or improve **Agentic Orchestration** primitives.
