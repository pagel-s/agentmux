# Bounded spawning and dynamic teams

Agentmux can let an authorized agent request a child worker, create a team, and
invite collaborators. These features are off unless explicitly configured.
Roles and self-declared leadership never grant runtime authority.

Start with [the bounded team configuration](../examples/managed/bounded-teams.yaml):

```sh
./bin/agentmux managed up examples/managed/bounded-teams.yaml --state /tmp/amx-teams --broker-sandbox bubblewrap
```

The example has `lead`, `peer`, the static `team` board, shared memory `findings`,
and an approved `helper` template. Its `/bin/cat` fixtures make no model calls;
they echo tasks and do not independently decide to recruit collaborators.
The broker, static agents, and spawned helpers use Bubblewrap; working Linux
namespace support is required.

## Owner policy and child capabilities

The relevant configuration is:

```yaml
spawn:
  enabled: true
  allow:
    lead: [helper]
  templates:
    helper:
      harness: command
      command: [/bin/cat]
      sandbox: bubblewrap
      network: none

teams:
  enabled: true
  creators: [lead]
  history: since-join   # default; "all" is an explicit alternative

limits:
  max_runtime_seconds: 600
  max_concurrent_tasks: 4
  max_spawns: 4
  max_spawn_depth: 1
  max_teams: 4
  max_team_members: 4
```

Templates are immutable owner-approved process definitions. A spawn request
selects a template and concrete capabilities; it cannot supply executable text,
a new endpoint, mounts, credentials, or an arbitrary workspace. Spawn templates
must use bubblewrap and receive a fresh workspace and identity. Model access and
networking come from the template; `network: host` means unrestricted network
access, while `none` keeps the network namespace separate.

Using the lead's credential:

```sh
./bin/agentmux managed call agent.spawn \
  --credential-file /tmp/amx-teams/credentials/lead.json \
  --request-id recruit-helper-1 \
  --json '{
    "template":"helper",
    "capabilities":{
      "boards_read":["team"],
      "boards_write":["team"],
      "memory_read":["findings"],
      "targets":[{"to":"peer","actions":["message.send"]}],
      "join_teams":true
    }
  }'
```

The response contains the new agent ID and redacted registry metadata, not its
credential. With `managed up`, the supervisor adds a worker window and the
worker runs each accepted task in its own sandbox. With manual `managed serve`,
registration alone does not create a tmux worker; the owner must start its worker
or use the supervised launch path.

Supported capability fields are:

| Capability | Delegates |
| --- | --- |
| `boards_read`, `boards_write` | Exact existing board namespaces. |
| `memory_read`, `memory_write` | Exact existing shared-memory namespaces. |
| `targets` | Concrete peer IDs and `message.send`, `ping.send`, or `instruct` actions. |
| `spawn_templates` | Named templates the parent is already allowed to recruit. |
| `create_teams` | Explicit authority to create new team namespaces. |
| `join_teams` | Explicit authority for a child to accept eligible invitations; false by default. |

Requested rights must fit the parent's delegable authority and session policy.
A child does not inherit static wildcard ACLs. Its namespace and target access
remain subject to the parent's effective rights. A parent may control its own
child; a child may send a note or ping back but cannot instruct its parent merely
because it was spawned. Recruiting a child does not renew the persisted run
deadline, create unlimited resources, or grant provider-spending guarantees.

Creating a new team is a deliberate resource-creation exception: an agent with
`create_teams` may create a fresh board/memory namespace and become its owner.
That does not confer access to an existing unrelated namespace or change any
agent-to-agent instruction edge.

## Invitations, consent, and versions

Create a team with the lead credential:

```sh
./bin/agentmux managed call team.create \
  --credential-file /tmp/amx-teams/credentials/lead.json \
  --json '{"name":"Review group"}'
```

The result supplies the team `id`, current `version`, dedicated `board`, and
shared-memory namespace `memory`. An agent creator becomes the first member.
The human owner can administer teams without impersonating a participant.

Copy the returned team ID, then invite the peer using the current version:

