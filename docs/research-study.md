# Plan and run a controlled study

Use a declared design to compare coordination conditions while keeping the
participant population, environment and execution settings explicit. Research
supports [behavioral primary outcomes, role/personality prompt profiles,
one-to-six-factor designs and Latin-square scheduling](research.md#manifest-baselines-and-analysis).
The [command-only role/personality example](https://github.com/pagel-s/agentmux/blob/main/examples/research/roles-personality.yaml)
lets you inspect those controls without provider calls.

## Define the question and controls

The [factorial pilot example](https://github.com/pagel-s/agentmux/blob/main/examples/research/factorial-pilot.yaml)
crosses individual/shared points with consented private-team permission off/on.
Public board/direct messaging, roles, native tool permissions, resource limits,
game mechanics and conversation mode stay fixed. Permission is the treatment;
participants decide whether to create, invite or join a team. A helper does not
choose their strategy or force a coalition.

Declare the question, primary outcome, hypotheses, complete factorial cells,
ordered factor levels, fixed controls and analysis before collection. Choose
sample size using the intended inference and report complete paired blocks.
The example is a small feasibility design; it is not a powered behavioral study.
The runner rejects missing/duplicate cells, undeclared factor changes and varying
extra instructions. Prompt treatments do not establish intrinsic agent traits.

Keep model assignments and tool permissions explicit. A fixed mixed-model dyad
supports comparisons within that population; it does not rank the models. A
private channel for two agents may overlap with already permitted direct
communication, narrowing the intervention being tested. For broader conclusions,
consider larger populations, multiple environments, counterbalanced actor/model
assignments and independent scoring.

## Prepare and inspect without model calls

```sh
python3 scripts/research/prepare-study.py --output /tmp/amx-study-input
amx research plan /tmp/amx-study-input/study.yaml
```

Preparation discovers installed code dependencies and selects explicit private
native credential files. It does not generate responses, log in, install
packages or mount a whole host home. Configure executable/auth/model paths with
its flags and inspect the generated study before starting. Native monetary
options and usage reports do not establish actual charges or a total bill cap;
one task may contain several provider calls. The durable ledger limits task
starts across completion, failures and interrupted reruns.

## Collect and inspect observations

Use the same frozen executable and inputs for the collection so rebuilds cannot
change broker/worker code between cells. Select available models explicitly and
verify their tools and isolation separately before consuming the study budget.
Then opt into native turns:

```sh
amx research run /tmp/amx-study-input/study.yaml \
  --output /tmp/amx-native-study --allow-models
amx research analyze /tmp/amx-native-study --csv /tmp/amx-native-summary.csv \
  --behavior-csv /tmp/amx-native-behavior.csv
```

The runner has no output-dependent early stopping or automatic native retry.
Failures and invalid game actions are reported separately. Infrastructure
uncertainty, budget exhaustion and deadlines interrupt collection and preserve
artifacts. Do not rerun an uncertain native turn as though it never happened.

Private artifacts include the preregistration/hash, immutable world truth,
exact task payloads, behavior IDs, objective actions/costs/payoffs, reported
native usage, preflight versions/executable hashes, broker coordination and
analysis. Keep these records with the study. Raw owner/agent state and native
authentication caches can contain sensitive data; review exports before sharing.

Report paired main effects and interactions with their assumptions, intervals,
complete block counts, invalidity, execution failures and actual team formation.
Check extraction against adapter-visible final answers before interpreting
behavioral outcomes. An operationally complete run can still have inadequate
measurement validity or statistical power.

## Trace and continuation safeguards

New native research turns require `trace.enabled: true` for every participating
native agent and eligible spawn template. Preparation sets this explicitly.
Traces retain adapter-visible event boundaries for final-answer extraction;
malformed non-JSON framing is a protocol failure. Trace capacity is finite and
exhaustion stops execution. Trace coverage does not reveal hidden reasoning or
provider-internal context.

Execution provenance records the decision protocol and hashes of the runner,
worker/broker executable, native entrypoints and regular absolute file arguments.
Continuation checks that fingerprint before starting more turns, including at
phase boundaries. Credentials can refresh independently; the fingerprint does
not pin provider weights, all imported dependencies, remote infrastructure or
the kernel.

Completed phase evidence must match its declared configuration, trial and
durable task reservations. Duplicate or inconsistent result cells are rejected.
Analysis reports descriptive quality warnings and complete paired-block counts.
Missing traces, invalid actions, failures and inadequate sample sizes still
limit interpretation when collection completes.
