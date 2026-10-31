# Harness adapters

`BuildInteractive` / `RunInteractive` start native Claude, Codex, Pi, or command
interfaces on an inherited tmux PTY. They preserve native prompts and approvals,
append role/boundary context through supported native flags, and keep broker
messages in the inbox. They do not emulate terminal input or run the headless
turn protocol described below. Direct interactive Antigravity role injection
is unverified; use its Pi bridge. See [work sessions](../../docs/work-sessions.md).

`Run` starts one serialized headless turn. It never sends keys to tmux or
claims live steering support. By default each turn starts fresh; an explicit
`Conversation` selects native creation/resume for a supported adapter. The caller serializes
requests per agent and provides a deadline; a ten-minute fallback applies if a
longer or absent deadline would otherwise leave a process unattended.

`Conversation{Mode: "create"|"resume", ID, Dir}` supports Claude Code, Codex,
and Pi. `Capabilities(name)` reports this explicitly; Antigravity and command
harnesses reject persistence instead of pretending they resumed. Claude/Pi
creation can omit ID: `Run` generates one without changing the caller's request;
Codex creation learns its ID from its event stream. `Build` performs no session
creation. A resumed turn requires an exact existing transcript and matching
native ID. The adapter never chooses a most-recent session. Interrupted or
uncertain turns require controller resolution; the adapter does not retry them.

Private `Dir` requires `Wrap` and exposes one `Spec.SessionDirs` mount: Claude's
projects, Codex's sessions, or Pi's sessions directory in an otherwise fresh
private home. Authentication remains in separately selected read-only files.
Managed workers bind this to one agent and generation under the private state.
An empty `Dir` is standalone trusted-local compatibility using native host
session storage/authentication, without an isolation claim. Transcripts may
contain prompts, files and tool output; they are sensitive data. New role and
instruction provenance still arrive as a new input on every turn.

`Request.Command` is owner-controlled argv. For native adapters it overrides the
executable and an optional argv prefix (for example, a Node interpreter and Pi
entrypoint); generated adapter arguments follow that prefix. For `command` it is
the entire argv. No shell parses prompts, model IDs, or arguments. Configuration
must never allow an agent to replace its executable or sandbox wrapper.

Codex defaults to its own `workspace-write` command sandbox. An explicit
`Request.ExternalSandbox` renders `--sandbox danger-full-access` and requires a
caller-provided `Wrap`; a failed/missing wrapper prevents execution. The library
cannot prove an arbitrary wrapper is a security boundary. Managed mode permits
this choice only for a Codex worker with enforced Bubblewrap isolation and
never selects it automatically after a native sandbox failure. Native approval
handling is not bypassed globally, and no host policy is changed. This supports
hosts that disallow nested user namespaces while retaining the outer boundary.

| Harness | Input / completion | Endpoint and authentication |
| --- | --- | --- |
| Claude | One JSONL user message in safe mode; structured `result` is required | `BaseURL` maps to `ANTHROPIC_BASE_URL` and requires the Anthropic messages protocol; `APIKeyEnv` maps to `ANTHROPIC_API_KEY` |
| Codex | `exec --json` reads stdin; `turn.completed` is required | Named key maps to `OPENAI_API_KEY`; generic base URL overrides are rejected |
| Antigravity | One JSONL `user` event; terminal `result.status` must be `SUCCESS` | Uses the locally configured `agy` authentication; generic endpoint/key overrides are rejected |
| Pi | JSONL RPC state request and prompt; wait for `agent_settled`, not `agent_end` | An optional OpenAI-compatible API base uses a private `models.json`; named key stays in an environment variable |
| Command | Literal stdin and argv; process exit | The owner supplies all command-specific configuration |

`Result.Text` is final only when `Run` returns no error. Claude must emit its own
successful result string; Codex selects the last completed agent-message item
before its terminal event; Pi requires an accepted `started` prompt, a completed
assistant response and `agent_settled`. Explicitly emitted empty final strings
remain valid; missing/null/malformed answers cannot reuse earlier commentary.
A Pi `handled` or `queued` acknowledgement is receipt, not completion of this
adapter's one requested model turn, and returns `ErrIncomplete`. A dangling
tool-use response or pending assistant message cannot establish a final answer.

Native session IDs are pinned within the invocation. Claude subagent messages
remain in `Events` without replacing the main answer or session identity.
Contradictory/duplicate terminals and later answer mutations return
`ErrProtocol`, while unrelated informational records remain available. A full
stdin delivery error also fails the invocation even if a wrapper emits a
successful result and exits zero. These checks establish native completion and
input delivery, not that a model actually satisfied the task; owner verification
is separate.

