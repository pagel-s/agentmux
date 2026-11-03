# Managed teams and durable coordination

Agentmux now has an opt-in managed mode alongside the existing tmux workspace mode. Managed mode gives each logical agent a separate credential, enforces message and memory permissions in a coordination service, and stores durable tasks in SQLite. It is a developer-first foundation for mixed-harness teams.

The existing `load`, `render`, pane launchers, and v1 workspace YAML remain available. Managed configuration uses `version: 2` and `mode: managed`; use `amx managed up` for automatic startup, or `managed init` for manual service control. Do not pass it to `amx load`.

For a single coding request with fresh reviews, executable checks and a retained
patch, use [`amx work`](working.md). For owner-authored comparisons, missions
and handoffs, use [`amx flow`](workflows.md). Those journeys have separate plans,
result storage and isolation settings; starting a managed team does not enable
workflow isolation.

## Implemented capabilities

- Authenticated operations over a private Unix socket; agent identity comes from its credential rather than a caller-supplied name.
- Shared boards with exact read/write ACLs, direct messages, stored pings, and separately authorized instructions.
- Durable instruction tasks, versioned state changes, instance-bound leases, uncertain-delivery recovery, and explicit owner verification.
- Private per-agent memory and named shared memory, with version checks on updates.
- Serialized headless turns through Claude Code, Codex, Antigravity, Pi, or an owner-configured command; optional private persistent conversations for Claude, Codex and Pi in Bubblewrap.
- Optional bubblewrap worker and broker isolation that fails closed when unavailable, plus explicitly trusted local execution.
- One-command tmux startup, preflight diagnostics, an owner workbench, ordered shutdown, and explicit restart.
- Bounded spawning from owner-approved sandbox templates and consent-based dynamic teams with private team boards/memory.
- Audience-checked reply threads, live owner role updates, independently removable collaboration groups, and versioned admission controls.
- Bounded local event traces and portable bundles verified against an explicitly pinned owner signing key.

Agents can run as native interactive terminals (`interaction: interactive`) or serialized managed task workers (the default). Human input and native approvals stay in tmux; broker inbox messages never inject keystrokes. See [work sessions](work-sessions.md).

Interactive `native_only: true` sessions require an empty role and omit Agentmux prompt injection. They retain broker coordination access, but tasks and role-change notices never become native terminal input. Managed-only conversation leases, automatic wakeups, traces, disk capsules and allowlist egress are rejected for interactive agents.

[Persistent conversations and bounded wakeups](autonomous-workflows.md) support multi-turn workflows from one goal. Notes and pings are observations unless an explicitly authorized `instruct` creates a command task. Configured subscriptions can wake workers without injecting text into an interactive terminal. The finite [pilot runner](experiments.md) and [repeated-round research environment](research.md) support controlled comparisons separately from tmux supervision. Coordination history and native transcripts are useful evidence, but do not constitute a complete immutable trace of every model-visible input and tool execution.

## Try it without a model

Build the executable using the repository's normal build flow, or run `go build -o bin/agentmux ./cmd/agentmux`. These examples use `./bin/amx` from the repository root. No provider credentials or model calls are needed for the `/bin/cat` demo.

Check setup, then start the complete team detached:

```bash
./bin/amx managed doctor examples/managed/command-demo.yaml
./bin/amx managed up examples/managed/command-demo.yaml --state .agentmux-state/demo
```

`up` returns a dedicated tmux attach command. Add `--attach` to open it immediately, or run `managed status --state .agentmux-state/demo --watch` for a live terminal summary. The broker, supervisor, status view, and each worker have separate named windows. Submit an instruction using the lead's credential:

```bash
./bin/amx managed call instruct \
  --credential-file .agentmux-state/demo/credentials/lead.json \
  --request-id demo-instruction-1 \
  --json '{"to":["worker"],"text":"Echo this task input so I can inspect the handoff."}'


./bin/amx managed call task.list \
  --credential-file .agentmux-state/demo/owner.json \
  --json '{}'
```

The command harness receives the worker's prepared input on stdin, and `/bin/cat` returns that text as its result. The task becomes `submitted`; a successful process exit does not independently verify its work. Copy the task's `id` and current `version` from `task.list` to verify it explicitly:

```bash
./bin/amx managed call task.verify \
  --credential-file .agentmux-state/demo/owner.json \
  --json '{"id":"TASK_ID","version":CURRENT_VERSION}'

./bin/amx managed status --state .agentmux-state/demo
```

Replace `TASK_ID` and `CURRENT_VERSION` with the returned values before running verification. Workers started by `up` consume queued tasks automatically. Their windows show managed worker processes executing headless turns; persistent mode resumes an explicit native conversation between tasks. Inspect published results through the broker API.

Stop the run without deleting its tasks or memory:

```bash
./bin/amx managed down --state .agentmux-state/demo
# Explicitly start another runtime attempt using saved configuration:
./bin/amx managed restart --state .agentmux-state/demo
```

The example permits `lead` to instruct `worker` and lets the worker send notes or pings back. A worker attempting to instruct the lead receives `forbidden`, even if its role text describes it as a coordinator.

## CLI contract

```text
amx managed up <config> [--state <directory>] [--broker-sandbox local|bubblewrap] [--attach]
amx managed down --state <directory>
amx managed restart --state <directory> [--broker-sandbox local|bubblewrap] [--attach]
amx managed doctor <config> [--state <directory>] [--broker-sandbox local|bubblewrap]
amx managed init <config> --state <directory>
amx managed serve --state <directory> [--sandbox local|bubblewrap]
amx managed call <operation> [--credential-file <file>] [--socket <path>]
    [--json <object> | --stdin] [--request-id <id>]
amx managed worker --state <directory> --agent <id> [--once]
amx managed status --state <directory> [--watch]
amx managed activity --state <directory> [--watch]
amx managed rotate --state <directory> --agent <id|'$owner'>
```

Flags can appear before or after positional arguments. `call` uses `{}` when no arguments are supplied. `--stdin` reads one JSON object with a bounded size. `AGENTMUX_CREDENTIAL_FILE` can supply the credential path, and `AGENTMUX_SOCKET` can override its socket path. An explicit flag takes precedence.

Command results and failures use a JSON envelope with `schema_version`, `kind`, `operation`, `ok`, and either `result` or `error`. API calls also report `request_id`. Installation tokens and credential contents are never printed. The service startup notice goes to stderr; `serve` stays active until interrupted. `status --watch` and `up --attach` deliberately use interactive terminal output.

The CLI generates a random request ID when one is omitted. Within the same authenticated credential instance, retrying a write with the same ID and arguments retrieves its durable result. The cache is scoped to both logical actor and credential instance, so rotation cannot replay an old instance's lease receipt. Reusing an ID for different arguments fails with `conflict`. A transport failure can mean that a write committed but its response was lost: retain the reported request ID and retry that exact call, rather than submitting a new instruction.

## Lifecycle, diagnostics, and tmux ownership

`up` runs preflight before creating managed state or starting processes. It checks configured binaries and supported flags, named credential variables, dependencies, local endpoint metadata, and requested sandbox support without generating model output. Cloud authentication or dependencies that cannot be verified are reported as warnings rather than represented as tested. `doctor` exposes the same report without starting a run.

When `--state` is omitted, `up` uses `.agentmux-state/SESSION` under the current directory. Choose a short explicit path if the Unix socket path would exceed 100 bytes. An existing state directory must contain identical saved configuration; use a different directory for a changed experiment or team definition.

Each run uses its own private `STATE/tmux.sock`, with an attempt ID on the session and tracked panes. It does not connect to the user's default tmux server. A supervisor watches the authenticated agent registry and adds windows for newly spawned agents. Worker or broker crashes are observed rather than automatically restarted. The owner workbench shows the roster, native terminal previews, coordination content, tasks, shared memory, and live permission controls. It is a content-sensitive owner surface; agents must not receive its credential. For a metadata summary, use `managed status --watch`.

