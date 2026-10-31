# Finite multi-agent research environments

`agentmux research` runs repeated-round environments with private observations,
scarce resources, validated decisions, and live objective payoff feedback. It
complements the single-task [`experiment` pilot](experiments.md). The initial
built-in environment is `public-goods-v1`; the exported `Scenario` / `Episode`
interfaces separate owner-controlled mechanics from participant observations.

## Run the no-model protocol smoke

Build Agentmux, inspect the entire execution matrix, then run the included
deterministic command participants:

```sh
go build -o /tmp/agentmux ./cmd/agentmux
/tmp/agentmux research plan examples/research/public-goods.yaml
/tmp/agentmux research run examples/research/public-goods.yaml --output /tmp/amx-research
/tmp/agentmux research analyze /tmp/amx-research
/tmp/agentmux research analyze /tmp/amx-research --csv /tmp/amx-research-analysis.csv
```

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
and does not silently pool rewards or confer command authority.

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
cannot cross the phase barriers. Population stays fixed during a run.

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
each seed/repetition block, in a deterministic randomized order. Runs execute
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

Analysis reports objective welfare, funding rate, credits spent, invalid
decisions, valid zero contributors on funded rounds, failed tasks, and actual
teams with multiple members, plus baseline welfare. Pairwise contrasts align
condition runs by seed and repetition. Sample standard errors are descriptive
and assume independent runs; repeated worlds, provider dependence, order, and
uncontrolled model sampling can violate that assumption. No automatic trait
classification, causal conclusion, significance claim, or power calculation is
made. Define behavioral outcomes and hypotheses before collecting model data.

## Models, artifacts, failures, and resuming

Replace the command fixture team with native managed harnesses and verified
dependency/auth provisioning. The [mixed harness acceptance](mixed-harness-acceptance.md)
documents real Claude/Codex/Pi boundaries and native-tool policy. Run planning
first: repeated rounds use two turns per agent, so model studies can be costly.
Actual native execution additionally requires explicit `--allow-models`:

```sh
/tmp/agentmux research run /private/study.yaml --output /tmp/amx-model-study --allow-models
```

Agent count, roles, harness/model versions, native permissions, tools, context,
and provider settings should be held constant across an incentive comparison.
Otherwise those differences compete with the proposed explanation. The runner
uses explicit phase observations and feedback with fresh native turns by default
or the configured within-trial persistent conversation; it does not
claim to capture every native model context, tool read, or hidden state. There
are no automatic model retries and no billing guarantee from reported usage.

The private output contains the normalized manifest/configuration and hashes,
balanced plan, executable hashes and nongenerative version/auth/isolation
preflight, owner-only ground truth, baseline trajectories, exact phase
instructions and results with reported usage, objective round transitions,
owner-call JSONL, complete paginated broker record export, final results, and
descriptive analysis. These files contain private messages/observations, model
outputs, and configuration. Review/redact them before sharing. CLI run/analyze
output is the descriptive aggregate, not the private observations.

The overall trial and each phase have finite deadlines, including phase task
queuing, with a bounded cleanup window after workers stop. Native failures are
recorded and invalid actions are scored under the declared rule. Worker or
transport uncertainty interrupts the attempt; it never becomes an invented
decision or an automatic model rerun. The runner joins workers and stops the
broker on exit. Completed runs are skipped on resume. A partial or uncertain
attempt requires stopping any remaining processes and explicit rerun consent:

```sh
/tmp/agentmux research run examples/research/public-goods.yaml \
  --output /tmp/amx-research --rerun-interrupted
```

Reruns create new attempts and preserve old evidence. A changed manifest or
resolved team cannot reuse an existing output. This prevents quiet reuse of
cross-condition memory and accidental replacement of failed observations.
Persistent native histories fail closed after a failed/interrupted begun turn;
if later phases would need that blocked history, the run interrupts for explicit
owner resolution rather than silently resetting or starting a fresh history.
