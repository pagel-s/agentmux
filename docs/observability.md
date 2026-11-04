# Observe running work

Start with one operational view, then inspect the exact task that needs attention:

```sh
amx context --session work
amx observe --session work
amx task TASK_ID --session work
amx task TASK_ID --open --session work
```

Replace `TASK_ID` with the printed `task_…` identity. `observe` prints commands
containing the exact state directory so a later inspection cannot silently
target another workspace. All four commands are read-only. Opening a task
does not retry it or resolve its delivery.

`amx observe`, live managed-turn progress and the reporting timestamps on
this page are included in [v0.13.0](getting-started/installation.md).
Upgrade an older workspace's
controller and workers explicitly; installing a new CLI does not replace a
running controller. An older controller can report this operation as unsupported. The output records
`observer_version` and the pinned controller path separately; installing a new
observer does not prove that an existing workspace upgraded its controller.

![Actual amx observe output showing capacity, uncertain delivery and a live command-worker check-in](assets/guide/terminal-observability.jpg?v=d9ac7487da7c)

The capture uses disposable command-only fixtures. The running worker reports a
check-in and zero native model events; stopped delivery remains uncertain.

## Read the evidence separately

| Observation | What it establishes | What it does not establish |
| --- | --- | --- |
| Broker connected, with observation time | The owner received a current broker response | A worker or model is alive |
| Observed worker pane | The ownership-checked tmux inventory reports that pane's process state | Useful model progress or successful work |
| Live execution lease | This retained task has a running dispatch whose lease has not expired | Its process is responding or its outcome is known |
| Worker check-in | The exact leased worker recently reported to the broker | The model produced a new event |
| Native event time, count and kind | The adapter observed that many JSON events for this attempt | Useful reasoning, a complete transcript, or task correctness |
| Native adapter readiness | This registered adapter reported readiness; connection becomes stale after ten seconds without reporting | A retained `running` receipt is current or completed |
| Submitted result | The worker submitted an outcome | Independent verification or owner approval |
| Verified result or trace | The recorded verification or authenticated adapter evidence passed its stated checks | Universal correctness or complete hidden provider activity |

The terminal and web workbenches show their last successful snapshot time.
An offline view retains its previous evidence and disables changes. Component
errors remain separate: a reachable broker cannot turn unavailable worker
inspection into confirmed liveness.

## Use attention without losing old failures

`observe` counts the entire retained task history. It reports both **recorded
delivery** and **effective delivery**. An expired dispatch can therefore remain
recorded as `dispatching` while its effective delivery is `uncertain`.
Inspection does not rewrite that record, its version or its lease.

Attention takes one item from each populated category in rounds: recovery,
recorded failure, submitted result awaiting verification, then pending work.
It takes a second item from a category only after giving the other categories
representation. Historical failures therefore cannot hide every new review
request. Healthy live execution leases have a separate list. Each list defaults to 25 items; `--limit 1..200` changes the bound. Counts
remain complete when a list is truncated, and the output labels that truncation.
Older tasks and retired recipients still contribute to the totals.

Retained capacity shows records, idempotency receipts, messages and tasks against
their configured ceilings. Reported headroom does not guarantee admission: some
capacity is reserved for shutdown/recovery and other limits still apply. The CLI
also samples database, WAL and shared-memory file sizes outside the broker lock.
It never follows file links or recursively walks traces and workspaces; unknown
file sizes remain unknown. These byte counts are observations, not disk quotas.

```sh
amx observe --session work --limit 100
amx tasks --session work --status running
amx tasks --session work --status failed --limit 100
amx task TASK_ID --open --session work
```

Use the task history's printed continuation command for further pages. In the
workbenches, **Tasks for selected session** opens the recipient's retained
history. The exact task view separates instruction/result, task version,
recorded delivery, native receipt, trace metadata and current recovery actions.

## Watch reporting

```sh
amx observe --session work --watch
amx observe --session work --watch --interval 2s --json
```

`--watch --json` emits JSONL: one complete JSON object per observation, with
`schema_version: 1`. The interval is a delay after collection, not a promise
that samples arrive at that frequency. Collection has a finite deadline;
`--interval` accepts 250ms through one minute and requires `--watch`.

For a private capture, choose a new output path:

```sh
umask 077
amx observe --session work --watch --json > /private/new-observations.jsonl
```

Stop with Ctrl+C. Watching continues through unavailable components and emits
their errors; interruption exits successfully. For automation, a **one-shot**
partial observation emits its available JSON and exits nonzero:

```sh
amx observe --session work --json
```

