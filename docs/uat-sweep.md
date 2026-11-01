# Usability and failure sweep — 31 October 2026

This sweep exercised native terminal workflows, the web owner console,
coordination and knowledge boundaries, research automation, recovery, and a real
two-machine SSH deployment. All fixtures were model-free. Temporary sessions and
remote deployments were isolated from everyday user workspaces.

## Coverage

| Situation | Validation |
| --- | --- |
| Daily interactive work | Twelve real terminal sessions at 80×24, duplicate/untitled/long Unicode titles, rapid navigation, search/no results, native addition/removal, independent clients |
| Sessions launched separately | Recognizable process with a controlling TTY, observational discovery, explicit bridge setup, disconnect preserving the original process |
| Knowledge boundaries | Separate pairs and overlapping groups, private/shared memory, version conflicts, in-flight permission revocation, credential rotation, reconnect/reopen retaining scoped history |
| Asynchronous editing | Late memory responses after navigation/Esc, rename conflicts and failed delivery retaining drafts, typing while a save is pending |
| Reply routing | Original sender/task recipient differs from selection; native inbox versus managed instruction; restored board draft cannot redirect a reply; another session's draft survives |
| Web failure handling | Injected outage, hanging snapshot, committed write with delayed response, reconnect without replay, stale actions disabled while drafts remain editable |
| Larger teams | Forty real Bubblewrap terminals with network disabled; readable scrollable graph and searchable roster |
| Registry capacity | Two hundred definitions: forty real PTYs plus 160 explicitly synthetic records; selection outside the first forty; 201st admission rejected without partial state |
| Concurrent final-slot admission | Revoked static definition plus two competing additions; exactly one succeeds at 200 unique IDs |
| Native process failure | Missing/retagged/dead pane status, failed focus preserving another terminal client's selection, broker-loss diagnostics |
| Research | Isolated repeated rounds, scarce credits, consent-based teams, exchange and dynamic population workflows |
| Recovery | Missing/null/incomplete/altered permission, autonomy and conversation state; lost state plus digest; intact legacy-state compatibility |
| Crash handling | Ten verified tasks, two restarts, preserved deadline and trace continuity; uncertain in-flight work is not automatically replayed |
| Distributed work | Actual SSH worker on a second machine, board/memory handoff, reverse command denied, credential separation and explicit remote resume |

## Problems found and fixed

- Fast terminal input could be grouped into one event and ignored. Navigation
  and slash-search now process real keystrokes in order; bracketed paste outside
  an editor remains inert.
- Late memory reads could reopen a previous group's editor under a different
  session. Requests now carry session/request identity and navigation invalidates
  stale results.
- Failed terminal title edits lost their text. Drafts now remain with their ID
  for explicit review and retry.
- Missing native panes could still appear running. A successful complete pane
  inventory now reports them unavailable without respawning or stopping peers.
  External bridge status separately reports whether the original native terminal
  is currently observed; registration and revocation do not imply process lifetime.
- The circular graph overlapped at forty sessions and could omit a selected
  session beyond its display cap. Larger graphs use a scrollable grid, keep the
  selected session/recipient in the displayed subset, and show the cap explicitly.
- Unbounded web requests could leave refresh stuck. Reads/writes now have bounded
  timeouts; disconnected snapshots disable mutations and retain drafts. Writes
  are never automatically replayed.
- Concurrent web title edits lacked a useful resolution flow. The editor now
  shows the current saved version and requires explicit review before rebasing a
  preserved draft. Newer memory text is marked unsaved when an earlier save ends.
- Reply could show the selected session while delivering to the original sender
  or task recipient. It now selects the resolved recipient before submission,
  waits for that session's boundaries, and confirms the actual delivery kind and
  recipient. The server rejects a mismatched reviewed recipient without writing.
  Existing memory key/value drafts remain editable offline while their namespace,
  load and save controls remain disabled.
