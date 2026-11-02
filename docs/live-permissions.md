# Live permissions and resource controls

A workspace combines immutable launch configuration, explicit owner overrides,
owner collaboration groups and consented teams. `boundaries.get` is the current
authority snapshot. `control.get` is stored owner intent and group membership;
a stored `true` is never proof that required membership or ancestry still holds.
Use the selected agent's credential for its own `boundaries.get`; only the owner
may inspect another agent or read/update complete owner controls. Roles describe
responsibilities and titles label sessions; neither adds a permission source.

## Independent group contributions

An active owner collaboration group contributes directed note/ping grants among
its current members, and instruction grants only when `commands: true`. Its own
board/memory require current membership. Disabling the group removes only that
contribution. Another overlapping active group keeps its grants. Explicit owner
edge/scope overrides win, including denies; configuration and parent delegation
remain underlying sources. Therefore notes-only means this group grants no
commands, rather than revoking a command grant supplied elsewhere.

`boundaries.get` target entries include allowed `actions`, per-action `sources`
and `denied_reasons`. Scope entries include effective `read`/`write`, `source`,
`reason` and optional `stored_read`/`stored_write`. The optional stored fields
identify an override, not actual current access. `suspended` labels isolation.
The terminal Connections and Memory views display these differences directly;
**o** opens the complete owner snapshot.

To remove one explicit override rather than replace it with another allow/deny:

```json
{"version": 7, "kind": "edge", "from": "alice", "to": "bob", "action": "instruct", "reset": true}
```

Submit it to `control.update` with the current boundary version. Board/memory
resets use `kind`, `agent`, `namespace`, `reset: true`. Reset can clean an exact
historical entry even after membership ended or an agent was revoked; it cannot
grant membership. Terminal **M/P/I** reset the selected directed message/ping/
instruction override; Memory **R** resets its selected scope override. Ordinary
lowercase keys still set explicit permissions. Team namespaces always follow
consent and ancestry, regardless of an old stored namespace override.

## Isolation and reconnecting

Isolation adds `source: "isolation"` suspension overlays without overwriting the
prior explicit owner intent. It removes current owner-group memberships and
consented memberships, dissolves teams owned by the isolated agent, and blocks
ambient spawning/team creation. Native terminals keep running and private memory
remains private. Existing accepted tasks and already-read data remain.

Reconnecting selected agents removes their directed pair's isolation overlays,
restores configured static scope intent and rejoins their selected group. It
keeps unrelated isolated relationships blocked. An explicit owner deny survives
reconnect. A new exact owner grant can deliberately replace its isolation overlay
without restoring ambient team/spawn authority. Rejoining a consented team still
requires an invitation and fresh participant consent.

Older state files did not distinguish generated controls from explicit owner
edits. These groups have no `contributions_version` (zero); new/reviewed groups
have version 1. Editing an old group fails closed with `conflict`, before any
membership or control changes, until the owner explicitly chooses one policy:

- `legacy_policy: "preserve"`: keep historical pair overrides as direct owner
  intent, including their allows and denies. Disabling this group can therefore
  leave those deliberately preserved direct grants effective.
- `legacy_policy: "group"`: remove only untagged historical `allow: true` edges
  between distinct members of this exact original group, then apply its requested
  state. All denies, other pairs, labeled isolation overlays and stored namespace
  controls remain. Other new active groups still contribute their own grants.

Neither choice guesses which old allows were manually intended. Review
`control.get` and the exact pair first. The choice, requested membership/state,
control revision, event and receipt commit atomically; rejection changes nothing.
An unreviewed legacy group adds no new implicit pair grants; its historical
controls remain the authority until review. A stale permission revision cannot
apply a migration choice against changed owner intent.
For the everyday CLI:

```sh
amx collaborate alice bob --legacy-policy preserve --session work
amx collaborate alice bob --legacy-policy group --session work
```

The terminal Connections **c** opens a migration review for an exact two-agent
legacy group. **1** selects preserve, **2** group, **o** reads the historical pair
edges, **Enter** submits that explicit choice with the frozen original members
and policy version, and **Esc** cancels. No policy is preselected. Larger legacy
groups use the CLI or the web's explicit review. Current groups never manufacture
global edge or namespace overrides. After review, exact overrides remain
individually resettable without granting membership.

## Capacity and live admission budgets

`capacity.get {"agent":"alice"}` is owner-only when inspecting somebody else;
participants inspect themselves. It reports resource version, remaining lifetime
spawns/teams, remaining spawn depth, current team slots, delegated templates,
blocking reasons and effective agent budget. Owner `capacity.get {}` also reports
the effective session budget. `budget.status` returns effective limits and
permanently charged usage. Read-only snapshots do not reserve future capacity.

Owners can atomically edit admission budgets with `capacity.update`:

```json
{"version": 1, "agent": "alice", "agent_budget": {"max_turn_tokens": 10000, "max_turn_cost_microusd": 200000, "max_subtree_tokens": 100000, "max_subtree_cost_microusd": 2000000, "enforcement": "reservation"}}
```

Use the `resource_version` returned by capacity/budget inspection, which is
separate from permission revisions. A supplied budget replaces that complete
budget object; omitted budget objects remain unchanged. `session_budget` accepts
`max_tokens` and `max_cost_microusd`. Zero means an unset reservation/ceiling.
Invalid negative limits, missing required turn reservations, stale versions and
ceilings below permanently reserved usage fail without changing anything. New
session ceilings validate every current agent and spawn template. No edit
refunds a failed, cancelled or uncertain attempt, and launch configuration stays
immutable. The same effective limits govern readiness and transactional claims.
The terminal Agents view uses **B** for a four-field, reviewed agent-budget editor.

`capacity.update {"version":2,"admission_seconds":300}` can shorten admission
runtime from now. It cannot extend the existing process/run deadline or a previous
admission deadline. An admission deadline stops future broker admissions; it does
not assert that an already-running native process was terminated. Process-tree
resource caps remain enforced launch settings and are not silently weakened.

These controls bound conservative broker admissions, not hard external provider
billing. Direct interactive CLI inference remains governed by that native CLI.
Saved resource overlays use recovery digests; missing or damaged persisted state
fails closed rather than restoring larger defaults.

Role changes use a separate owner `agent.role {id,role,version}` operation and
`role_version`, rather than the resource or permission revision. They apply at
the next managed claim or through a native session's broker notice. In native
Review/Auto mode, a role/boundary notice can become an attributed native turn;
Manual mode leaves it for explicit inbox access. They never grant additional
permissions. See [native collaboration](native-delivery.md) and
[managed runtime](managed-runtime.md#boards-messages-instructions-and-memory).
