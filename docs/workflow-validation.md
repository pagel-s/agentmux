# Workflow validation record — 2–3 November 2026

This record preserves successive development runs of `amx flow` and `amx work`,
including earlier builds based on `e205a24`. Evidence and hashes belong to their
individual runs. They do not establish universal correctness, production
availability or security certification. Earlier live acceptance used Codex and
Antigravity with an explicit Gemini model. The owner now also permits explicit
Claude profiles. The model-free regression runs and the separately bounded
three-provider live checks are identified below.

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
Provider-free runs use an allowlisted environment, an empty private HOME and
private Codex/XDG paths; ambient API keys, login roots, shell/Python hooks and
Git overrides are excluded. The record labels this `private-fixtures`.
Explicit native or Antigravity-diagnostic modes instead label
`explicit-native-login` and retain the owner's login roots and relevant desktop
login/runtime references. They still exclude arbitrary API-key variables and
unrelated secrets. PATH remains owner-selected executable discovery. Environment
filtering alone does not restrict host filesystem authority; full isolation is
selected separately.
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
commit and push completed validated changes. At that point live validation
selected Codex and Antigravity/Gemini; Claude is now permitted too. That
publication pass used no live
providers. Frozen executable hashes describe the tested binaries; a rebuild
with committed version metadata can have a different executable hash.

## Private storage, isolated tools and workflow fault soak

The provider-free integration build passed **19 guided** and **14 advanced**
acceptance cases with private storage and full Bubblewrap isolation. Its strict
one-cycle workflow soak passed **all 11 scenario classes**, **143 assertions**,
and observed **2,294 process identities**, from **21:23:53 to 21:24:19 UTC** on
2 November 2026. The runner reaped **891** adopted exited fixture processes and
required no surviving observed descendants. These are finite integration runs,
not a multi-day reliability result or a live isolated-provider acceptance run.

Evidence:

- `/tmp/agentmux-guided-full-integration-20261102/validation.json`
- `/tmp/agentmux-flow-full-integration-20261102/validation.json`
- `/tmp/agentmux-soak-full-integration-current2-20261102/soak.json`

All three used executable SHA256:

```text
4e4a96d91234a4f70fb1d56b20237212308bf1af3b03dd45ffb011cf979dcc95
```

The first full soak remains failed at
`/tmp/agentmux-soak-full-integration-current-20261102/soak.json`. It exposed an
incorrect fixture oracle: a write to an absolute `/tmp` path can succeed inside
the capsule’s private temporary filesystem. The corrected probe verifies host
secret read denial, host outside-write absence and host listener isolation.
A separate regression exercises the actual probe with private temporary writes
and confirms that a host write fails the oracle. Failed evidence is retained.

Reproduce a new bounded run after building the intended source:

The acceptance and soak harnesses require Linux with `/proc` and pidfd support
for identity-safe process observation and cleanup. Unsupported hosts fail before
workload admission. This harness requirement is separate from the CLI's macOS
build support.

```sh
python3 scripts/test-validate-workflows.py
python3 scripts/test-soak-workflows.py
python3 scripts/validate-workflows.py --binary bin/amx --output /tmp/amx-new-guided-full --guided --storage private --isolation bubblewrap
python3 scripts/validate-workflows.py --binary bin/amx --output /tmp/amx-new-flow-full --storage private --isolation bubblewrap
python3 scripts/validate-workflows.py --binary bin/amx --output /tmp/amx-new-flow-shared --storage shared
python3 scripts/soak-workflows.py --binary bin/amx --output /tmp/amx-new-workflow-soak --iterations 10 --duration-seconds 1800 --seed 20261102 --containment pid-namespace --storage private --isolation bubblewrap
```

Each output must be new. `make test-workflow-soak` builds and allocates a private
output parent automatically. Ten iterations mean ten seeded permutations of all
11 scenario classes: sustained guided history, actual acceptance failure,
structured review rejection, stale source, stale result, cancellation, owner
SIGKILL, source-apply crashes before and after mutation, concurrent application
and the file/network boundary probe. Guided requests change each iteration,
retain one history and check exact turn counts, source staging and owner bytes.
Fault cases inspect durable uncertainty, held leases and recovery without patch
replay. The duration is a maximum budget, not a minimum time commitment.
`--scenario` explicitly narrows coverage; without full isolation the boundary
case is recorded as excluded rather than passed.