Codex exec currently drops app-server's `will_retry` flag when translating error
notifications into its JSONL stream. Although the SDK event comment calls a
top-level `error` fatal, the native mapping also accepts notifications carrying
retry state. For compatibility, a later explicit `turn.completed` plus successful
process exit resolves preceding errors; all original error records remain in
`Events`. Error-only/incomplete streams, errors after completion and a nonzero
process exit fail. The adapter does not resubmit or retry the task itself. See
the native [JSONL mapper](https://github.com/openai/codex/blob/main/codex-rs/exec/src/event_processor_with_jsonl_output.rs)
and [error notification contract](https://github.com/openai/codex/blob/main/codex-rs/app-server-protocol/src/protocol/v2/notification.rs).

For Pi against Ollama use an API base such as `http://127.0.0.1:11434/v1`;
against vLLM use its `/v1` API base. URLs are used exactly as configured and do
not refer to Ollama's separate `/api/generate` protocol. `Request.ModelMetadata`
accepts bounded context/output, text/image inputs, and explicit reasoning/tool
declarations. A declared `Tools: false` adds `--no-tools`; capability metadata
grants no filesystem or broker permissions. Missing metadata uses conservative
text-only, 8,192 context/2,048 output defaults (reduced for smaller contexts).
Setup performs bounded local endpoint discovery; this process adapter never
contacts metadata endpoints automatically. See [managed setup](../../docs/managed-setup.md).
No model weights are fetched by this package.

Pi endpoint configuration lives in a private temporary agent directory, with no
plaintext key in the file, and is removed after every attempt, including errors.
`Spec.HomeFiles` exposes just `models.json` to `Request.Wrap`, the sandbox hook.
The Bubblewrap wrapper mounts it read-only into a writable private agent
directory: Pi creates its credential store when a prompt starts, so mounting
the whole directory read-only is not a valid runtime setup.
Automatic Pi catalog activity is disabled for these endpoint-backed turns.

The child environment contains only runtime path/locale settings, explicit
owner-provided `Env`, and the credential named by `APIKeyEnv`. The caller must
use a real sandbox for filesystem/network boundaries; the process backend alone
can still read host files, including native CLI login/config files. Native
configuration and workspace instructions can affect a harness; strict sandbox
images should provide only the intended configuration and credentials. Claude
uses its verified `--safe-mode` to disable ambient project instructions, plugins,
hooks, skills, MCP servers, and other customizations while preserving native
authentication and permissions; managed policy still applies.

Stdout is limited to 16 MiB, one event to 2 MiB, events to 10,000, and retained
stderr to 64 KiB. Events, partial text, and reported usage survive errors.
`Request.OnEvent` receives a separate copy of each bounded JSON-object stdout
frame after it enters the partial result, before the next frame is read. This
includes a frame that causes a semantic protocol rejection, preserving the
decisive failure evidence. Invalid JSON and nonobject framing are excluded from
the structured callback and reported as errors. A recording error cancels the
process rather than silently losing trace events; simultaneous recording and
protocol failures are both retained in the returned error.
Plain command output remains a final `Result.Text`, not a structured stream.
`AfterProcess` runs once after the child has been waited for, before native
transcript verification. A failed private checkpoint fails the turn, preserving
any original process error. Neither callback runs during `Build`.
Structured terminal failures fail even if a CLI exits zero. Claude permission
denials and observed Antigravity soft denials return `ErrToolDenied` with partial
results; this conservatively marks work as incomplete rather than silently
claiming every requested action succeeded. On Unix cancellation kills the whole
process group. Other platforms rely on the sandbox backend for descendant
cleanup.

Pi usage accumulation preserves missing/null counters as unknown rather than
inventing zero values. Known numeric observations must be finite, nonnegative,
and integer-valued for token counts; invalid/overflowing observations fail the
turn without erasing earlier valid usage. Provider usage remains observational,
not an independent billing ledger or a spending guarantee. The aggregate sums
reported observations: earlier known counters remain when a later response
omits them. It can therefore be a partial sum, not a known whole-turn total.

`AGENTMUX_PI_LIVE=1 go test -v ./internal/harness -run
TestPiCLIWithCompatibleEndpoint` checks a real installed Pi CLI against a local
fake OpenAI endpoint. This verifies endpoint configuration, input framing,
usage, and final RPC completion without model downloads or paid calls. It was
verified with the maintained `@earendil-works/pi-coding-agent` package, version
1.4.0. The older `@mariozechner` package is deprecated and lacks the current
`agent_settled` completion event.

`AGENTMUX_PI_SANDBOX_LIVE=1 go test -race ./internal/sandbox -run
TestPiSandboxRPCPromptUsesWritablePrivateAgentDirectory` checks the actual
installed Pi against a local fake endpoint inside real Bubblewrap. It covers
prompt-time credential-store writes, full RPC completion, and configuration
cleanup without provider calls.

Protocols verified against these primary references:

- [Claude CLI](https://code.claude.com/docs/en/cli-reference),
  [gateway endpoint variables](https://code.claude.com/docs/en/llm-gateway-connect),
  also checked against local Claude Code 2.1.316 help.
- [Antigravity headless protocol](https://antigravity.google/docs/cli/headless/),
  also checked against local `agy` 1.4.12 help.
- [Codex noninteractive execution](https://developers.openai.com/codex/noninteractive),
  also checked against locally installed `codex exec --help`.
- [Pi RPC](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md),
  [JSON events](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/json.md),
  [endpoint configuration](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/models.md),
  [agent directory](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/configuration.md).