Check `complete`, `broker_error`, `runtime_error`, `native_error` and each
`storage.files` entry's `state`/`problem` before treating a sample as available.
An unsafe or unreadable storage file makes the collection partial; an absent
WAL/shared-memory file is reported separately and is not an error. `complete` means component observations were
available; inspect `attention_more`, `active_more` and `native_more` for bounded
inventories. The top-level collection time, broker snapshot time, worker
check-in, native event time and readiness time describe different observations.
They are not one atomic snapshot of every process.

These metadata snapshots and JSONL are the supported integration surface.
Agentmux has no bundled OTLP/Prometheus exporter or cross-workspace trend
database. Preserve sample timestamps, `complete` and component errors in any
external aggregation; unavailable samples do not mean zero work.

The native inventory is a fast reporting/receipt summary. It deliberately does
not evaluate all queued admission candidates; `native[].pending_count` is
`null` when not computed. Use `amx delivery ID` and the exact task view to
inspect current admission policy and queued native work. Metadata indexes keep
large task bodies out of operational counts and ranking. These projections use
a separate read-only database snapshot, outside the worker coordination lock.
The query budget remains 250 ms; slow query cancellation cannot hold the
worker's connection or its unchanged 500 ms lease monitor.

One operational read runs at a time. Overlapping observations return
`unavailable`; a watch collects again on its next tick. An unfinished query
reports unavailable evidence. Credentials are rechecked before a completed
sample is published.

The output omits task/result bodies, raw native events, provider usage bodies
and credentials. It still contains local paths, task/session identities,
timestamps and authenticated trace metadata. Keep captures private and review
them before sharing. This is an operational record, not a credential bundle or
an immutable execution attestation.

## Diagnose silence and interruption

For a managed headless turn, a fresh worker check-in with an unchanged native
event count means the controller is reporting while no new adapter event has
been observed. Some native tools run quietly. A stale check-in can reflect a
stopped process, lost transport or an unavailable broker. Neither observation
alone proves that a model is stalled or that side effects stopped.

Progress is metadata for one exact task attempt, fenced by recipient,
credential instance/generation and execution lease. Heartbeats do not advance
the native event timestamp or renew execution permission. Retry, cancellation
and credential rotation cannot borrow the previous attempt's observations.
Broker restart loses these ephemeral observations; missing progress means
**unknown**, not zero activity. An expired unchanged attempt may retain its
last observed progress beside effective uncertainty, but cannot refresh it.

For interactive sessions, inspect **Native collaboration** and the original
native terminal. Adapter readiness and transport receipts remain separate.
An adapter can stop reporting while its retained receipt still says `running`;
the reporting warning does not convert that receipt into success or failure.
External/manual bridges do not gain automatic delivery from being listed.

If delivery is uncertain, inspect the task's complete evidence and stop or
fence its prior process before considering retry. Recovery is an explicit
review of the exact current task/version. A retry can repeat side effects and
spend another attempt's allowance. Expired runtime budgets can remove retry
from the available actions. See [recovery](traces-and-recovery.md#credentials-interruption-and-upgrades).

## Drill into retained evidence

Task output includes the native receipt and any trace summary. Receipt phase
is transport evidence; owner task verification remains a separate action.
For a completed opted-in managed trace, use the exact IDs and MAC anchor from
that task:

```sh
amx managed trace verify --state STATE --agent AGENT_ID --id TRACE_ID --mac TASK_MAC
amx managed trace show --state STATE --agent AGENT_ID --id TRACE_ID --mac TASK_MAC
```

These trace commands also work with the broker offline. `show` contains
sensitive task/tool content and emits verified JSONL; the live progress channel
contains none of that raw content. The [trace guide](traces-and-recovery.md)
describes opt-in capacity limits, coverage exclusions and portable signed
bundles. The workbench's trace summary is recorded metadata, not a fresh file
verification.

Guided coding runs have their own retained evidence rather than this broker's
task queue. Use `amx work status` and `amx work show RUN_ID --open`, or
`amx flow status RUN_DIRECTORY` and `amx flow explain RUN_DIRECTORY` for advanced
runs. See [checked coding](working.md) and [advanced workflows](workflows.md).
Research additionally exposes a separate runner heartbeat and reviewed phase
barrier controls in [Research](research.md#observe-and-intervene-live).

## Validate the route you use

Run the [model-free observation regressions](workflow-validation.md#observation-and-reporting)
to check metadata fences, unavailable components and honest recovery state.
Recorded protocol frames and local compatible-endpoint fixtures establish
adapter behavior for those fixtures. Subscription login/startup establishes
only acceptance of that configured route. Actual provider task and continuation
checks must record the selected CLI version, authentication route and verified
artifact separately. These are not interchangeable evidence of provider
reliability, elapsed operational use or comparative task quality.