- Revoked static definitions were counted twice at admission. Capacity now counts
  unique IDs, and owner-added native sessions do not consume delegated spawn quota.
- Damaged saved lifecycle records could restore permissive or fresh defaults.
  Validation plus transactional recovery digests and initialization evidence now
  prevent permission, pause/budget, and conversation-history fences from silently
  resetting. Intact legacy records remain readable; incomplete legacy autonomy
  counters are rejected.

## Reproduction

```sh
make test-managed
make test-sandbox
make test-research
make test-soak
make test-scale
node cmd/agentmux/workweb/app.test.cjs

# Larger live terminal fixture for manual browser/terminal acceptance:
python3 scripts/smoke-managed-scale.py --live-agents 40 --serve --keep-output
# Its stdin supports: status, measure, expand 200, quit.

# Requires an explicitly authorized host and a fresh private remote directory:
python3 scripts/smoke-managed-remote.py --host HOST --remote-dir /tmp/FRESH_DIRECTORY
```

## Live research and dynamic collaboration follow-up

The owner consoles now expose consented teams and invitations separately from
owner-configured groups, including real team board/memory access, spawned-agent
lineage, task origins and lifecycle changes. Terminal Teams/Events views use
reviewed exact IDs and versions. Polling preserves independently selected action
recipients rather than changing them when a new child enters the sorted registry.

A real sandboxed, model-free dynamic study was started paused and stepped from
the browser. Its participants created and joined a team, spawned a helper, and
admitted that helper only at the next-round boundary. An owner team-memory edit
and membership removal appeared as separate assisted-run mutations; the removed
participant's actual memory read was immediately denied. The phase deadline
remained unchanged. The web interface distinguishes a responsive broker from a
runner heartbeat and labels headless workers without promising terminal input.

A second real 80×24 terminal/browser pass reviewed and stepped one phase from
each surface, preserved physical spaces in intervention reasons, displayed the
consented team, and stopped at the next-round boundary after child admission.
Fresh page loads then displayed the authenticated final saved state with all
writes disabled. Terminal offline status remains visible across navigation;
saved worker counters do not claim current execution state.

Review found and fixed an atomic admission race where a committed pause/stop
could lose to a stale running publish, and an evidence-validation gap where
missing intervention metadata could turn an assisted result into apparently
clean evidence. Tests now cover both cases, exact one-phase stepping, deadline
preservation, authenticated runner provenance, recorded owner steering, and
exclusion of assisted runs from default estimates. A second permission bug let
named delegated namespace capabilities bypass team consent; durable team
namespaces now require actual membership, including after removal/dissolution.

Saved research metadata remains readable after broker shutdown through the
original authenticated owner token, without enabling writes or inventing live
tasks, panes or permissions. Rotation and damaged checkpoints fail closed.
Legacy single-task pilots separately print their active owner-console commands
and save metadata-only lifecycle pointers; they do not gain phase controls or
independent assisted-run classification. None of this establishes hidden model
reasoning, native-interactive research turns, or model cooperation quality.

The scale report distinguishes live PTYs from synthetic definitions and records
snapshot sizes/timings. In the inspected fixture, five snapshot samples were
69–95 ms at forty definitions and 284–355 ms at two hundred. These are observations
from this machine and workload, not production capacity guarantees.

This is bounded acceptance coverage. It does not establish multi-day provider
reliability, the quality of model collaboration, or 200 simultaneous model
processes. Recovery digests detect damaged state; they do not defend against an
owner who can rewrite both records and their recovery metadata. Remote trace
collection remains unsupported without a central collector and was not validated.

## Ten-reviewer usability pass — 1 November 2026

Ten additional reviewers independently exercised distinct user journeys, in
batches of three alongside the coordinating reviewer. Each used disposable
fixtures and cleaned owned processes. The original reviewed binary was
`69c0c68`; confirmed issues were then retested against candidate builds. These
are separate fixtures, not one concurrent deployment or ten model participants.

