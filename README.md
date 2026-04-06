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

---

## 🛠 Status

Agentmux is production-ready and currently ships with:
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

---

## 🤝 Contributing

Agentmux is built on the Unix philosophy: simple, composable tools that work together. We welcome contributions that strengthen the **Workspace Manager** identity or improve **Agentic Orchestration** primitives.
