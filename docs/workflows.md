# Compare, verify and hand over work

`amx flow` provides seven owner-driven workflows: independent alternatives,
revision-bound checks, cross-provider handoff briefs, bounded snapshot review,
missions with dependencies, evidence explanations and repeated comparisons.
It complements native tmux sessions and the managed broker. It does not replace
their identity, permission, delivery or recovery controls.

These commands are included in [Agentmux v0.15.0](getting-started/installation.md).
For a first checked fixture with no provider configuration, run
[`amx demo`](getting-started/first-result.md). For one coding request with defaults for
implementation, fresh reviews and acceptance checks, start with
[`amx work`](working.md). For a visual map of execution and explicit source
application, see the [illustrated journey](visual-guide.md#ask-for-a-checked-coding-change).

Workflow file handling is supported on the published Linux/macOS platforms.
Input reads reject symlinks and special files, use bounded nofollow/nonblocking
opens, and reject changed-file reads. Other platforms refuse workflow input
until a platform-safe opener has been implemented and validated.

Every plan explicitly chooses `"execution": "local"`. Without full isolation,
commands, checks and native provider tools execute with trusted host authority;
a separate Git worktree does not restrict filesystem or network access.
Workflows do not inherit a managed worker's Bubblewrap configuration. Select
[full workflow isolation](#private-storage-and-full-isolation) explicitly when
required. Use trusted owner-authored plans. Provider turns require
`--allow-models`; command plans and checks can execute owner-selected code
without that flag, within the selected isolation policy.

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
[native comparison example](https://github.com/pagel-s/agentmux/blob/main/examples/workflows/native-compare.json) asks
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
Steps run in dependency order in one mission worktree. Implementation steps
receive preceding result summaries as context. Structured review steps receive
the task, criteria and current files without predecessor narratives or earlier
reviews. The declared final checks run before the mission can finish
successfully. The original project stays separate until explicit application.

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
by the orchestration engine. Without full isolation, trusted local reviewer
tools retain their normal host authority. Configure review commands and checks
accordingly.
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

## Private storage and full isolation

New plans default to `"storage": "private"`. The run has its own snapshot Git
store, and candidates have independent object stores with no source alternates
or shared hardlinks. Source objects, refs, configuration and index remain
unchanged while collecting and running work. Explicit application may leave its
coordination file `.git/agentmux-apply.lock`; it preserves the source index and
Git objects. `"storage": "shared"` opts into legacy source-object retention.
Older saved reports retain their recorded behavior.
Private selection uses exact case-sensitive paths and raw file contents,
overriding mutable Git path settings. Verification binds selected files;
untracked ignored build artifacts are neither included nor applied.
Private snapshots and verifications pin a content-addressed path manifest, so
worker commits, index changes or ignore changes cannot hide starting or checked
paths. Missing or damaged private selection evidence is refused.
Keep each fork's sibling `<worktree>.selection.json` with its run: standalone
checks use it to restore the controller's starting path set. Private records
require `selection_sha256`; shared legacy records keep their existing format.
The cumulative manifest is limited to 200,000 paths and 32 MiB of path names;
current selected contents remain limited to 100,000 files and 512 MiB.
Private preparation refuses partial-clone and promisor repositories before
reading objects. Use a complete local repository; capture never downloads missing
objects into the source or starts a remote transport.
Native workflow profiles cannot select saved sessions. Non-Codex `-c` and `-r`
are reserved as session controls; compact short-option forms are also refused.
Use long options and script files for interpreter wrappers. Owner-selected
wrappers remain trusted to honor the fresh-session contract.

For full Linux isolation, add these fields to a private plan:

```json
{
  "storage": "private",
  "isolation": {
    "mode": "bubblewrap",
    "network": "none",
    "read_only_paths": ["/opt/selected-runtime"],
    "home_bytes": 67108864,
    "temp_bytes": 67108864
  }
}
```

This is a plan fragment, not a complete executable plan. Full isolation requires
working Bubblewrap user/PID/mount/network namespaces and capped private tmpfs;
there is no weaker fallback. Tools can write their candidate workspace. Review
workspaces are read-only. Acceptance checks may create build output in their
assigned worktree but cannot change selected content without failing verification;
they receive no credentials or network. System runtime files and explicitly selected read-only dependencies are
exposed; the original home, source project and unrelated host files are absent
from native tool access. The trusted Go controller reads the original source
and grants its own Git helper source writes only during explicit admitted apply.
Original sources may be linked Git worktrees. Only trusted controller Git receives
their exact read-only admin/common directories; tools and checks do not receive
those directories. Sources, Git metadata and private run state that overlap a
default system runtime mount are refused, since that mount would expose them.
Read-only runtime dependencies must also stay outside original source/input
trees. Source paths cannot be used to bypass snapshot selection.

Native inference can use `"network": "allowlist"` with
`"allow": [{"origin": "https://inference.example"}]`. Origins are exact scheme,
host and port. DNS resolves once at startup, rejects non-public addresses unless
explicitly pinned with `"ips"`, and the relay enforces the selected destination.
Proxy settings alone are insufficient: direct networking is denied by the
namespace. HTTPS uses a trusted per-turn inspecting proxy with an ephemeral CA.
Every HTTP/1.1 Host and HTTP/2 authority must match the granted origin; upstream
TLS hostname and certificate verification remains enabled. Only the public CA
is mounted read-only, and standard client trust variables (including
`CODEX_CA_CERTIFICATE`, `SSL_CERT_FILE`, Node, requests, curl and Git variables)
point to it. The CA signing key stays in memory. Certificate-pinned, mutual-TLS
and clients with incompatible custom trust fail closed; opaque tunnels and HTTP
upgrades are refused. Forwarded requests have a ten-minute total lifetime,
including uploads and streamed responses; shorter run deadlines still apply.
Existing managed-mode networking retains its separately documented
[CONNECT/SNI destination boundary](hardening.md). Checks have neither egress nor
provider auth even when a native candidate has grants. Runtime HOME/tmp sizes
are enforceable; workspace growth
does not have a hard disk quota.

Select native credentials explicitly per candidate:

```json
{
  "auth": {
    "coder": {"file": "/private/codex-auth.json"},
    "reviewer": {"file": "/private/pi-auth.json", "provider": "selected-provider"}
  }
}
```

`auth` belongs inside `isolation`. It accepts supported filtered Codex, Claude
or Pi credentials and hands them to only the selected candidate through sealed
anonymous data mounted read-only. The source and exposed runtime dependencies
cannot contain the selected original login. No original host login directory
or ambient provider environment is implicitly imported.

Selected credential values are pinned before snapshots and evidence are created.
Diagnostics can be redacted, but identifiers, schema fields and verification
results keep their meaning. If a selected value collides with those fields or
with final JSON/HTML bytes, publication fails closed. Very short values can make
even a fixed diagnostic unsafe, so a nonzero exit may have no printable error.
The isolated CLI defers stdout and diagnostics until the operation finishes so
it can check credentials selected by later child turns too. This buffer is
limited to 64 MiB; exceeding it cancels execution and refuses publication.
Retained run evidence remains available during execution.

Direct Antigravity’s native tools are unavailable in full isolation. Select
`"harness": "antigravity-bridge"` and an explicit `gemini-…` model instead.
Pi tools then run inside the capsule; a host adapter supplies inference only
using its existing Antigravity login. It receives no workspace tool authority.
The plan must select allowlist networking for this bridge. Its private bridge
endpoint is granted by the controller; `bridge_max_requests` defaults to 32
and is bounded to 1–10,000. Retained `bridge_usage` records admission and
rejections. `bridge_command` and `relay_executable` can explicitly select
trusted adapter/relay executables. They are owner configuration, not model
tool permissions. A request count is not a monetary cap.

## Evidence and limits

Plans may set `"process_containment": "pid-namespace"` to require the Linux
Bubblewrap lifetime boundary for native tools, checks and Git helpers, including
watcher and laboratory snapshots. The default is `"process-group"`. Required PID
containment is probed before execution and never silently downgraded. It preserves
trusted host filesystem/network authority unless `isolation` is also selected.
Standalone verification accepts `--containment pid-namespace` too. The
standalone `flow check` defaults to `--storage private` and retains its
independent fingerprint store beside the requested output at `OUTPUT.git`.
Keep that store with the verification record for later freshness checks.
`--storage shared` explicitly selects legacy source-object behavior. The
standalone check command provides process containment; use a full isolated plan
with acceptance checks to require filesystem and network isolation as well.

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

The [validation guide](workflow-validation.md) provides reproducible checks and
explains the limits of command fixtures, provider checks and isolation tests.

Plans and checks use strict JSON with unknown fields rejected. Repository paths
are relative to the shell's working directory; `--repo` explicitly overrides the
plan path. The repository needs an initial Git commit. Dirty tracked files and
nonignored untracked files are captured without changing the source index or
working files. Ignored files are excluded, so provide required ignored build
dependencies through your own environment or reproducible setup.

Put the new private run directory outside the source repository. Retained
worktrees and Git snapshot objects are intentional evidence and consume disk;
this version does not delete them automatically. Keep them for review or remove
owned run directories deliberately after preserving needed results. Legacy
shared runs may also retain source Git worktree registrations and snapshot refs;
manage those with Git rather than deleting arbitrary source metadata.
No workflow automatically commits, pushes, publishes, spends additional turns
after its limits, or grants an agent broader broker authority.
