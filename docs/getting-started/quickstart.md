# Quickstart

Let's launch your first Agentmux session.

## 1. Create a Configuration

Agentmux uses declarative YAML files to define your workspace. You can create one from scratch, but it's easier to use a built-in starter template.

```bash
# See available templates
agentmux templates

# Initialize a standard AI development workspace
agentmux init ai-dev my-workflow.yaml
```

## 2. Review the Plan

Before launching, you can ask Agentmux to show you exactly what it intends to do:

```bash
agentmux render my-workflow.yaml
```

You'll see a breakdown of the windows, panes, commands, and environment variables that will be created.

## 3. Launch the Session

To create the tmux session and attach to it:

```bash
agentmux load my-workflow.yaml
```

Welcome to your new workspace! You should see your editor on the left and a dedicated pane for your AI agent on the right.

## 4. Live Modifications

If you realize you want to change the layout or add a new pane, you don't need to kill the session.

1. Detach from tmux (`Prefix + d`).
2. Edit your configuration file (`agentmux edit my-workflow`).
3. Reload the session non-destructively:

```bash
agentmux reload my-workflow.yaml
```

Agentmux will surgically apply your changes, respawning only the panes whose configuration was altered.
