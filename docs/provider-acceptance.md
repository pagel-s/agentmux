# Provider acceptance · 1 November 2026

This pass distinguishes real model turns from native CLI startup, transport
conformance and configuration-retention tests. It does not establish every
provider/model combination or the quality of generated work.

For current setup, use [interactive work sessions](work-sessions.md) for native
Claude/Codex/Pi/Antigravity terminals and [managed setup](managed-setup.md) for
isolated task workers, exact endpoints and credentials. The
[Antigravity/Pi bridge](antigravity-bridge.md) is its injected-context managed
route. This page records evidence; its installed versions and connectivity
markers are not recommended defaults or substitutes for those setup guides.

## Real bounded generation

Each cloud check requested only a fixed connectivity marker in a disposable
workspace, with no requested tool use and a 45-second timeout. Claude explicitly
disabled tools/MCPs and used a native $0.10 print-mode budget. Antigravity used
an explicit deny-all tool policy. Local Pi checks disabled tools, MCPs and
extensions, selected the exact endpoint/model, and capped output at 32 tokens.
The Ollama check used a 60-second timeout and an owned temporary SSH forward to
the previously authorized host; it changed no server configuration and pulled
no model.

| Harness | Actual provider/model selection | Result | Evidence bound |
| --- | --- | --- | --- |
| Claude Code 2.1.316 | Existing login, native `haiku` alias | Exact marker; 4 native events; 3.5 s | One successful headless turn, no tool events |
| Codex 0.165.0 | Existing login and owner-configured default model | Exact marker; 4 native events; 6.2 s | One successful headless turn, no tool events; resolved model identity was not separately captured |
| Antigravity `agy` | Existing local authentication, `gemini-3.8-flash-low` | Exact marker; 5 native events; 7.1 s | One successful headless turn with tools denied |
| Pi 1.4.0 | Cached `Qwen/Qwen2.5-0.5B-Instruct` through a local OpenAI-compatible fixture | Exact `2`; 16 native events; 456 input / 2 output tokens; 0.97 s | Genuine local model inference, **Transformers 5.15.1 / PyTorch 2.11.0+cu129**, CPU eager attention; not an Ollama/vLLM engine |
| Pi 1.4.0 | Existing Ollama on `dragonsoop`, exact `qwen2.5:14b` | Exact `2`; 16 native events; 456 input / 2 output tokens; 4.4 s | Genuine Ollama inference through its OpenAI-compatible endpoint; no tool events |

The cached local endpoint and the owned SSH forward were stopped after their
checks. Two initial Claude startup attempts rejected an invalid empty MCP
configuration before generation; using the native `{"mcpServers":{}}` schema
resolved the fixture error. No failed startup was counted as a model turn.

## Endpoint and configuration contracts

`TestInstalledCodexCustomEndpointDoesNotFallBackToOwnerLogin` ran the actual
installed Codex with an empty disposable `CODEX_HOME` against an intentional-401
loopback fixture. It observed `/v1/responses` and exactly the explicitly selected
Bearer credential. The rejected response correctly failed the turn. This proves
the per-process native provider parser, endpoint routing and selected-key path;
it is not a substitute generation result.

```sh
AGENTMUX_CODEX_INTEGRATION=1 go test ./internal/harness \
  -run TestInstalledCodexCustomEndpointDoesNotFallBackToOwnerLogin -count=1
```

The regular harness tests verify that native interactive mode retains owner
executable arguments, project, inherited local environment and normal
customizations; it does not add managed safe-mode/no-skills/no-extensions
switches. `native_only: true` adds no Agentmux prompt. Native Antigravity uses
that prompt-free path because no verified appended-system-prompt flag is
available; injected coordination context can use its separately owned Pi bridge.

No live vLLM endpoint was present on the standard local or remote port in this
pass. The generic Pi Chat Completions transport has genuine Transformers and
Ollama evidence above; it should not be presented as a fresh vLLM deployment
test. Earlier engine-specific validation remains separately documented.

Native PTY startup, normal native skills/MCP availability and actual use of those
tools are separate acceptance claims. A no-tools marker does not establish that
an arbitrary owner's tool or plugin works. Provider updates and account/model
availability still require the corresponding live acceptance when changed.

The actual installed Claude, Codex, Pi and Antigravity CLIs were also started
through `BuildInteractive`/`RunInteractive` in separate disposable 120×30 PTYs.
Each emitted native terminal output and remained running through a six-second
startup observation. Claude/Codex/Pi received appended Agentmux context without
an initial user message; Antigravity received no injected prompt. No user prompt
or trust acceptance was submitted. Only those owned process groups were stopped.
This establishes native startup/trust/UI paths, not completed tool/MCP loading
or interactive model-turn semantics.
