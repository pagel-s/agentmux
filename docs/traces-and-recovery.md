# Trace coverage and operational recovery

For current reporting, start with `amx observe --session work`, then inspect
the printed exact task with `amx task TASK_ID --open --session work`. The
[observability guide](observability.md) separates worker check-ins, native event
observations, adapter readiness and retained outcomes. These controls
are included in v0.15.0. Live progress contains metadata only; opt-in
traces below contain sensitive prepared input and adapter events.

```yaml
agents:
  coder:
    trace:
      enabled: true
      max_turn_bytes: 33554432
      max_total_bytes: 268435456
```

Opt-in local traces contain the exact prepared owner role/task/observations,
adapter-visible native JSON events and final result/usage. Known credential
values are redacted before writing. Traces can still contain sensitive task,
code and tool content; files are private and never mounted into agent sandboxes.

Each turn is a JSONL HMAC chain using a private controller key. A terminal
marker and task-completion anchor bind the sequence and byte count. The broker
verifies the trace before accepting a traced task completion. Capacity is
reserved before claim; hitting the turn limit stops the native process and
records failure rather than silently dropping evidence. Keep an owner's copy
of task anchors outside the state directory if independent later comparison
is needed. Someone holding the owner key and editable database can rewrite
both; this is authenticated local evidence, not public immutable attestation.

```bash
amx managed trace list --state STATE --agent coder
amx managed trace verify --state STATE --agent coder --id TRACE_ID --mac TASK_MAC
amx managed trace show --state STATE --agent coder --id TRACE_ID
```

These commands require the state owner's credential/filesystem access and work
with the broker offline. They reject malformed framing, altered records,
truncation, data after the footer and mismatched anchors. Signed record envelopes
require the nine canonical fields exactly once; duplicate fields and case aliases
are rejected before authentication. Native event data remains covered by its
record's MAC. `show` authenticates a private capture before emitting those exact
captured JSONL bytes, so a concurrent append or replacement cannot add unverified
content to the output. Copy traces together with their task anchors to owner-controlled archive
storage before intentionally freeing capacity; never delete active turn files.

## Portable authenticated bundles

An owner can export a completed trace as a private signed bundle. Export first
checks the exact captured JSONL against the local HMAC chain and optional task
anchor, then signs that content, its SHA256, session, agent, coverage and summary
with an Ed25519 owner-exporter key. The key is created once at
`STATE/traces/portable-signing.key` with mode 0600 and is never included in a
bundle. Neither the HMAC key nor broker credentials or environment snapshots
are exported. The bundle contains the trace's task/tool content, so it remains
sensitive even when known credential values were redacted during capture.

```sh
amx managed trace export --state STATE --agent coder --id TRACE_ID \
  --mac TASK_MAC --output /private/trace-bundle.json
# Copy the printed fingerprint through an independently trusted channel.
# Verification works on another machine with no state directory or secret key:
amx managed trace verify-bundle --input /private/trace-bundle.json \
  --trust OWNER_PUBLIC_KEY_SHA256
```

The trust fingerprint is mandatory: an embedded public key alone does not
establish who exported a file. Verification authenticates the pinned owner
exporter's signed attestation of local verification, not a model provider,
unmodifiable execution environment, or the truth of agent output. An owner
with the signing key can issue another bundle. Retain the fingerprint outside
the state directory, protect/back up the signing key, and compare task anchors
independently when that stronger comparison matters. Bundle creation refuses an
existing output path; captured input to an export is bounded to 64 MiB and the
encoded bundle to 128 MiB. Verification never extracts or executes files.
Offline verification does not contact providers or read local
broker state.

Native/provider hidden state, internal prompts, undeclared file reads and
unreported tool/network activity are outside this adapter's coverage. A native
CLI may condense or omit event details. The prepared input is exact for that
turn; this is not a claim of full hidden-model context or every filesystem read.

## Credentials, interruption and upgrades

