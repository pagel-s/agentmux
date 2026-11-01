# Finite multi-agent research environments

`agentmux research` runs repeated-round environments with private observations,
scarce resources, validated decisions, and live objective payoff feedback. It
complements the single-task [`experiment` pilot](experiments.md). Built-in
environments are `public-goods-v1` and `trust-exchange-v1`; the exported `Scenario` / `Episode`
interfaces separate owner-controlled mechanics from participant observations.
See [exchange, reward pooling, and population mechanics](research-environments.md)
and the [preregistered native pilot](research-study.md).

## Run the no-model protocol smoke

Build Agentmux, inspect the entire execution matrix, then run the included
deterministic command participants:

```sh
make build
./bin/agentmux research plan examples/research/public-goods.yaml
./bin/agentmux research run examples/research/public-goods.yaml --output /tmp/amx-research
./bin/agentmux research analyze /tmp/amx-research
./bin/agentmux research analyze /tmp/amx-research --csv /tmp/amx-research-analysis.csv
```

Run these commands from the repository root. Later examples use `agentmux` on
your PATH; see [installation](getting-started/installation.md). `plan` validates
and prints the trial matrix and task counts without starting participants.
`run` occupies its terminal until completion; use another terminal to observe it.

Use a new short private output path; directories are `0700`, artifact files
`0600`, and Unix socket paths must fit within 100 bytes. The CSV must be a new
file. Linux Bubblewrap with working unprivileged namespaces, Python 3, and the
current Agentmux binary are required. This example makes **zero model calls**.

The example schedules six independent runs, each with three participants and two
rounds. Every round has a coordination task and a decision task per participant:
72 command tasks in total. All workers and each broker are independently
sandboxed. Alice creates a real broker team and invites Bob; Bob accepts using
his own credential, then writes a receipt to the team's private board. Charlie
does not become a member. These are deliberately scripted strategies that test
the protocol, not evidence of intelligent self-organization.

## Observe and intervene live

Research uses the same terminal and web owner consoles as other managed work.
The runner prints the exact current state path and console commands when a new
attempt becomes ready. The private output's `live.json` points to that attempt.
To open the controls before the first participant phase:

```sh
agentmux research run examples/research/dynamic-population.yaml \
  --output /tmp/amx-live-study --start-paused

# In another terminal; OUTPUT follows the current attempt:
agentmux research status /tmp/amx-live-study
agentmux research watch /tmp/amx-live-study --follow
# Use the exact STATE printed by the runner:
agentmux dashboard --state STATE --view research
agentmux web --state STATE
```

`OUTPUT` means the study's top-level output directory (here
`/tmp/amx-live-study`); `STATE` means one attempt's broker directory printed by
the runner. `status` and `watch` accept either. OUTPUT resolves the most recently
published attempt. Add `--follow` to watch the complete study across trial
transitions. Dashboard/web start with an exact STATE; use study follow in Events
or the web Research view to observe the matrix. Web follow switches snapshots
with attempt-fenced controls, while terminal opening of another attempt is explicit. Before the first attempt is ready, there may be
no live pointer yet. A missing `live.json` alone does not establish a failed run;
check the runner terminal.

`status` prints one JSON snapshot. `watch` prints JSON snapshots as they change
and exits when the attempt finishes or is no longer live. Ctrl-C in the watcher
stops observation while the runner continues. Closing a dashboard or stopping
the web process also leaves the runner running. `watch OUTPUT --follow` stays
open through gaps until all trials complete or you interrupt it. Stopped,
interrupted and incomplete attempts are labeled explicitly; missing results
do not establish that a runner is executing. Use `research study OUTPUT` for
overall status and `research history OUTPUT` for paginated archived evidence.

Final-phase controls also apply at the transition between trials. An accepted
stop finishes the current phase and prevents the next trial. Pause and an
already-consumed step initialize the next attempt paused. Without `--start-paused`,
an unused step admits exactly one phase there; `--start-paused` takes precedence
and pauses every new attempt. Carried controls appear as audited interventions
before worker admission. If the entire study has no remaining phases, it can
complete without waiting for another step. Completion atomically acknowledges
the latest control version, so a racing accepted stop cannot be lost.

Study status and archived history validate the saved plan against the frozen
manifest. Omitted, reordered or relabeled trials are rejected instead of changing
completion counts or silently skipping evidence.

