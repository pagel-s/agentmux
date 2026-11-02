# Workflow validation record — 2 November 2026

This record covers the new `amx flow` implementation on the current local
working tree based on `e205a24`. It is development validation, not a claim of
universal correctness, production availability or security certification.
Only Codex and Antigravity with an explicitly selected Gemini model are used
for live model acceptance. No Claude model calls are made.

## Reproduce

```sh
make build
go test -race -count=1 ./...
go vet ./...
go test ./internal/workflow -run '^$' -fuzz '^FuzzWorkflowPlanRoundTrip$' -fuzztime=20s -parallel=2

python3 scripts/validate-workflows.py --binary bin/amx --output /tmp/amx-flow-validation
# Explicitly permits up to nine provider turns. Use another new output path.
python3 scripts/validate-workflows.py --binary bin/amx --output /tmp/amx-flow-native-validation --native
```

The script freezes a private copy of the executable and records its SHA256,
UTC timestamps, each case's exit status and observed provider usage. It uses
new disposable repositories and retains all evidence in the selected directory.
The external acceptance assertions are owner-authored argv outside candidate
source files. They accept a known-good implementation and reject a known-bad
one. Existing live teams and conversations are not used for these checks.
Native model IDs are explicitly `gpt-6.1-sol` and
`gemini-3.8-flash-low`; they are dated validation selections, not a promise of
permanent account or model availability.

## Defects reproduced and fixed

- Newly staged files that subsequently matched `.gitignore` were omitted.
- Git normalization hid LF/CRLF byte changes from content freshness checks.
- A patch could reconstruct different bytes from the verified candidate.
- Concurrent changes after application could produce misleading success.
- Special-file inputs could block before their file type was checked.
- Duplicate, case-aliased and Unicode-aliased JSON fields were ambiguous.
- Invalid candidate configurations could be rejected after peers started.
- Failed rendering could leave a persisted completed result.
- Cancelled mission turns could lose their interruption identity.
- Retry records could retain the prior attempt's completion state.
- Interrupted check diagnostics could leave a stale running attempt record.
- Verbose maximum-sized missions could exceed their own report reader limit.
- Review results could remain current after the source changed during review.
- Failed review collections could return overall success.
- Handoff exports inside a checked worktree invalidated their own evidence.
- Flag errors and suggested commands did not safely represent hostile text.
- Background children could modify a worktree after their parent exited.
- Antigravity could emit `SUCCESS` while recording a denied terminal action;
  the adapter did not recognize that separate denial field.

Regression tests exercise these failures with actual temporary Git repositories,
subprocesses, byte changes, FIFOs, hostile filenames and deterministic command
fixtures. Exact per-attempt evidence is retained separately from bounded
aggregate excerpts. Application previews reconstruct and check selected results
before changing the source, with explicit applied/uncertain receipts afterward.

## Recorded evidence so far

The complete repository race suite passed, including a CI-equivalent run with
`AGENTMUX_MANAGED_INTEGRATION=1` and `AGENTMUX_EXPERIMENT_INTEGRATION=1`, and the
existing broker, native bridge, tmux and recovery tests. Focused race tests
also passed for descendant cleanup, ambiguous JSON, special files, raw content,
application uncertainty, large reports and interrupted checks. All seven
command-only workflow journeys passed against a frozen executable at
`/tmp/agentmux-validation-hardened-model-free-20261102/validation.json`.

The parser fuzz run completed 644,011 executions without a decoder failure.
Linux ARM64 and macOS ARM64 workflow test binaries cross-compiled. Cross-compiling
does not establish runtime behavior on those platforms.

`make test-sandbox` passed its actual Linux namespace, network, disk, team and
interactive-workspace checks, including ten command-only native sessions.
`make test-research test-soak` passed; the bounded soak verified ten turns,
two clean restarts, all completed trace anchors and one interrupted in-flight
task without automatic replay. These made zero model calls. The existing web
console, installer (22 cases), release guards (10 cases), soak regressions
(3 cases) and acceptance verifier controls (8 cases) also passed.

CI now runs the command-only workflow acceptance script and a bounded parser
fuzz check so these checks remain repeatable after this development session.

The preliminary native suite is deliberately retained as **failed** at
`/tmp/agentmux-validation-native-20261102/validation.json`. Codex produced a
passing implementation, while Antigravity returned success without creating
the requested artifact. The external checker rejected that result. Passing
the preliminary mission/watch command did not establish a substantive review
when the reviewer returned an empty response; that condition now has a separate
explicit acceptance requirement. A subsequent captured stream identified
`denied_actions` for an attempted `ls -la`, despite the CLI's `SUCCESS` status.
That denial now fails closed and has a public-adapter regression test. The
file-tools validation profile does not widen command permissions. Subsequent
native results are recorded below.

