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
