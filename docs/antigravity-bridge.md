# Antigravity inference with isolated Pi agents

For an ordinary local Antigravity terminal in an already-running workspace:

```sh
agentmux add --session work --harness antigravity --native-only \
  --project . --title "Antigravity investigation"
```

This uses its normal native configuration with no Agentmux prompt injection.
Use its stable ID to attach or configure manual coordination; native tools and
history stay in that terminal. There is no verified native appended-system-role
flag. The bridge below is the separate option for injected Agentmux context,
isolated Pi tools and managed persistent turns. See [work sessions](work-sessions.md)
and the [dated provider acceptance](provider-acceptance.md) for the precise
native-startup and inference evidence.

Antigravity's native CLI supports trusted-local fresh managed turns. Its native
keyring and private history layout are not a verified independent sandbox
contract. The optional inference bridge keeps that native login in an
owner-controlled host service, while Pi provides each agent's isolated tools
and private persistent conversation. No host home, keyring, DBus socket, or
native Antigravity history is mounted into an agent.

Start an authenticated loopback bridge and keep its key in the controller's
environment. Generate a new private key rather than copying this example text:

```sh
export AGENTMUX_AGY_BRIDGE_KEY="$(python3 -c 'import secrets; print(secrets.token_urlsafe(48))')"
agentmux provider antigravity serve \
  --listen 127.0.0.1:8097 \
  --models gemini-3.8-flash-low \
  --max-concurrent 1 --max-requests 20 --timeout 45s &
bridge_pid=$!
```

This uses the explicitly selected host `agy` executable and its existing login;
it does not log in or change provider settings. Native CLI processes run in a
fresh empty private working directory. Every invocation applies deny rules for
all documented native file, command, URL, unsandboxed-command, and MCP action
namespaces through `--settings`. Native tool events fail the request. The
service is trusted host code and consumes the owner's native CLI configuration;
only use it with a reviewed native installation/configuration. Native provider
history/logging can retain inference prompts on the host, outside agent access.

Set up the isolated client from the same shell, preserving the bridge key:

```sh
agentmux managed setup --spec examples/managed/setup-antigravity-bridge.json --output agy-team.yaml
agentmux managed up agy-team.yaml --broker-sandbox bubblewrap
# When the managed run is finished:
agentmux managed down --state /absolute/path/to/its/state
kill "$bridge_pid"
wait "$bridge_pid"
unset AGENTMUX_AGY_BRIDGE_KEY
```

The setup harness alias `antigravity-bridge` produces a `pi` agent connected to
the supplied bridge API. Its authenticated HTTP key is an inference capability,
not the underlying Google/OAuth credential. The example uses a network namespace
with an exact-origin allowlist and an explicit loopback IP pin. Pi receives
only its private broker credential, selected bridge key, explicit code
dependencies, workspace and session generation. The provider service needs a
separate lifecycle from the managed team; starting/stopping a team does not
silently start a credential-bearing service.

The service exposes authenticated `/v1/models` and `/v1/chat/completions` only.
It rejects non-loopback binds, browser-origin requests, unlisted models,
unsupported roles, excessive bodies, schemas, output and concurrency. The
default allowance is 100 native requests, not a billing guarantee. A request
deadline is at most two minutes. Native failures consume an accepted allowance
and are not retried by the bridge. A calling harness can have its own HTTP retry
behavior; the finite service allowance still bounds native requests. Output
token requests are advisory to native Antigravity; the enforced bridge bound is
128 KiB of response text plus the process deadline. No hard provider token or
currency ceiling is claimed.

Client tools are translated to a strict textual JSON response contract. The
bridge validates the returned function name and arguments against the exact
declared schema and translates them to OpenAI tool-call messages. It never
executes these tools. Pi executes the resulting calls inside its own sandbox.
Supported schemas cover bounded object/array/primitive types, required fields,
additional properties, enums/constants, unions, numeric/string/collection bounds
and RE2-compatible patterns. Unsupported schemas/forced tool choices fail
closed. This is a compatibility layer, not native OpenAI tool calling, native
Antigravity session resume, vision, realtime streaming, or a claim that all
OpenAI API features are implemented. SSE begins after the native turn completes.

Validation on 2026-10-31 used Antigravity 1.4.12 and Pi 1.4.0:

- A native Flash trusted-local turn returned the fixed reply with zero tool
  steps while all documented tool namespaces were denied.
- Exactly two real `gemini-3.8-flash-low` bridge calls passed through separate
  Bubblewrap Pi processes. The second turn recovered the exact private marker
  from the first using the same native Pi session ID.
- Actual isolated Pi against a fake native bridge response executed a validated
  `bash` call only in its sandbox workspace, then returned the tool result. No
  host tool ran in the inference bridge.

Reproducible opt-ins:

```sh
# No provider calls: real Pi/Bubblewrap + fake native process
AGENTMUX_PI_SANDBOX_LIVE=1 go test -race -v ./internal/agybridge -run TestPiSandboxBridge
# Makes at most TWO native Flash calls; requires an existing local agy login
AGENTMUX_AGY_PI_LIVE=1 go test -race -v ./internal/agybridge -run TestAntigravityBridgeRealPiPersistentTwoTurns
```

The native behavior follows the official [headless protocol](https://antigravity.google/docs/cli/headless),
[permission rules](https://antigravity.google/docs/permissions?tab=cli), and
[per-session settings](https://antigravity.google/docs/settings?tab=cli).