The soak freezes the executable and harness with hashes, uses an empty private
HOME, excludes ambient credentials/Git overrides, and admits no live providers.
It records per-case commands, exit status, stdout/stderr and observed Linux
process identities including runtime-thread children. It acts as a subreaper
and signals only pinned pidfds after checking process identity. Descendant
observation is bounded and is not a hostile-process containment boundary.
If the soak itself is
killed, its admitted CLI has a parent-death SIGKILL; incomplete manifests remain
incomplete. Artifact bytes are checked between quiescent cases with a final
record reserve; this is an evidence budget, not a hard workspace disk quota.
CI runs both safety suites, explicit shared compatibility, full isolated fixture
journeys and one strict soak cycle in the namespace-capable sandbox job. Failed
model-free evidence is retained as a short-lived CI artifact.

Independent review must start a fresh context each round. Supply only the task,
acceptance criteria and current source/diff. Do not supply implementation chat,
previous reviewer conclusions or approvals. Implementation feedback and stream
replay are useful validation but do not count as a new independent approval.
The guided runner follows the same separation: each review receives a new
session with current files and criteria, and its exact revision and fresh-context
requirement are recorded and checked before application.

## Frozen hardening build — 3 November 2026

The final Go/build source manifest contains 574 files; its SHA256 is
`c27e0de2c385c2db5adae509ddf843be8f31dc31bea0d700df0f6a8c96aa9d0c`.
All entries were rechecked after validation with no source changes. The frozen
Go 1.27.3 executable has SHA256
`c69df172975f4387e1d521e3343d98cec21ea2d96a382f4a5550e2330fe84573`.
It predates the publication commit's version metadata. Evidence is retained
privately under `/tmp/agentmux-publish-20261103-2942z0ya`; these local paths are
not portable published artifacts.

| Check | Result on this build |
| --- | --- |
| Complete race suite, managed and experiment integration enabled | Passed in 342.25 seconds; 2,669 test/subtest passes across 37 tested packages; two packages have no tests. |
| Complete minimum-toolchain suite, Go 1.24.0 | Passed in 210.36 seconds with the same 2,669 test/subtest passes. |
| Actual Linux namespaces and installed-native protocol fixtures, race enabled | Passed in 426.75 seconds; 726 test/subtest passes across authentication, AG bridge, egress, sandbox and workflow packages. No provider inference. |
| Workflow parser fuzzing | Passed 252,230 executions in 21.028 seconds. |
| Static analysis and portability | `go vet ./...` passed. All packages and tests compiled for Darwin ARM64; this does not establish macOS runtime behavior. |
| Owner console interaction regressions | Node test passed. The full and Go 1.24 suites include the real tmux session-search integration. |

The full and minimum-toolchain records each contain 67 skip events, including
the two packages without tests. Optional installed-provider, resource and other
explicitly gated integrations remain visible in their JSONL/status records.
The separate namespace/native-fixture suite contains seven skip events; its
selected real-isolation and installed Codex/Pi endpoint fixtures ran. Counts
include subtests and should not be read as independent end-to-end scenarios.
Records: `full-race.status.json`, `go124-full.status.json`,
`native-fixtures-race.status.json`, their complete JSONL/logs, `fuzz.log`,
`vet.log` and `darwin-compile.log`.

Three additional real tmux search journeys passed on a distinct corrected
search build with SHA256
`5ea650c563f32b3a627cead245b9b68a4eeb6ec98a34c885ffd25fd183d4a2ac`.
Their records are
`/tmp/agentmux-release-final-20261103-1oyfkhgu/search-fixed-uat.json` and
`search-fixed-uat-{0,1,2}.log` in that directory; these are supplementary
implementation checks, not three more runs of the frozen publication binary.

### Model-free acceptance on the frozen build

All six complete CLI matrices passed with zero provider turns and no inherited
provider keys. The environment profile is `private-fixtures`.

| Mode | Private storage | Explicit legacy shared storage | Private storage with full isolation |
| --- | --- | --- | --- |
| Advanced `flow` journey | 14 cases | 13 cases | 14 cases |
| Guided `work` journey | 19 cases | 18 cases | 19 cases |

