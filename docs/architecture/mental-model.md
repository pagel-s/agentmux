# Layout compiler mental model

This page describes the legacy v1 workspace compiler (`load`, `render`, and raw
tmux pane commands). Its declarative launch layer is stateless.

The v2 [managed runtime](../managed-runtime.md) is stateful: it keeps authenticated
coordination, tasks, permissions, memory, and process ownership in a private
store. For the terminal-first daily workflow, start with
[work sessions](../work-sessions.md).

## The Core Concept

In this mode, Agentmux compiles declarative YAML into standard `tmux` CLI commands.

*   It does **not** run a background daemon.
*   It does **not** intercept your keys.
*   It does **not** have its own database.

Once the launch command exits, tmux owns the pane processes. Removing that
launcher does not by itself stop them. This description does not apply to the
managed broker/supervisor, which has persistent state and explicit lifecycle
controls.

## The Hierarchy

A configuration breaks down into three core concepts:

1.  **Agents (The "Who"):** These are reusable execution templates. They define *what* runs (e.g., a Python script or an LLM CLI), the environment variables it needs, and how it behaves when it exits.
2.  **Windows (The "Where"):** These define the physical layout of your screen (e.g., `even-horizontal`). They provide shared context (like a working directory) for the panes inside them.
3.  **Panes (The "What"):** These are the actual, running processes. A pane instantiates an Agent. You can have five Panes all running the same "Researcher" Agent, and they will be five distinct shell processes.

## Injected Metadata

To manage these panes without a daemon, Agentmux injects state directly into the tmux processes using **Tmux User Options** (e.g., `@agentmux_name`, `@agentmux_startup_command`). 

When you run `amx status`, Agentmux simply asks `tmux` to print these options. These options make legacy layout state inspectable without a separate database.
