# UAT gap closure

This follow-up addresses the eight remaining categories recorded in the
[20-angle UAT](uat-20-angles.md#historical-capability-proposals-and-design-limits).
It separates implemented controls from the limits of what was tested. The
original review and its measurements remain historical evidence.

## Category-to-implementation map

| Previous category | Concrete behavior now available | Regression evidence and practical limits |
| --- | --- | --- |
| Everyday session continuity | `relaunch ID` requests one deliberate relaunch through the running supervisor, retaining the stable ID, peer processes and workspace attempt. Native history selection is explicit: a new conversation, `last`, a supported picker, or a provider-native ID. `workspaces` inventories default/named locations and registered custom directories; terminal **W** and web expose workspace navigation. | `TestIndividualNativeRelaunchPreservesPeerAndAttempt`, workspace concurrent-index tests and web identity-fencing tests. Relaunch refuses live, external or revoked sessions and stale attempt/pane identities. An old controller needs one workspace restart to support individual relaunch; absent custom paths require `workspaces --register DIR`. No native conversation is guessed. |
| History and navigation | Broker `task.list` supports visibility-preserving query/recipient filters, descending history, cursors and `has_more`. Terminal Tasks uses **f**, **PgUp/PgDn**; web has complete-history search and pagination alongside the recent snapshot. **?** and **Ctrl-G** provide contextual terminal help, including editor/review guidance. Graph zoom/fit and keyboard controls remove the mandatory wide mobile canvas; team labels include human titles and exact IDs. | `TestTaskHistorySearchPagingNeverWidensVisibility` traverses 85 tasks and finds the oldest while rejecting hidden cursors. Frontend tests cover short byte-limited pages, late queries, failed pagination, mobile graph fit/zoom, identity and focus. Help/intent tests preserve literal `?` inside drafts. This is targeted accessibility/responsive coverage, not certification across every terminal, browser or assistive technology. |
| Overlapping permission semantics | Current owner groups contribute grants independently; disabling one removes only its contribution. Exact owner allow/deny overrides remain separate. Effective boundaries expose source and denial reasons plus stored-versus-effective scope values. Isolation suspends prior intent rather than overwriting it; reconnect restores the selected relationships and configured static intent while unrelated isolation remains. Explicit override reset controls are available. | Overlap, isolation/reconnect, explicit-scope and scale regressions cover grant provenance and rollback. Historical groups fail closed until the owner reviews **preserve** versus **group** migration semantics; tests cover exact-pair removal, retained denies, atomic rollback and receipts. New semantics do not guess whether an old untagged edge was manually intended. See [live permissions](live-permissions.md). |
| Knowledge provenance and portable traces | Imported entries retain source session, exporter agent, namespace, original author/key/version, value digest and complete archive digest as structured claims. Terminal/web readers expose them beside the destination author. `managed trace export` creates an owner-signed portable bundle; `verify-bundle --trust FINGERPRINT` verifies offline without owner state or secret keys. | Cross-run CAS/import-resume tests check exact retained claims and destination authorship. Portable-bundle tests reject tampering, mismatched/missing trust pins, overwrite and secret-key export; verification works without local state. Source claims and checksums are not authenticated source identity. Trace signatures authenticate the pinned owner exporter, not a provider or immutable execution environment. Exports can outlive source deletion. |
| Messaging intent and ancestry | CLI/API, terminal and web distinguish informational notes from instruction tasks. Replies store broker-validated `reply_to` and inferred `thread_id` on messages/tasks; text is preserved. Shared-board replies retain an explicit board or choose a visible direct recipient. | Broker ancestry tests cover task/message chains, invisible references, forged roots and audience widening. CLI/API and terminal/web tests preserve reviewed recipient, mode, reference and failed drafts. Every direct recipient must already see the reference; a reply cannot move private context to a board or another board. Native agents read broker instructions explicitly; nothing injects user input or guarantees a native turn starts. |
| Research design | Declared role assignments and personality prompt profiles compile into condition-specific roles. Seeded Latin-square counterbalancing balances condition positions when complete cycles are supplied. Preregistered designs support one to six binary factors, supported behavioral primary outcomes, paired effects and higher-order interactions. Analysis warns about tiny samples, scripted participants and limited evidence. | Design tests cover profile compilation without source mutation, balanced ordering, confounded/invalid factors, team and return-fraction outcomes, higher-order effects and legacy hashes. A command-only treatment study exercises planning/execution/export. Prompt profiles are operational treatments, not personality measurements; scripted participants ignoring the prompt do not establish treatment efficacy. Unsupported outcomes/invalid designs fail validation rather than invent an estimator. See [research](research.md). |
| Dynamic team planning | `capacity.get` and identity report remaining lifetime spawns/teams, depth, slots, templates, admission state and denial reasons. Participants see their own inherited access reason; owners inspect members. Teams support live name/description edits, separately consented ownership transfer and atomic invited participant moves. Owner CAS resource overlays edit future agent/session admissions and can shorten admission runtime. | Team tests cover metadata CAS, authority rechecks at transfer acceptance, invitation/participant consent, two-team version checks, failed-move rollback and denial privacy. Capacity/resource tests cover read-only inspection, permanent charges, stale versions, invalid limits and claim/readiness parity. Revocation/dissolution does not refund lifetime allocations. Admission edits do not weaken process caps, extend the original deadline, terminate an in-flight turn or impose hard provider billing caps. |
| Native/custom configuration | `add --native-only` and the terminal/web profile choice append no Agentmux prompt; coordinated sessions retain optional roles. `agent.role`, CLI `role`, terminal **V** and web use owner CAS edits, safe conflict review and broker notices. Managed workers capture the current role with the next claim. `reviewer-pair` and `research-team` presets launch idle role-configured workers. Codex custom Responses providers use per-process configuration; Pi supports explicit compatible endpoints; native Antigravity has a prompt-free route and its Pi bridge handles injected coordination context. | Native configuration/argv/environment tests, role-claim and conflict-review tests, preset tests and actual installed-Codex endpoint conformance. Real bounded Claude/Codex/Antigravity/Pi model turns and four native PTY startups are recorded in [provider acceptance](provider-acceptance.md). Native Antigravity appended-system-role injection remains deliberately unsupported, with a supported alternative. No claim covers every provider, arbitrary skill/MCP, or every account/model combination. |

## Trust and execution boundaries

The native terminal remains the primary interaction surface. Notes, instructions,
live role edits, collaboration changes and web controls never become unsolicited
terminal keystrokes. Managed workers execute queued tasks; a native session must
read its broker inbox/tasks when the human wants coordination. Observing a process
or preparing a bridge is distinct from the agent reading its setup.

Dynamic collaboration and membership are broker authority, while a role or
personality description is prompt content. An authorized participant may retain
or relay information it already read. Revoking future access cannot retract
delivered context, provider history, archives or artifacts. Local trusted-native
execution preserves owner customizations and also retains same-user filesystem
authority; choose an isolated profile when that boundary is required.

Live capacity edits use conservative reservations and immutable past charges.
They cannot promise hard external token/dollar limits, change an already-running
provider request, or establish what an opaque provider actually billed. An
owner-signed trace attests to that owner's locally verified adapter-visible
record; it excludes hidden provider state and unreported native activity.

## Validation record

The final local build passed `go test -race -count=1 ./...` with the managed,
experiment, research and sandbox integration flags enabled, all 78 frontend
interaction tests, and all seven release-installer tests. Independent backend,
lifecycle and messaging reviews found no remaining blockers after their fixes.

Real browser acceptance used ten sandboxed, model-free native terminals. It
loaded all 115 historical tasks and found the oldest, checked a 390-pixel graph
viewport, sent an explicit inbox note and a structured scoped-board reply,
saved a versioned role and inspected exact saved memory metadata. A reviewed
single-session relaunch retained the workspace attempt and stable ID; the peer
PID was unchanged and both terminals accepted input afterward. A separate
workspace created by the previous binary exercised explicit legacy-policy
migration: disabling its group denied the old peer message grant without
restarting its native sessions. These fixtures establish control behavior, not
model throughput or a production capacity benchmark.

The [provider acceptance matrix](provider-acceptance.md) distinguishes genuine
inference, native PTY startup, endpoint conformance and configuration retention.
Pi exercised real cached Transformers inference and the authorized remote
Ollama `qwen2.5:14b` endpoint. No fresh vLLM deployment was run in this pass;
generic protocol support must not be reported as a live vLLM benchmark. Tool-free
markers and startup do not establish arbitrary native tool/skill/MCP behavior.
