# Agentmux

Agentmux is a tmux-native CLI for reproducible multi-agent terminal workspaces. It reads a declarative YAML config, validates it, resolves params and environment inheritance, and launches the corresponding tmux session.

## v1 scope

- Local YAML configs
- Schema and semantic validation
- Param interpolation with `{{param_name}}`
- Session-, agent-, and pane-level env merging
- tmux session, window, and pane creation
- Commands: `load`, `validate`, `render`, `list`, `doctor`

## Build

```bash
go build ./cmd/agentmux
```

## Commands

```bash
agentmux load <config> [--param key=value] [--attach|--no-attach] [--dry-run] [--replace]
agentmux validate <config> [--param key=value]
agentmux render <config> [--param key=value]
agentmux list
agentmux doctor
```

## Example

Canonical examples:

- [testdata/examples/minimal.yaml](testdata/examples/minimal.yaml)
- [testdata/examples/ai-dev.yaml](testdata/examples/ai-dev.yaml)
- [testdata/examples/review-loop.yaml](testdata/examples/review-loop.yaml)
- [testdata/examples/ssh-ops.yaml](testdata/examples/ssh-ops.yaml)
