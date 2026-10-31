# Native feasibility pilot: recorded results

The preregistered 64-task collection completed on 2026-10-31, from 16:30:50
through 16:52:43 UTC. All eight fresh trials and 16 game rounds finished.
This is feasibility and measurement-quality evidence; it does not establish
model cooperation, competitiveness, motives, or traits. The collection exposed
an answer-extraction confound, described below. Original outcomes are retained.

See the [preregistered design and reproduction commands](research-study.md),
[manifest](../examples/research/factorial-pilot.yaml), and
[aggregate machine-readable results](research-pilot-results.json). The shared
results contain no private prompts, clues, native output, sessions, or credentials.

## Collection and reliability

Alice used `claude-haiku-5-5` through Claude CLI `2.1.316`; Bob used
`gpt-6.1-sol` through Codex CLI `0.165.0`. Each actor's executable hash remained
constant across the eight preflights. One tools-off Haiku availability call
preceded the study and is separate from its allocation. Every participant and
broker used Bubblewrap; native network access was explicitly `host`.

The frozen worker SHA-256 was
`961593b68550b4b33cdd7ec7ccf9f202d3bd6df9ccaf38d0a47f67c41bb29b53`.
The normalized manifest SHA-256 was
`1e02038c953ce65c1d4863739233a7ba28fd063ea1e05a02cb6f35e6896a2b7e`.

- Exactly **64 native tasks** were reserved and executed: 63 submitted or
  verified, one Codex decision stopped at its 120-second limit.
- There were **no automatic retries** and no interrupted/uncertain trials.
- Of **32 game decisions**, 22 were valid and 10 invalid: seven returned
  prose before JSON, two used a non-null project with zero contribution, and
  one had the recorded execution timeout. The timeout is included in the ten.
- The original strict parser was retained throughout. There was no JSON-suffix
  recovery, post-hoc rescoring, replacement of failed observations, or paid rerun.
- Bob sent 26 authenticated direct notes; Alice sent none. No shared-board or
  memory writes and no team create/invite/join events were recorded. No
  consented teams formed in the four team-enabled trials.
- Two valid decisions contributed positive credits; total costs, including
  invalid-decision penalties, were 13 credits. No project was funded.

An independent Python re-evaluation checked all 16 rounds against the original
adapter results and owner-held truth: validity, costs, productive projects,
funding, wallets, utilities, payoffs, and cumulative points agreed. It also
checked that all 64 unique task IDs exactly matched the durable budget ledger.
Private result/analysis/behavior/ledger files and both CSV exports were `0600`.
Verification made zero model calls.
An explicit completed-run resume then skipped all eight trials in 0.019 seconds:
no new attempts/task IDs were created and the 64-reservation ledger stayed
byte-for-byte unchanged. The failed task was retained rather than replayed.

The configured Claude native cap was `$0.10` per turn. Actual billing was not
measured, and no Codex monetary cap is claimed. Task slots count native turns;
a turn can make several provider requests.

## Objective outcomes under the original measurement pipeline

Each cell contains two independent fresh runs using the same two paired worlds.
Welfare is total objective utility across both actors and both rounds per run.
Invalid and failed decisions keep their preregistered penalties.

| Incentive | Team permission | Run welfare values | Mean welfare | Invalid decisions / 8 | Execution failures / 16 |
| --- | --- | --- | ---: | ---: | ---: |
| Individual | Off | -3, 0 | -1.5 | 3 | 0 |
| Individual | On | -1, -1 | -1.0 | 2 | 0 |
| Shared | Off | -2, -1 | -1.5 | 3 | 1 |
| Shared | On | -1, -4 | -2.5 | 2 | 0 |

All cells funded zero of four rounds and formed zero consented teams. The
informed cooperative comparator achieved welfare 6 per run; all-defect achieved
0; the seeded random comparator averaged -5. These are declared strategies
under the same resource accounting, not interchangeable model participants.

| Paired factorial quantity | Estimate | Descriptive 95% interval | Complete blocks |
| --- | ---: | --- | ---: |
| Shared minus individual, averaged over team permission | -0.75 | [-16.63, 15.13] | 2 |
| Team permission on minus off, averaged over incentive | -0.25 | [-22.49, 21.99] | 2 |
| Incentive-by-permission difference of differences | -1.50 | [-7.85, 4.85] | 2 |

These are unadjusted Student-t intervals on paired world-level contrasts.
They assume independent blocks and do not account for provider dependence or
the measurement confound. The preregistered minimum was 20 blocks; two is
underpowered. The numerical contrasts are archived, not evidence for a social
effect. Per-agent behavior IDs and CSV rows support subsequent audit without
treating repeated rounds as independent samples.

## Answer-extraction confound and limits

The frozen Codex adapter concatenated every completed `agent_message`, which
can combine progress commentary with a final answer. Seven rejected Codex
decision results contain a prose prefix followed by one JSON object. Native
event tracing was off for this collection, so their original event boundaries
cannot be reconstructed reliably from the retained aggregated text. Those
rejections therefore cannot be attributed confidently to the model's final
answer behavior. The current adapter has been corrected for future runs to
select the last completed agent message while preserving raw events when
captured. OpenAI's native documentation distinguishes the full JSONL event
stream from the final message exported by `--output-last-message`.
[Official non-interactive documentation](https://learn.chatgpt.com/docs/non-interactive-mode).

The original scores remain evidence of the actual frozen pipeline. They are
unsuitable for claims that a model is uncooperative or that shared incentives
reduce cooperation. The two zero-contribution schema violations, single timeout,
small sample, differing actor/harness capabilities, and fixed role-to-model
assignment are additional limits. Individual rewards are also not a zero-sum
competition treatment. Private team channels add limited opportunity in a dyad
that already permits direct communication.

A subsequent study should preregister a new collection after validating final
answer extraction, enable native event traces before collection, test a larger
population and additional environments, counterbalance actor/model assignments,
and use an independently justified sample size. Dynamic population, reward
pooling, and trust-exchange mechanics have command-based integration coverage;
they were deliberately held off in this native pilot. No extra native study
was silently started to replace these results.
