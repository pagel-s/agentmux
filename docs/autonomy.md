# Message wakeups and bounded agent turns

Autonomy is opt-in per agent. It lets an idle worker react to selected observations while keeping `instruct` as a separately authorized command. Subscriptions grant no board access, team membership, or instruction authority.

These controls dispatch managed task workers. Interactive native sessions use
the separate [native delivery](native-delivery.md) policy: Manual by default,
with explicit Review/Auto and observation subscriptions. They retain
their ordinary terminal workflow. Use
[work sessions](work-sessions.md) for that daily interactive setup and
[autonomous workflows](autonomous-workflows.md) for persistent managed turns.

```yaml
agents:
  reviewer:
    harness: codex
    role: Review supplied changes; wait when there is nothing useful to do.
    autonomy:
      enabled: true
      boards: [work]
      direct_notes: false
      pings: true
      max_turns: 10
      cooldown_seconds: 1
      max_backlog: 20
      batch_size: 5
      max_hops: 8
      max_inflight: 1
```

Board names are exact; an explicit `"*"` subscribes to currently readable boards, including authorized dynamic-team boards. Own messages and instruction messages do not create observation wakeups. Direct notes and pings are independent switches. Messages written before a subscription's agent exists or before it gains access are not retrospectively queued. Current team history rules still apply when sources are delivered.

Defaults are disabled triggers, 100 turns, zero cooldown seconds, a backlog of 100 references, batches of at most 20 references, eight causal hops, and one inflight turn. Workers serialize turns, so `max_inflight` currently accepts only one. An explicit smaller backlog also reduces an omitted batch-size default.

The turn budget applies to every dispatch attempt when autonomy is enabled, including explicit instructions, observation wakeups, retries, and skipped/failed attempts. Instructions take priority over queued observations. Resume does not replenish the lifetime turn budget or session deadline. Start a new owner-approved run for a new budget.

That lifetime `max_turns` limit is distinct from token/cost admission allowances.
The owner may explicitly update future token/cost reservations using versioned
`capacity.update`, or shorten admission runtime; it cannot reset consumed turns,
refund past reservations or extend the original process deadline. See
[live resource controls](live-permissions.md).

## Durable queue and processing receipts

The broker stores bounded message references in one state record per subscribed agent. Original message content remains in the message store. When a backlog is full, the original message is retained, its wakeup is not queued, and `dropped` increases. Existing queue entries are retained in order. There is no implicit retry or unbounded overflow queue.

`autonomy.status {}` is read-only. It returns `status.ready`, `mode`, a derived state/reason, pending and inflight counts, turn usage, the next eligible time, drop/suppression counts, a cursor, and bounded processing receipts. Repeated idle discovery changes no durable records or request receipts. `suppressed` counts queued references removed after they cease to qualify; it is not a complete audit of every unsubscribed message.

`pending` remains the observation-notification count for compatibility; `pending_observations` names that count explicitly, while `pending_instructions` counts queued instruction tasks. When otherwise eligible queued work cannot fit its session or ancestry admission allowance, status reports `ready: false`, `state: "halted"`, `admission_blocked: true`, and the existing admission denial in `reason` and `admission_reason`. This check reserves no allowance and does not renew any budget.

The cursor advances when a source is scheduled or explicitly acknowledged. Receipts distinguish `scheduled`, terminal task outcomes, and manual `acknowledged` processing; they do not establish that a model understood or correctly acted on a message. An agent can acknowledge its currently visible queue through an opaque visible message ID:

```json
{"operation":"autonomy.ack","args":{"message_id":"MESSAGE_ID"}}
```

`worker.next {}` is an explicit idempotent mutation. It atomically creates and claims a deduplicated observation task, or claims an existing instruction. It retains the ordinary lease, conversation-readiness, concurrency, runtime, and storage checks. Workers call it only after read-only discovery reports ready.

## Source privacy and provenance

Observation tasks carry `origin.kind: observation` and broker-derived source IDs/causal metadata. Their stored task text is a generic notice. Source bodies are resolved separately with read-only `wake.read {"id":"TASK_ID"}`, using current ACLs, active-agent capabilities, and team history floors. A replayed claim cannot restore revoked source references. If every source is no longer accessible, the worker settles a skipped observation without starting a native model turn.

Automatic source context is bounded to approximately 256 KiB of encoded message data. Remaining queued sources form later tasks. A single oversized source receives an explicit reference-only omission notice and `omitted_message_ids`; `message.get {"id":"MESSAGE_ID"}` can retrieve its original content under the normal visibility checks. This keeps automatic framing bounded without silently changing stored messages.

The broker derives causal roots, the parent task, and hop counts from an author's running turn. Callers cannot supply forged provenance in send arguments. A hop limit suppresses further observation wakes; enabled targets also reject instructions beyond their configured hop budget. Root references are bounded to 32 with an explicit truncation flag; task/source edges remain available to reconstruct causality from an owner export. Provenance describes origin rather than privilege.

Source permissions are checked again before delivery and while an observation runs. Revoking access can stop subsequent autonomous execution and deny future reads. It cannot erase context already delivered to a native model or history already written by that model.

## Owner controls

For a running workspace, inspect the selected agent using its exact state path:

```sh
agentmux managed call autonomy.status --credential-file STATE/owner.json \
  --json '{"agent":"reviewer"}'
agentmux managed call capacity.get --credential-file STATE/owner.json \
  --json '{"agent":"reviewer"}'
```

Replace `STATE` and `reviewer` with the actual workspace directory and stable
agent ID. The terminal workbench and web **Agent** view expose the same status;
web also reviews effective token/cost admission edits separately from pause.

Read the latest `autonomy.status {"agent":"reviewer"}` and pass its version to `autonomy.control`:

```json
{"agent":"reviewer","mode":"paused","version":3}
```

Only the owner can choose `running`, `paused`, or `stopped`. Pause blocks new dispatch while an existing turn may finish. Stop clears queued wakes and requests cancellation of proposed/running tasks; an active process still requires its stopped acknowledgement. Resume requires fresh capacity, preserves consumed budgets, and does not recreate discarded wakeups. Enabled agents receive their bounded state at initialization/spawn, reserving safety capacity for their first pause/stop before any message arrives. For disabled agents, first use of these optional controls needs normal storage capacity; ordinary credential revocation remains separately reserved. Idle is a derived read-only state.
