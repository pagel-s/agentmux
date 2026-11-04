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
reject reads of task bodies during operational counting/ranking, and a blocked
observation must release its bounded read before the worker's unchanged 500 ms
lease-monitor deadline. Worker check-ins remain separate from native events;
unknown reporting never becomes a successful task outcome.

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
It also identifies a future optimization: avoid the response and second-review
turns after an exact initial approval, with explicit skip evidence and unchanged
final checks, review freshness and application guards.

Use [workflow validation](workflow-validation.md) for engineering checks,
[evaluation](evaluation.md) for usability and comparative task studies, and
[release verification](releasing.md) for the bytes delivered to users.
