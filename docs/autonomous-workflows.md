# Persistent conversations and bounded autonomous workflows

Managed workers can run several turns from one initial goal. Three controls are
independent: the native harness conversation, broker command authority, and the
events that may wake a worker. Configure each agent explicitly.

```yaml
agents:
  lead:
    harness: claude
    sandbox: bubblewrap
    network: host
    conversation: persistent
    autonomy:
      enabled: true
      boards: [work]
      direct_notes: false
      pings: true
      max_turns: 20
      cooldown_seconds: 2
      max_backlog: 50
      batch_size: 10
      max_hops: 8
```

This fragment belongs in a version 2 managed configuration, alongside role,
authentication, board access, instruction grants and resource limits. Enabling a
subscription does not grant access to its board or permission to command anyone.
Defaults preserve fresh turns and no message-triggered execution.
See the [autonomy operation reference](autonomy.md) for exact defaults, queue
receipts, omitted-source references and causal ancestry bounds.

## Native history

`conversation: persistent` uses an explicit native session reference for Claude,
Codex or Pi inside Bubblewrap. Each agent and conversation generation receives
its own writable transcript directory. Credentials are separate filtered,
read-only snapshots. Peer histories and the controller state directory are not
mounted. Antigravity and the generic command harness reject persistent mode;
their managed turns remain explicitly fresh.

The worker reintroduces the role, current task and authenticated provenance on
every turn. Previous conversation text does not create broker permissions.
Only a confirmed successful native turn and atomic task completion commit a
session reference. There is no implicit “last session,” fallback to a fresh
conversation, or replay after an uncertain response.

Inspect a reference using the owner credential:

```sh
agentmux managed call conversation.status --credential-file STATE/owner.json \
  --json '{"agent":"lead"}'
```

An interrupted, cancelled or failed persistent turn blocks history reuse.
First stop its process and resolve any uncertain task using the managed task
recovery procedure. Then explicitly start a new conversation generation:

```sh
agentmux managed call conversation.reset --credential-file STATE/owner.json \
  --json '{"agent":"lead"}'
```

Reset preserves old transcripts for owner inspection. It does not renew the
session deadline or autonomy budget. Native transcript retention consumes disk;
archive completed state directories according to your retention policy. Native
history is a resume mechanism, not an immutable full tool audit.

## Observations and commands

An authorized `instruct` creates an instruction task. A subscribed board post,
direct note or ping can instead create an **observation** task. Its provenance
lists the triggering message references; its text does not impersonate a human
or a commanding agent. The worker resolves those references through `wake.read`
under current access and team-history rules immediately before execution.

The broker keeps a durable bounded unread queue, batches triggers, suppresses
self-authored wakeups and carries causal ancestry. It enforces a finite causal
hop limit. Overflow retains the original message but drops the extra wake
notification and increments a visible counter. Agents can still read messages
through normal authorized operations. A wakeup is not a guarantee of eventual
execution after budget exhaustion, revocation or expiry.

With autonomy enabled, owner and peer instructions also consume the same
per-agent turn budget. Cooldowns and serial execution apply to every launch.
Budgets, unread state and owner pause state survive broker restarts. Workers
check readiness without writing idle polling receipts. Unknown or interrupted
deliveries retain their slots until explicitly resolved.

Use `autonomy.status {"agent":"lead"}` with the owner credential (or `{}` with
the agent's credential). `autonomy.control` is owner-only and accepts the
agent, latest status version, and mode `running`, `paused` or `stopped`.
Pausing prevents further launches; cancellation of already running work uses
`task.cancel` and its process-stopped acknowledgement. Do not infer that a pause
has stopped an in-flight native tool. There is no automatic budget renewal.

## Acceptance

`scripts/smoke-managed-autonomy.py --binary bin/agentmux --allow-models` runs a
bounded five-turn Claude/Codex workflow. It requires installed authenticated
CLIs, working Bubblewrap, and user-systemd cgroup enforcement. It makes up to
five real cloud turns; model calls are never automatically retried. All agents
and the broker use separate namespaces. Scoped artifacts pass through broker
memory, the coder can ping but cannot command the lead, and an independent
credential-free verifier checks the final artifact before owner verification.

The fixture restarts the broker between confirmed turns, preserves its pending
wakeup, resumes both native histories, and requires exactly one initial owner
instruction. Private artifacts and a secret-safe JSON report distinguish a
verified run from a failed or uncertain attempt. Scripted broker and native
protocol tests cover failure, revocation, budget and replay cases without cloud
calls.

Both live acceptance runners first check the independent verifier against known
passing and failing implementations, without model calls. Verifier diagnostics
(command, exit status and bounded output) are retained privately beside each
artifact; a final verifier failure is also recorded in `report.json`. The
verifier uses a system Python under `/usr`, so a host virtualenv or PATH shim
outside its mounts cannot accidentally become its interpreter.

To recheck a retained implementation without restarting agents or spending more
model turns:

```sh
python3 scripts/smoke-managed-models.py \
  --verify-artifact /path/to/retained/tag_normalizer.py
```

This produces a separate artifact-only report and private diagnostics. It does
not change the original run's status or verify its broker tasks. A standalone
verifier preflight is also available:

```sh
python3 scripts/smoke-managed-models.py --check-fixtures
```

The installed-CLI acceptance passed on 2026-10-31 with three persistent Claude
turns, two persistent Codex turns, two observation wakeups and one initial owner
goal. A broker restart preserved one queued ping, its cursor/version and the
confirmed Codex session reference. Both agents ended with zero pending or
in-flight work and exhausted their exact configured turn budgets. The separate
oracle accepted six normalization cases, four invalid-element cases, a finite
generator and fresh-output behavior; the owner verified all five tasks. The
broker shut down cleanly. This is one integration result, not a reliability rate
or a claim that every autonomous task will succeed.