| Reviewer | Actual scenarios and outcome |
| --- | --- |
| 1 — First day | Default and named workspace setup, paths with spaces, cross-directory use, repeated start, project mismatch, coordination, stop/restart and missing providers. Recovery text and unlimited-run deadline display improved. |
| 2 — Terminal operator | Twelve native command sessions and two independent PTY clients; duplicate Unicode titles, exact focus/input, three editor sizes, frozen long readers and popup return. Multiline cursor movement and explicit input-limit feedback added. |
| 3 — Web operator | Keyboard search/selection, scoped memory, Unicode drafts across views, 480×800 layout, graph Space selection, literal HTML titles and broker outage/reconnect. Draft/target retained; joined emoji title support added. |
| 4 — Communication | Ten native terminals, forty assigned-credential checks and a separate mixed-mode hierarchy. Overlapping pairs, scoped/public boards, directional command authority, isolation and reconnect passed. Contradictory board selectors now fail before posting. |
| 5 — Memory | Real concurrent writers, private/shared separation, overlapping namespaces, deletion, expiry, revocation and reconnect. Terminal tombstone review and both consoles' memory pagination fixed. |
| 6 — Existing sessions | Two real external PTYs using transparent compiled echo fixtures named Claude/Codex. Stable discovery IDs, explicit helper setup, reconnect, stale process rejection and native input after removal passed without injected keystrokes. |
| 7 — Research | Three small isolated studies: completion/observation/archive/cache, paused stale-version rejection/one-phase step/resume, and stop/classification/explicit fresh attempt. Original trial evidence retained. |
| 8 — Dynamic teams | Real Bubblewrap delegated spawn, consent, history, scope denial, lifecycle, inherited permissions and cascade stop. Team displays now explain retained membership with blocked inherited access. |
| 9 — Failures | Native EOF with peer survival, broker loss, fail-closed operations and explicit restart preserving prior history. Inventory now separates coordination status from worker liveness. |
| 10 — Scale | 225 configured session definitions, 205 teams, 105 memory entries and 105 queued tasks. Fixed registry pagination in inventory, owner snapshots and bridge reconnect lookup; the exact tail ID remains present and selected. |

The native sessions above ran command participants. External-discovery fixtures
were clearly identified echo programs, not authenticated provider sessions. The
large registry consisted of synthetic metadata, with no agent processes. No
provider/model requests or new remote-machine validation were made.

Fixes preserve the reviewed identity and explicit action boundaries:

- Missing attach and stopped inventory output now provide the exact quoted
  workspace recovery command. Native-only activity says there is no automatic
  deadline; an unknown deadline remains unknown and real deadlines remain visible.
- A message specifying both `--group` and `--board` is rejected without a write;
  successful board posts identify their actual namespace.
- The terminal editor moves vertically through display rows and retains its
  preferred column across short rows. Home/End select logical line edges;
  Ctrl-A/Ctrl-E retain whole-buffer navigation. Rejected oversized input keeps
  the draft and explains the limit. The final PTY replay handles partial
  nonblocking writes, so a large paste and its closing marker are fully delivered;
  the initial oversized-paste observation alone was not sufficient evidence.
- Memory review uses the exact head version for missing/deleted/expired keys,
  checks the version again when retrieving live values, and keeps version
  adoption separate from saving. Memory lists follow every returned cursor and
  discard late pages from a previous namespace.
- Team access explanations come from the broker's recursive authorization rules
  and stable ancestor IDs, rather than inferring access from consent or titles.
  Registry pagination also fails explicitly instead of presenting partial data
  as a complete inventory.

Final validation passed the full uncached race suite with managed, experiment,
research and sandbox integrations enabled, plus all 58 frontend scenarios. The
coordinator's final browser pass rendered the joined-emoji title correctly and
loaded all 126 memory records, then opened the exact `pagination-125` entry and
verified its saved value and version. The earlier web reviewer's browser surface
became unavailable for this follow-up; the coordinator used its independently
available browser connection. Team access presentation has backend, terminal and
DOM regression coverage with real broker metadata; it was not manually retested
in a browser after the fix. All final observer processes and tabs were closed.

