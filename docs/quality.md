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

Use [workflow validation](workflow-validation.md) for engineering checks,
[evaluation](evaluation.md) for usability and comparative task studies, and
[release verification](releasing.md) for the bytes delivered to users.
