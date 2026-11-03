# Managed execution hardening

These controls are opt-in. They fail closed when the requested boundary cannot
be established. `sandbox: local` remains a trusted host process; it cannot
enforce the network or disk boundaries below.

Disk caps require Bubblewrap with working `--size` support (introduced in
[0.7.0](https://github.com/containers/bubblewrap/releases/tag/v0.7.0)), plus
permitted user namespaces and tmpfs mounts. A namespace-only check is
insufficient. Preflight verifies an actual capped filesystem before claiming
work; an unsupported host must be upgraded instead of falling back to uncapped
storage. CI builds the checksum-pinned official
[0.13.1 release](https://github.com/containers/bubblewrap/releases/tag/v0.13.1)
in a private prefix, retaining the runner's host security policy.

## Exact network destinations

```yaml
agents:
  reviewer:
    harness: pi
    sandbox: bubblewrap
    network: allowlist
    egress:
      allow:
        - origin: http://127.0.0.1:11434
        - origin: https://api.example.com
          ips: [192.0.2.10] # Example only: replace with owner-approved addresses.
```

Each origin grants an exact scheme, hostname, and port. Paths, credentials,
queries, fragments, wildcards, and redirects to other origins are not grants.
Connection `base_url` may still include `/v1`; an egress origin does not include
that API path. The example addresses above are placeholders.

The worker starts a private host-side Unix HTTP proxy. Bubblewrap creates a
network namespace with no host network route, exposes only that worker's proxy
socket, and starts a namespace-local loopback relay. The relay replaces all
case variants of HTTP/HTTPS/ALL proxy variables and removes `NO_PROXY`.
Clients which ignore these proxy variables fail to connect; host networking is
never substituted.

DNS is resolved once when the worker starts and the proxy dials only those
pinned addresses. Implicit DNS answers pointing at private/local addresses are
rejected; an owner must select a literal address or supply explicit `ips` for a
private service. An IP-literal origin cannot pin another address. Explicit pins
also avoid changes in DNS answers during the worker's lifetime.

This section describes managed-mode networking. Full
`work`/`flow` isolation instead uses the inspecting proxy described in the
[workflow guide](workflows.md#private-storage-and-full-isolation). It checks each
HTTP/1.1 Host and HTTP/2 authority inside HTTPS using an ephemeral public CA;
the managed proxy below keeps HTTPS opaque.

For HTTPS CONNECT, the managed-mode proxy checks the requested host/port and a bounded TLS
ClientHello's SNI before opening the upstream. Other SNI names, non-TLS tunnels,
and encrypted ClientHello are rejected. HTTP authority mismatches and proxy
credentials forwarded to upstream services are rejected/removed. The proxy
does not follow redirects; any client-followed redirect must pass its own
destination check. Concurrent connections and request/tunnel sizes are bounded.

This is a **network-destination boundary**. Opaque HTTPS does not reveal URL
paths or encrypted HTTP `Host`/`:authority`; those are not inspected. Shared-CDN
tenant routing remains subject to that service's policy. It is not a TLS
interception proxy or an application-layer URL allowlist. Certificate-pinned or
non-proxy-aware clients may be incompatible and fail closed.

Agents cannot delegate broader networking through spawning. A `none` parent
can spawn only `none`; an `allowlist` parent can spawn `none` or a subset of its
origins and explicit IP pins. A child cannot discard pins or substitute pins
for an unpinned DNS origin. The owner retains its explicit administrative
authority to select approved templates.

## Aggregate admission budgets

```yaml
budget:
  max_tokens: 1000000
  max_cost_microusd: 10000000 # $10 of declared reservations, not a bill cap.
agents:
  lead:
    budget:
      enforcement: reservation
      max_turn_tokens: 20000
      max_turn_cost_microusd: 1000000
      max_subtree_tokens: 300000
      max_subtree_cost_microusd: 5000000
```

Token and cost values are separate owner-declared admission allowances. Cost
uses integer millionths of a US dollar. Every agent/template under a session
ceiling must declare the corresponding nonzero per-turn allowance. A spawned
child also needs a declaration whenever any ancestor limits that dimension.

Claiming a task atomically charges its full per-turn allowance against the
session and each configured ancestor/subtree ceiling. Explicit instructions and
observation wakes use the same claim path. An idempotent claim replay does not
charge twice; retrying uncertain work charges a fresh allowance. Charges survive
restarts, failed tasks, cancellation, and missing/untrusted usage reports. There
is no automatic budget renewal or usage-based refund. `budget.status` exposes
the caller's own subtree, or the full session to the owner.

The owner can update effective session/agent admission budgets with versioned
`capacity.update`; readiness and transactional claims use the same effective
limits. Edits cannot lower a ceiling below permanently reserved usage. A live
admission deadline can only shorten the existing deadline and does not kill an
already-running native process. These overlays survive restart without changing
the launch definition. `capacity.get` reports current capacity without reserving
it. See [live admission controls](live-permissions.md#capacity-and-live-admission-budgets)
for replacement semantics and CAS versions.

These limits bound **declared admissions**, not provider billing or actual
native-model token generation. Native CLIs can make multiple requests, and
their usage reports do not provide an authoritative pre-spend enforcement
point. Configuration requesting `enforcement: hard` is rejected. A provider
account's own spending controls, or a separately enforceable credential-holding
gateway, are necessary for a hard external bill ceiling. Do not treat a reported
usage observation or `max_tokens` on one request as a total-turn bill cap.
Reservations govern managed and [native bridge](native-delivery.md) task claims in either local or isolated mode;
human-driven inference inside an interactive native CLI does not claim broker
tasks and is outside these turn allowances.

## Capped workspaces and history

```yaml
agents:
  coder:
    sandbox: bubblewrap
    # Omit workdir: the controller creates state/workspaces/coder.
    disk:
      workspace_bytes: 67108864
      history_bytes: 33554432
      temp_bytes: 16777216
      max_entries: 10000
```

Every size is required, between 1 MiB and 1 TiB. Entry limits are 1–100000.
Disk capsules apply to managed turns; interactive sessions reject this setting.
Disk capsules require a canonical, private, state-owned workspace at exactly
`state/workspaces/<agent>`. External/shared workdirs are rejected. Use scoped
messages and explicit owner-controlled artifact handoffs between workspaces.

Workspace, the entire private HOME, and `/tmp` are separate sized tmpfs mounts.
The kernel returns `ENOSPC` when each filesystem is full. There are no writable
host workspace/history binds in this mode. The trusted inner shim imports a
read-only snapshot, runs the native harness, and exports only the workspace and
selected native transcript directory through a private checkpoint socket.
Other home files and temporary data are discarded. Read-only credential files
remain narrow snapshots; auth refresh still belongs to the external owner
credential source.

The host accepts only bounded regular-file/directory archives into fresh
private staging. It rejects traversal, absolute paths, implicit directories,
symlinks, hardlinks, devices, FIFOs, duplicate entries, PAX metadata, over-sized
files, and excess entries. Source trees with links are also rejected. Export
currently uses USTAR-compatible paths; files which cannot be represented fail
the checkpoint. Entry count limits apply to imported/checkpointed storage;
tmpfs runtime inode exhaustion remains a kernel/resource limit.

The history byte/entry allowance includes previous conversation generations,
so resetting a conversation does not create a new quota. Existing generations
consume the allowance available to the current checkpoint. A workspace/history
digest check prevents silently publishing over observed host edits. Publication
finishes before the adapter verifies the native transcript. A journal and old
snapshots are retained if publication is interrupted; uncertain work is not
automatically repeated.

Successful staging is removed. Failed, cancelled, malformed, or conflicted
checkpoints keep one bounded quarantine under `state/capsules/<agent>` and block
another capsule for that agent. Recovery is an authenticated owner operation
which also works with the broker offline:

```sh
amx managed capsule inspect --state /private/run --agent coder
amx managed capsule recover --state /private/run --agent coder \
  --action restore --expect DIGEST_FROM_INSPECT
```

Stop the worker first. Inspection/recovery hold the worker's exclusive lock and
the capsule controller lock, preventing recovery during active or concurrently
starting execution. Inspection returns digests, state, and safe available
actions. Recovery requires that exact current digest. `publish` selects an
accepted, validated checkpoint; `restore` selects the known previous snapshots;
`discard` is allowed only when existing host snapshots match their baselines.
Interrupted publications retain fsynced before/after hashes and old snapshots.
A partial publish with the only old snapshot in `previous-*` cannot be blindly
discarded. Changed owner files, unexpected snapshots, links, or changed other
history generations fail closed for manual inspection.

Filesystem recovery does not certify native task success. Resolve uncertain
task leases and conversation state separately before resuming agents.

The enforced quantities are tmpfs filesystem sizes, checkpoint file-content
bytes, and checkpoint entry counts. This is not a host filesystem project quota
for all controller logs, database storage, metadata blocks, or unrelated owner
files. A checkpoint can temporarily retain both old and new snapshots; plan for
up to twice the selected workspace/history content plus bounded metadata. Native
process-tree memory limits are separately configurable and should account for
tmpfs usage. No privileged host mounts, quota configuration, or policy changes
are performed automatically.

## Check the boundaries

The local adversarial tests cover origin/Host/redirect/pin/SNI checks, ECH and
non-TLS rejection, live-tunnel revocation, claim deduplication and uncertainty,
ancestor/session budgets, bounded archives, history generations, cancellation,
snapshot conflicts, quarantine, active-worker recovery denial, stale recovery
digests, and restore/publish after interrupted multi-volume publication.

Opt-in integration tests exercise real Bubblewrap namespaces without model
calls:

```sh
make build
AGENTMUX_INTEGRATION_BINARY="$PWD/bin/amx" go test ./internal/egress ./internal/capsule \
  -run 'TestRealNamespaceProxyCannotBypassAllowlist|TestRealCapsuleENOSPCAndHistoryCheckpoint' -count=1
```

The endpoint test proves allowed traffic succeeds through the proxy while a
denied endpoint receives no request and direct/proxy-opt-out attempts fail. The
disk test fills all three actual tmpfs mounts to `ENOSPC`, verifies read-only
seed enforcement, and checks workspace/native-history publication. An enclosing
container may deny namespaces; such a failure is not silently downgraded.

`TestRealManagedHardeningWorkerAndIsolatedBroker` additionally combines an
isolated broker, allowlisted worker, capped capsule and session reservation in
one actual managed task and checks rejection of a second task before any
further endpoint request. These tests use local deterministic processes and
do not measure model quality or provider billing.