The records are `flow-private-release/validation.json`,
`guided-private-release/validation.json`, `flow-shared-release/validation.json`,
`guided-shared-release/validation.json`, `flow-full-release/validation.json` and
`guided-full-release/validation.json` under the evidence root. Each records
`passed: true` and `completed: true`, the frozen executable hash and the exact
runner/helper hashes. The validator safety suite passed 38 tests, including
malformed revision/policy evidence and completion-publication failures.

The acceptance runner SHA256 is
`d58708cd0addb880ad3bdf27d1c112029dcbcb25870bc158d2d345218afaec61`.
The shared process helper SHA256 is
`6247562568f9b67624807075e29240b531df2007394182cfb1b88b2d4bf9387a`.
Consumed Git/SHA256 evidence requires valid strings; booleans, numeric aliases,
missing private selection pins and mismatched revision pins are rejected.
Mission assertions follow the complete revision chain through writing,
read-only review, verification-only steps and the final artifact. Review roles
and fresh context are required explicitly. Signaling support is probed before
workload admission; unsupported environments fail closed.
`diagnostic.json` records only `cases_completed`/`cases_passed` with an explicit
case scope; `validation.json` is its single run-completion authority. Broken
output, failed cleanup/publication and SIGKILL before that commit cannot be
inferred successful from the diagnostic case record.

### Ten-cycle strict fault soak

The reviewed soak completed ten seeded permutations of all 11 scenario classes:
**110 cases**, **4,030 assertions**, **33,971 observed process identities** and
**13,540 reaped adopted fixture processes**. Every case passed, with no coverage
exclusions or model calls. Private storage, PID namespaces and full Bubblewrap
isolation were required. The run lasted from **08:54:49 to 09:04:06 UTC on
3 November 2026** (9 minutes 17 seconds), within its 1,800-second budget.

Record: `soak-final/soak.json` under the frozen build's evidence root. It records
`state: passed`, `completed: true`, `iterations_completed: 10` and seed
`20261103`. Per-case commands, outputs, exact source/staging observations,
uncertain apply/recovery evidence and process identities remain retained.
Observed-tree cleanup is finite operational evidence, not a guarantee about
unobserved hostile processes or independent host services.

The soak runner SHA256 is
`f0246430ad9cbba31791b22bd472d8939493a68eb03e861450618e8ed2097a69`;
the helper is the `624756…` version recorded above. The run froze and checked
the then-current regression suite SHA256
`1007ef3eafc8bbbfac00bdf6bc51ef0abf9a854fe26e62d739af001b903ae64d`.
That suite passed all 29 tests with actual namespace integration and no skips.
The runner/helper were unchanged throughout the long run.

A subsequent test-only CI retention patch expanded the regression suite to
32 tests; all passed with actual namespaces and zero skips in 156.832 seconds.
The exact validated test file was then copied into the repository and its
three focused retention tests passed again. Its SHA256 is
`187ad690aca3c80dba9b1132c500d586e427f8f4e0dbdf583fec254a1caad907`.
The production runner/helper and validator did not change. This distinguishes
the long soak's frozen regression-test provenance from the published
retention-only test update.

CI explicitly enables both real namespace and real-CLI mutation integrations.
`AGENTMUX_WORKFLOW_TEST_EVIDENCE_ROOT` selects a private parent for retained
unique `0700` integration directories; when configured, every outcome remains
available, including failures caught by `subTest`. Without it, tests retain
their ordinary temporary cleanup. Invalid empty, relative, file or symlink
sinks are refused. Failure artifacts include this directory and hidden Git
stores/markers. The exact retention patch and CI configuration received a
separate fresh-context review with no actionable findings.

Three provisional long runs were deliberately interrupted while independent
reviews identified missing assertions. Their nonpassing records remain in
`soak-pre-semantic-fix/`, `soak-pre-review-bindings/` and
`soak-pre-final-chain/`. They are not counted as passing final evidence. Their
relocation records identify original absolute paths; these archived records
are not live application inputs.

Final independent scope reviews started with fresh contexts. They covered
private storage/application, isolation/authentication/network/publication,
workflow guides, validator revision/role semantics, and the soak/process
helper. Reviewers' default tool sandboxes sometimes denied namespace creation;
those probes failed closed and remain distinct from the authorized host
namespace runs that passed. These are scoped reviews and finite validation,
not universal correctness or a multi-day reliability result.

