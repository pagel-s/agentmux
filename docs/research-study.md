# Preregistered native feasibility pilot

This page describes the specific 31 October 2026 feasibility design and its
reproduction, rather than the full current design schema. Current research
supports [behavioral primary outcomes, role/personality prompt profiles,
one-to-six-factor designs and Latin-square scheduling](research.md#manifest-baselines-and-analysis).
The [command-only role/personality example](https://github.com/pagel-s/agentmux/blob/main/examples/research/roles-personality.yaml)
is a starting point for inspecting those controls without provider calls.

The concrete design is [factorial-pilot.yaml](https://github.com/pagel-s/agentmux/blob/main/examples/research/factorial-pilot.yaml).
The [2026-10-31 collection and its measurement caveat](research-pilot-results.md)
are recorded separately; its original scores were preserved.
It holds the mixed-model population fixed and crosses two binary factors:
individual/shared points and consented private-team permission off/on. Public
board/direct messaging, roles, native tool permissions, resource limits, game
mechanics, and conversation mode remain constant. Permission is the treatment;
agents decide naturally whether to create/invite/join a team. No helper chooses
their strategy or forces a coalition.

The pilot uses two paired worlds, two rounds, two phases, and two actors:
eight independent fresh runs and **64 native task slots**. One actor uses exact
pin `claude-haiku-5-5`, the other `gpt-6.1-sol`. These IDs were observed in actual
local native metadata; an additional tools-off isolated Haiku availability turn
returned the expected response under a configured native `$0.10` option before
the study. Billing was not measured. The tool permissions of the two harnesses
differ, so this is a within-fixed-dyad treatment comparison, not a model ranking.

The preregistration declares the question, primary outcome (total objective
welfare), hypotheses, complete factorial cells, ordered factor levels, fixed
controls, analysis, and a minimum of 20 paired blocks for stronger work. This
two-world feasibility pilot is explicitly underpowered. The runner rejects
missing/duplicate cells, changes to undeclared condition factors, and varying
extra instructions. Confirmatory trait/personality conclusions are not an
outcome of this pilot.

## Prepare and inspect without model calls

```sh
python3 scripts/research/prepare-study.py --output /tmp/amx-study-input
agentmux research plan /tmp/amx-study-input/study.yaml
```

Preparation discovers installed code-only dependency closures and selects
explicit private native credential files. It does not generate text, change
logins, install packages, or mount a whole host home. Executable/auth/model paths
are configurable with its flags. Claude has a native per-turn `$0.10` option;
the study makes no inferred claim about actual charges or a Codex monetary cap.
Native tasks can contain multiple provider calls. The durable study ledger
limits task starts across normal completion, failures, and interrupted reruns.

## Run and inspect the recorded observations

Use a current uniquely named executable for the entire collection so concurrent
development rebuilds cannot change broker/worker code between cells:

```sh
agentmux research run /tmp/amx-study-input/study.yaml \
  --output /tmp/amx-native-study --allow-models
agentmux research analyze /tmp/amx-native-study --csv /tmp/amx-native-summary.csv \
  --behavior-csv /tmp/amx-native-behavior.csv
```

There are no runner retries or output-dependent early stopping. Native failures
and invalid game actions are reported separately. Infrastructure uncertainty,
budget exhaustion, or deadlines interrupt collection and preserve evidence.
Do not rerun an uncertain native turn as though it never happened.

The private artifacts include the preregistration/hash, immutable world truth,
exact task payloads, per-agent behavior IDs, objective actions/costs/payoffs,
native task usage where reported, preflight versions/executable hashes, broker
coordination, and descriptive analysis. Record the chosen worker binary hash
with the study. Never publish raw owner/agent state or native auth caches.

Main effects are paired differences averaged over the other factor. The
interaction is a paired difference of differences. Report means, assumption-
dependent 95% intervals, the number of paired worlds, invalidity/execution
failure, and actual team formation. A two-agent team channel can overlap with
already permitted direct communication, so its opportunity structure is narrow.
Increasing population/tasks, testing multiple environments, counterbalancing
actor/model assignments, and a separate powered design are required before
broader claims about self-organization or durable behavioral characteristics.

## Safeguards for subsequent collections

New native research turns require `trace.enabled: true` for every participating
native agent and eligible native spawn template. The preparation script now
sets this explicitly. Traces retain adapter-visible event boundaries so final
answer extraction can be audited; malformed non-JSON framing remains a reported
protocol failure. Trace capacity is finite and exhaustion stops execution.
These changes do not replace or rescore the original untraced pilot.

Execution provenance records the decision protocol and hashes of the runner,
worker/broker executable, native entrypoints and regular absolute file arguments.
Continuation that would start more turns checks the fingerprint before proceeding,
including phase boundaries. Use the same frozen executable and input files for
the collection. Credentials remain independently refreshable; provider weights,
all imported dependencies, remote infrastructure and the kernel are not pinned
by these hashes.

Completed phase evidence must remain associated with its declared configuration,
trial and durable task reservations. Duplicate or inconsistent result cells are
rejected instead of being silently counted twice. Analysis reports descriptive
quality warnings and complete paired-block counts. Missing native traces, invalid
actions, failures and inadequate sample sizes still limit interpretation even
when the runner successfully completes.
