# Legacy layout handoff tutorial

For your first native Claude/Codex team with scoped communication, use the
[optional verification walkthrough](../getting-started/native-walkthrough.md). This separate
tutorial demonstrates the original v1 layout compiler with two shell commands;
it makes no model calls and provides no per-agent broker ACLs or sandboxes.

We will create a **Planner** agent that writes a task, and a **Worker** agent that waits for the task to be ready before starting.

## 1. The Configuration

Create a file named `my-team.yaml` with the following content:

```yaml
version: 1
session: first-team

agents:
  planner:
    provider: command
    # The planner does some work, then updates the Blackboard
    command: |
      printf "Thinking about the architecture...\n"
      sleep 3
      printf "Plan complete.\n"
      amx kv-set first-team status "PLAN_READY"
      exec $SHELL

  worker:
    provider: command
    # The worker uses the 'wait' command to block until the planner is done
    command: |
      printf "Worker waiting for the plan...\n"
      # Block until the planner pane outputs the specific string
      amx wait first-team planner "Plan complete"
      
      # Now the worker can proceed safely
      printf "\n[STARTING] Executing the plan!\n"
      exec $SHELL

layout:
  - window: workflow
    layout: even-horizontal
    panes:
      - name: planner
        agent: planner
        title: "Agent: Planner"
      - name: worker
        agent: worker
        title: "Agent: Worker"
```

## 2. Launching the Team

Start the session:

```bash
amx load my-team.yaml
```

When tmux opens, you will see two panes side-by-side. 

Watch closely: The **Worker** (on the right) will explicitly say "waiting for the plan..." and will pause. It will not continue until the **Planner** (on the left) prints "Plan complete."

## 3. Mission Control

Once the team is running, detach from tmux (`Prefix + d`) and open the Mission Control dashboard:

```bash
amx dashboard first-team
```

In the dashboard, you will see your two agents running. Look at the **Blackboard (KV)** section on the right. You should see `status: PLAN_READY`. This is the shared memory your planner agent created using `kv-set`!

## Summary

You have coordinated two tmux pane processes using:
1. **Synchronization (`wait`)**: Blocking execution based on another pane's output.
2. **Shared Memory (`kv-set`)**: Storing state that outlives the script execution.

For bounded spawning, consented membership and permission-controlled shared
knowledge, continue with [dynamic teams](../dynamic-teams.md). V1 KV and output
polling do not establish those controls.