### Isolated live provider checks

Each profile received only a synthetic disposable repository, a fresh session,
one admitted turn, a 120-second native deadline and a 150-second workflow
budget. Each used private storage and required Bubblewrap/PID isolation. The
external checks required exactly `HELLO_AGENTMUX\n` in the new file and the
original README unchanged. All three finished verified, passed checks and left
the original source and its Git files unchanged.

| Explicit profile | Observed result | Retained record |
| --- | --- | --- |
| Codex 0.166.0, `gpt-6.1-sol` | Actual file tool completed, one attempt. Inspected HTTPS fallback succeeded after the strict proxy refused a WebSocket upgrade. | `live-codex/report.json`, `live-codex-validation.json` |
| Antigravity/Gemini `gemini-3.8-flash-low`, isolated Pi 1.4.0 tools | Actual file tool completed, one attempt; two inference requests admitted of four allowed. The selected host inference adapter remains trusted. | `live-gemini/report.json`, `live-gemini-validation.json` |
| Claude 2.1.317, `claude-haiku-5-5` | Actual file tool completed, one attempt, with a selected filtered login and explicit HTTPS origins. The configured native USD 0.25 option was not a measured billing guarantee. | `live-claude-corrected/report.json`, `live-claude-corrected-validation.json` |

The first Claude fixture stopped at local MCP schema validation because its
empty configuration omitted the required `mcpServers` record. Its uncertain
result remains retained under `live-claude/`. A separately recorded fresh
fixture corrected that local configuration; the engine did not retry the
uncertain turn. These checks establish the recorded versions and task, not
future provider compatibility or arbitrary task correctness. No Agentmux source
or design was sent in these synthetic live checks.

## Documentation and connection-layout follow-up — 3 November 2026

After the workflow hardening commit, a separate frontend/documentation pass
added deterministic grouping by enabled connections, collision avoidance around
manual pins, and an **Auto arrange** control. Pins are stored separately for
each runtime session and study attempt. Denied previews do not attract nodes;
unchanged topology stays stable. Layout changes do not grant permissions.

All **56 tracked Markdown files** were audited. The pass refreshed 20 guides
and references, added four accessible SVG diagrams, and preserved the dated
v0.11.0 screenshots. All **358 local links, assets and anchors** passed the
link check; the strict MkDocs build passed. The SVGs were rasterized and
visually inspected during implementation. A separate fresh-context docs review
checked source/CLI agreement, links and SVG structure with no actionable
findings; independent raster inspection was unavailable in that review.

The frontend suite passed **119 tests with no skips**, including eight new
regressions for topology changes, denied previews, manual and keyboard pins,
40-card collision avoidance, unchanged-layout stability, persistence and stale
responses across sessions/attempts. A fresh-context code review found no
actionable issues and also checked 1,000 seeded layouts with 1–40 cards for
finite positions and nonoverlap. Browser checks against a model-free loopback
fixture confirmed connected groups, keyboard pinning, denied previews, Auto
arrange, saved reset after reload, and desktop/narrow rendering. Automatic
approval review rejected the browser permission toggle, so connection-edit
behavior is covered by the automated regressions rather than that browser
action. No provider was invoked for this follow-up.

Follow-up evidence is retained locally under
`/tmp/agentmux-frontend-final-20261103/`. All **574 files** in the earlier
Go/build-source manifest remained byte-identical. Embedded web assets changed,
so the frontend follow-up was rebuilt separately; its unversioned validation
binary SHA256 is
`1b3f5f3657674ab1dcadb1687be5362c10109be1049ebe9de9e71283410d5b51`.
The full CLI package passed again with the race detector (96.479 seconds)
and with minimum Go 1.24 (41.769 seconds). Initial probes in the default tool
sandbox failed because local broker sockets were denied; their logs remain
retained separately from the passing runs with authorized local socket access.
The earlier full race, namespace, soak and live-provider evidence above belongs
to its explicitly frozen binary, not this newly embedded frontend build.

## Enforced controls and operational boundaries

