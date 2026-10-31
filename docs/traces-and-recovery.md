# Trace coverage and operational recovery

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
agentmux managed trace list --state STATE --agent coder
agentmux managed trace verify --state STATE --agent coder --id TRACE_ID --mac TASK_MAC
agentmux managed trace show --state STATE --agent coder --id TRACE_ID
```

These commands require the state owner's credential/filesystem access and work
with the broker offline. They reject malformed framing, altered records,
truncation, data after the footer and mismatched anchors. `show` emits verified
JSONL. Copy traces together with their task anchors to owner-controlled archive
storage before intentionally freeing capacity; never delete active turn files.

Native/provider hidden state, internal prompts, undeclared file reads and
unreported tool/network activity are outside this adapter's coverage. A native
CLI may condense or omit event details. The prepared input is exact for that
turn; this is not a claim of full hidden-model context or every filesystem read.

## Credentials, interruption and upgrades

Workers reload the selected, filtered native credential source between turns.
An external native login/refresh can atomically update the owner's source file;
the next turn sees it without restarting a healthy worker. Snapshots remain
read-only in sandboxes. Automatic provider OAuth renewal and writes back from
an agent are not performed. A missing/invalid source stops new claims.

Use `managed activity`, `managed status`, `task.list`, `task.get` and
`conversation.get` to inspect state. Delivery uncertainty is preserved after a
broker crash or transport loss. Stop old workers before any owner resolution:

```bash
agentmux managed down --state STATE
# Restart the broker under owner control for API inspection/resolution.
agentmux managed serve --state STATE --sandbox bubblewrap
agentmux managed call task.resolve --credential-file STATE/owner.json \
  --json '{"id":"TASK_ID","version":CURRENT_VERSION,"resolution":"fail"}'
```

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
