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

## 📖 Sophisticated Documentation

We have moved our technical reference, tutorials, and architectural deep-dives to our official documentation site:

👉 **[https://pagel-s.github.io/agentmux/](https://pagel-s.github.io/agentmux/)**

### What you'll find there:
*   **[Quickstart Guide](https://pagel-s.github.io/agentmux/getting-started/quickstart/)**: Launch your first session in 60 seconds.
*   **[Your First AI Team](https://pagel-s.github.io/agentmux/tutorials/hello-team/)**: A tutorial on building a coordinated 2-agent workflow.
*   **[Architecture Mental Model](https://pagel-s.github.io/agentmux/architecture/mental-model/)**: Understand the stateless "compiler" design.
*   **[Portable Bundles](https://pagel-s.github.io/agentmux/tutorials/portable-bundles/)**: How to package and share your sessions.

---

## 🛠 Status

Agentmux is production-ready (`v0.7.0`) with:
- **Agentic Orchestration:** `wait`, `kv-set`, and `scale`.
- **Remote Loading:** Fetch and launch configurations directly from URLs.
- **Declarative Workspaces:** YAML configs with `include` for modular reuse.
- **Reliability:** `requires` block for pre-flight dependency and API key verification.
- **Mission Control:** A real-time TUI dashboard for monitoring your entire team.

---

## 💻 Build and Run

Agentmux requires Go 1.21 or newer and **tmux 3.2+**.

```bash
# Build locally
make build

# Install globally via Go
go install github.com/pagel-s/agentmux/cmd/agentmux@latest

# Run diagnostics
./bin/agentmux doctor
```

---

## 🤝 Contributing

Agentmux is built on the Unix philosophy: simple, composable tools that work together. We welcome contributions that strengthen the **Workspace Manager** identity or improve **Agentic Orchestration** primitives.

See the [Milestone Board](milestones_2026.md) for the 2026 roadmap.
