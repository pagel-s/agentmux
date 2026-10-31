# Repeatable research pilots

`agentmux experiment` runs finite, auditable comparisons on the managed runtime.
It creates a fresh broker, credentials, workspaces, homes, and private/shared
memory for every trial. Agents within a trial run concurrently; trials run
serially. Tmux is not required.

The included command fixtures validate orchestration and scoring. They do not
produce evidence about agent cooperation, competition, or personality.

```sh
go build -o /tmp/agentmux ./cmd/agentmux
/tmp/agentmux experiment plan examples/experiments/pilot.yaml
/tmp/agentmux experiment run examples/experiments/pilot.yaml --output /tmp/amx-pilot
```

Use a new, empty output directory. Existing output must be private (`0700`).
Choose a short path: each trial's Unix socket path must fit within 100 bytes.
The example uses only `/bin/sh` and makes no model calls.

## Manifest and execution

A manifest has `version: 1`, an `id`, a relative or absolute managed `team` YAML,
`trials` (repetitions), `seeds`, `randomization_seed`, `timeout_seconds`, and
`max_concurrency: 1`. Unknown fields and additional YAML documents are rejected.
The matrix contains every condition/seed/repetition combination exactly once.
Seed/repetition blocks and condition order are shuffled deterministically using
`randomization_seed`. Planning performs no agent execution or provider calls.

Every condition supplies:

- A unique `id` and an initial `instructions` entry for each configured agent.
- `visibility: board-only` or `direct`.
- `reward_mode: shared`, `individual`, or `winner`.
- Optional `reward_instructions` describing the incentives to participants.
- Optional `assertions` overriding the manifest's common owner-defined oracle.

`board-only` removes inter-agent direct message, ping, and instruction grants.
Board ACLs continue to apply. `direct` preserves explicit configured grants and
does not expand them. Both conditions retain the owner's initial task delivery.
Dynamic spawning and team creation are disabled in this initial runner.

Each initial instruction is submitted by the authenticated owner. The managed
worker adds the configured role, condition and seed, task/message provenance,
and authenticated CLI instructions. Every task starts a fresh harness turn.
There is no prescribed research leader unless one is present in the team roles.
Original configured workdirs are replaced with empty, dedicated trial workspaces;
source repositories and previous results are not copied in. Explicit read-only
program dependencies remain configured and are captured as possible confounds.

Each agent receives `AGENTMUX_EXPERIMENT_SEED` and the seed in its role context.
This seeds the runner's matrix and gives scenarios a reproducible input; it does
not seed a model's sampling, tool behavior, or asynchronous message ordering.
Model calls and live reruns can differ.

## Independent scoring and payoffs

An assertion has an `agent`, exactly one `exact` or nonempty `contains` string,
and an optional positive `weight` (default one). It evaluates the published task
result only after a successful submission. Failed/cancelled tasks score zero.
Assertions and expected answers stay in the owner artifact directory; they are
not inserted into participant prompts or mounted as broker state.

The objective score is the sum of that agent's passing assertion weights:

| Reward mode | Computed payoff |
| --- | --- |
| `shared` | Every agent receives the sum of all objective scores. |
| `individual` | Each agent receives its own objective score. |
| `winner` | One unit is split among agents tied for the highest positive score; all-zero outcomes pay zero. |

These are deterministic, recorded post-run payoffs. There are no live transfers,
scarce-resource game mechanics, or repeated-round payoff feedback in this
initial runner. Merely naming a condition "competitive" does not establish a
scientifically consequential competitive environment. The incentive framing
and actual payoff rule are recorded separately.

An agent's initial task is verified only if all of its assertions pass. A
submitted response, a completed model turn, or "I succeeded" does not count as
independent verification. Result truncation and known-secret redaction occur in
the worker before scoring and are part of the measurement process.

## Artifacts and resuming

All files are private to the owner (`0600`, directories `0700`). They can contain
private messages, task content, memory, and explicit configuration secrets.
Do not publish an output directory without reviewing it.

The output contains the normalized manifest and SHA-256 digest, randomized plan,
and aggregate `results.json`. Each trial attempt contains:

- `started.json`, `result.json`, and the exact initial `instructions.json`.
- `config.json`: full normalized resolved configuration and its SHA-256 digest.
- `preflight.json`: model/harness identifiers, resolved executable hashes, and
  native CLI versions where a bounded `--version` probe succeeds. Owner-defined
  command fixtures are hashed and are not run just to guess a version.
- `runner-calls.jsonl`: owner requests and responses issued by the runner.
- `coordination.jsonl`: complete paginated broker records, including messages,
  memory, task state, and coordination events. Lease secrets and credential
  tables are excluded; process environment values in agent records are redacted.
- `state/`: the trial's durable managed state and private agent workspaces.

This is a coordination trace. It does not capture every native harness prompt,
hidden model context, tool invocation, retrieval/read observation, or compaction
event. Tool actions outside the broker are not a complete system trace. Provider
usage is retained only where the adapter reports it and is not a spending cap.

Running the same manifest and resolved team against the same output skips
completed trials. A different manifest/config digest is rejected. A partial,
failed-infrastructure, or interrupted attempt is never silently retried:

```sh
# First stop any processes left by the interrupted attempt.
/tmp/agentmux experiment run examples/experiments/pilot.yaml \
  --output /tmp/amx-pilot --rerun-interrupted
```

Explicit reruns create a new attempt and preserve the old state. An exclusive
output lock prevents two runners from mutating the same experiment. A completed
trial with failed oracle assertions is still a completed observation and is not
automatically repeated.

## Using real models

Replace the command fixtures with supported managed harnesses and connections.
`run` additionally requires `--allow-models` because it can incur provider
charges. Use explicit API credential references; fresh trial homes intentionally
do not reuse native login/history directories automatically. Authentication
availability is validated before accepting tasks.

Use `sandbox: bubblewrap` for supported Linux filesystem/process isolation.
Provider calls require explicit `network: host`; that grants unrestricted
network access and is not an endpoint allowlist. `local` remains a trusted host
process. Separate state/homes in local mode do not prevent a hostile agent from
reading other trials or other host files. Isolation and information boundaries
must be part of the experiment's declared conditions.

Begin with neutral roles, objective tasks, fixed populations, and strong
single-agent/independent-agent baselines. This runner does not infer traits,
measure causal effects automatically, determine statistical power, or implement
a resource-allocation environment. Repeated tasks, controlled observation
scheduling, richer payoff feedback, and harness-level input capture require
additional scenario work before making stronger research claims.