The current hardening pass enforces the controls below. It preserves
protective bounds and identifies guarantees that require a different authority
boundary or an external service.

| Area | Enforced behavior | Boundary that remains |
| --- | --- | --- |
| Escaped child processes | Required `pid-namespace` mode probes Linux user/PID namespaces and contains descendants after normal completion, cancellation and controller death. Native tools, checks and Git helpers inherit the policy. Full Bubblewrap isolation also requires this boundary. No fallback. | The default without full isolation is ordinary process groups. PID containment alone retains trusted host filesystem/network authority. Independent host services are not descendants. |
| Filesystem, authentication and network | Explicit `isolation.mode: bubblewrap` restricts native tools to selected workspaces, system runtime and explicit read-only dependencies. Reviewers receive a read-only worktree. Checks may create build output in their assigned worktree, receive no network/auth, and fail verification if selected content changes. Selected filtered native credentials are read-only anonymous data. Direct networking is denied. Workflow HTTPS is inspected with a per-turn CA: HTTP/1.1 Host and HTTP/2 authority, pinned dialing and upstream TLS are checked. The isolated AG inference bridge is explicit. | The Go controller and selected host inference adapter remain trusted. Broad runtime grants expose their contents. Independent services and unrelated host writers remain outside controller authority. Certificate-pinned, mutual-TLS or incompatible custom-trust clients fail closed. Existing managed mode retains its documented CONNECT/SNI destination boundary. Full isolated live-provider acceptance is distinct from protocol-fixture validation. |
| Interrupted application | A durable uncertain intent precedes source mutation. The same journal is finalized after source verification and syncing changed files/directories. Unresolved or damaged history blocks another application. | Exact content observation cannot establish that arbitrary unrelated host writers have stopped. Divergent or unavailable evidence stays uncertain. |
| Recovery | `work recover` / `flow recover` compare exact source HEAD/content and finalize existing evidence only. Source-writing Git inherits the application lease; recovery cannot proceed while that admitted writer holds it. | Recovery never guesses a missing outcome, replays a patch, undoes external effects or controls unrelated programs. |
| Durable records | Files and containing directory entries are synced after creation or atomic replacement. Unsupported sync semantics fail closed. Registration and application locks release after their owning processes/descriptors exit. | Filesystem, kernel and hardware must honor their sync contracts. Existing source Git objects have their own durability and retention. Tests simulate hard process interruption, not arbitrary physical storage failures. |
| Snapshot storage | Private snapshots default for new plans and guided work. Current selected content is limited to 512 MiB and 100,000 existing files. Raw snapshot streaming rechecks the bounds and records byte/file counts; metadata and imported base objects have separate bounds. Preview exposes observed selection. | Selection limits are not a hard workspace disk quota. Private HOME/tmp caps are separate. Unsupported gitlinks and nonreproducible conversions are refused. |
| Git object privacy | Private run stores and candidate stores are independent, with no source alternates or hardlinked objects. Capturing/running leaves source objects, refs, config and index unchanged. Standalone `flow check` defaults to an independent `OUTPUT.git` fingerprint store. | Explicit `storage: shared` and old reports retain legacy source storage. Keep the fingerprint store for freshness. Explicit apply may retain its coordination lock file. Run deletion cannot securely erase backups or independent copies. |
| Provider compatibility | Native frames are bounded and validated; ambiguous or incompatible outcomes fail closed with retained evidence. Guided defaults are Codex and explicit Antigravity/Gemini; profiles can explicitly select Claude. | Future provider releases and arbitrary tools require validation. Recorded stream replay establishes parser compatibility with those recordings, not future service behavior. |
| Cost | Native admission, retries, parallelism and whole-run/check deadlines are bounded. Uncertain provider turns are never automatically replayed. | A native turn can contain multiple billable requests. A hard monetary cap requires a provider-side spending control; missing or delayed usage cannot support a local billing guarantee. |
| Correctness | Acceptance commands, two structured reviews, exact revision binding and explicit application gate the result. | These provide task-specific evidence, not infallible model judgments or a proof of arbitrary program correctness. Long-term reliability requires continued operational evidence. |

These boundaries are deliberate. Removing safety caps, silently downgrading a
requested boundary, treating uncertainty as success, or promising control over
an external provider would weaken the guarantees above.