Use `managed activity --state DIRECTORY` for a content-sensitive owner JSON snapshot of recent messages, task results/failures, events, and reported usage. Add `--watch` for a bounded terminal view; model and message text is quoted to escape terminal controls and Unicode direction controls. This view always uses the state's owner credential, includes private coordination content, and is not an agent-facing API. The default workbench also uses the owner credential and displays coordination content. See [interactive work sessions](work-sessions.md) for its shortcuts and native terminal workflow.

`down` interrupts owned worker processes first, waits for their terminal processes to exit, and then stops the broker and supervisor. It removes only the attempt's tracked session. If termination or ownership cannot be confirmed, it reports attention and leaves the broker available for inspection. A stale stop request cannot stop a new attempt. Durable tasks and memory remain on disk. `restart` performs an explicit ordered stop/start; it does not silently retry uncertain deliveries or reset the persisted runtime budget. After a run's budget expires, start a new state directory for a new run.

For one exited workspace-owned interactive session, use `amx relaunch AGENT_ID --state DIRECTORY`. Healthy peers keep running and the stable agent identity is retained. The default starts a fresh native conversation; `--resume last`, `--resume picker`, or an explicit native ID selects history through the supported harness. A live or revoked session cannot be relaunched, and no deadline is renewed. Older running controllers require one deliberate workspace restart before individual relaunch is available. See [recovery](traces-and-recovery.md) for the distinction from retrying a broker task.

The lower-level commands remain useful without automatic tmux management:

```bash
./bin/amx managed init examples/managed/command-demo.yaml --state .agentmux-state/manual
# One shell:
./bin/amx managed serve --state .agentmux-state/manual
# Another shell:
./bin/amx managed worker --state .agentmux-state/manual --agent worker
```

For a manual worker, `--once` handles one queued task or exits if none is waiting. Do not launch a second manual worker for an agent already supervised by `up`; its exclusive lock rejects duplicates.

## Boards, messages, instructions, and memory

| Operation | Typical arguments | Authority |
| --- | --- | --- |
| `identity` | `{}` | Any authenticated actor |
| `board.post` | `board`, `text` | Board writers |
| `message.list` | Optional `board`, `after`, `limit` | Only messages visible to the caller |
| `message.send` | `to` array, `text` | `message.send` grant for every recipient |
| `ping.send` | `to` array, `text` | `ping.send` grant for every recipient |
| `instruct` | One recipient in `to`, `text` | Separate `instruct` grant; creates a task |
| `task.list` / `task.get` | Optional state / task `id` | Sender, recipient, or owner |
| `task.cancel` | Task `id`, current `version` | Sender or owner |
| `task.verify` | Task `id`, current `version` | Owner, after submission |
| `agent.role` | Agent `id`, `role`, current role `version` | Owner only |
| `capacity.get` / `budget.status` | Optional `agent` | Own capacity/usage, or owner inspection |
| `capacity.update` | Current resource `version`, budget/deadline fields | Owner only |
| `memory.get` | `namespace`, `key` | Namespace readers |
| `memory.list` | `namespace` | Namespace readers |
| `memory.put` | `namespace`, `key`, `value`, `version` | Namespace writers |
| `events` / `status` | `{}` or optional event limit | Owner only |

For example, publish a board note and create private memory:

```bash
export AGENTMUX_CREDENTIAL_FILE="$PWD/.agentmux-state/demo/credentials/lead.json"

./bin/amx managed call board.post \
  --json '{"board":"team","text":"Please post evidence with each result."}'

./bin/amx managed call memory.put \
  --json '{"namespace":"private","key":"next_step","value":"Review the worker result","version":0}'

./bin/amx managed call memory.put \
  --json '{"namespace":"project","key":"decision","value":"Require owner verification","version":0}'
```

`private` always means the authenticated agent's private namespace. Named shared namespaces use their configured ACLs. Version `0` creates an entry; subsequent updates require its current version. Memory values are strings; applications can store structured JSON as a string if needed. The broker owner remains trusted with access to the state directory, even though ordinary API access to private memory uses an agent credential.

Roles describe responsibilities. They grant no authority. Policy defaults to deny; explicit grants control messaging and instruction relationships, while board and memory ACLs are configured separately. A literal `"*"` covers configured agents only. It does not create identities or authorize future operation types.