Dense selected-session snapshots in the 225-definition/205-team fixture initially
took 1.72–2.23 seconds and contained about 444 KB on this host. CI then caught a
timeout in the unchanged four-second 225-session snapshot regression. Boundary
evaluation repeatedly loaded the same agent, control, team and group inputs.
Those reads are now reused within one authenticated, read-only evaluation;
mutations remain uncached and each later request reads fresh state. Permission
parity, revocation across requests, cached failures and a linear SQL-query bound
have regression coverage. Independent review found no authorization regression.
Three snapshots against the same retained fixture, with no reseeding or user
mutations, then took 80.2, 62.8 and 70.3 ms with the same counts and selected ID.
These are bounded local observations, not a production capacity guarantee.

The research reviewer also suggested printing a direct analysis command after
an owner stop; existing analysis commands remain available. Neither observation
is evidence of model collaboration quality or multi-day provider reliability.

## Extended usability acceptance pass

A further pass on the same day followed everyday terminal users, people with
many independently started sessions, operators of overlapping groups, and
researchers starting, observing and reopening studies. The fixtures used command
participants and actual terminals; none made provider or model calls. The counts
below describe separate fixtures and must not be added into a claim about one
concurrent deployment.

| Situation | Bounded validation in this pass |
| --- | --- |
| Terminal workbench | Thirteen actual command-only sessions, 30 team records and 20 memory entries; 80×24, 80×20 and 60×24 terminals, including resize while reading/editing |
| Daily workspace | Twelve actual Bubblewrap command sessions plus 48 synthetic registry entries; duplicate, Unicode and long titles; eleven overlapping pairs alongside one existing group; 25 memory entries |
| Reading and navigation | Long message/task/memory/event content, full frozen readers, scrolling to the tail, stable selection during refresh, off-screen team/memory selection and returning to the original view |
| Input and failed drafts | Physical spaces, fast keys, single-character and longer bracketed paste, long editor drafts, delayed failures, explicit draft restoration and exact original recipients/namespaces |
| Concurrent memory edits | Actual saved-version conflict, retained draft beside the latest saved value, explicit adoption of the displayed version without a save, then a separate version-checked save |
| Knowledge and native input | Scoped board/shared-memory access in overlapping groups, isolation and retained historical context, private-memory separation, and continued native terminal interaction |
| Research | Three complete model-free studies, completed-run cache reopening, saved status after broker shutdown through both output and state paths, and logical SQLite contents compared before/after reopening |
| Automated regressions | Full uncached Go integration suite passed with the race detector; 55 frontend regression scenarios passed |

The synthetic registry entries exercised identity, search and display behavior;
they were not 48 additional running agents. The command-only terminal sessions
exercised real process/input/focus and broker behavior; they do not establish
how a native model will interpret instructions or cooperate.

The pass found and fixed these usability problems:

- Long results and dense lists could hide the information needed to act. The
  terminal now offers a frozen full-content reader, keeps the selected row
  visible, and shows task results before their instruction/provenance. Resizing
  and polling preserve the reader's record and readable content.
- Physical spaces could disappear from composer input, while a one-character
  clipboard paste could be interpreted as a control key. Editors now retain
  those spaces, and paste outside an editor does not execute shortcuts.
- Failed messages, board posts, memory edits and native setup could lose useful
  drafts. Ctrl-R explicitly restores the original target and draft without
  resending it or replacing a newer open editor. A memory conflict can now be
  reviewed against the exact saved value/version: one Enter adopts only that
  displayed version, and a separate Enter in the editor attempts the save.
- Team actions could open a different selected terminal. Opening an action
  target now uses the exact agent selected in Teams.
