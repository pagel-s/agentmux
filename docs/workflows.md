# Compare, verify and hand over work

`amx flow` provides seven owner-driven workflows: independent alternatives,
revision-bound checks, cross-provider handoff briefs, bounded snapshot review,
missions with dependencies, evidence explanations and repeated comparisons.
It complements native tmux sessions and the managed broker. It does not replace
their identity, permission, delivery or recovery controls.

Workflow file handling is supported on the published Linux/macOS platforms.
Input reads reject symlinks and special files, use bounded nofollow/nonblocking
opens, and reject changed-file reads. Other platforms refuse workflow input
until a platform-safe opener has been implemented and validated.

Every plan explicitly chooses `"execution": "local"`. Commands, checks and native
provider tools execute with trusted host authority; a separate Git worktree is
not a filesystem or network sandbox. These workflows do not inherit a managed
worker's Bubblewrap configuration. Use trusted owner-authored plans. Provider
turns require `--allow-models`; command plans and checks can execute arbitrary
owner-selected code without that flag.

## Try the complete journey without model calls

Build `amx`, then create a small separate repository. Run these from your
Agentmux checkout; use fresh temporary paths if any already exist:

```sh
mkdir /tmp/amx-flow-project
git -C /tmp/amx-flow-project init
python3 -c "from pathlib import Path; Path('/tmp/amx-flow-project/README').write_text('Workflow fixture' + chr(10))"
git -C /tmp/amx-flow-project add README
git -C /tmp/amx-flow-project -c user.name='Workflow demo' -c user.email=demo@example.invalid commit -m 'Initial fixture'

amx flow run examples/workflows/compare.json --repo /tmp/amx-flow-project --output /tmp/amx-comparison
amx flow status /tmp/amx-comparison
amx flow explain /tmp/amx-comparison
amx flow handoff /tmp/amx-comparison --candidate calculated --output /tmp/amx-handoff.md
```

The two candidates start from the same captured content and work separately.
Comparisons run up to two candidates concurrently by default; `max_parallel`
can select 1–4 concurrent candidate turns within the plan's overall deadline.
Each writes `answer.txt` in its own worktree. Both undergo the same exact-content
check. The source repository is unchanged. The JSON envelope names each retained
worktree, patch, result and verification. Open `/tmp/amx-comparison/report.html`
in your browser for an offline review of the results. It is generated after a
CLI comparison or mission; `flow report RUN --output FILE.html` creates another
exclusive report copy. Reports execute no model calls and contain private task
and code content.

For an actual provider-backed coding task, the
[native comparison example](../examples/workflows/native-compare.json) asks
Antigravity and Codex to implement `normalize_tags` in separate worktrees. External
Python assertions check normalization, error behavior, generators, fresh outputs
and nonmutation. Installed authenticated CLIs are required. Opt in explicitly:

```sh
amx flow run examples/workflows/native-compare.json --repo /tmp/amx-flow-project --output /tmp/amx-native-comparison --allow-models
```

Provider startup, tool approvals and successful execution depend on the selected
native configuration. The model-free validation does not establish that this
provider-backed task will complete on a particular account or CLI version.
The Antigravity example selects a session-only `accept-edits` profile and asks
for native file tools; it does not grant terminal-command permission. A denied
tool is a failed turn even when Antigravity's terminal record says `SUCCESS`.
Actual native events and their digests are retained beside each attempt.
The examples explicitly select `gemini-3.8-flash-low` for Antigravity and
`gpt-6.1-sol` for Codex; update those model selections deliberately when needed.

After inspection, explicitly select the result to bring back:

```sh
amx flow apply /tmp/amx-comparison --candidate calculated
```

Applying is a separate mutation. It checks the retained patch and source state,
and refuses stale or incompatible work instead of choosing a winner or merging
conflicts automatically. It neither commits nor pushes changes. Inspect the
returned result and your source diff before continuing.

## Independent checks and freshness

A check is an owner-configured executable argv, not shell text:

```json
[
  {"name": "unit-tests", "command": ["go", "test", "./..."], "timeout_seconds": 60}
]
```

Run checks against a candidate's retained worktree or a selected repository:

```sh
amx flow check --repo /tmp/amx-flow-project --checks examples/workflows/checks.json --output /tmp/amx-checks.json
```

Save verification outside the verified repository. Starting and ending Git
content trees bind the result to the checked tracked and nonignored untracked
files. A check that changes that content invalidates its result. Ignored build
outputs do not affect the content fingerprint. Later edits make the recorded
verification stale; `flow explain RUN` recomputes candidate freshness. Missing
worktrees produce an unknown freshness result, not a fresh approval.

Passing means these declared checks passed against that content. It is not a
proof of general correctness or an independent security assessment. Checks are
owner-controlled and should exercise meaningful acceptance criteria.
Each check runs in a separate process. A candidate can modify test files in its
own worktree, so tests stored there are not independent of its edits. For a
stronger acceptance gate, use an owner-maintained external verifier or explicit
assertions in the configured check argv, as the command-only examples do.

## Handoff to another native agent

`flow handoff` creates a private Markdown brief with the objective, pinned
revision, selected result, patch digest, checks and remaining uncertainty. It
does not copy native transcripts, login caches or provider-internal state.
Artifact paths in a brief are references; they are not uploaded attachments.
A remote recipient needs a separately transferred artifact.

Inspect the saved brief, then send its exact bytes through an existing broker:

```sh
amx flow handoff /tmp/amx-comparison --brief /tmp/amx-handoff.md --state /absolute/workspace/state --to reviewer --request-id review-login-fix-1
```

The owner credential queues an `instruct` operation. Existing receiver
Manual/Review/Auto settings remain authoritative; accepting a broker task does
not prove a native turn started or completed. The brief grants no permissions.

Keep the request ID. On an uncertain transport outcome, repeat the same
`--brief`, recipient and request ID to recover the original receipt. Changed
payloads under that ID are rejected. Do not regenerate a handoff for the retry:
its freshness description may have changed. Saved briefs must be regular files,
not symlinks, and at most 512 KiB. Outputs are mode 0600 and never overwrite an
existing destination.
Handoff output must be outside the source and candidate worktrees. Otherwise
saving a brief could invalidate the very revision its freshness statement
describes. Saved briefs must contain nonempty valid UTF-8 text; invalid bytes
are rejected rather than silently changed during JSON delivery.

## Bounded missions

```sh
amx flow mission examples/workflows/mission.json --repo /tmp/amx-flow-project --output /tmp/amx-mission
```

A mission's named steps reference candidates and explicit `depends_on` IDs.
A step with `"verify": true` and no candidate runs the declared checks. Combined
with a candidate, it runs that turn and checks the result before successors;
`max_attempts` permits bounded corrections after a completed turn fails checks.
Missing dependencies, duplicate IDs and cycles are rejected before execution.
Steps run in dependency order in one mission worktree, with preceding result
summaries supplied as context. The declared final checks run before the mission
can finish successfully. The original project stays separate until explicit
application.

`timeout_seconds` bounds the plan; candidate timeouts can shorten individual
turns. `max_attempts` is finite and bounded. Failed acceptance checks may allow
a configured correction attempt; an uncertain or failed native transport is
never automatically replayed. Ctrl+C cancels the active run and preserves its
recorded partial outcome. This initial workflow has no automatic resume of an
interrupted mission and does not infer that external side effects were undone.

## Quiet review of changed snapshots

```sh
amx flow watch examples/workflows/shadow.json --repo /tmp/amx-flow-project --output /tmp/amx-watch --interval 2s --max-runs 2 --duration 30s
```

Watch polls the selected source and reviews stable changed content once per
eligible revision, using separate retained worktrees. The source is not edited
by the orchestration engine. Trusted local reviewer tools still have their
normal host authority. Configure review commands and checks accordingly.
Repeated unchanged content does not start another reviewer turn. Each run keeps
its own findings and verification; a later revision supersedes earlier evidence.

The poll interval is 1 second to 1 hour, the duration is at most 24 hours, and
`max-runs` is 1–100. These limits bound admission to reviews, not monetary
provider billing. Closing the foreground command ends the watch; it is not an
installed background service or a native terminal input injector. `watch.json`
links to individual run reports.

## Repeated comparisons

```sh
amx flow lab examples/workflows/compare.json --repo /tmp/amx-flow-project --output /tmp/amx-lab --repetitions 3
```

Lab repeats the comparison against one pinned starting snapshot and rotates
candidate order. `lab.json` reports recorded outcomes, duration and usage rather
than selecting a universal winner. Repetitions are 1–20 and the plan deadline
bounds the entire collection. Lab forces sequential candidate execution to
make the rotated order meaningful; ordinary comparisons can run concurrently.

The scripted example establishes mechanics only. Meaningful team comparisons
need representative tasks, appropriate acceptance checks, human assessment,
comparable budgets and enough repetitions. Reported provider usage may be absent
or have provider-specific meanings; it is not an inferred bill. The existing
[research runner](research.md) supplies separate controlled-study capabilities.

## Evidence, storage and limits

Plans may set `"process_containment": "pid-namespace"` to require the Linux
Bubblewrap lifetime boundary for native tools, checks and Git helpers, including
watcher and laboratory snapshots. The default is `"process-group"`. Required PID
containment is probed before execution and never silently downgraded. It preserves
trusted host filesystem/network authority; it is not the managed capsule sandbox.
Standalone verification accepts `--containment pid-namespace` too.

`amx flow recover RUN_DIR --candidate ID` inspects and resolves an interrupted
application's durable journal against exact source HEAD/content. It does not
replay a patch. Omit `--candidate` for a mission or an unambiguous single result.
Unresolved or damaged application history blocks a new application. See the
[everyday guide](working.md) for recovery details and boundary requirements.

`flow status RUN` reads the stored report. `flow explain RUN` connects recorded
inputs, dependencies, candidate results, revisions, patch digests and check
evidence; it verifies retained patch integrity and checks current freshness.
It reports observable records, not hidden model reasoning or every external
filesystem/network action. Owner-editable local evidence is not provider
attestation.

The [dated validation record](workflow-validation.md) records reproduced bugs,
regression coverage, live Codex/Antigravity results and explicit limits.

Plans and checks use strict JSON with unknown fields rejected. Repository paths
are relative to the shell's working directory; `--repo` explicitly overrides the
plan path. The repository needs an initial Git commit. Dirty tracked files and
nonignored untracked files are captured without changing the source index or
working files. Ignored files are excluded, so provide required ignored build
dependencies through your own environment or reproducible setup.

Put the new private run directory outside the source repository. Retained
worktrees and Git snapshot objects are intentional evidence and consume disk;
this version does not delete them automatically. Keep them for review or remove
owned detached worktrees deliberately with Git after preserving needed results.
No workflow automatically commits, pushes, publishes, spends additional turns
after its limits, or grants an agent broader broker authority.
