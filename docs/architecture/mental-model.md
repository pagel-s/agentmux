# Mental Model

Agentmux sits exactly between two extremes:
1.  **Manual Multiplexing:** Hand-typing `tmux split-window` commands.
2.  **Black-Box Daemons:** Complex background services that manage agent memory and execution opaquely.

Agentmux is **declarative, stateless, and inspectable.**

## The Core Concept

At its heart, Agentmux is a compiler. It takes a declarative `yaml` file and translates it into a deterministic series of standard `tmux` CLI commands.

*   It does **not** run a background daemon.
*   It does **not** intercept your keys.
*   It does **not** have its own database.

If Agentmux crashes, your session is fine. If you uninstall Agentmux, your session keeps running.

## The Hierarchy

A configuration breaks down into three core concepts:

1.  **Agents (The "Who"):** These are reusable execution templates. They define *what* runs (e.g., a Python script or an LLM CLI), the environment variables it needs, and how it behaves when it exits.
2.  **Windows (The "Where"):** These define the physical layout of your screen (e.g., `even-horizontal`). They provide shared context (like a working directory) for the panes inside them.
3.  **Panes (The "What"):** These are the actual, running processes. A pane instantiates an Agent. You can have five Panes all running the same "Researcher" Agent, and they will be five distinct shell processes.

## Injected Metadata

To manage these panes without a daemon, Agentmux injects state directly into the tmux processes using **Tmux User Options** (e.g., `@agentmux_name`, `@agentmux_startup_command`). 

When you run `agentmux status`, Agentmux simply asks `tmux` to print these options. This stateless design makes the tool incredibly fast and resilient.
