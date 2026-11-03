# Evaluate Agentmux against alternatives

Agentmux has no established public task-quality ranking. A passing regression
suite establishes the exercised implementation behavior. It does not show that
an Agentmux team produces better patches, saves time or costs less than one
coding agent or another orchestrator. `scripts/benchmark-managed.py` measures
logical broker API throughput; it does not measure native agent task quality or
simultaneous model-worker scale.

## First verify the evaluator without models

The matched-attempt runner requires Linux, Git and Python with pidfd support.
Run from a checkout:

```sh
python3 scripts/test-compare-workflows.py
python3 examples/evaluation/run-offline-controls.py --output /tmp/amx-controls-NEW
```

Choose a new output path. The control suite creates a tiny pinned source
repository and runs deterministic actors that produce a correct artifact,
produce an incorrect artifact while exiting zero, exit nonzero with a correct
artifact, and exceed their deadline. It verifies that failures remain failures,
unknown usage/cost remain unknown, and the original Git state and dirty files
are preserved. `controls-result.json` is a harness test result, **not an AI
benchmark**. Its deterministic actors are not Agentmux or competitor results.

## Declare a matched live comparison

Copy [the example plan](https://github.com/pagel-s/agentmux/blob/main/examples/evaluation/live-plan.example.json) outside
the checkout and replace every placeholder. It deliberately refuses to run
until a real local repository and full commit object ID are supplied. The tiny
normalization task and example grader illustrate the interface; they cannot
support general coding-quality claims.

Use a preregistered task set that includes representative bug fixes, tests,
refactors and changes spanning files. Include a single-agent baseline,
Agentmux and named alternatives relevant to the intended workflow. Record exact
tool/wrapper versions and hashes, model names/settings, authentication route,
machine, dependencies, task selection and exclusion rules before execution.
Freeze checks before viewing results. Every task needs evaluator-selected
independent checks; a model's completion message is never the result authority.
Keep disclosed development checks separate from held-out grading.
Each independent check declares absolute `evaluator_files` outside the source
repository. The runner pins their resolved paths and content hashes before any
arm starts and rejects changed evaluators before or after grading. Include the
grader and every grading dependency you own. Merely running a project test
inside the actor-writable repository is a development check, not an independent
oracle. This detects changed pinned files; it does not sandbox graders or hide
their contents from a trusted local arm.
Grading must inspect without changing the candidate files. The runner hashes
all candidate files and symlink targets, excluding the root `.git` metadata,
before and after each check and invalidates a check that changes them, including
a grader that repairs an incorrect answer before exiting zero. Traversal does
not follow symlinks and has the same file-count/byte/deadline bounds. Put test
caches and generated outputs outside the candidate repository; Python bytecode
writing is disabled for invoked commands. The fingerprint is content evidence,
not isolation against an independent hostile host writer.

Each arm is an executable argv list. `{repo}` names a newly initialized private
Git repository containing the same pinned source files, `{task}` names a JSON
file with the task ID, prompt and optional disclosed `development_check`, and
`{output}` names its private artifact directory. Substitutions never invoke a
shell. Wrappers must work in the supplied repository, save relevant evidence,
return their actual status and finish with the proposed patch in that
repository. They must not push or merge into the original project. The example
Agentmux adapter runs `amx work` and explicitly applies its checked result to
that disposable repository; applying to the study copy is part of that arm's
timed workflow. You supply pinned single-agent and competitor wrappers for the
chosen native CLIs/SDKs. A tool requiring human interaction belongs in the
usability pilot, rather than being recorded as an unsupported unattended arm.

Declare which question is being tested:

- **Equal-budget comparison:** match the available model family, reasoning,
  context, total inference allowance and task time across arms. Report where a
  workflow cannot meet those constraints.
- **Whole-workflow comparison:** use each workflow's intended coder/reviewer
  configuration. Report extra model turns, time and measured expense alongside
  outcome. A four-turn team beating a one-turn baseline does not isolate an
  orchestration effect.

Run only after reviewing the commands and native profiles:

```sh
python3 scripts/compare-workflows.py --config /private/study/plan.json \
  --output /private/study/run-NEW --allow-models
```

`--allow-models` authorizes live arms and their potential charges. A fixture plan
instead requires `--fixtures`; those modes cannot be combined. The declaration
is an operator-controlled label, not an executable classifier or a sandbox.
No provider calls are required to prepare the plan or run the offline controls.

For each repeat and task, the runner rotates arm order; alternate complete
blocks reverse it. Use a multiple of twice the arm count for full position and
direction balance. This deterministic ordering is recorded and reproducible;
it is not randomized sampling. Run arms serially on otherwise comparable hosts
and control caches, provider limits, previous conversation context and service
changes. Do not silently rerun a failed attempt. Any replacement must retain
the original and be reported as an additional declared run.

## Inspect evidence and its limits

The new output directory is mode 0700. It retains the configuration, its hash,
source commit/content fingerprint, attempt repositories, actor/check logs,
artifacts and `comparison.json`. A source snapshot accepts only regular tracked
files; symlinks, submodules, ambiguous paths, trees over 10,000 files or 128 MiB
are refused. Source Git reads disable optional locks, lazy fetching and inherited
Git configuration, replacement objects and graft files. Only the pinned commit's
tree is read, not a history traversal. Partial/promisor sources are refused, and
Git transports are disabled, including on Git versions without the lazy-fetch
guard. Source preparation and materialization
share the overall deadline; each Git subprocess is additionally limited to 30
seconds. Each attempt gets its own initialized object store, with no
worktree, alternates or source-object borrowing. Dirty, staged and untracked
source files are not task inputs. Git configuration inherited by commands is
also cleared so environment-based Git redirection cannot target the source.

Read `completed`, `planned_attempts`, each attempt's `completed`/`passed`, actor
status and every independent check. Failed actors are graded too, preserving
partial outcomes. A zero actor exit cannot override a failed check, and a good
artifact cannot override a nonzero or timed-out actor. Missing/interrupted
final evidence is not success. The runner's exit status is zero only if every
planned attempt completes and passes; mixed success is normal study data and
returns nonzero. Analyze all attempted outcomes, including setup failures,
timeouts and incorrect patches, using the declared exclusion rules.

Elapsed wall time includes local command execution and process cleanup. Token
usage and cost are explicitly **unknown** in this schema, even for successful
attempts. Obtain independent provider receipts or native metering and bind them
to attempt IDs before making cost claims; unknown is never zero. No aggregate
score or product ranking is generated. Report per-task outcomes, uncertainty,
sample sizes and configuration differences instead of treating a small average
as a general winner.

The commands and checks are trusted local code. This runner is not a sandbox,
disk quota or provider billing cap. The controller uses pinned Linux process
identities to stop observed descendants when a command ends or times out.
Very short-lived detached descendants can escape observation, and independent
host services remain outside that boundary. For hostile code or strict process
containment, run each entire attempt in a separately enforced disposable
environment. Logs and repositories may contain prompts, credentials and
proprietary code; review and redact evidence before publishing it.

## Grade artifacts without revealing the arm

Before collecting results, define a rubric for functional correctness,
regressions, maintainability and unnecessary changes. Have a separate person
export proposed file changes under neutral attempt IDs, remove tool-identifying
metadata where feasible, and retain the mapping privately. Give graders the
same pinned task, acceptance contract and development context, excluding arm
names, transcripts and elapsed time. Do not label missing artifacts as missing
data to be discarded: record the declared failure outcome. Two independent
graders should score artifacts before discussing disagreements; record both
scores, agreement and an adjudicated result. State where patch style or native
metadata makes full blinding impossible. Functional checks complement grading;
neither alone proves arbitrary task correctness.

## Run a small usability pilot

Recruit 6–8 developers who did not build Agentmux and represent its intended
power-user audience. Include experienced CLI/tmux users and record prior
familiarity with Agentmux and the comparison tool. Evaluate novices separately
if making beginner-accessibility claims. Treat this as a qualitative pilot,
not a statistically powered product ranking. Counterbalance the order of Agentmux and the selected
alternative. Give each participant an equivalent disposable repository and
the same tasks: install, resume a workspace, launch two agents, delegate a task,
find an agent needing attention, inspect the exact delegated task, review/apply
a change, revoke collaboration and recover an interrupted task.
Predeclare task completion criteria and a time limit. Keep a facilitator script
and separate unassisted performance from assistance.

Record successful completion, time, navigation mistakes, permission surprises,
recovery failures and help needed. Ask participants to explain what they believe
the controls allow before revealing the answer. Follow with a consistent short
usability questionnaire and an interview. Retain failures and observations,
then fix repeated problems and repeat the pilot with fresh participants. Never
present these small-sample preferences as evidence of better coding quality.

## Evidence needed for a strong rating

A future 9/10 assessment needs delivered behavior and independent evidence,
not a larger feature list or a self-assigned score. Set thresholds before
collecting data and publish the failures as well as successes:

| Dimension | Evidence that would justify reconsidering a high rating |
| --- | --- |
| First-run usability | Fresh users complete the core journey without developer assistance; predefined completion/time thresholds hold across supported platforms. |
| Reliability | Repeated realistic native-provider runs, restart/credential-refresh faults and multi-day observation meet a declared failure/recovery target; no silently lost tasks or falsely passing evidence. |
| Task value | A representative, adequately sized matched study shows useful outcomes against single-agent and relevant competitor baselines, with blinded grading and uncertainty. |
| Safety and authority | Independent adversarial review confirms the declared boundaries and failure behavior; fixes are retained as regressions. |
| Maintainability and distribution | Released, reproducible install paths; tested upgrades; bounded compatibility support; independently reproduced fixes and contributions. |

Agree on quantitative thresholds and sample-size justification for the target
users before the study. An illustrative target is a high unassisted core-task
completion rate with no serious permission misunderstanding, but a small pilot
cannot establish that rate. Passing the evaluator or meeting one dimension's
target does not automatically upgrade the overall rating. Public stars and
downloads can describe reach; they do not establish correctness or superiority.

## Prepare a native-subscription pilot

The repository includes a small **whole-workflow pilot**, separate from a
representative benchmark or human usability study. Three selected tasks use
historical source `d52e940c473be6a3b7d7983a704e2bac9547e6de`: escaped native
credential redaction, authenticated trace display during concurrent mutation,
and a reusable worker credential-discovery refactor. A separate reviewer wrote
external Go-overlay graders and checked that the historical source fails and
the corrected implementation passes. These selected repository tasks do not
establish general coding quality or productivity.

```sh
python3 scripts/test-subscription-pilot.py
python3 examples/evaluation/subscription-pilot.py prepare \
  --output /tmp/amx-subscription-pilot-NEW \
  --go /absolute/path/to/go --amx /absolute/path/to/pinned/amx
python3 examples/evaluation/subscription-pilot.py preflight \
  --manifest /tmp/amx-subscription-pilot-NEW/pilot.json
```

Preparation makes no model calls. It freezes private grader copies and records
tool versions, executable/wrapper/grader hashes, model settings, native auth
paths and the protocol. Preflight retains only the authentication route summary.
It requires existing Codex ChatGPT authentication and Claude first-party
Pro/Max authentication, refuses API/alternate-provider environment variables,
and does not install tools or copy account credentials. Codex's
[authentication methods](https://learn.chatgpt.com/docs/auth) and
[forced login setting](https://learn.chatgpt.com/docs/config-file/config-reference#forced_login_method)
distinguish subscription access from API-key access. Model availability still
depends on the account; an unavailable model is a retained setup failure.

Review `plan.json`, then explicitly start the six live attempts:

```sh
python3 scripts/compare-workflows.py \
  --config /tmp/amx-subscription-pilot-NEW/plan.json \
  --output /tmp/amx-subscription-pilot-NEW/results --allow-models
```

Each arm gets 480 seconds. The baseline permits one native Codex invocation;
Agentmux permits four native coder/reviewer invocations, one implementation
attempt per phase and a 7m15s workflow deadline. Codex uses `gpt-6.1-sol` at
medium reasoning; the Claude reviewer uses `claude-sonnet-5-5` at medium effort,
with no automatic model/provider fallback. The selected model pins can be
changed explicitly when preparing a new study. Agentmux's retained plan can
still name its `sonnet` profile alias; the pinned native wrapper supplies the
recorded full reviewer model. This is a workflow comparison with different
inference allowances, not an isolated orchestration experiment.

Failures and limit responses remain failures. No automatic paid fallback or
retry is provided. Authentication status does not expose account billing or
extra-usage entitlement, so these local controls do not establish a financial
cap. Usage and cost remain unknown in the comparison schema. Native command
output stays in private attempt files; review it before publishing. Changed
pinned files invalidate the study: prepare a new directory, preserving existing
records. Functional grading is external and frozen, but these trusted local
arms can read host files; the graders are not hidden by an isolation boundary.

### Add an operator trial with Agent Deck

Agent Deck's [official CLI reference](https://github.com/asheshgoplani/agent-deck/blob/main/skills/agent-deck/references/cli-reference.md)
documents native session management and `session send --wait --json`.
It has its own completion/confirmation semantics and native hook setup.
An unattended adapter has not been validated here; its absence from the six
attempts is not a failed competitor result.

For an operator comparison, pin the Agent Deck release/binary and use a private
disposable profile, XDG directories and tmux socket. Give the operator the same
source commit, task, model settings, development checks and eight-minute limit.
Record setup separately from task time, capture every operator intervention,
retain the native delivery/confirmation fields, then grade the resulting patch
with the same frozen grader. Use its supported launch/send controls rather than
equating an idle pane or zero command exit with task completion. Keep results
separate until the adapter and supervision/cleanup have their own controls.