The terminal Events view and web Research view show trial/condition, round and
phase, population, authoritative phase-task delivery, finite deadline, reserved
model-task count and requested versus acknowledged control versions. A fresh
runner heartbeat is separate from a responsive broker. A stale/unknown heartbeat
does not establish that a task stopped. Heartbeats are ephemeral, consume no
durable request receipts, and become unknown after broker restart.

Pause, resume, single-phase step and stop require the displayed control version
and an explicit reason. In CLI JSON, use
`result.research.control.version` from a fresh `status` response; do not use the
progress revision or acknowledged version. For example, after reading version 2:

```sh
agentmux research step /tmp/amx-live-study --version 2 \
  --reason "Inspect team formation after one phase"
```

Fetch `status` again before the next control, because accepted changes advance
the version. A conflict means review the current state before submitting again.
Use `research resume OUTPUT --version N --reason "..."` to release a paused
live runner; this does not restart a stopped or interrupted runner.

These controls act at phase barriers. A phase already admitted may finish;
cancellation of accepted tasks is a separate action in Tasks. A step admits
exactly one phase and then waits. Pausing never renews the trial deadline or model
budget. `--start-paused` pauses each new attempt and is itself an intervention.
Transport failures never trigger automatic write replay or model reruns.

Teams exposes real agent-created teams, invitations awaiting participant consent,
membership versions and scoped board/memory. Owner invite/remove/dissolve actions
are reviewed against exact IDs and versions; the owner never impersonates an
agent's acceptance. Spawned children appear with template and parent; admission
to a dynamic study still happens only at the next-round boundary. Permission
versions change with membership so revoked shared knowledge is not presented as
currently readable. Previously delivered context cannot be erased.

Observation alone does not mark a run assisted. Every explicit phase control,
and successful owner changes to messages, knowledge, permissions, teams or tasks,
are recorded separately from authenticated runner scheduling and participant
activity. Results retain the intervention journal and owner-mutation count, and
default condition/paired/factorial/per-agent estimates exclude these exploratory
runs. `intervened_runs` reports them, and behavior exports carry an `intervened`
flag. New evidence is checked against both its private intervention artifact and
the durable broker checkpoint; missing metadata does not silently become clean
evidence. This provenance assumes a trusted host owner.

After broker shutdown, the web/terminal console can show saved research metadata
without enabling writes or claiming live execution. The local read authenticates
the original owner token; credential rotation and damaged state still fail
closed. `research status OUTPUT` and `research status STATE` both distinguish
live and saved metadata. When the broker is unavailable, they authenticate and
read the saved broker checkpoint; cached `live.json` content is not the status
authority. Saved status reports `live: false` and no current runner heartbeat,
and does not restart workers or prove their processes stopped.

Research participants use bounded headless managed turns. There is no native
terminal to type into during those turns; such a configuration is rejected
before execution. Use the normal interactive workspace for native conversation.
Within a study, steer through explicit phase controls, scoped notes/knowledge or
task cancellation. Extra owner instruction tasks interrupt the finite phase
protocol rather than quietly becoming unscheduled model work. Broker and
adapter-reported traces do not expose hidden model reasoning or unreported tools.

## The environment and actual incentives

Each round has two projects. Every participant privately receives one bit. XOR
of all participants' bits identifies the productive project. An individual clue
alone is incomplete. New bits are generated for each round from an owner-held
seed; the seed, other clues, and future cases are not put in participant prompts
or environment variables. Hidden ground truth stays outside broker state and
participant workspaces. Seeds reproduce environment cases, not native model
sampling or communication timing.

Each agent starts with a finite credit balance. Its decision must be exactly:

```json
{"project": 0, "contribute": 2}
```

or a valid zero contribution:

```json
{"project": null, "contribute": 0}
```

Project is 0 or 1; contributions are integers within both the per-round cap and
the agent's remaining balance. Unknown or duplicate keys, extra text/fences,
missing fields, and overspending are invalid. The environment charges every
valid contribution permanently, including money spent on a wrong or unfunded
project. Only the productive project pays a dividend, and only if contributions
reach its threshold. Every valid participant receives that dividend, including
a valid zero contributor. This creates a concrete free-rider opportunity.

Individual utility is dividend received minus contribution. Invalid or failed
decision tasks receive no dividend, utility `-1`, and lose one remaining credit
when available. An exhausted balance cannot go negative; the utility penalty
still applies. Invalid execution is recorded separately from intentional zero
contribution. The declared reward rule computes and accumulates actual points:

| Reward mode | Points credited each round |
| --- | --- |
| `shared` | Total participant utility divided equally among participants. |
| `individual` | That participant's own utility. |
| `winner` | One point split among those tied for highest strictly positive utility; otherwise zero. |

Agents are told to maximize their final cumulative points under that rule.
They receive the resolved productive project, all actions and validity, actual
costs, remaining balances, utilities, round points, and cumulative points before
the next round. The reward mechanism therefore affects subsequent decisions;
it is not a label added after the run. Points are experimental scores, not
external monetary payments. Team membership adds private collaboration channels
and does not silently pool rewards or confer command authority. Explicit
`team_rewards: pool` allows mutually selected members to pool scoring utilities;
wallets and raw objective welfare remain separate and are recorded.

## Scheduling and information boundaries

The runner creates every participant's private observation, commits all phase
instructions, then starts one managed task turn per participant concurrently.
It waits for every worker to stop before passing the phase barrier. It then
repeats this for sealed action tasks and resolves all contributions together.
Participants cannot read other participants' task results before resolution.
They can voluntarily communicate through allowed broker channels. Native
conversation mode is fresh by default. An explicitly configured
`conversation: persistent` retains that participant's private native history
between phases within the same trial; the mode is captured in the resolved
configuration and should be held constant across a comparison. Launch order
is seeded and recorded; exact process start time and asynchronous message order
are not controlled. Coordination outputs are supplied back to that participant
for its decision, alongside public previous-round feedback.

Conditions choose `visibility: isolated`, `board`, or `direct`. Isolated removes
shared boards/memory and direct communication. Board gives a common `research`
board/memory namespace. Direct additionally preserves only the source team's
explicit `message.send` / `ping.send` edges. No participant receives instruction
rights over another. Original source workdirs and namespace ACLs are replaced;
agent roles and declared runtime dependencies are captured as potential
confounds. Autonomy wake subscriptions and spawning are disabled so extra turns
cannot cross the phase barriers. Population is fixed by default. Explicit
`population.mode: dynamic` admits approved broker-spawned children only at the
next-round boundary, within declared resource and actor caps; see the mechanics
guide for entry-credit transfers and protocol-deviation handling.

With `teams: true` in a direct condition, all participants may create teams and
use existing invitation/consent/CAS rules. Actual memberships, invitations, and
private boards remain broker-authorized. Full team snapshots are captured at
phase barriers; each decision receives only its own memberships. Team changes
and private communication are observable coordination events, not invented
coalition labels. See [dynamic team semantics](dynamic-teams.md), including
since-join message history. Members can still communicate/change membership
during a decision turn; that ordering is retained in the coordination trace.

Every run gets fresh broker credentials, workspaces, private homes, and local/
shared memory, including between conditions. Native model participants must use
Bubblewrap. Custom command participants may explicitly use trusted local mode;
that mode cannot establish hostile-agent information confidentiality. The
default broker mode is Bubblewrap; `broker_sandbox: local` explicitly trusts the
host broker. Read-only dependencies cannot contain the current owner output or
an ancestor. Keep declared dependencies code-only; external services, shared
files, native auth accounts, and configured environment values can still create
confounds and belong in the study design.

## Manifest, baselines, and analysis

Manifests require `version: 1`, a conservative `id`, a managed `team` path, bounded
`trials` / `seeds`, `randomization_seed`, trial and phase timeouts, game options,
and explicit conditions. Unknown nested fields and multiple YAML documents fail.
Planning makes no participant/provider calls. Every condition appears once in
each seed/repetition block, in a deterministic randomized order by default. Runs execute
serially; participants within a phase execute concurrently. The sample varies
both visibility and reward to cover implementation paths. It is not a causal
comparison: use a declared factorial or vary one factor at a time for a study.

Three independent scripted comparators run against exactly the same hidden
cases and resource accounting: all-defect, seeded random actions, and an informed
cooperative oracle that funds the productive project when feasible and
immediately socially beneficial. The oracle comparator is a declared strategy,
not a proof of optimal long-horizon planning. Its choices are never supplied to
participants. The generic `Scenario` / `Episode` contracts allow additional
versioned environments without delegating ground-truth scoring to a model.

Optional `counterbalancing: latin_square` assigns cyclic condition orders over
the seeded, shuffled seed/repetition blocks. Every condition occupies every
serial position equally; `trials * len(seeds)` must be divisible by the condition
count, so a partial cycle is rejected. The base condition permutation is seeded,
and the exact schedule is frozen in `plan.json`. This balances serial positions;
it does not promise first-order carryover balance or seed native model sampling.
Omitting the field retains the historical randomized plan and manifest hashes.

