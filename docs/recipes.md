# Agentmux Recipes

This guide shows concrete starting points for common Agentmux workflows.

Use it when you know roughly what you want to do, but do not want to assemble the config from the full reference docs.

If you want the fastest copy-and-tweak path, start from one of these files:

- [`docs/recipes/coding-starter.yaml`](recipes/coding-starter.yaml): edit 3 fields and launch a coding workspace
- [`docs/recipes/review-starter.yaml`](recipes/review-starter.yaml): edit 3 fields and launch a review workspace

## Coding Session

Use this when you want one session for editing, tests, git, and an AI helper pane.

Best starter:

```bash
./bin/agentmux init coding-starter coding.yaml
```

Fuller starter:

```bash
./bin/agentmux init default coding.yaml
```

`coding-starter` is the fastest path when you want a small file to tweak.

`default` gives you the fuller `ai-dev` template with a session name derived from `coding.yaml`.

Fastest copyable file:

- [`docs/recipes/coding-starter.yaml`](recipes/coding-starter.yaml)

Typical next steps:

```bash
./bin/agentmux validate coding.yaml
./bin/agentmux render coding.yaml
./bin/agentmux load coding.yaml --replace --no-attach
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
./bin/agentmux status coding
./bin/agentmux capture coding tests --tail 20 --strip-empty
./bin/agentmux send coding coder "Summarize the current diff"
```

Related files:

- [`testdata/examples/ai-dev.yaml`](../testdata/examples/ai-dev.yaml)
- [`dogfood.yaml`](../dogfood.yaml)

## Review Session

Use this when you want a dedicated code-review workspace with an editor pane, a reviewer pane, and a checks pane.

Best starter:

```bash
./bin/agentmux init review-starter review.yaml
```

Fuller starter:

```bash
./bin/agentmux init review-loop review.yaml
```

Fastest copyable file:

- [`docs/recipes/review-starter.yaml`](recipes/review-starter.yaml)

Typical next steps:

```bash
./bin/agentmux validate review.yaml
./bin/agentmux load review.yaml --replace --no-attach
./bin/agentmux status review
```

What to customize first:

- `params.review_model`: set the review model you actually want
- `agents.reviewer.env.OPENAI_API_KEY`: keep this as `${OPENAI_API_KEY}` so the shell resolves it at runtime
- the checks pane command: swap `npm test` for the command your repo actually uses

Typical shape:

```yaml
version: 1
session: review

params:
  project_dir: "."
  review_model: "gpt-5-mini"

agents:
  reviewer:
    provider: command
    command: "openai api chat.completions.create -m {{review_model}}"
    cwd: "{{project_dir}}"
    env:
      OPENAI_API_KEY: "${OPENAI_API_KEY}"

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
./bin/agentmux focus review reviewer
./bin/agentmux send review reviewer "Review the current diff for regressions"
./bin/agentmux capture review reviewer --tail 40 --strip-empty
./bin/agentmux rerun review checks
```

Related files:

- [`testdata/examples/review-loop.yaml`](../testdata/examples/review-loop.yaml)

## Remote Ops Session

Use this when you want one tmux session for a remote shell plus supporting local panes.

Best starter:

```bash
./bin/agentmux init ssh-ops ops.yaml
```

Typical next steps:

```bash
./bin/agentmux validate ops.yaml
./bin/agentmux load ops.yaml --replace --no-attach
./bin/agentmux status ops
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
./bin/agentmux status ops
./bin/agentmux capture ops logs --tail 40 --strip-empty
./bin/agentmux focus ops prod
```

Related files:

- [`testdata/examples/ssh-ops.yaml`](../testdata/examples/ssh-ops.yaml)

## Watcher Session

Use this when you want panes that behave differently when their startup command exits.

Best starter:

```bash
./bin/agentmux init on-exit watcher.yaml
```

What this starter demonstrates:

- `shell`: run once, then drop into a shell
- `remain`: keep finished output visible
- `restart`: automatically rerun the command
- `kill`: remove the pane after the command exits

Useful commands while testing it:

```bash
./bin/agentmux load watcher.yaml --replace --no-attach
./bin/agentmux status watcher
./bin/agentmux capture watcher watcher --tail 20 --strip-empty
./bin/agentmux restart watcher watcher
```

Related files:

- [`testdata/examples/on-exit.yaml`](../testdata/examples/on-exit.yaml)

## Choosing A Starter

If you are unsure where to begin:

- use `default` for day-to-day coding
- use `review-loop` for dedicated code review
- use `ssh-ops` for remote shell workflows
- use `on-exit` when testing watcher and restart behavior
- use `minimal` when you want the smallest possible starting point

List all starters:

```bash
./bin/agentmux templates
```
