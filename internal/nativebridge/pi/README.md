# Pi native delivery adapter

The adapter adds an explicit extension to Pi's normal interactive invocation.
It preserves the owner's arguments, terminal, environment, tools, skills,
extensions, MCP servers and native approval dialogs. `Prepare` creates a private
per-invocation asset; `Run` executes inside the session's existing sandbox.
It does not select RPC mode, rewrite configuration, or type terminal keystrokes.

The adapter requires the maintained `@earendil-works/pi-coding-agent` package
with the `agent_settled` lifecycle boundary (version 1.4.0 or later).
`Run` probes only the known Pi entry point with `--version`, under a bounded
timeout and output limit. Older, unparseable or unavailable versions retain the
exact original terminal invocation with a manual-coordination warning and no
injected extension or native completion claim. Inline interpreter commands,
unrecognized launchers and Pi's administrative subcommands also stay manual.
Ordinary Pi and known Node/script launches support automatic delivery. Owner
arguments after `--` remain prompt data; neither the probe nor bridge flags enter
that prompt. A bridge asset preparation failure likewise preserves the native
terminal. If required editor/trust APIs are missing at runtime, the extension
reports itself unavailable with completion and delivery capabilities disabled.

Delivery uses Pi's `sendMessage` with an attributed custom message and structured
delivery ID. Pi presents it in the conversation and supplies it to the model as
a user-role message. Peer content does not expand slash commands, skills or
prompt templates.

The extension registers a scoped native session through the Agentmux CLI and
reports its readiness. It waits for project trust, approvals, existing native
queues, running work and human drafts. It rechecks readiness after broker round
trips. Native delivery policy and command authority remain broker-enforced.
Changing permission alone cannot start a turn.

`submitted` is an attempted native submission, `running` requires a correlated
Pi event, and `completed` requires successful final settlement. Intermediate
`agent_end` events are insufficient. Only final assistant text is returned;
thinking and tool results are excluded. Replies honor the broker's UTF-8 byte
limit and explicitly identify truncation while retaining the full native answer.
If the human submits input during a peer run, Pi can queue it in the same native
run. The completion receipt then returns an explicit withheld-result notice,
so a later human answer is never automatically shared with the peer. Merely
typing an unsent draft does not suppress the peer result. Native interaction
and the full native answer remain available in either case.

An active peer request can be cancelled through Pi's native abort API. If human
input has intervened, cancellation waits for a safe settlement instead of
aborting the human's work. Completed and uncertain receipts are durably recorded
in the native session. Reload never replays an uncertain submitted turn. Broker
report failures pause delivery and retry receipts without another model turn.
When the broker fences an interrupted attempt, its late outcome is retained
locally as uncertain. The adapter waits for the owner to resolve that attempt,
then admits newly requested work; it never replays the interrupted native turn.

In native interactive workspaces, a broker outage leaves human terminals alive.
The supervisor retries only its owned dead broker under the pinned controller,
with bounded backoff. A live but temporarily unreachable broker is left running.
Explicit workspace shutdown and configured runtime deadlines still stop workers.

## Verification

The default test suite runs model-free extension contract tests through Node:

```sh
go test -race ./internal/nativebridge/pi
```

An installed-Pi test uses a real PTY, isolated profile, fixture broker and an
in-process offline provider. It verifies draft protection, native user-role
delivery, settlement and the returned result:

```sh
AGENTMUX_PI_NATIVE_LIVE=1 go test -race -v ./internal/nativebridge/pi
```

Full acceptance uses a freshly built Agentmux binary, the production broker,
managed launcher, two actual Pi TUI workers and disposable tmux sessions:

```sh
go build -o bin/amx ./cmd/agentmux
python3 internal/nativebridge/pi/testdata/native_broker.py --binary bin/amx
python3 internal/nativebridge/pi/testdata/native_broker.py --binary bin/amx --sandbox bubblewrap
python3 internal/nativebridge/pi/testdata/native_broker.py --binary bin/amx --recovery
python3 internal/nativebridge/pi/testdata/native_broker.py --binary bin/amx --sandbox bubblewrap --recovery
```

Both variants cover Manual, exact Review approval, Auto, draft preservation,
native completion, scoped result return without reverse command authority, and
stable native worker PIDs. Bubblewrap covers the broker and both workers. The
`--recovery` variants kill only the disposable broker during an active turn,
verify task uncertainty and unchanged native worker PIDs, explicitly resolve
the old attempt, and complete new work without replaying the old instruction.
The native-harness preflight requires `network: host`; the fixture sets `PI_OFFLINE`
and implements the provider entirely in-process, making no HTTP/model calls.
`--keep-output` retains private fixture artifacts for debugging. The script
stops only its disposable workspace and never changes the user's sessions or
native profiles.