Named `role_assignments` replace the source role for the specified initial actor
IDs in that condition. Unspecified actors retain their source roles. Named
`personality_profiles` append a declared behavioral prompt to the specified
actors. Select profiles with condition fields `role_assignment` and
`personality`; unknown profiles/actors and empty prompts fail validation.
The controlled-game protocol remains appended to every role. These are prompt
treatments, not personality measurements. Profile labels and exact text are
frozen in the manifest; the resolved prompt/configuration is hashed separately
for every trial. Spawned participants retain their declared template role;
initial-actor assignments do not implicitly alter descendants.

See [the role/personality pilot](https://github.com/pagel-s/agentmux/blob/main/examples/research/roles-personality.yaml) for
a complete two-factor manifest with reversed role assignments and exact order
counterbalancing. Its scripted participants ignore prompt treatments, so it
tests collection mechanics only. A model study must separately choose adequate
independent blocks, provisioning and a native-turn budget before execution.

Try that design without any model calls, from the repository root:

```sh
agentmux research plan examples/research/roles-personality.yaml
agentmux research run examples/research/roles-personality.yaml \
  --output /tmp/amx-role-profile-pilot
agentmux research analyze /tmp/amx-role-profile-pilot \
  --csv /tmp/amx-role-profile-summary.csv \
  --behavior-csv /tmp/amx-role-profile-behavior.csv
```

This schedules 16 command-only trials and 192 task turns, with four complete
paired blocks. The selected outcome is observed consented-team count; analysis
labels the scripted, tiny pilot and its unmet 20-block preregistered minimum.
Use new output/CSV paths when changing the design. To study real participants,
prepare a separate managed team and manifest, inspect its plan, provision the
exact harness/model/auth/isolation choices, declare a positive native-task
budget, and explicitly opt in with `--allow-models`.

Analysis reports objective welfare, funding rate, credits spent, invalid
decisions, valid zero contributors on funded rounds, failed tasks, and actual
teams with multiple members, plus baseline welfare. Pairwise contrasts align
condition runs by seed and repetition. Preregistered designs support one to six
binary factors and require every factorial cell exactly once per block (at most
64 conditions). Factors are `reward_mode`, `visibility`, `teams`, `team_rewards`,
`role_assignment` and `personality`. Undeclared changes to any of these factors
or extra condition instructions are rejected. Main effects and every declared
interaction use complete paired blocks, averaged over other factor levels.
Per-decision
behavior IDs, model/harness labels, and separate validity/execution-failure
fields support reproducible behavioral coding. Per-agent uncertainty uses
run-level means rather than treating rounds as independent samples.

`preregistration.primary_outcome` selects the metric for condition
`primary_outcome_estimate`, `paired_primary_outcome_difference_to_minus_from`
and all factorial `paired_effect` estimates. Each effect names its `outcome`.
Legacy welfare summary/contrast fields retain their original meaning. An
unregistered exploratory manifest can select top-level `primary_outcome`;
conflicting top-level and preregistered choices are rejected. The default remains
`total_social_welfare`. Supported run-level outcomes are:

| Primary outcome | Definition |
| --- | --- |
| `total_social_welfare` | Sum of objective round welfare. |
| `productive_project_funding_rate` | Fraction of rounds funding the productive project; public-goods only. |
| `total_credits_spent` | Sum of objective round credit costs. |
| `invalid_action_count` | Sum of invalid decisions across rounds. |
| `funded_zero_contributors` | Sum of valid zero contributors on funded rounds; public-goods only. |
| `teams_with_multiple_members` | Distinct active teams with at least two members observed at phase barriers; repeated snapshots count once. Teams created and dissolved between snapshots are not counted. |
| `failed_task_count` | Phase tasks not submitted or verified. Infrastructure uncertainty still interrupts collection. |
| `escrow_return_fraction` | Total escrow returned / total escrow received over the run; trust-exchange only. Runs with zero escrow are unavailable, not zero, and are excluded from this metric and its paired contrasts. |

The escrow denominator can be affected by treatment; analysis explicitly warns
about this selection and reports metric-specific sample sizes. It must be read
alongside investment/escrow activity. Research CSV keeps its original leading
columns and appends primary outcome, mean and sample size; `analysis_json`
retains all contrasts, uncertainty, preregistration and evidence warnings.

Every unregistered collection is labeled exploratory. Analysis flags any
scripted command participants, incomplete evidence coverage, owner intervention,
and fewer than eight complete paired blocks. Eight is an explicit descriptive
disclosure threshold, not a power calculation or a claim that eight is enough.
Preregistered minimum-block shortfalls also retain their dedicated warning;
meeting that minimum does not establish adequate statistical power.

Sample standard errors and unadjusted 95% Student-t intervals are descriptive
and assume independent runs; repeated worlds, provider dependence, order, and
uncontrolled model sampling can violate that assumption. No automatic trait
classification, causal conclusion, significance claim, or power calculation is
made. Define behavioral outcomes and hypotheses before collecting model data.
The critical values follow [NIST's Student-t table](https://www.itl.nist.gov/div898/handbook/eda/section3/eda3672.htm),
rounded upward, with degrees of freedom capped at 30 for conservative larger-
sample bounds. Tiny-sample or degenerate intervals do not establish stability.

## Models, artifacts, failures, and resuming

Replace the command fixture team with native managed harnesses and verified
dependency/auth provisioning. The [mixed harness acceptance](mixed-harness-acceptance.md)
documents real Claude/Codex/Pi boundaries and native-tool policy. Run planning
first: repeated rounds use two turns per agent, so model studies can be costly.
Actual native execution additionally requires explicit `--allow-models`, a
strict preregistration, and positive `budget.max_model_tasks`:

```sh
agentmux research run /private/study.yaml --output /tmp/amx-model-study --allow-models
```

Agent count, roles, harness/model versions, native permissions, tools, context,
and provider settings should be held constant across an incentive comparison.
Otherwise those differences compete with the proposed explanation. The runner
uses explicit phase observations and feedback with fresh native turns by default
or the configured within-trial persistent conversation; it does not
claim to capture every native model context, tool read, or hidden state. There
are no automatic runner task retries and no billing guarantee from reported
usage. A durable ledger reserves every native task before workers start,
including interrupted attempts. Resuming or rerunning cannot reset that budget;
an absent/corrupt ledger fails closed. Task counts bound native turns, not the
number of provider requests a native harness may make within a turn.

The private output contains the normalized manifest/configuration and hashes,
balanced plan, executable hashes and nongenerative version/auth/isolation
preflight, owner-only ground truth, baseline trajectories, exact phase
instructions and results with reported usage, objective round transitions,
owner-call JSONL, complete paginated broker record export, final results, and
descriptive analysis. These files contain private messages/observations, model
outputs, and configuration. Review/redact them before sharing. CLI run/analyze
output is the descriptive aggregate, not the private observations.
`behaviors.jsonl` contains deterministic per-decision IDs and objective fields.
Export those rows separately without raw prompts:

```sh
agentmux research analyze /tmp/amx-research --behavior-csv /tmp/amx-behavior.csv
```

The overall trial and each phase have finite deadlines, including phase task
queuing, with a bounded cleanup window after workers stop. Native failures are
recorded and invalid actions are scored under the declared rule. Worker or
transport uncertainty interrupts the attempt; it never becomes an invented
decision or an automatic model rerun. The runner joins workers and stops the
broker on exit. To continue a study after the runner has exited, invoke the same
`research run MANIFEST --output OUTPUT` command again, retaining `--allow-models`
for native studies. It skips completed trials and runs those not yet started.
This differs from `research resume`, which only releases a live pause. A partial
or uncertain attempt requires stopping any remaining processes and explicitly
choosing a fresh attempt with `--rerun-interrupted`:

```sh
agentmux research run examples/research/public-goods.yaml \
  --output /tmp/amx-research --rerun-interrupted
```

Reruns create new attempts and preserve old evidence. A changed manifest or
resolved team cannot reuse an existing output. This prevents quiet reuse of
cross-condition memory and accidental replacement of failed observations.
Persistent native histories fail closed after a failed/interrupted begun turn;
if later phases would need that blocked history, the run interrupts for explicit
owner resolution rather than silently resetting or starting a fresh history.

Behavior CSV schema 2 also retains selected projects/partners, received escrow,
reward pools, parent IDs and phase-task references without prompts. Population
policy is verified against the frozen manifest; dynamic trajectories carry an
initial-fixed-population baseline scope and comparability warnings. See the
[control center guide](usability-control-center.md).
