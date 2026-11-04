# Quality targets and evidence

Agentmux's next quality improvements focus on a reliable first result,
maintainable controls and measured outcomes. Numerical ratings are judgments;
these acceptance criteria make the evidence behind a future rating reviewable.
An implemented feature or passing fixture does not earn a maturity or
productivity score by itself.

| Area | Evidence required before arguing for 9/10 |
| --- | --- |
| Technical capability | The primary released workflow works from installation through inspected application; supported provider routes have recorded native task and continuation checks with independent artifact validation. Unsupported routes are clear. |
| Safety design and assurance | Boundary regressions pass with actual isolation; an independent adversarial review covers credentials, authorization, source application and recovery; material findings are resolved and retested. |
| Regression testing | Linux and macOS installed-artifact first runs pass; failure controls prove that wrong output, failed actors, timeouts, stale evidence and interrupted publication cannot become success; conditional provider/resource coverage is recorded explicitly. |
| Maintainability | Public entry points have focused responsibilities; changes to workflow state and authority have documented invariants and targeted regression coverage; another contributor can implement and review a representative change without maintainer intervention. File count alone is not a score. |
| Onboarding and usability | At least five first-time participants from the intended power-user audience install the release and complete the first-result journey unaided; at least four finish within five minutes after prerequisites, with no unsafe source mutation. Also observe resume, launch, delegation, inspection and recovery in daily use with declared timing/error criteria. Record failures, experience and assistance; repeat after fixing blockers. |
| Operational maturity | Record at least seven days of realistic supervised use on supported environments, including provider interruption, credential refresh, restart and storage growth. Preserve task outcomes and unresolved incidents; a fast fault soak does not substitute for elapsed operation. |
| Productivity advantage | Run the [matched evaluation](evaluation.md) against a declared single-agent baseline and a relevant public alternative; retain failures, costs where actually available, blind artifact grading and uncertainty. Require a repeatable useful improvement with acceptable quality and overhead. A small pilot cannot establish a general advantage. |

The first-result command and offline comparison controls validate infrastructure.
They do not represent new-user observations, native model quality or a
comparative benchmark. Keep real evaluation evidence private until its contents
have been reviewed for publication.

## Recorded engineering evidence

The [bounded command-worker soak](assets/quality/managed-soak-2026-11-03.json)
records 60 verified turns, 61 audited tasks, 12 clean restarts and an in-flight
broker crash. The interrupted attempt remained uncertain, its process stopped,
and the owner resolved it as failed without automatic replay. Completed traces
were verified after recovery. This took 66.66 seconds, made zero model calls,
and used disposable fixtures. It is not evidence of seven days of native use.

The observability regressions cover large retained histories, complete counts,
fair bounded attention and additive metadata-index migration. Query-plan checks
reject reads of task bodies during operational counting/ranking. A separate
read-only connection keeps slow queries and cancellation outside the worker's
unchanged 500 ms lease monitor. Tests block the actual read path, retain the
250 ms query budget and check credential rotation and shutdown before
publication. Worker check-ins remain separate from native events; unknown
reporting never becomes a successful task outcome.

Agentmux v0.14.0 adds fixed diagnostic codes and local collection timings to
schema-1 observations. The [offline operational report](observability.md#summarize-a-private-capture-offline)
summarizes sampled availability, gaps, retained-state/capacity gauges and
storage changes, while preserving missing evidence as unknown. It omits source
paths, task identities and raw errors, and records the complete input hash.
Regression controls cover malformed captures, privacy and bounded memory;
an optional actual-CLI integration exercises a live command-only task and a
partial observation after shutdown. This prepares operational evidence
collection; it does not supply seven days of native use or unaided user results.

The [comparison summary](evaluation.md#summarize-retained-comparison-evidence)
keeps failures, incomplete/unstarted attempts and paired outcomes visible.
Its timing summaries do not infer a winner, model quality or unknown provider
usage. Deterministic fixture results validate that reporting route, rather than
establishing a competitive advantage.

Fresh AI peer reviews added reproducible controls for escaped secret publication,
ambiguous signed trace envelopes, concurrent trace changes and cancellation
races. These are scoped code reviews and regressions, not an external human
security audit. The [subscription pilot](evaluation.md) has frozen contract
graders and route guards; preparation and metadata checks alone are not measured
model or competitor outcomes.

A [subscription-route smoke](assets/quality/subscription-smoke-2026-11-03.json)
completed a newly authored Go exercise in 86.541 seconds using Codex 0.166.0
with ChatGPT authentication and Claude Code 2.1.317 with first-party Pro
subscription authentication. Four native CLI sessions ran. Controller checks and
the frozen external grader passed; the confirmed patch applied only to the
fixture and independently reproduced its final files. Observed process cleanup
was clean. The record identifies the source build, rather than claiming that a
later release archive was used.

Both model reviews approved without findings and reported not running tests.
Their approvals are separate from controller and external test evidence. The
preceding duplicate-flag setup failure remains recorded. The workflow's `sonnet`
profile was overridden by the pinned `claude-sonnet-5-5` request; the provider's
resolved model and inference-request count remain unknown. This small route
check establishes no comparative productivity or long-term reliability result.

Guided work in v0.14.0 addresses the redundant-turn case identified by
that smoke. After independently passing implementation checks, an initial
fresh-context approval of the exact same revision can skip the response and
second-review phases. Retained reports mark those phases `skipped` with their
reason, rather than recording imaginary model turns. Final acceptance checks
still run, and source/result freshness plus explicit application guards remain.
With one implementation attempt, the clean path uses two native CLI turns;
retries increase that count and requested changes keep the full review loop.
Legacy plans without the declared
policy retain their previous behavior. Model-free controls establish those
branches and rejection behavior.

A [November 4 subscription smoke](assets/quality/subscription-smoke-2026-11-04.json)
exercised that clean path in 69.093 seconds with exactly two native CLI sessions:
Codex implementation and Claude review. The original approval, both skipped
phases, final checks, full retained-report load, external grader and confirmed
fixture-only application were verified. Its generic final files and grader
contract reproduce without models. This used the clean `bf3a578` source build
labeled `0.14.0-pre`, not a published release archive. The separate November 3
and November 4 records establish no speedup or competitive advantage.

A [private exploratory subscription pilot](assets/quality/subscription-pilot-2026-11-04.json)
ran three selected historical repository tasks once with a native Codex baseline
and once through the actual published v0.14.0 Codex/Claude workflow. The baseline
passed **3/3**, while Agentmux passed **1/3**. Two Agentmux implementations hit
an implicit 180-second profile limit before review/application, despite a
longer overall deadline. The outer guard separately rejected observed background
processes and cleaned its tracked identities. These are undelivered-workflow
failures, not correctness scores for completed patches.

The first four admissions were invalidated by a cumulative process-tracker limit
and are retained separately. The corrected frozen study used seven native CLI
admissions, with eleven across both studies. Grader/artifact integrity and the
independent evidence audit passed. Raw source, logs and local paths remain private;
the linked aggregate contains only reviewed metadata. Unequal workflow and turn
allowances, one repeat, selected historical tasks, trusted-local access and unknown
served-model revisions prevent a general speed, quality or competitive claim.

The v0.15.0 budget and bounded process-diagnostic changes address the concrete
engineering findings. Later targeted rechecks must remain separate from this
unchanged original pilot; passing code controls does not rewrite its failures.

Use [workflow validation](workflow-validation.md) for engineering checks,
[evaluation](evaluation.md) for usability and comparative task studies, and
[release verification](releasing.md) for the bytes delivered to users.