### Final live acceptance

The final run passed **all 12 acceptance cases**, with nine actual native
turns and nine retained native event streams, from **14:39:22 to 14:43:14 UTC**.
Only Codex `gpt-6.1-sol` and Antigravity `gemini-3.8-flash-low` were selected.
The installed Codex CLI was `0.166.0`; Antigravity reported language-server
version `1.4.12` during its model inventory. This records the tested versions,
not every future provider release.

Both implementations passed the external normalization contract. The mission
completed implementation, a substantive nonempty review and final acceptance;
two rotated comparison trials completed; snapshot review, status, explanation,
HTML export, handoff creation, explicit application and source checks passed.
The source remained unchanged before application. The known-bad implementation
was rejected by its recorded acceptance check.

The frozen executable tested by that earlier run had SHA256:

```text
4299fc8c09d3f20200135305e7b25f6e63776c6420bc4eaca6dc6c9fdfb3f5ea
```

Private run evidence:
`/tmp/agentmux-validation-native-final-20261102/validation.json`.
Each nested run retains the requested model, exact inputs, outputs, checks,
artifact fingerprints and native event digest. No global provider settings or
terminal permission grants were changed. Earlier failures remain retained;
they were not relabeled or replaced with passing results.

## Guided everyday journey

The later `amx work` implementation removes the JSON-plan requirement from the
everyday path. Its preview, implementation, structured review, correction,
final review, checks, private history, patch inspection and explicit application
were exercised through the actual CLI.

Reproduce without provider calls using transparent protocol fixtures:

```sh
python3 scripts/validate-workflows.py --binary bin/amx --output /tmp/amx-guided-validation --guided
```

For the live version, add `--native`. The guided validator sets one coding
attempt per phase and uses four native turns: Codex, Antigravity/Gemini, Codex,
Antigravity/Gemini. Only these providers are selected. Models are not started
by a preview, including `--dry-run --allow-models`.

The final live guided run passed **all 13 acceptance cases**, from
**18:27:31 to 18:28:54 UTC**, with four retained native event streams. Both
structured reviews approved the exact checked revisions. Human-readable
`work show` displayed the retained patch, and status correctly reported the
applied outcome with guidance that includes new untracked files. The original
source stayed unchanged before explicit application.

Evidence: `/tmp/agentmux-work-guided-release-native-20261102/validation.json`.
The frozen executable for that run had SHA256:

```text
89312cee7f451fdd704de302e19e1f2e75ad8c02d470777cf32a79783b3dd738
```

The complete CI-equivalent race suite and static analysis passed after these
changes. A bounded parser fuzz run passed 315,482 executions. Additional real
command-process regressions exercise requested changes, malformed reviews,
reviewer file edits including EOL-only changes, stale approval metadata,
interruptions, catalog privacy/scope, safe patch rendering and complete findings
in the correction input. An independent audit reproduced a review-gate bypass
when review and verification were combined in one step; that invalid combination
is now refused before execution.

The display layer additionally verifies no-op work: a fresh approved empty patch
shows “No changes needed” without offering application. Stale or unavailable
evidence still requires inspection. Current display checks are exercised against
the retained live run and fresh command fixtures. CI runs the guided fixture
journey automatically. No human usability study or browser visual QA was
performed; browser surfaces were unavailable in this session.

## Follow-up deep review, 2 November 2026

A second independent review covered native event interpretation, review gates,
snapshot reads, integration freshness, history selection, application receipts,
the guided CLI and the acceptance validator. It reproduced and fixed nine
defects:

- Duplicate native JSON fields could overwrite failure or denied-action
  evidence. Frames now reject duplicate keys, invalid UTF-8 and excessive
  nesting before interpreting their outcome; decisive raw frames are retained.
- An empty commit during integration preview could move source HEAD without
  changing file fingerprints. HEAD is checked again immediately before apply.
  A HEAD move after application also produces an uncertain result.
- A file replaced with a FIFO between selection and opening could block raw
  fingerprinting. A nonblocking, no-follow opener now checks the selected file's
  identity and metadata; replacement and in-place mutation regressions pass.
- The first preview's suggested command could fail when private catalog parents
  did not yet exist. Generated output paths are now allocated by execution.