- CLI help sometimes attempted discovery or treated help as a session/path.
  Help now returns before those actions. Literal message text after `--` stays
  text, while the legacy two-target raw-pane form keeps its original routing.
  Copied attach/connect hints quote paths and retain the selected state.
- A failed session registry read could look like an empty workspace. Inventory
  now reports unknown registered/bridge counts separately from observed native
  terminals and gives a recovery command. Read failures no longer imply an
  uncertain write. Removal reports descendant coordination revocation and
  distinguishes it from confirmed process termination.
- Saved research status through an explicit state directory was unavailable
  after the broker exited. Output and state paths now reopen the same
  authenticated broker checkpoint with an unknown runner heartbeat. Cached
  pointer metadata is not accepted as authoritative status, and reopening does
  not restart workers or create new turns. Logical SQLite comparison checks
  durable content rather than relying on database-file byte identity.
- Browser polling could disrupt reading position, expanded details or keyboard
  focus. Regression scenarios cover preserving those contexts, graph keyboard
  controls, stable recipients, unsaved-change protection and explicit memory
  conflict review. These automated browser checks are distinct from manual
  browser acceptance.
- Onboarding mixed original pane-layout commands with everyday managed
  work-session commands. The guides now explain workspace/session/group IDs,
  role versus title, exact group-board selection, external-session recovery,
  terminal shortcuts and research observation/resumption.

Final manual browser acceptance used the live daily-workspace fixture and the
compiled owner console. At 480×800 and 800×600, connection state, Refresh, focus,
close and autonomy controls remained visible without page-wide horizontal
overflow. The background computed to pure black. Keyboard skip navigation kept
the owner connection and moved focus into the workspace. Reply focus, an expanded
event and graph-node keyboard focus survived polling; Space selected the exact
graph agent. A revoked synthetic recipient stayed selected with its switches
disabled rather than selecting another recipient.

The browser also retained a memory draft across entry and namespace switches.
A competing broker write caused a version conflict; review displayed the exact
draft beside version 2. Adopting that version left the broker value unchanged,
and a separate Save wrote the draft as version 3. An attempted reload retained an
unsent message; explicitly clearing that disposable draft allowed navigation.
The final initial connecting view disabled owner mutations before the first
snapshot. Independent review found one further destination problem: removing a
board could silently change an unsent board draft to direct delivery. The fix
keeps the original unavailable board selected and blocks Send until an explicit
new choice. Manual access revocation confirmed that behavior against the broker;
four added regressions cover revoked, read-only and restored board drafts.

This pass adds no new remote-machine validation. The earlier SSH evidence above
remains the remote coverage for this report. The results establish bounded
model-free usability and orchestration behavior on the tested machine, not
provider reliability, model quality, multi-day operation or unbounded scale.

An earlier [CI run](https://github.com/pagel-s/agentmux/actions/runs/41242918692/job/124660332398)
reported an unexpected tmux 3.2a server exit that initially resisted local
reproduction. A subsequent [failure with retained diagnostics](https://github.com/pagel-s/agentmux/actions/runs/41247017287/job/124672444506)
identified queued output in the test's two control-mode clients when a retained
worker pane exits. A local stress reproducer also crashed Ubuntu's exact tmux
3.2a package. Its pending-output path can use a freed pane event;
[tmux 3.3 guards the exited descriptor before accessing that data](https://github.com/tmux/tmux/blob/3.3/control.c#L623-L630).

Agentmux's ordinary attach uses native terminal clients. The integration fixture
now uses real PTY clients too, retaining the independent navigation, simultaneous
worker-exit, detach, owned cleanup and foreign-session assertions. It does not
patch tmux or claim control-mode reliability on 3.2a. Unexpected server death still
fails the test. CI retains failed model-free tmux logs for seven days; the fixture
server and clients use a minimal environment without inherited user or CI
credentials, and successful fixtures remove their logs. Local package checks use
host libraries and do not reproduce the full CI VM.
