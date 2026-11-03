# Everyday control center

Agentmux keeps each native conversation interactive. The terminal is the main
work surface; the local black/purple web app provides observation and reviewed
control. Both surfaces identify actions by stable session ID.

## Upgrade an existing workspace

`make install` updates the `amx` executable. New workspaces use the
new controller. Existing workspaces keep their pinned controller and native
conversations. A new `amx attach -s NAME` upgrades the observer without
restarting those agents; backend features such as retirement, memory patches
and complete task counts require the updated controller. An older controller
reports unsupported operations; it does not silently substitute a weaker edit.
An unavailable complete-history API stays local to Board/Events: the roster
and owner controls remain connected, with recent snapshots labeled as partial.
Use a fresh workspace for the complete feature set, or stop and reopen an old
one only after its conversations are finished.

## Find and act

- **Ctrl+P** finds a session by title, ID, provider, project or original terminal.
  Enter opens its native terminal; Tab selects it in the current workbench.
- **Ctrl+K** opens searchable Actions. Search for rename, budget, delivery,
  attention, disconnect, archive or recovery. Each action shows its target and
  explains unavailable actions.
- **Shift+N** creates another session using the selected provider and project.
  Enter an optional task title and review the launch. Advanced settings remain
  available through Actions.
- **F1** returns from a native terminal to that attachment's workbench. Focus
  follows the exact native session; editing drafts keep their original target.
- **Ctrl+W** opens the workspace chooser while retaining an editor or review.
  The chooser owns the keyboard immediately while inventory loads; Esc cancels
  late results and returns to the unchanged draft or review.
  Interactive handoff uses the outer attachment, preserving previous dashboards
  and other clients. A standalone observer shows an exact attach command.

The web Actions menu invokes the same kinds of session operations. Sessions,
Existing terminals and Connections use the same meaning across surfaces.
Connections has graph and searchable list representations. The list pages 50
entries at a time, distinguishes loaded permissions from uninspected sessions,
and reveals keyboard focus. Explicit selection reveals its roster entry;
background refreshes preserve your current focus. Attention filters
show recorded review, delivery blockage, uncertainty and failure; they do not
infer whether a model is thinking or waiting at an unreported native prompt.

The v0.12.0 web graph automatically groups enabled connections and
shared-access links. Drag or **Shift+Arrow** pins a node; local pins survive
connection changes for that workspace/study attempt. **Auto arrange** releases
pins and refits, while **Fit** preserves them. An unchanged visible topology
stays still through polling, title changes and selection. Denied preview edges
do not influence grouping, and arranging nodes changes no permissions. Use
**List view** to inspect exact directions or work beyond the graph's 40-session
display limit.

## Connect and disconnect precisely

```sh
amx link claude codex -s work
amx allow claude codex commands -s work
amx mode codex review -s work
amx unlink claude codex -s work
```

`unlink` disables only the exact pair/group contribution. Select a particular
group with `amx disconnect --group GROUP_ID -s work`. Other groups and explicit
permissions can still allow access; inspect the effective explanation.
Reconnect preserves the group's saved instruction setting. A saved group can
still be disabled after an original member has been revoked or retired.

`amx isolate claude -s work` disconnects Claude from all peer coordination.
Workbench and web isolation controls review affected groups, scopes and teams.
Isolation also dissolves teams owned by the selected session for **every**
member, removes its memberships, and withdraws pending invitations. These team
changes are permanent: reconnecting does not restore the original team. Reviews
list and version-check the exact team effects, including an initially empty set.
Already accepted tasks need separate cancellation, and previously returned
information remains known. Permission to send and native delivery are separate:
note/ping subscriptions can leave an allowed message in the inbox. The controls
show the receiver's reason and a route to its delivery settings.

## Recover without guessing

Stopping or revoking a session in a dashboard reviews the exact session and
descendants first. Detaching keeps them running. Native Manual pauses future
peer admission; human-steered turns continue until they settle or the human
interrupts them explicitly. Untouched Codex peer turns cancel using their
original thread and turn IDs, including after a conversation switch.

Uncertain task recovery reviews the exact task/version and allowed fail/retry
choice. Expired execution leases also appear in owner recovery and attention,
while recorded delivery state remains unchanged. Expired cancellation permits
fail resolution only; deadlines can also rule out retry.
A retry is another execution and can repeat side effects. The web retains an
unconfirmed submission's identity across reconnect so checking/replaying the
same submission does not silently create a second instruction. Editing a
draft creates a distinct intent only after the previous uncertainty is handled.

Existing external bridges keep their original terminal location. Their helper
can read the broker inbox after the native agent receives the setup; it does
not retrofit automatic delivery into the existing process. Known ownership in
another authenticated workspace is displayed before preparing another bridge.

## Keep history and retire finished sessions

Board/inbox history is filtered at the broker before pagination. Task history
has recipient/state filters. Reading an older event keeps that identity until
Home explicitly returns to live activity.

After an owned session is stopped and its worker exits:

```sh
amx retire SESSION_ID -s work
amx ls --archived -s work
```

Retirement releases the active definition slot while preserving the immutable
ID, attribution and history. A retired ID cannot be reused or revived. External
bridges can be revoked and retired without closing their original terminals.
Value-only editing of live memory preserves sources, tags and absolute expiry.
After reviewing a deleted or expired entry, saving deliberately creates a new
live value using the exact reviewed CAS version. Concurrent import changes
report a conflict rather than claiming the archive was already present. The
exposure ledger distinguishes current access from authenticated retrieval of a
specific version; retrieval does not prove understanding. Ledger retention is
bounded to 10,000 receipt records and absence is not proof of no prior exposure.

## Follow a study

```sh
amx research watch PRIVATE_OUTPUT --follow
amx research study PRIVATE_OUTPUT
amx research history PRIVATE_OUTPUT --type team.join --limit 100
```

Study watching continues across completed attempts and gaps between trials.
Status and archived history verify the saved trial plan against the frozen
manifest; inconsistent plans are rejected rather than changing the observed
completion count or omitting archived trials.
The terminal research details expose trial, admitted population, deadline and
phase tasks. Web **Follow study** observes later trials; mutations retain an
exact attempt fence and old reviews cannot apply to a new trial. Archived
coordination and retrieval evidence can be searched and paged after shutdown.
A stop accepted during the last phase prevents the next trial. At trial
transitions, pause and a consumed step keep the next attempt paused. Without
`--start-paused`, an unused step permits exactly one phase; `--start-paused`
takes precedence and pauses every new attempt. Carried controls are audited
interventions.

Behavior CSV schema 2 preserves decision choices, partners, received escrow,
reward pools, lineage and phase-task IDs without exporting prompts. Dynamic
population analysis includes trajectories and labels baselines as using the
initial fixed population, with a comparability warning when composition changes.

For a multi-session CLI setup, specify the project on every addition; selecting
a workspace does not inherit its project directory:

```sh
amx up -s daily -p /path/to/project -d
amx new reviewer --harness codex -p /path/to/project -s daily
amx new investigator --harness claude -p /path/to/project -s daily
```