- Lexical timestamp sorting could select an older run as `latest`. Catalogs now
  compare parsed RFC3339 instants, including fractional seconds and offsets.
- Unrelated artifacts could exhaust receipt enumeration and hide an uncertain
  application. Artifact and receipt bounds are now handled separately.
- Damaged or unreadable application receipts could be treated as absent.
  Incomplete history now requires inspection and offers no apply command.
- The comparison validator could exit successfully despite failed evidence
  exports. Its exit status now reflects every recorded acceptance case.
- The negative control could count an infrastructure failure as successful
  rejection. It now requires the exact contract's assertion failure, a real
  process exit, and unchanged content. Regression tests cover both stdout and
  stderr diagnostics.

Human CLI output also displays actionable review findings with file and line,
and labels truncated review excerpts. An additional adversarial test confirms
that oversized review output cannot approve its truncated prefix; this existing
defense required no behavior change.

The complete CI-equivalent `go test -race -count=1 ./...` suite passed with the
managed and experiment integration flags enabled and model integration flags
disabled. `go vet ./...`, formatting and diff checks passed. New regressions
exercise real child processes and deterministic concurrent HEAD changes.
The guided journey passed all **13** cases and the advanced flow journey passed
all **12** cases using command/protocol fixtures. Evidence:

- `/tmp/agentmux-deep-review-guided-20261102/validation.json`
- `/tmp/agentmux-deep-review-flow-final-20261102/validation.json`

The executable tested by both journeys had SHA256:

```text
96e1e39c8052d306387d4b08bdac30e3f7eac36e0c0d5031eb8eeaea5b9dc82c
```

All four exact native event streams from the earlier live guided acceptance run
were hash-checked and replayed successfully through the current public harness
adapter under the race detector. Replay used temporary test fixtures, made no
new provider calls, and does not claim another live provider run. Its temporary
test overlay is retained at
`/tmp/agentmux-deep-native-replay-ifgg7iz0/overlay.json`.

The initial stricter negative-control assertion expected a traceback in stdout;
the actual command adapter retains stderr in its error field. That validator
expectation was corrected and covered by regression before the final run above.
The initial failed validation remains at
`/tmp/agentmux-deep-review-flow-20261102/validation.json`.

## Durability, recovery, containment and storage acceptance

The subsequent hardening build passed the complete CI-equivalent race suite
with managed/experiment integrations enabled and live model integrations
disabled, plus `go vet ./...`, formatting and diff checks. The separate real
namespace race run passed in workflow (4.123s) and sandbox (3.417s). It covers
session-escaped descendants after normal completion, cancellation and owner
SIGKILL; Git-filter descendants; recorded policy binding; unavailable boundaries;
and an application lease retained while the PID namespace writer is alive.

Real process fault tests also kill application owners before and after source
mutation, prove surviving writers retain their lock, refuse missing intent or
damaged history, preserve uncertainty when final receipt persistence fails, and
recover exact starting/final source without patch replay. A killed registrar
does not strand the catalog lock. Tests preserve all 1024 actual catalog slots.
Directory sync refuses FIFO and symlink replacements. Oversized sparse-file
admission is refused before Git starts adding objects.

The frozen build passed **62 CLI acceptance cases** from 19:40:06 to 19:40:15 UTC
on 2 November 2026: 18 guided plus 13 advanced cases under each of the default
process-group and required PID namespace policies. These include durable intent
inspection and explicit recovery. The two PID journeys use real Bubblewrap
namespaces and transparent provider protocol fixtures; no live provider was
called in this hardening pass.

Evidence:

- `/tmp/agentmux-boundaries-group-guided-20261102/validation.json`
- `/tmp/agentmux-boundaries-group-flow-20261102/validation.json`
- `/tmp/agentmux-boundaries-pid-guided-20261102/validation.json`
- `/tmp/agentmux-boundaries-pid-flow-20261102/validation.json`

All four journeys tested executable SHA256:

```text
ac672c21b3bb0c07234e03341bd215aa4dc52e869ea53a88da87d426f3719c80
```

CI runs the guided PID journey in the sandbox job, and `make test-sandbox`
includes the real workflow containment tests. Protective size bounds, refusal
conditions and operational boundaries below remain explicit.

## Publication readiness