Managed workers reload the selected, filtered native credential source between turns.
An external native login/refresh can atomically update the owner's source file;
the next turn sees it without restarting a healthy worker. Snapshots remain
read-only in sandboxes. Automatic provider OAuth renewal and writes back from
an agent are not performed. A missing/invalid source stops new claims.
An interactive native process retains its prepared snapshot until exit;
relaunch it when a refreshed source must be loaded.

With a running v0.13.0-or-newer broker, use direct owner inspection:

```sh
amx tasks --status running --state STATE
amx task TASK_ID --state STATE
amx task TASK_ID --open --state STATE
```

`--open` pins the exact task in the workbench, including older tasks and retired
recipients. It shows recorded delivery, current recovery evidence and the
explicit task/version review; opening never retries or resolves the task.
These shortcuts have been available since v0.13.0. Trace verification and bundle
inspection above also work without a running broker.

The lower-level `managed activity`, `managed status`, `task.list`, `task.get`
and `conversation.status` remain available for inspecting state. Delivery
uncertainty is preserved after a broker crash or transport loss. Stop old
workers before any owner resolution:

```bash
amx managed down --state STATE
# In one terminal, run the broker in the foreground:
amx managed serve --state STATE --sandbox bubblewrap
```

If `managed down` or the shutdown stage of `managed restart` fails, keep the
JSON fault and follow the exact-state recovery commands printed to stderr.
Inspect status, open affected owned native terminals with `amx attach --state
STATE`, and finish or exit their native CLIs normally. Detaching or closing an
observer leaves those processes running. Retry `managed down` and confirm it
succeeds before restarting a broker or resolving uncertain delivery. Broker
and terminal availability remain unconfirmed until inspected.

In another terminal, inspect the exact task and resolve its current version:

```bash
amx managed call task.get --credential-file STATE/owner.json \
  --json '{"id":"TASK_ID"}'
amx managed call task.resolve --credential-file STATE/owner.json \
  --json '{"id":"TASK_ID","version":CURRENT_VERSION,"resolution":"fail"}'
```

Replace `TASK_ID` and `CURRENT_VERSION` with the returned task identity/version.

Use `resolution: retry` only after checking side effects and deciding to execute
again. It is a new charged attempt. Workers validate the exact current lease
before native launch, during monitoring and before completion; an old worker
cannot borrow the replacement attempt's running state. A periodic check cannot
undo side effects already in progress, so stopping the old process is still
required before retry. Upgrade every worker, including SSH workers: a new
broker does not add these observation checks to an older worker executable.
Persistent native conversations can remain
blocked after interrupted execution; `conversation.reset` explicitly starts a
new generation after task delivery is resolved. Old history is retained, not
silently resumed as confirmed work.

For an exited workspace-owned interactive terminal, `amx relaunch AGENT_ID
--state STATE` keeps its identity and healthy peers, and starts a fresh native
conversation by default. Select `--resume last`, `--resume picker`, or a native
conversation ID explicitly when supported by that harness. This does not retry
a broker task, reset a managed conversation fence, renew runtime, or revive a
revoked agent. A live terminal must exit before relaunch. See [work sessions](work-sessions.md)
for the CLI and dashboard workflow.

Disk capsule quarantine has separate owner inspection and digest-fenced
recovery controls; see [hardening](hardening.md). Do not remove journals or
partially published workspace/history trees by hand.

For an upgrade, stop workers and broker, back up the complete private state
(including SQLite/WAL consistency, credentials, history, trace key and anchors),
then install an explicitly selected release and run `managed doctor` before
restart. Preserve the original deadline and native conversation generations.
Never replace a running executable in place. The release installer checks
published SHA256 and uses an atomic destination replacement with explicit
`--replace`; checksum verification is not an independent publisher signature.
Restore a matching binary plus complete state backup together if rollback is
needed. No live schema migration or zero-downtime upgrade guarantee is made.
Older collaboration records remain readable but require an explicit owner
`legacy_policy` choice before group edits; see [live permissions](live-permissions.md).
Keep recovery digests and their matching records together. Removing lifecycle
or live-resource records is not a supported way to clear a pause or budget.
