# Research environment mechanics

The owner-controlled environment validates choices and computes outcomes.
Models choose actions; they do not supply verdicts or payoffs. The existing
`public-goods-v1` game is described in [research.md](research.md). This guide
covers the second game and opt-in coalition/population mechanics.

## Repeated trust and resource exchange

Run the command-only protocol example:

```sh
agentmux research plan examples/research/trust-exchange.yaml
agentmux research run examples/research/trust-exchange.yaml --output /tmp/amx-exchange
agentmux research analyze /tmp/amx-exchange --behavior-csv /tmp/amx-exchange-behavior.csv
```

`trust-exchange-v1` has two economic stages per round:

1. **Invest.** Each actor privately learns its own current productivity (an
   integer from two through `game.multiplier`, at most four). It may communicate
   and form consented teams if permitted, then submits
   `{"to":"other-actor","amount":2}` or `{"to":null,"amount":0}`.
   The nonnegative integer amount must fit its current wallet and per-round cap.
   It is immediately deducted from that wallet. The recipient receives
   sender-specific escrow equal to investment times the recipient's hidden
   productivity. All investments commit at one barrier.
2. **Return.** Each actor privately sees incoming escrows and chooses
   `{"returns":{"sender-id":3}}` or `{"returns":{}}`. Each repayment must fit
   that particular sender's escrow. The recipient keeps the rest; there is no
   enforced repayment promise. Actual repayments and retained value enter the
   respective wallets and affect next-round capacity. Invalid returns burn all
   incoming escrow, rather than silently granting a successful transfer.

Invalid investments/returns each cost one available wallet credit, or one
utility point if the wallet is empty. The same credit is not charged twice.
Individual utility is the round's net wallet change minus any such empty-wallet
penalties. Shared/individual/winner point rules are applied to the resolved
utilities. New private productivity is sampled each round. The owner retains
the entire hidden schedule outside participant workspaces.

Outputs include every actual investment, multiplier, escrow, repayment, retained
amount, burned amount, validity decision, wallet balance, utility, and payoff.
Return fraction is measured against total created escrow. Public-goods funding
metrics are not meaningful for this exchange game; use welfare and transfer/
repayment metrics. Scripted baselines are all-defect, seeded random choices, and
an informed cooperative strategy that invests with a productive partner and
returns escrow. These are explicit comparators, not a proof of optimal strategy.

The example's deterministic participants exercise real broker invitation,
voluntary acceptance, private channels, and objective transfers. Their chosen
50% repayment policy tests mechanics; it is not language-model behavior.

## Explicit consented reward pools

Set `teams: true`, `visibility: direct`, and `team_rewards: pool` to allow score
pooling. During an action/return stage an actor may include
`"reward_team":"team-id"`. The ID must identify an active team in which that
actor was a member at the frozen end-of-coordination/investment snapshot.

Pooling occurs only when at least two eligible members independently select the
same team. Each actor selects at most one pool, so overlapping memberships
cannot double-count utility. The selected members' utilities are summed and
divided equally into scoring utilities, then the declared point rule is
applied. Raw wallets and social welfare do not change. Outputs record raw and
scoring utilities plus the actual pool members and totals. A lone selection has
no effect. An unauthorized/stale nonmember selection is invalid. With
`team_rewards: none`, membership creates channels without redistributing points.

The snapshot defines economic eligibility for that stage; subsequent membership
changes do not retroactively change the settled rule. Actual board/memory
access still follows current broker membership and its revocation rules.

## Declared dynamic populations

```sh
agentmux research run examples/research/dynamic-population.yaml --output /tmp/amx-population
```

The default is a fixed population with spawning disabled. Opt in explicitly:

```yaml
population:
  mode: dynamic
  max_agents: 3
  entry_credits: 1
```

The managed source team must contain approved spawn templates and parent/template
allowlists. Research preserves those controls, including parent capability
subsets, sandbox boundaries, depth, and lifetime spawn limits. Templates have
autonomous wakeups disabled. A registered child receives no research turn until
the **next round boundary**. At that boundary the environment admits it only
when its parent can transfer `entry_credits` from its own remaining wallet.
No additional capital is minted. The transfer allocates existing endowment and
reduces the parent's future spending capacity; it is not retroactively scored as
a prior-round investment. A child lacking funding remains deferred.

Population snapshots record actor IDs, parents, entry transfers, deferrals, and
retirement. Retired wallets remain frozen rather than being reminted. Private
observations and hidden cases are extended for admitted actors, and all workers
at the next phase use that declared snapshot. Current-version limits impose a
conservative lifetime ceiling as well as the active cap; stopping a child does
not replenish spawn slots. The minimum active game population is two, maximum
16. The plan reports a worst-case task count using the maximum population.

Participants must let the owner issue all phase tasks. An extra parent-issued
child instruction is detected as an out-of-protocol pending task and interrupts
the attempt. Revocation/transport uncertainty during an active phase also fails
closed; it is not silently converted into a missing decision or replay. Broker
spawning can register a child during a turn, but activation and new observations
remain at the next-round barrier.

Dynamic composition is an explicit treatment, not a hidden change to a fixed
study. Spawn identities and timing are not seeded. Replaying an exact dynamic
trajectory requires its recorded population/coordination history in addition to
the environment seed. Baselines currently use the initial fixed population;
they do not model endogenous birth/retirement strategies. Comparing different
population paths therefore requires a separately declared analysis and resource
normalization.