The final publication pass reproduced and fixed six additional defects:
inherited `GIT_DIR` could redirect disposable validation into another repository;
direct diagnostics silently ignored requested PID containment; requested JSON
errors were omitted after some CLI validation failures; aggregate final-save
failure could return `completed`; and running watch/lab children were missing
from saved progress until they finished. Regression tests cover outside-repo
HEAD/index preservation, preflight refusal, literal JSON arguments, real
permission failures and interruption while child processes are running.

The exact source intended for publication passed the full race/integration suite
again, static analysis, all four Python validator regressions, and all **62**
fixture CLI cases under process-group/PID policies from 19:48:15 to 19:48:24 UTC.
Go **1.24.0** compiled every package and test, and relevant workflow/CLI tests
also ran successfully on that toolchain. This checks the declared minimum Go
version as well as the primary Go 1.27.3 validation environment.
The final bounded configuration fuzz run passed 80,374 executions in 21 seconds.

Evidence is retained under `/tmp/agentmux-publish-{group,pid}-{guided,flow}-20261102/validation.json`.
All four journeys used the same frozen pre-publication executable SHA256:

```text
441942b38f09729f708374420fb93cfee6deb948721c66d2b128067b94cf7867
```

The repository working agreements record the owner's standing preference to
commit and push completed validated changes and use only Codex and
Antigravity/Gemini for live agent validation. This publication pass used no live
providers. Frozen executable hashes describe the tested binaries; a rebuild
with committed version metadata can have a different executable hash.

## Enforced controls and operational boundaries

The next hardening pass replaces several previous limitations with enforced
controls. It preserves protective bounds and explicitly identifies guarantees
that require a different authority boundary or an external service.

| Area | Enforced behavior | Boundary that remains |
| --- | --- | --- |
| Escaped child processes | Required `pid-namespace` mode probes Linux user/PID namespaces and contains descendants after normal completion, cancellation and controller death. Native tools, checks and Git helpers inherit the policy. No fallback. | The default is ordinary process groups. PID containment retains trusted host filesystem, network, devices and native authentication. Independent host services are not descendants. Full hostile-code isolation belongs to the existing managed capsule boundary. |
| Interrupted application | A durable uncertain intent precedes source mutation. The same journal is finalized after source verification and syncing changed files/directories. Unresolved or damaged history blocks another application. | Exact content observation cannot establish that arbitrary unrelated host writers have stopped. Divergent or unavailable evidence stays uncertain. |
| Recovery | `work recover` / `flow recover` compare exact source HEAD/content and finalize existing evidence only. Source-writing Git inherits the application lease; recovery cannot proceed while that admitted writer holds it. | Recovery never guesses a missing outcome, replays a patch, undoes external effects or controls unrelated programs. |
| Durable records | Files and containing directory entries are synced after creation or atomic replacement. Unsupported sync semantics fail closed. Registration and application locks release after their owning processes/descriptors exit. | Filesystem, kernel and hardware must honor their sync contracts. Existing source Git objects have their own durability and retention. Tests simulate hard process interruption, not arbitrary physical storage failures. |
| Snapshot storage | Selection is admitted before Git writes objects: at most 512 MiB and 100,000 existing files. Exact hashing rechecks the bounds and records byte/file counts. Preview exposes observed selection. | These are protective selection limits, not a disk quota against trusted filters or concurrent writers. Unsupported gitlinks and nonreproducible conversions are refused. |
| Git object privacy | Run directories and records are private; canonical patches and exact snapshots are checked before application. | Snapshots and retention refs remain in the source Git object store. Its permissions, sharing, backups and garbage collection remain repository concerns. A private report does not privately relocate or securely erase those objects. |
| Provider compatibility | Native frames are bounded and validated; ambiguous or incompatible outcomes fail closed with retained evidence. Guided selections are Codex and explicit Antigravity/Gemini. | Future provider releases and arbitrary tools require validation. Recorded stream replay establishes parser compatibility with those recordings, not future service behavior. |
| Cost | Native admission, retries, parallelism and whole-run/check deadlines are bounded. Uncertain provider turns are never automatically replayed. | A native turn can contain multiple billable requests. A hard monetary cap requires a provider-side spending control; missing or delayed usage cannot support a local billing guarantee. |
| Correctness | Acceptance commands, two structured reviews, exact revision binding and explicit application gate the result. | These provide task-specific evidence, not infallible model judgments or a proof of arbitrary program correctness. Long-term reliability requires continued operational evidence. |

These boundaries are deliberate. Removing safety caps, silently downgrading a
requested boundary, treating uncertainty as success, or promising control over
an external provider would weaken the guarantees above.
