# Pane harness integration

For managed identities, native-auth snapshots, separate agent/broker sandboxes,
and an opt-in real Claude/Codex/local-Pi workflow, see
[mixed harness sandbox acceptance](mixed-harness-acceptance.md).

The `provider` field in the existing pane configuration selects a local agent harness. Each pane runs its own process. Model endpoints and credentials remain configured in the harness; a harness name is not a model-provider connection.

| Configuration value | Executable | Role delivery | Agentmux coordination |
| --- | --- | --- | --- |
| `claude` | `claude` | Appended system prompt | Per-session native MCP when supported; CLI bridge fallback |
| `codex` | `codex` | Initial prompt | Per-session native MCP when supported; CLI bridge fallback |
| `antigravity` or `agy` | `agy` | Interactive initial prompt | CLI bridge |
| `pi` | `pi` | Appended system prompt | CLI bridge |
| `gemini` or `gemini-cli` | `gemini` | Workspace context artifact in native mode | Native MCP when supported; CLI bridge fallback |
| `gptme` | `gptme` | Initial prompt | CLI bridge |

Existing Gemini configurations continue to select the Gemini executable. They are not redirected to Antigravity, whose flags and configuration differ.

## Claude Code

Agentmux combines the persona file and operational coordination instructions into one `--append-system-prompt` argument. This preserves Claude Code's default instructions. Native MCP uses a per-session `--mcp-config` with `--strict-mcp-config` so other MCP configuration is excluded. Agentmux does not bypass Claude's tool permissions.

The [official CLI reference](https://code.claude.com/docs/en/cli-reference) documents these flags. The ordinary pane launcher remains an interactive terminal process; streaming input or interruption through the Agent SDK is a separate integration surface.

Managed turns use `--safe-mode`, structured print output, and `--permission-mode dontAsk`. A coding role therefore needs an owner-approved native tool allowlist; role text alone does not grant tool permissions. For example, `command: [claude, --allowedTools, 'Read,Write,Edit,Bash']` enables those tools while retaining the managed adapter's safety flags. Narrow the Bash patterns for the task where practical. A structured permission denial fails the managed task even when the CLI exits zero. This configuration passed a real bounded coding turn with Claude Code 2.1.316; see the [Claude/Codex example](../examples/managed/claude-codex.yaml) for trusted local login reuse and workspace setup. Local mode does not isolate those tools from host files.

## Antigravity

Use `provider: antigravity` or its `agy` alias. The launcher passes `--prompt-interactive`, optional `--model`, and optional `--effort`. Persona text and CLI bridge instructions become the initial user prompt. The supported effort values are `low`, `medium`, `high`, `xhigh`, and `max`; the selected model and installed harness can impose further restrictions.

Agentmux uses the CLI bridge even if Antigravity advertises MCP support. Its [MCP documentation](https://antigravity.google/docs/mcp?tab=cli) describes global and workspace files rather than a per-session configuration flag. Writing those files for several agents sharing a workspace could mix their identities. The launcher creates no MCP configuration and changes no Antigravity settings.

Antigravity's [headless protocol](https://antigravity.google/docs/cli/headless/) supports structured streaming turns, but that is separate from this interactive pane launcher. In headless mode, tool permissions can deny an action while the overall run still exits successfully; a controller must inspect structured outcomes and denials.

## Pi

Use `provider: pi`. Agentmux appends the persona and coordination instructions using `--append-system-prompt`. Set `model` to the configured model identifier, including a provider prefix when needed. `reasoning_effort` maps to `--thinking` and accepts `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, or `max`; Pi adjusts reasoning to the selected model's capabilities.

The launcher does not install extensions, modify MCP configuration, or approve project resources automatically. Pi's [CLI reference](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/cli.md) and [RPC interface](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md) describe the separate interactive and structured control surfaces. Installed versions can differ; CLI preflight checks the flags the launcher requires.

For npm-installed Pi inside a managed bubblewrap sandbox, use an explicit Node executable and the real package entrypoint: `command: [/absolute/node, /absolute/pi-package/dist/bundle/cli.js]`. Declare that Node binary and the complete Pi package directory in `read_only_paths`. A host symlink such as `~/.local/bin/pi`, when mounted as a standalone file and invoked through another wrapper, loses its package-relative module paths. Its version check can pass while CLI help and actual turns fail. The managed acceptance fixture uses the explicit entrypoint; it does not mount a whole host home or change global installations.

## Boundaries

A pane launcher provides convenient startup and context delivery. It does not authenticate agents to a coordination service, provide sandbox isolation, or turn terminal input into reliable structured commands. Those guarantees require managed identities, an enforced policy service, sandbox backends, and structured harness adapters. Native harness permissions still apply to CLI bridge calls.