```sh
./bin/agentmux managed call team.invite \
  --credential-file /tmp/amx-teams/credentials/lead.json \
  --json '{"id":"TEAM_ID","agent":"peer","version":1}'

# After that invitation, use the version returned by it (normally 2).
./bin/agentmux managed call team.join \
  --credential-file /tmp/amx-teams/credentials/peer.json \
  --json '{"id":"TEAM_ID","version":2}'
```

An invitation requires the manager to be allowed to message the invitee. The
invitee accepts using its own credential; an invitation does not silently enroll
it. Static participants can accept permitted invitations when teams are enabled.
A spawned child additionally needs delegated `join_teams`, and its parent must
currently have the relevant membership/access. A child cannot accept an
unrelated invitation to bypass its parent's namespace limits. Parent departure
removes the dependent child's effective access too. The explicit team-creation
right above governs an authorized child's own newly created team.

`team.list` returns teams visible to the caller: those it owns, belongs to, or is
invited to. The human owner can inspect all teams. Use it to obtain current
versions before mutations:

```sh
./bin/agentmux managed call team.list \
  --credential-file /tmp/amx-teams/credentials/lead.json --json '{}'
```

Membership mutations use compare-and-set versions. Concurrent changes produce
`conflict`; read the current team before issuing a new decision. Reuse the same
request ID and arguments when recovering an uncertain response to an existing
write, rather than issuing a second logical invitation or removal.

| Operation | Arguments | Authority/effect |
| --- | --- | --- |
| `team.create` | `name` | Owner or configured/delegated creator; creates a new team. |
| `team.invite` | `id`, `agent`, `version` | Team manager; creates a pending invitation. |
| `team.join` | `id`, `version` | The eligible invitee; records its consent. |
| `team.remove` | `id`, `agent`, `version` | Team manager; removes a member or pending invitee. |
| `team.leave` | `id`, `version` | A member/invitee leaves or declines. |
| `team.dissolve` | `id`, `version` | Team manager; closes the team and future namespace access. |

The manager is the human owner or the team's creator. A team creator dissolves
its team instead of leaving or removing itself. Team membership enables its
scoped board and memory; it does not grant `instruct`, interrupt, spawn, or
access to another team's data. Voluntarily following a proposal on a board is
agent behavior, not an administrative privilege grant.

## History, memory, and departure

`teams.history: since-join` is the default. New members can read team-board
messages posted after their most recent join, rather than the whole historical
conversation. Rejoining begins a new history window. `history: all` explicitly
permits members to read earlier team-board messages.

Author receipts are an exception: agents can still retrieve messages they
authored through their visible-message feed. Existing direct-recipient receipts
also retain their own visibility rules. Leaving or removal revokes future team
reads/writes; it cannot erase content already delivered or retract a participant's
own sent-message record. The human owner remains trusted with the complete
durable state. A query restricted to a board still requires current board access.

Shared memory is the team's current knowledge state, not a historical message
stream. A newly admitted member can read current shared-memory entries, including
entries created before it joined. Message-history filtering is not a guarantee
that current memory, quoted messages, or artifacts contain no older information.
Treat memory promotion and sharing as part of the scenario's information policy.

## Stopping and inspecting agents

The owner can inspect the registry with `agent.list`. A parent/ancestor can stop
its descendants; the owner can stop any configured agent:

```sh
./bin/agentmux managed call agent.stop \
  --credential-file /tmp/amx-teams/credentials/lead.json \
  --json '{"id":"CHILD_ID"}'
```

Stopping revokes that agent's and its descendants' credentials and fences their
work. It reports `process_stopped: false`: authorization revocation is not proof
of OS-process termination. The supervisor interrupts the affected workers, and
a running worker fails closed when its next authenticated task observation is
rejected. For an uncertain turn, confirm the process is stopped before resolving
or retrying its task. Agent IDs and pane positions are separate identities.

The runtime bounds concurrent tasks, lifetime spawning, spawn depth, teams,
membership, records, requests, and elapsed run time. Usage reported by a provider
is observational; these limits do not imply a hard token/dollar cap. All of these
features remain optional, and their security depends on the chosen sandbox:
local participants retain trusted host access.

For controlled comparisons, see [repeatable research pilots](experiments.md).
The initial experiment runner disables dynamic spawning and teams so that
population changes and membership history do not become accidental treatments.
