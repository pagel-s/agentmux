# Your First AI Team

Agentmux is more than just a window manager; it's a platform for building coordinated teams of AI agents. In this tutorial, we will build a simple "Assembly Line" team.

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
      agentmux kv-set first-team status "PLAN_READY"
      exec $SHELL

  worker:
    provider: command
    # The worker uses the 'wait' command to block until the planner is done
    command: |
      printf "Worker waiting for the plan...\n"
      # Block until the planner pane outputs the specific string
      agentmux wait first-team planner "Plan complete"
      
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
agentmux load my-team.yaml
```

When tmux opens, you will see two panes side-by-side. 

Watch closely: The **Worker** (on the right) will explicitly say "waiting for the plan..." and will pause. It will not continue until the **Planner** (on the left) prints "Plan complete."

## 3. Mission Control

Once the team is running, detach from tmux (`Prefix + d`) and open the Mission Control dashboard:

```bash
agentmux dashboard first-team
```

In the dashboard, you will see your two agents running. Look at the **Blackboard (KV)** section on the right. You should see `status: PLAN_READY`. This is the shared memory your planner agent created using `kv-set`!

## Summary

You have just orchestrated two isolated terminal processes using:
1. **Synchronization (`wait`)**: Blocking execution based on another pane's output.
2. **Shared Memory (`kv-set`)**: Storing state that outlives the script execution.

These are the building blocks of massive, self-organizing agent teams.