`agent.role` updates a role with its own `role_version` compare-and-swap check. Roles accept up to 16384 UTF-8 bytes without Unicode control characters except newlines/tabs; an empty role clears it. Managed workers capture the current role at the next task claim; in-flight input stays unchanged. Native sessions receive an inbox notice and can read the current role through `identity`; no keystrokes are injected. A nonempty role is rejected for `native_only` sessions.

`board.post`, `message.send`, `ping.send` and `instruct` accept optional `reply_to` for a visible message or instruction task. The broker derives `thread_id`; callers cannot choose a root. A board reply retains the original board, while each direct recipient must already be able to read the referenced message. A reply adds no authority or ancestor access. `task.list` supports `state`, `to`, `query`, `order`, `after` and `limit`, with a returned `has_more` flag; use `task.get` for an exact task. Native provider-only chat turns are outside this broker history.

Owner collaboration groups, explicit overrides and consented teams remain separate permission sources. See [live permissions](live-permissions.md) for overlapping groups, isolation overlays, explicit legacy-state review, reset semantics and live admission edits. [Memory lifecycle](memory-lifecycle.md) covers retention, retrieval and archive provenance.

## Bounded spawning and dynamic teams

[The bounded team example](https://github.com/pagel-s/agentmux/blob/main/examples/managed/bounded-teams.yaml) enables these features explicitly. It uses command harnesses, so inspecting the API makes no model calls. The command fixtures echo their instructions; they do not decide to spawn or cooperate by themselves.

```bash
./bin/amx managed up examples/managed/bounded-teams.yaml --state .agentmux-state/teams
./bin/amx managed call agent.spawn \
  --credential-file .agentmux-state/teams/credentials/lead.json \
  --json '{"template":"helper","capabilities":{"boards_read":["team"],"memory_read":["findings"],"join_teams":true}}'
```

The response contains a new agent ID, not its credential. The supervisor creates its worker window. Its immutable template chooses the harness, argv, network, and dependency mounts; the child receives a new private workspace and a separate bubblewrap sandbox. An agent cannot provide an arbitrary executable, mount, credential, or working directory in a spawn request. Native model templates need explicit provider networking and credentials; the example helper uses `network: none`.

Children receive only the requested capabilities that the parent may delegate. Static wildcard ACLs are not inherited. The parent gets a control edge to the child; the child gets a note/ping reply edge and cannot thereby command its parent. Delegable scopes include board/memory reads and writes, target-specific messaging/instruction edges, approved further spawn templates, and team creation. A child needs explicit `join_teams: true` to accept invitations, and its parent must currently belong to that team. Lifetime spawn count, depth, concurrent tasks, and the persisted run deadline bound expansion. Creating a new child does not renew the run's time budget.

Use `agent.list` with the owner credential to inspect registry metadata. `agent.stop` with `{"id":"AGENT_ID"}` revokes an agent and its descendants' credentials; the supervisor then interrupts their workers. Revocation itself returns `process_stopped: false` because authorization withdrawal is distinct from process termination. Exited workers are not automatically restarted.

Revocation fences tasks addressed to the revoked agents. Previously accepted
instructions they issued to other active agents remain durable; cancel those
tasks explicitly with `task.cancel` when withdrawing the work itself.

Team creation is separately enabled and restricted to configured creators or explicitly delegated child capabilities:

```bash
./bin/amx managed call team.create \
  --credential-file .agentmux-state/teams/credentials/lead.json \
  --json '{"name":"Review group"}'
```

Use the returned team ID and version for `team.invite` (`id`, `agent`, `version`). The invitee must accept through its own credential using `team.join` (`id`, `version`); membership is not silently forced. `team.list` supplies current versions. Members receive the team's dedicated board and shared-memory namespace named in its response. Team membership does not grant instruction authority or access to unrelated namespaces. Board history defaults to messages written since joining; `teams.history: all` explicitly enables earlier history. Shared memory contains current values rather than a history of messages. Leaving removes future access but cannot erase information already read. `team.remove`, `team.leave`, and owner/creator `team.dissolve` update versioned membership. Lifetime team and member counts are bounded. See the [dynamic-team guide](dynamic-teams.md) for complete commands and consent rules.

Managers can change team name/description and propose ownership transfer. Only the proposed owner can accept, under current delegated authority. `team.move` is a non-owner member's own consent to join a pending destination invitation and leave the source atomically; it requires both current team versions. `team.list` gives participants their own `self_access` explanation when ancestry or other current requirements block access.

## Model harnesses and local endpoints

| Harness | Managed transport | Endpoint support |
| --- | --- | --- |
| Claude Code | Streaming JSON print mode | Existing login, or an Anthropic-compatible base URL and credential environment variable |
| Codex | `exec --json` | Existing Codex authentication, or an explicitly configured custom Responses endpoint and selected API-key environment |
| Antigravity | Streaming JSON input/output | Existing local `agy` authentication; arbitrary endpoint overrides are rejected |
| Pi | JSONL RPC | Existing Pi configuration, or a private OpenAI-compatible chat-completions connection |
| Command | Literal owner-configured argv; prompt on stdin | Defined by the command |

The local setup was checked with Claude Code **2.1.316** and Pi **1.4.0** from the maintained npm package **`@earendil-works/pi-coding-agent`**. Installation does not imply login or account access. The deprecated `@mariozechner/pi-coding-agent` package is not the current package. Harness versions and supported flags should be checked when upgrading.

The examples include:

- [Mixed Claude/Codex/Antigravity/Pi team](https://github.com/pagel-s/agentmux/blob/main/examples/managed/mixed-team.yaml).
- [Claude coder and Codex reviewer](https://github.com/pagel-s/agentmux/blob/main/examples/managed/claude-codex.yaml), with an explicit Claude tool allowlist and Git workspace setup.
- [Pi with local Ollama](https://github.com/pagel-s/agentmux/blob/main/examples/managed/pi-ollama.yaml), using exactly `http://127.0.0.1:11434/v1`.
- [Pi with vLLM](https://github.com/pagel-s/agentmux/blob/main/examples/managed/pi-vllm.yaml), using exactly `http://127.0.0.1:8000/v1`.

Replace each model placeholder with a model already served by that endpoint. For an unauthenticated vLLM server, remove the example's `api_key_env` line. For an authenticated server, export the named variable in the worker's environment. Keep actual credentials out of YAML, argv, role text, and task messages.

Codex custom connections select `protocol: openai-responses` and the owner's
explicit `base_url`. The adapter supplies a per-process native model-provider
definition with `wire_api: responses`, disables implicit OpenAI login for that
provider, and uses only the selected API-key environment when configured. It
does not edit the owner's Codex files. This follows the [official custom-provider
configuration](https://learn.chatgpt.com/docs/config-file/config-advanced#custom-model-providers).
Chat Completions endpoints, including many Ollama/vLLM deployments, use Pi's
`openai-compatible`/`ollama` connection instead. A successful metadata probe or
protocol fixture is distinct from a successful real model turn.

Claude managed turns retain `--safe-mode` and `--permission-mode dontAsk`. Configure an explicit owner-approved `--allowedTools` prefix for coding tools, as shown in the Claude/Codex example; a role description does not grant native permissions. Tool denials fail the task rather than count as successful submission. Codex `exec` requires its working directory to be a Git repository with the current adapter. Default managed workspaces start empty, so initialize the example's state and run `git init` in its workspaces before `managed up`. Startup queues no tasks and makes no model-generation call; submit a bounded instruction explicitly when ready.

The adapter uses the configured API base exactly; it does not append `/v1` or switch to Ollama's `/api/generate`. See the official [Ollama compatibility documentation](https://docs.ollama.com/api/openai-compatibility) and [vLLM server documentation](https://docs.vllm.ai/en/latest/serving/openai_compatible_server/). Pi receives a private temporary `models.json`, rather than changes to the user's configuration. Setup and doctor perform bounded metadata discovery for explicitly configured local/private endpoints, including exact model records and Ollama `/api/show` metadata. Explicit `model_metadata` fields override discovery; setup saves the selection, while doctor leaves hand-authored YAML unchanged. Missing fields use conservative defaults, not a claim about the engine's actual capacity. See [compatible endpoint metadata](managed-setup.md#compatible-endpoint-metadata) for context/output limits, capability declarations and validation scope.

## Isolation and state ownership

`sandbox: local` runs trusted processes as the current OS user. Separate workdirs and API credentials provide organization, but do not stop such a process reading host files or another agent's credentials. Use this mode for development and trusted workflows. API ACLs are meaningful only while the caller cannot bypass the API through host access.

`sandbox: bubblewrap` requests kernel-enforced worker isolation. It must not fall back to local execution when the executable, user namespaces, mounts, or network policy are unavailable. `network: none` isolates networking; `network: host` deliberately allows unrestricted host networking. `network: allowlist` supports exact approved HTTP/HTTPS origins through a private proxy and namespace-local relay, including explicitly approved local inference endpoints. Clients must support the supplied proxy settings; incompatible clients fail to connect without a host-network fallback. See [exact network destinations](hardening.md#exact-network-destinations) for configuration, DNS/IP rules and protocol limits. A real namespace probe passed on the implementation host after running outside the enclosing development sandbox. Validate the complete isolation setup on each deployment machine before relying on it.

The broker is a separate process with a private state directory. Use `amx managed serve --state DIRECTORY --sandbox bubblewrap` to isolate the broker with a minimal environment, no network namespace access, its private state mounted for persistence, and the Agentmux binary mounted read-only. It fails when the requested kernel isolation is unavailable. Without that flag, the broker runs as a normal trusted host process. Do not expose its complete state directory, owner credential, host tmux socket, container-management socket, or peer home directories to an isolated agent. A Node-based harness may need an explicit Node executable/CLI path in `command` and narrowly selected package dependencies in `read_only_paths`; do not mount the whole host home merely to make a wrapper work.

Initialization creates private default workdirs beneath the state directory for agents without `workdir`. Explicit relative workdirs and `read_only_paths` resolve against the configuration file's directory. The state contains SQLite data and credential files; treat it as sensitive and keep it outside version control. `.agentmux-state/` is ignored by this repository. Restart the service with the same directory to retain tasks and memory; initialization refuses to replace existing nonempty state.

### Selected native login files

Native subscription logins can be explicitly provisioned to Bubblewrap workers:

```yaml
agents:
  coder:
    harness: claude
    command: [claude, --allowedTools, 'Read,Write,Edit,Bash']
    sandbox: bubblewrap
    network: host
    auth:
      file: ~/.claude/.credentials.json
    resources:
      memory_bytes: 2147483648
      cpu_percent: 100
      processes: 256
      open_files: 1024
```

`auth.file` is an explicit owner choice, not automatic home-directory discovery. Claude selects only its OAuth credential object. Codex selects only the authentication fields from `~/.codex/auth.json`. Pi additionally requires `auth.provider` and selects that one provider from a Pi-native `auth.json`; command-backed keys and credential environment overrides are excluded. Other native settings, histories, hooks, plugins, and provider entries are not copied. Pi subscription credentials are not translated from another harness's format.

The original must be a owned private regular file (`0600` or `0400`) without hardlinks or symlink components. Originals must stay outside every configured agent workspace, declared dependency mount, and default shared runtime mount. Relative paths resolve against the team YAML; `~/` expands in CLI configuration loading. Native auth cannot be combined with a connection credential or environment overrides that change its location or login method.

Each worker creates a filtered private temporary snapshot and mounts it read-only at its harness's fixed location inside the private home. The model can read its own credential; this feature separates workers and does not hide credentials from their owning model. Host settings and original credential files are not mounted. Snapshot values never enter normalized configuration or status. Exact known values are also redacted from worker-published results and usage on a best-effort basis.

Managed workers reload and replace their filtered snapshot between turns, before claiming new work. Refresh or sign in through the selected owner-side source; a healthy worker sees its next valid contents without a restart. Interactive sessions retain their prepared snapshot for that native process, so relaunch them after a source change when needed. Read-only snapshots cannot write a token refresh back to the source. Normal and handled-error cleanup removes snapshots; a hard kill or host crash can leave a private temporary directory for owner cleanup. An unavailable source prevents new managed claims rather than falling back to host access. `managed doctor` checks native readiness inside the actual filesystem boundary when initialized state and its broker socket are available, with provider networking disabled.

### Enforced worker resources

All resource fields are optional and require `sandbox: bubblewrap`. `memory_bytes`, `cpu_percent`, and `processes` use a transient systemd user cgroup for the entire turn's process tree. A CPU value of `100` allows one CPU's time; process counts include threads. Memory limits also disable swap for the group and group OOM termination prevents partial survivors. These controls require Linux cgroup v2, delegated controllers, and a recent reachable systemd user manager (including scope OOM policy and literal environment handling).

`open_files`, `cpu_seconds`, and `address_space_bytes` use inherited per-process `prlimit` bounds. CPU seconds are not an aggregate tree budget. Address-space limits default to unset because Go and Node/V8 reserve large virtual ranges. Values are validated, actual cgroup controls are checked before the sandbox starts, and unavailable enforcement fails without an unlimited fallback. The host user bus and cgroup filesystem are not mounted into the agent.

These are per-turn execution limits. They do not bound an external inference server, provider token charges, or a parent's aggregate spending across children. Provider networking with `network: host` remains unrestricted. Use `network: allowlist` with explicit `egress.allow` origins for the supported destination boundary; encrypted URL paths are outside that boundary. See [hardening](hardening.md) for network and aggregate admission controls.

Reproduce the real kernel resource tests with `make test-resources` on a supported host. They include aggregate multi-process memory exhaustion and termination of a descendant that creates a separate session.

### Codex inside an outer sandbox

Codex defaults to its native `workspace-write` command sandbox. Some hosts block nested user namespaces, even though Agentmux's outer Bubblewrap sandbox works. An owner may explicitly select `codex_sandbox: external` for a Codex agent already configured with `sandbox: bubblewrap`. The managed worker enforces that outer boundary and requires it to start successfully before execution. Local execution and other harnesses reject this setting; native failures never select it automatically.

In this mode the adapter passes Codex `--sandbox danger-full-access`: the accessible filesystem is the isolated Bubblewrap filesystem, including its own writable workspace and private home, while host state and peer credentials remain absent. It does not pass the approval-bypass flag or change host policy. This is an explicit execution choice for an externally isolated runner, as described in the [official CLI reference](https://learn.chatgpt.com/docs/cli/reference). The lower-level harness library remains a trusted process API; an arbitrary caller-supplied wrapper function alone is not proof of isolation.

## Recovery, cancellation, and bounds

Task acceptance, dispatch, process completion, and verification are separate facts. Workers serialize turns per logical agent. Completion requires a valid instance-bound lease and the expected task version; credential rotation fences old instances. Rotate an agent credential with its ID, or quote the reserved owner identity:

```bash
./bin/amx managed rotate --state .agentmux-state/demo --agent worker
./bin/amx managed rotate --state .agentmux-state/demo --agent '$owner'
```

The worker should be stopped and restarted after rotation. The owner path is separate from the agent namespace, so an agent named `owner` remains a normal agent.

On broker restart, a turn that may already have reached a harness becomes uncertain; it is not silently resent. The owner can inspect the task and use `task.resolve` with its current `version` and `resolution: "retry"` or `"fail"`. Before retrying uncertain work, stop any old worker/harness process and assess partial side effects. Cancellation similarly distinguishes a request to stop from confirmation that the process tree stopped; cancelled work must not become automatically retryable after a restart.

Configured limits bound message sizes, stored work, runtime, and durable storage/request counts. The defaults include 30000 durable records and 50000 cached requests. Capacity is reserved for active task completion, cancellation, stopped-process acknowledgements, agent revocation, and team membership cleanup, so ordinary writes cannot consume the slots needed to finish those transitions. They are not guaranteed provider-spend caps. Harness token and cost reports are observations with provider-specific coverage. API list/response limits can require narrower pages. When storage limits are reached, the owner should export/archive state or start a new session; the runtime does not silently delete the request records required for reliable retries.

## Validation scope

The acceptances below describe their recorded fixtures. Later provider and UAT
checks are tracked separately; their versions and networking choices are not
requirements or capability limits for every current deployment.

The real three-sandbox handoff smoke test passed: a network-isolated broker and two separately isolated agents exchanged board messages and shared memory, enforced instruction hierarchy, hid peer/owner credentials, and produced two owner-verified task results. Reproduce it on a Linux host with working bubblewrap and a built binary:

```bash
python3 scripts/smoke-managed.py --binary bin/agentmux
```

The supervised dynamic-team smoke test also passed with a sandboxed broker, two sandboxed static agents, and a separately sandboxed spawned worker. It checks delegated board and memory access, task execution, invitation consent, stale-version conflicts, since-join history, nonmember denials, removal/leave/dissolution, the lifetime spawn limit, observed worker termination after revocation, and restart without renewing the deadline or replaying submitted work:

```bash
python3 scripts/smoke-managed-team.py --binary bin/agentmux
```

Automated coverage uses fake harness processes and local transports so protocol failures, permission denials, task handoffs, and recovery can be checked without paid calls. Installed Pi also passed an opt-in RPC test against a temporary localhost OpenAI-compatible SSE endpoint, and a basic cached Qwen CPU inference smoke test through that same interface.

Separate bounded real Codex and Claude coding tasks ran through managed startup with a sandboxed broker and trusted local workers using existing local authentication. Codex's result passed its five unit tests and 1000 independent randomized checks. Claude's interval-merging result passed 11 unit tests, 1000 independent doubled-coordinate coverage checks, and a separate graph-component oracle over 3642 valid cases and three invalid cases. Both results were explicitly owner-verified, followed by clean managed shutdown. Claude used the explicit `Read,Write,Edit,Bash` allowlist with `safe-mode` and `dontAsk`.

The [mixed-harness sandbox acceptance](mixed-harness-acceptance.md) also passed with a real Claude lead, Codex coder, and Pi reviewer backed by cached local Qwen 1.5B inference. The broker and all three harnesses ran in separate Bubblewrap sandboxes under normal tmux supervision. Each turn had enforced 2 GiB/one-CPU/256-task cgroups and open-file/CPU-time limits. Claude delegated through authenticated board, memory, and instruction calls; Codex published a hashed source artifact through scoped memory; Pi received only the lead-supplied artifact and completed an analysis-only review. A separate credential-free, network-disabled verifier checked normalization, type errors, finite iterables, nonmutation, and fresh outputs before owner-verifying the coding task. Reverse authority and unauthorized board/artifact reads were denied. The initial pending task retained its ID and version across a broker restart without resubmission, and managed shutdown completed cleanly.

The acceptance explicitly uses `codex_sandbox: external` because this host forbids nested user namespaces. Native login originals, peer credentials, owner state, and unrelated owner environment values were absent from each model sandbox. Actual native auth readiness was checked with networking disabled. A real installed-Pi/local-fake-endpoint regression now checks prompt-time credential-store writes, full RPC completion, and generated configuration cleanup inside Bubblewrap; help alone had missed that runtime requirement. Known standalone Codex companion binaries are also checked before startup.

Those coding and mixed-harness acceptances used `network: host`; the local inference server remained a separately trusted provider outside worker resource groups, and that mixed-harness run included no Antigravity turn. Later bounded Ollama/vLLM and Antigravity acceptances are recorded separately in [provider validation](provider-validation.md). They establish their measured paths, not broad model quality, production engine support, complete hidden-model context coverage, or a comprehensive sandbox security audit. Current destination controls and trace guarantees are described in [hardening](hardening.md) and [traces and recovery](traces-and-recovery.md).

The opt-in real tmux lifecycle test also covers startup, a durable command task, ordered shutdown, idempotent shutdown, and another attempt preserving submitted work:

```bash
AGENTMUX_MANAGED_INTEGRATION=1 go test -race ./internal/managedlaunch
```

Build `bin/agentmux` first, or set `AGENTMUX_TEST_BINARY` to an absolute built executable path. Add `AGENTMUX_SANDBOX_INTEGRATION=1` to include the real spawned bubblewrap worker test; it requires a host with usable user namespaces.
