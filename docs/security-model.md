# Trust boundaries and validation scope

Managed mode remains experimental. Agentmux has separate execution boundaries
for managed teams, native terminals
and owner-driven coding workflows. Managed policy is enforced by the broker;
Linux Bubblewrap supplies the selected worker and broker boundaries. Native
terminals retain their provider's approvals. The [`work`](working.md) and
[`flow`](workflows.md) commands select workflow isolation separately and do not
inherit managed sandbox settings. Local execution retains trusted host authority.

## What must be trusted

The owner, host administrator, kernel, Agentmux executable and chosen sandbox
tools are trusted. The owner selects executable code, native authentication,
mounts, endpoints and policy. Someone able to change owner state, executable
code or the host kernel is outside the isolation threat model. Bubblewrap is
not a separate virtual machine or a defense against a compromised kernel.

Native providers and remote SSH host administrators are separate trusted
parties for the data and credentials deliberately supplied to them. Private
local history does not establish provider-side deletion or confidentiality.
Native login snapshots may be readable by the worker they authenticate; avoid
sharing a credential that gives more provider authority than that worker needs.

## What the runtime enforces

| Boundary | Enforcement | Evidence and limits |
| --- | --- | --- |
| Agent identity and coordination | Individual credentials, broker-side authorization, separate note/ping/instruction rights, board and memory scopes | Adversarial broker tests exercise denied reads/writes, revoked membership, cached requests and private history. An agent can still try to persuade a permitted recipient through message content. |
| Live owner policy | Versioned overrides, independent collaboration contributions, isolation overlays and explicit review of ambiguous legacy grants | Group overlap, deny/reset, migration rollback and stale-version tests preserve unrelated intent. Roles and titles grant no authority. Local processes can still bypass API ACLs through host access. |
| Dynamic organization | Owner-approved spawn templates, child permission subsets, finite spawn resources, consented membership and history floors | Spawn/team integration checks use real sandboxed workers. Team membership does not implicitly grant instruction authority. |
| Execution attempts | Instance-bound leases checked before launch, during monitoring and before completion | Regressions replace a task lease before launch and while a real subprocess runs. Stale workers stop and cannot complete the replacement attempt. Monitoring cannot undo side effects already in progress. |
| Native results | Required native completion records, strict protocol handling and complete stdin delivery | Recorded/fake native streams exercise missing, malformed and conflicting results. Installed provider smoke checks are listed separately in [provider validation](provider-validation.md); protocol fixtures do not certify every future CLI release. |
| Filesystem and resources | Explicit mounts, separate histories, optional capped tmpfs capsules and cgroup limits | Actual namespace, ENOSPC, cgroup exhaustion and descendant cleanup checks are available. Requested unsupported boundaries fail closed. Cgroup checks require a delegated user manager and are distinct from namespace checks. |
| Managed network destinations | Independent network namespace and an explicit destination proxy in allowlist mode | Integration checks reject direct routes and proxy opt-out. CONNECT/SNI checks leave encrypted HTTP authority and paths opaque. `network: host` grants host networking explicitly. |
| Restart and uncertain delivery | Durable state, preserved deadlines, charged retry attempts and explicit owner resolution | The model-free soak injects a broker crash and rechecks completed task anchors after recovery. Operators must stop old processes and inspect side effects before requesting a retry. |
| Admission and portable evidence | Permanent declared turn reservations, versioned live overlays, local HMAC traces and owner-signed portable bundles | Admission is not a provider bill cap. Bundle verification requires an independently obtained owner-key fingerprint and authenticates that owner's attestation, not provider identity or output truth. |
| Research records | Native traces, execution fingerprints, fixed plans and durable native-turn reservations | New native turns require trace coverage. Continuation checks reject changed execution provenance; legacy completed results remain readable. Local evidence is not an external immutable attestation. |

Revoking an agent fences work addressed to that agent and its descendants.
Instructions already accepted from that issuer to other active agents remain
durable tasks. Use `task.cancel` explicitly to withdraw those instructions;
issuer revocation is not a retroactive cancellation of everything it requested.
Broker permissions govern API operations. They do not guarantee how an LLM
interprets instructions embedded in otherwise permitted messages; a recipient's
tools and sandbox still need appropriate limits.

