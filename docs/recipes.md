# Agentmux Recipes

This guide shows concrete starting points for common Agentmux workflows.

These recipes use the original YAML layout mode (`init` / `load`) and raw pane
controls. For everyday Claude/Codex conversations, begin with `amx start`
and the [work-session guide](work-sessions.md). That workflow provides the
authenticated coordination broker, collaboration groups and live permissions.

Use the recipes below when you want custom tmux pane layouts and commands.

If you want the fastest copy-and-tweak path, start from one of these files:

- [`docs/recipes/coding-starter.yaml`](recipes/coding-starter.yaml): edit 3 fields and launch a coding workspace
- [`docs/recipes/review-starter.yaml`](recipes/review-starter.yaml): edit 3 fields and launch a review workspace

## Coding Session

Use this when you want one session for editing, tests, git, and an AI helper pane.

Best starter:

```bash
./bin/amx init coding-starter coding.yaml
```

Fuller starter:

```bash
./bin/amx init default coding.yaml
```

`coding-starter` is the fastest path when you want a small file to tweak.

`default` gives you the fuller `ai-dev` template with a session name derived from `coding.yaml`.

Fastest copyable file:

- [`docs/recipes/coding-starter.yaml`](recipes/coding-starter.yaml)

Typical next steps:

```bash
./bin/amx validate coding.yaml
./bin/amx render coding.yaml
./bin/amx load coding.yaml --replace --no-attach
```

What to customize first:

- `params.project_dir`: set the repo path you want to work in
- `agents.coder.command`: point to the local coding model or shell command you actually use
- `agents.reviewer.command`: point to your preferred review command
- pane `name` fields: add stable names if you want runtime control

Typical shape:

```yaml
version: 1
session: coding

params:
  project_dir: "."

agents:
  coder:
    provider: command
    command: "ollama run codellama"
    cwd: "{{project_dir}}"

layout:
  - window: workspace
    root: "{{project_dir}}"
    layout: grid
    grid_rows: 2
    grid_columns: 2
    panes:
      - name: editor
        command: "nvim"
        focus: true
      - name: tests
        command: "go test ./... -count=1"
      - name: git
        command: "git status"
      - name: coder
        agent: coder
```

Good runtime loop:

```bash
./bin/amx status coding
./bin/amx capture coding tests --tail 20 --strip-empty
./bin/amx send coding coder "Summarize the current diff"
```

Related files:

- [`testdata/examples/ai-dev.yaml`](https://github.com/pagel-s/agentmux/blob/main/testdata/examples/ai-dev.yaml)

## Review Session

Use this when you want a dedicated code-review workspace with an editor pane, a reviewer pane, and a checks pane.

Best starter:

```bash
./bin/amx init review-starter review.yaml
```

Fuller starter:

```bash
./bin/amx init review-loop review.yaml
```

Fastest copyable file:

- [`docs/recipes/review-starter.yaml`](recipes/review-starter.yaml)

Typical next steps:

```bash
./bin/amx validate review.yaml
./bin/amx load review.yaml --replace --no-attach
./bin/amx status review
```

What to customize first:

- `agents.reviewer.provider`: choose an installed native CLI such as `codex`
- `agents.reviewer.model`: optionally select a model supported by that native CLI/account
- the checks pane command: swap `npm test` for the command your repo actually uses

Typical shape:

```yaml
version: 1
session: review

params:
  project_dir: "."

agents:
  reviewer:
    provider: codex
    cwd: "{{project_dir}}"

layout:
  - window: review
    root: "{{project_dir}}"
    layout: even-vertical
    panes:
      - name: diff
        command: "git diff --stat"
      - name: reviewer
        agent: reviewer
      - name: checks
        command: "npm test"
```

Good runtime loop:

```bash
./bin/amx focus review reviewer
./bin/amx send review reviewer "Review the current diff for regressions"
./bin/amx capture review reviewer --tail 40 --strip-empty
./bin/amx rerun review checks
```

Related files:

- [`testdata/examples/review-loop.yaml`](https://github.com/pagel-s/agentmux/blob/main/testdata/examples/review-loop.yaml)

## Remote Ops Session

Use this when you want one tmux session for a remote shell plus supporting local panes.

Best starter:

```bash
./bin/amx init ssh-ops ops.yaml
```

Typical next steps:

```bash
./bin/amx validate ops.yaml
./bin/amx load ops.yaml --replace --no-attach
./bin/amx status ops
```

What to customize first:

- `agents.prod-shell.command`: replace the placeholder SSH target
- any supporting panes: logs, health checks, deploy scripts, or dashboards
- pane names: give the important panes stable names so you can target them later

Typical shape:

```yaml
version: 1
session: ops

agents:
  prod-shell:
    provider: ssh
    command: "ssh user@prod.example.com"
    title: "Prod"

layout:
  - window: ops
    layout: even-horizontal
    panes:
      - name: prod
        agent: prod-shell
        focus: true
      - name: logs
        command: "tail -f /var/log/syslog"
      - name: checks
        command: "printf 'run health checks here\\n'; exec $SHELL"
```

Good runtime loop:

```bash
./bin/amx status ops
./bin/amx capture ops logs --tail 40 --strip-empty
./bin/amx focus ops prod
```

Related files:

- [`testdata/examples/ssh-ops.yaml`](https://github.com/pagel-s/agentmux/blob/main/testdata/examples/ssh-ops.yaml)

## Watcher Session

Use this when you want panes that behave differently when their startup command exits.

Best starter:

```bash
./bin/amx init on-exit watcher.yaml
```

What this starter demonstrates:

- `shell`: run once, then drop into a shell
- `remain`: keep finished output visible
- `restart`: automatically rerun the command
- `kill`: remove the pane after the command exits

Useful commands while testing it:

```bash
./bin/amx load watcher.yaml --replace --no-attach
./bin/amx status watcher
./bin/amx capture watcher watcher --tail 20 --strip-empty
./bin/amx restart watcher watcher
```

Related files:

- [`testdata/examples/on-exit.yaml`](https://github.com/pagel-s/agentmux/blob/main/testdata/examples/on-exit.yaml)

## Coordinated Team

Use this when you want multiple agents to work together using shared state and synchronization.

Best starter:

```bash
./bin/amx init supervisor-demo team.yaml
```

What this starter demonstrates:

- **Shared Blackboard (KV-Store):** Agents reading and writing to `amx kv-set`.
- **Synchronization (Wait):** One agent waiting for another to print a specific message.
- **Dynamic Scaling:** A manager agent expanding the team automatically.

Typical next steps:

```bash
./bin/amx load team.yaml
# Observe the manager pane coordinating the engineer panes
```

Key coordination commands:

```bash
# Set a shared variable
./bin/amx kv-set my-team current_task "research"

# Wait for a pane to finish
./bin/amx wait my-team worker "DONE"

# Scale the workforce
./bin/amx scale my-team worker 5
```

Related files:

- [`testdata/examples/research-team.yaml`](https://github.com/pagel-s/agentmux/blob/main/testdata/examples/research-team.yaml)

## Choosing A Starter

For authenticated native conversations and opt-in collaboration, begin with
`amx start` and the [optional verification walkthrough](getting-started/native-walkthrough.md).
If you specifically want a v1 custom layout:

- use `default` for day-to-day coding
- use `review-loop` for dedicated code review
- use `ssh-ops` for remote shell workflows
- use `on-exit` when testing watcher and restart behavior
- use `minimal` when you want the smallest possible starting point

List all starters:

```bash
./bin/amx templates
```
