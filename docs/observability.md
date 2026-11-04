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
this page were introduced in v0.13.0.
Upgrade an older workspace's
controller and workers explicitly; installing a new CLI does not replace a
running controller. An older controller can report this operation as unsupported. The output records
`observer_version` and the pinned controller path separately; installing a new
observer does not prove that an existing workspace upgraded its controller.
Agentmux [v0.14.0](getting-started/installation.md) adds structured collection diagnostics and the offline summary
described below. Older v0.13.0 schema-1 captures remain readable; missing
diagnostic timings stay unknown.

![Actual amx observe output showing capacity, uncertain delivery and a live command-worker check-in](assets/guide/terminal-observability.jpg?v=3b3ac01704fa)

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

### Structured collection diagnostics

Agentmux v0.14.0 adds `diagnostics` without changing observation schema version 1
or the existing human output and `complete` behavior:

| Field | Meaning |
| --- | --- |
| `diagnostics.collection_duration_ms` | Local elapsed collection time, including acquisition and decoding; excludes the watch delay and final output formatting |
| `diagnostics.components.broker` | Owner credential loading and the broker observation read |
| `diagnostics.components.runtime` | Ownership-checked runtime inspection; `not_recorded` is a legitimate headless workspace |
| `diagnostics.components.native` | Native summary read after a successful broker observation; `not_attempted` has null duration and error code |
| `diagnostics.components.storage` | Metadata inspection of the three known database files |

Every component has `state`, `error_code`, nullable `reason_code` and `duration_ms`. `observed` means
the collection succeeded, including a known unavailable runtime or absent
storage file; it does not assert worker health. Component errors use
`unavailable` with one of these fixed codes: `credential_unavailable`,
`unauthenticated`, `forbidden`, `timeout`, `canceled`, `transport`, `unsupported`,
`invalid_response`, `unavailable`, `metadata_unavailable` or `unknown`.
Successful components have a null error code. Local call deadlines and
cancellation are classified from their context; a server cancellation can also
produce `canceled`. Unknown causes remain unknown instead of being guessed
from error prose. Existing free-form component errors remain separate.

Updated controllers refine broker/native operational-read failures with three
fixed reason codes: `operational_read_busy` means another operational read
already owns the single read slot; `query_budget_exhausted` means the server's
250 ms query budget expired; `shutdown` means the Store is closed or its
lifetime ended during collection. Existing error codes and human errors stay
unchanged. A shorter caller deadline does not claim a server query-budget
expiry. Legacy controllers and unrecognized causes leave the reason null;
null never means healthy. New clients opt into the wire field so older clients
continue receiving the original fault shape.

Durations use the local monotonic clock. Parallel components overlap, so their
durations must not be added. Broker and native reads retain their existing
three-second call limits within the collection's four-second context. These
measurements describe local collection, not model latency, task completion or
an uptime guarantee.

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

## Summarize a private capture offline

Stop the capture with Ctrl+C, then run the dependency-free Python reporter.
Release archives include `scripts/summarize-observations.py`; the binary
installer installs `amx` only. Extract the `scripts/` folder from the archive
matching your observer version, or use the corresponding source checkout.
Run from that archive/checkout directory as below, or pass its absolute script
path to `python3` from any directory. Choose a gap threshold appropriate to
your capture interval:

```sh
python3 scripts/summarize-observations.py /private/new-observations.jsonl --max-gap 5s
python3 scripts/summarize-observations.py /private/new-observations.jsonl --max-gap 5s \
  --json > /private/new-observation-report.json
```

The command also accepts `-` for standard input. It makes no provider calls,
does not open the workspace and does not write the input capture. A successful
report records the SHA256 of every original input byte. That hash identifies
the consumed capture; it does not authenticate who produced it.

The report includes sample count, first/last timestamps, observed time span,
forward gaps above the chosen threshold and backward/repeated timestamps.
First and last follow file order; span means maximum minus minimum timestamp.
It counts reported completeness and component availability **at samples**,
with bounded gap and availability-transition examples. These counts do not
estimate uptime between samples or establish task quality.

Retained tasks, task states, recorded/effective delivery, recovery eligibility,
attention and live leases remain gauges. Their first, last and known peaks are
not completed-task throughput or success rates. Capacity ratios use each
sample's own ceiling; changing ceilings remain visible. Database, WAL and
shared-memory sizes distinguish absent zero-byte files from unknown sizes.
Adjacent size decreases can reflect checkpointing or replacement. Observed
runtime attempt-identity changes count adjacent known samples, not actual
restarts. Missing samples break those comparisons; progress event counts are
never correlated across attempts.

Native pending totals remain unknown when any listed pending count is null,
the inventory is truncated or its collection is unavailable. Known partial
sums are labeled separately. An unavailable inventory is not counted as a
confirmed untruncated inventory. Legacy captures without diagnostics keep
their collection durations and error-code observations unknown. The JSON
report's `diagnostic_reason_code_samples` counts only the three fixed reported
reasons with compatible unavailable component/error states; missing or null
reasons use `not_recorded`, and other or inconsistent values use `other`.
These counts describe sampled reports, not authenticated cause attestations.

Each file must contain one exact state identity and newline-terminated JSONL
records. The parser rejects mixed states, unsupported schemas, duplicate keys,
malformed UTF-8/JSON, non-finite numbers, records over 2 MiB and inventories over
200 items. It streams one bounded record at a time and retains at most 16
examples of each episode category, with omitted counts. A rejected capture
exits nonzero and emits no report or partial-input hash.

Only fixed aggregate fields enter the report: workspace paths, task/agent/trace
identities, MACs, raw errors and unknown fields are omitted; unrecognized map
labels and diagnostic codes use aggregate buckets. Original captures still
contain sensitive operational metadata and remain private. Review even the
filtered report before sharing it.

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