Replies derive ancestry from a visible broker message and check the outgoing
audience; a reply never grants access to an ancestor. Imported memory retains
the authenticated destination author plus explicit archive-source claims.
Those claims and archive checksums are not authenticated source identities.
See [memory lifecycle](memory-lifecycle.md), [live permissions](live-permissions.md)
and [portable traces](traces-and-recovery.md#portable-authenticated-bundles).

Native interactive input and managed task execution have separate boundaries.
The opt-in [native delivery bridge](native-delivery.md) rechecks recipient policy,
sender authority and source visibility before reserving an attributed turn.
Transport acceptance, execution evidence and task verification remain separate.
Supported cancellation only targets the correlated peer turn; Claude's already
queued inbox turns cannot be retracted. Manual native input retains the provider's
own approval and execution behavior.
An interactive `native_only` session omits Agentmux prompt injection; a live
role edit sends a broker notice rather than keystrokes. Relaunch requires an
exited workspace-owned native session and an explicit history choice when
resumption is desired. It preserves identity and deadlines without asserting
that provider-side history or billing is controlled by the broker.

## Coding workflow boundaries

`amx work` and `amx flow` run owner-selected commands and native tools in
separate Git worktrees. Worktrees separate changes; private snapshot storage
separates Git objects from the source repository. Neither alone restricts host
filesystem or network authority. Required PID containment stops namespace
descendants but also needs full isolation to restrict files and networking.

Full Linux workflow isolation requires working user, PID, mount and network
namespaces and capped private HOME/tmp filesystems. Native tools receive their
assigned workspace and selected runtime dependencies; review workspaces are
read-only. Checks receive no provider credentials or network. Workspace growth
has no hard disk quota. Requested boundaries fail closed.

Workflow allowlists use a different, inspecting proxy from managed egress.
A per-turn ephemeral CA lets it check every HTTPS HTTP/1.1 Host or HTTP/2
authority, while upstream certificate/hostname checks remain enabled. Only the
public CA enters the sandbox; its signing key remains in controller memory.
Opaque tunnels and HTTP upgrades are refused. Certificate-pinned, mutual-TLS
or incompatible custom-trust clients can fail closed. Grants still select
origins, not individual URL paths. See [full workflow isolation](workflows.md#private-storage-and-full-isolation)
and [managed destination controls](hardening.md#exact-network-destinations).

The workflow controller and any selected host inference bridge remain trusted.
Selected credentials are explicitly filtered for a candidate; managed broker
ACLs are not workflow tool permissions. Fresh reviews, recorded patches and
declared checks provide evidence for the inspected revision, not provider
attestation or a proof of correctness. Source application is explicit, checks
freshness and patch integrity, and retains uncertain application intent for
[recovery](working.md#recover-an-interrupted-application). It does not commit
or push changes. Host services, unrelated writers and provider billing remain
outside these boundaries.

## Validation gates

`make test-sandbox` exercises real broker/worker isolation, destination policy,
disk caps and dynamic teams. `make test-research` uses isolated command actors
to verify game accounting and continuation without provider calls.
`make test-soak` checks bounded execution, restarts and one in-flight crash.
CI runs these in a separate sandbox job whose namespace preflight must pass;
unavailable isolation fails that job instead of silently omitting coverage.

Workflow-specific regressions and bounded provider evidence are recorded in
[workflow validation](workflow-validation.md). Managed isolation checks do not
establish that a separately configured workflow or provider will run correctly.

`make test-resources` verifies actual cgroup controls on a suitable Linux host.
Provider-native calls, SSH placement, multi-day operation and statistically
powered behavioral studies need their own recorded runs. A green command-only
suite does not establish those outcomes. See the [validation record](release-validation.md)
for the measured scope and the [pilot record](research-pilot-results.md) for
the original measurement confound and preserved results.

## Remaining evidence work

Comparative task quality needs matched single-agent and team baselines with
independent artifact checks. Behavioral claims need validated extraction,
counterbalanced actor/model assignments, justified sample sizes and independent
scoring. Large-scale operation needs concurrent native worker measurements;
logical broker clients are a different workload. Long-running reliability
needs wall-clock observation under realistic provider interruptions, credential
refresh and storage growth. These claims remain open until those measurements
exist.
