# Interactive work sessions

Start with `agentmux start` from your project directory after installing and
logging in to Claude Code and Codex. It opens a workbench for the default
workspace `work`; select a session and press Enter to type into its native CLI.
F1 returns to the workbench. A workspace contains sessions and an owner
coordination service (the broker).

Use `interaction: interactive` per agent for a native terminal; omit it for
managed task execution. Both modes share the coordination broker and its permissions.
Each agent has one terminal/worker lock. Changing interaction mode requires a
new launch definition/run rather than a second process sharing a conversation.

## Recognize sessions throughout the day

Use a task title such as “Fix login redirect” to recognize a conversation. Titles
are editable and may repeat; the permanent session ID remains visible and is
used for every attach, message, permission and collaboration operation.

```sh
agentmux add --harness claude --project ~/projects/api --title "Fix login redirect"
# The command prints a generated stable ID; choosing a name is optional.
agentmux label SESSION_ID "Investigate expired login cookie"
agentmux label SESSION_ID ""  # Clear the title without changing the session.
agentmux sessions
```

The workbench and web app show title, project, provider and ID. Session details
also distinguish workspace sessions from connected external terminals and show
creation/start times and tmux/TTY locations when available. Untitled sessions use
a project/provider fallback. No task description is inferred from prompts or
transcripts. Two sessions with the same title remain separate identities.

In the terminal press `/` to find a session by task, project, provider, ID or
terminal location; Enter selects the exact result. `L` edits the selected task
title. `N` asks what you are working on and generates an ID automatically. In the
web app use **Find a session**, **Rename**, and **Sessions**. Titles are labels,
not role instructions; editing one never restarts the native conversation.
When adding a session, `--harness` selects Claude Code, Codex, Pi or prompt-free
native Antigravity;
`--title` labels the human task, while `--role` supplies optional instructions
to the agent at launch.

For a pristine native launch, use `agentmux add --harness claude --native-only`.
This appends no Agentmux prompt or role and preserves the native CLI's local
configuration. Broker credentials remain available for coordination you request
manually; listing/attaching works normally. It cannot be combined with `--role`.
Native Antigravity is available with `--harness antigravity --native-only`; its
direct CLI does not expose a verified appended-system-role interface. Use the
Pi bridge when you want injected Agentmux context with that provider.

Change a coordinated session's role with `agentmux role SESSION_ID "New role"`,
or press **V** in the terminal workbench. The role editor uses version checking
and preserves a failed draft. **Ctrl-R** reviews the latest role; Enter adopts
only the reviewed version before a separate save. Managed workers capture the
current role when claiming the next task. Native conversations stay running and
receive an informational inbox notice; `managed call identity` returns the
current role and `role_version`. No user turn is injected and in-flight inputs
are unchanged. Roles grant no broker permissions. **N** includes an explicit
coordinated/native-only profile choice before the optional role step.

| Name or ID | Where to use it |
| --- | --- |
| Workspace name, such as `work` | `--session work` selects the team and saved state |
| Registered session ID, such as `claude` or an ID printed by `add`/`connect` | `attach`, `send`, `label`, `role`, `relaunch`, `collaborate`, `isolate`, `remove` |
| Observed terminal ID from `sessions` | `connect` registers that live external terminal |
| Group ID printed by `collaborate` | `board --group ID` selects its shared board |

Provider-native conversation IDs belong to the provider's own history UI;
they are not Agentmux session IDs. Uppercase placeholders in examples below
must be replaced with the IDs or paths from your own workspace.

## Start and attach

With Claude Code and Codex installed and logged in:

```sh
agentmux start --project /path/to/project
# In another terminal:
agentmux attach claude
agentmux attach codex
agentmux dashboard
agentmux web --open
```

`start --detach` starts without attachment. Commands accept `--session NAME` or
`--state DIRECTORY`; the default session is `work`. These are alternative selectors
for existing workspaces: supplying both is rejected. `start` may combine them to
name a workspace stored in a custom directory. Do not reuse a saved session
for a different project or definition. `down` stops owned agents and broker in
order, retaining their data. Detaching or closing a dashboard leaves them running.
Running the same `start` command again, including `--config FILE`, attaches to
the running workspace only if the configuration still matches its saved definition.
Each attachment has its own dashboard process, selection and drafts. Only the
underlying agent terminals are shared; attaching twice to the same agent still
shows the same native conversation. Runtime service windows stay out of normal
navigation. **Ctrl-b**, release, then **Shift+D** opens read-only service logs.
An explicit `--sandbox` must also match the running broker's mode; reopening never
changes that mode. A mismatch is rejected without restarting the workspace.
Use `managed restart --state DIRECTORY` for explicit whole-workspace restart.

Use `agentmux relaunch AGENT_ID` to restart one exited workspace-owned native
session under the same ID while its healthy peers keep their processes. It starts
a fresh native conversation by default. `--resume picker`, `--resume last` or
`--resume NATIVE_ID` makes the native history choice explicit; it does not renew
deadlines or revive revoked identities. The dashboard offers the same flow with
`U`. A controller started before this feature needs one explicit workspace restart
after upgrading; it is never silently replaced.
Antigravity accepts `--resume last` or an explicit conversation ID; it has no
picker mapping. External bridges, revoked agents and still-running native CLIs
cannot be relaunched through this command.

Native updates may exit successfully and ask you to restart. Agentmux retains
the final output and marks that CLI `exited`; it never restarts it automatically.
New trusted-local interactive setups save the selected public launcher, so a
normal symlink-based native update is used on the next explicit relaunch.
Managed and sandboxed definitions retain their frozen executable dependencies.
If an older local definition directly invokes a removed or outdated native
binary, select its installed public launcher for this one invocation:

```sh
agentmux relaunch codex --native-launcher "$(command -v codex)"
```

This requires an updated running controller and an absolute executable path.
It applies only to trusted-local interactive sessions, preserves trailing
arguments, and does not rewrite the saved definition or change sandbox mounts.
It does not repair interpreter-plus-script definitions such as
`node /old/package/cli.js`: their script argument is preserved too. Review those
definitions explicitly, or create a new session with the updated setup.
An older controller rejects this option with upgrade guidance; it is never
silently ignored. Add the workspace's `--session NAME` or `--state DIR` selector
when it is not the default workspace.

`agentmux workspaces` lists named workspaces and remembered custom state paths.
Use `--json` for structured inventory or `--register DIR` to add an older custom
location. **Shift+W** opens the terminal chooser: Enter opens another observer; closing it
returns to the previous workspace without losing that observer's drafts. The web
Sessions view lists workspace commands. Identical names remain distinct by state
path. A failed or exhausted live check reports unknown instead of inventing a
stopped process; opening an unknown workspace attempts observation without starting
agents.

The current directory does not select a workspace for later commands. If you
used `start --session api`, keep using `--session api`, including on `add`,
`sessions`, `connect`, `attach`, `web` and `down`. For a second workspace:

```sh
agentmux start --session billing --project /path/to/billing
# Later, from any directory:
agentmux attach codex --session billing
agentmux dashboard --session billing
```

If `start` reports a different saved project, use `attach --session NAME` to
reopen that workspace, or choose a fresh name for the new project. If you only
detached, attach again; no restart is needed. After `down`, running the original
`start` command launches the saved definition again. Choose prior conversations
through each provider's native history controls; relaunching does not promise
automatic conversation resumption. See [recovery](traces-and-recovery.md) for
broker/task uncertainty and upgrades.

Tasks in both consoles browse the complete broker history. In the terminal,
`PgDn` loads older records, `PgUp` returns toward newer records and `f` searches
instructions, results or IDs; an empty search clears the filter. Web Tasks offers
search, recipient/state filters and Load older. These pages do not include human
turns typed only into a native provider's own conversation UI.

The coding preset preserves the normal local Claude/Codex environment, tools,
skills, MCP configuration, native approval prompts, login and history. It does
not enable Claude safe mode or suppress Pi customizations. Codex keeps the native
sandbox policy from your configuration. This is trusted host execution: broker
ACLs control coordination APIs, but do not isolate local files or same-user
credentials. Choose a sandbox when those boundaries are required.

`start --sandbox bubblewrap` uses selected credential snapshots, private homes,
separate native history, and an isolated broker. It cannot transparently inherit
arbitrary host skills/MCPs or their filesystem/network authority. Codex then
explicitly relies on the outer sandbox, while retaining native approvals. No
host policy is changed. A sandbox failure never falls back to local execution.

The preset creates the `team`/`project` namespaces with no agent access, denies
peer coordination, and disables automatic wakeups. It sets
`interactive_workspace: true`, so ordinary native terminals run until you close
them instead of expiring after a workday. This mode requires every initial agent
to be interactive and delegated spawning disabled. Managed automation/research
retains positive runtime budgets and durable deadlines. An explicit existing
deadline is still honored. Saved configurations are never silently migrated.
The controller is always checksum-pinned outside writable projects before launch,
including projects added later.

```sh
agentmux add backend --harness claude --project ~/projects/backend
agentmux add review --harness codex --project ~/projects/frontend --role "Review changes"
agentmux collaborate backend review
agentmux board "Use the group's API proposal as context."
agentmux isolate review
agentmux remove backend
```

One workspace can contain sessions from unrelated projects. `add` starts another
native session with a stable name/ID, normal local configuration by default, and
no peer authority. `remove` closes its owned terminal and revokes its bridge,
retaining native history. Names are not silently reused for different identities.
For an isolated addition use `--sandbox bubblewrap`; Pi also requires an explicit
`--auth-file` and `--auth-provider`. Dynamic additions require an owner broker able
to inspect the selected host project and authentication boundaries. An isolated
broker fails closed when those paths are unavailable; configure such teams before
launch instead of exposing host paths to the broker.

`collaborate A B` enables notes/pings and creates a dedicated owner-managed group
with its own board and memory. The exact member set reuses its stable group and
history after reconnect/restart. Other groups do not gain access. A notes-only
group contributes no command authority; pre-existing configuration or explicit
owner command grants still apply. Inspect effective Connections permissions. `board` selects the only active
group automatically; with multiple groups use `--group ID` or `--board NAMESPACE`.
`isolate A` revokes both directions of peer coordination, shared scopes and group
membership, and fences future delegated spawning/team formation. It leaves native
input running. It does not cancel already accepted tasks or erase previous context.
These owner-managed groups are distinct from consent-based agent-created research
teams; neither operation bypasses those teams' membership rules.

For several independent groups, save the **Group**, **Board**, and **Memory**
values printed by each `collaborate` call. The web app lists them in
**Teams → Owner collaboration groups**; select the exact board in the Board
view or the exact memory namespace in Memory.

```sh
# Assume api, api-review, billing and billing-review are registered IDs.
agentmux collaborate api api-review
agentmux collaborate billing billing-review
# Replace GROUP_ID with the first command's printed Group value:
agentmux board "API proposal ready for review" --group GROUP_ID
```

Repeating `collaborate` with the same complete member set reuses its group and
restores current membership in its retained history; explicit owner scope denies
remain effective until reset. A different member set
creates another group. A session in two groups can read both authorized contexts;
it can carry that information between them. `isolate api-review` removes that
session from **all** its groups and revokes peer coordination in both directions,
not just the board currently displayed. It leaves other members running.

Collaboration groups contribute permissions independently. Disabling one group
removes its note/ping/optional command contribution and its own scoped access;
another active overlapping group keeps its grants. Explicit owner edge/scope
overrides take precedence, including denies. Notes-only does not revoke a
configured command relationship. Connections shows each effective grant's source;
Memory distinguishes stored owner intent from effective read/write permission.

Isolation adds labeled suspension overlays without overwriting existing owner
intent. Reconnecting selected sessions removes their pair's isolation overlays,
restores their configured static scope intent, and rejoins the selected group.
Unrelated isolated relationships remain blocked. Explicit denies survive reconnect.
Use Connections **M/P/I** or Memory **R** to reset an exact override to underlying
policy. Already-read knowledge and accepted tasks are never erased.

Older saved groups have no contribution version and require an explicit migration
choice before a group edit; no changes are made by default. Review the historical
pair grants, then choose `--legacy-policy preserve` to keep them as direct owner
intent, or `--legacy-policy group` to remove untagged pair allows while retaining
all denies and unrelated controls. Connections **c** provides an explicit **1/2**
review for a legacy pair; larger groups can use the CLI or web. A reviewed/new
group contributes permissions independently. See [live permissions and resource
controls](live-permissions.md) for exact migration and reset semantics.

Each `attach` opens an independent tmux client view. You can keep Claude in one
terminal window and Codex in another; switching one does not move the other.
Detaching removes that view, not the agent. Session IDs remain distinct from the
workspace name, tmux panes, and provider-native conversation IDs.

To review/edit a configured team before launch:

```sh
agentmux managed setup --preset coding --output team.yaml
agentmux start --config team.yaml --session my-team
```

For an idle managed review team, select `--preset reviewer-pair`; for coordinator,
analyst and critic roles select `--preset research-team`. These preserve explicit
authentication and sandbox choices, provide a shared board/memory, and queue no
initial tasks. They do not enable automatic wakeups, spawning or dynamic-team
authority. Use the research runner for a bounded experimental study and its
preregistration/analysis rather than treating a role preset as a research design.

## Existing sessions

These commands use an already running Agentmux workspace. If you have none,
start one first with `agentmux start --detach`. The independently started native
terminal remains in its original window throughout discovery and connection.

```sh
agentmux sessions
agentmux connect OBSERVATION_ID --title "Review login changes"
# Optional: --name existing-review chooses the stable ID explicitly.
```

Discovery inspects current-user live TTY process names and the default tmux
server. It reads no prompts, transcripts, command arguments, target environments
or authentication files. Observation IDs include process lifetime identity so PID
reuse does not attach an old identity to a new process. Generic `node` wrappers,
non-TTY clients and some platform metadata cannot be identified reliably.

Connecting prepares a private per-agent helper and prints a short setup to paste
into the existing native conversation. There is no automatic input injection,
restart, model request or claim that the agent consumed the setup. The helper pins
its assigned credential and broker socket; it cannot act as the owner. The session
can then read permissions/inbox and use authorized coordination tools through its
normal shell capability. Native tool access must permit that helper path.

A bridge does not retrofit process/filesystem isolation into an existing terminal.
Disconnecting it revokes broker access and leaves the external process alone.
Use `agentmux remove REGISTERED_ID` to disconnect that bridge; `isolate` keeps
the bridge available but disables its peer collaboration. `attach` cannot move
an external session into the workspace; return to its original terminal.
Transport failures never trigger automatic registration retries. Explicit reconnect
with the same name safely recovers an uncertain registration and preserves identity.
If an observation is no longer live, run `sessions` again and select the new
observation; an exited process cannot be reconnected by its old observation ID.
If a terminal is absent from discovery, use `add` for a new supported native
session instead of guessing an ID.

Without `--project`, preset setup creates separate workspaces. JSON setup specs
accept `interaction` and optional `workdir` per agent. Pi interactive sessions
support configured compatible endpoints and selected authentication. Native
Antigravity is available with `--native-only`; use its Pi bridge for injected
coordination context. See the [provider acceptance matrix](provider-acceptance.md)
for the distinction between native startup, actual inference and tool coverage.

## Terminal dashboard

Keys are case-sensitive: `N`, `L`, `V`, and `P` mean Shift plus that letter. For
`Ctrl-b b`, press Ctrl-b, release both keys, then press b. The same two-step
prefix applies to the other Ctrl-b bindings. Shortcuts below depend on the
active view and selected record; the footer shows the actions available there.
Use Left/Right, Tab or a view number to switch between all eight views.
Connections and Teams use h/l or Alt-Left/Alt-Right for their recipient/target.
In an editor, printable characters are text;
use **Ctrl-G** for help and **Esc** to cancel before using navigation shortcuts.

| Key | Action |
| --- | --- |
| ↑/↓ or j/k | Select agent |
| Enter | Focus its tracked terminal; an exited native CLI opens a same-ID relaunch review. Outside tmux, open an independent attachment |
| / | Find by task, project, provider, ID or external terminal location |
| Shift+L | Edit the selected session's human task title; empty clears |
| Shift+V | Edit its coordinated role with CAS; prompt-free native sessions reject injected roles |
| Shift+W / Shift+U | Choose another workspace observer / deliberately relaunch the selected exited owned native session |
| ? / Ctrl-G | Open context help outside an editor / while editing or reviewing |
| F1 / Ctrl-b g in a native session | Return to the workbench |
| Ctrl-b, then b / Shift+P / m / t in a native session | Open Board / Connections / Memory / Tasks in a popup |
| Ctrl-b, then Shift+D | Choose read-only Broker or Supervisor logs without leaving the current terminal |
| Tab / Shift-Tab or 1–8 | Switch Board, Agents, Connections, Memory, Tasks, Sessions, Teams, Events |
| ←/→ or Ctrl+←/→ in every view | Switch views; editors, readers and reviews keep their own arrow keys |
| [ / ] in Teams; h/l or Alt+←/→ | Choose a team; choose an exact agent for a membership action |
| i / r / d in Teams | Review invite / remove or revoke invitation / dissolve with the current version; hints follow target membership |
| e / t in Teams | Edit team name/mission / propose the selected member as owner; acceptance is separate |
| b / v in Teams | Compose to its board / inspect its shared memory as owner |
| [ / ] in Events; Home | Inspect an event; return to following the newest event |
| P / R / S / T in Events | Research pause / resume / one phase / stop, each with a reason and separate review |
| Shift+N / x | Add a native session with an explicit profile / revoke the selected session and descendants; owned shutdown is requested |
| s / b | Compose a direct message/instruction or selected shared-board note |
| + / − in Board | Select an allowed board namespace |
| Enter / Esc in composer | Submit / cancel; Alt-Enter inserts a newline |
| Ctrl-T in a direct-message/reply composer | Switch the frozen draft's note/instruction intent without changing its text or recipient |
| ←/→ / Home / End in composer | Edit the draft without changing its recipient |
| f in Board | Filter all coordination, team board, or selected agent inbox |
| [ / ] then r in Board | Select a message and reply |
| h/l or Alt+←/→ in Connections | Select the recipient for the selected sender |
| c / d in Connections | Connect selected sender/recipient in a group / make sender independent |
| c in Sessions | Prepare a bridge for the selected observed terminal; paste setup manually |
| m / p / i in Connections | Toggle messages / pings / commands |
| Shift+M / Shift+P / Shift+I in Connections | Reset the selected explicit override to the underlying effective policy |
| [ / ] in Memory | Select a board or memory namespace |
| r / w in Memory | Toggle read / write |
| Shift+R in Memory | Reset the selected explicit scope override; membership remains authoritative |
| v in Memory | Inspect shared entries; [ / ] selects an entry |
| e / n in memory entries | Edit an existing entry / create a key and value |
| [ / ] in Tasks | Select a task in the loaded history page |
| PgDn / PgUp / f in Tasks | Load older / newer history pages / search all task instructions, results or IDs |
| r / c in Tasks | Follow up / cancel |
| o in Board, Agents, Memory, Tasks, Sessions, Teams or Events | Open the selected loaded content in a frozen, read-only reader |
| ↑/↓ or j/k in the reader | Scroll one line |
| PgUp / PgDn; Home / End in the reader | Scroll a page; jump to the start / end |
| Esc / q / o in the reader | Return to the previous view while keeping the observer open |
| Ctrl-R outside a composer | Restore the last failed message, board, memory, role or new-session draft for review |
| Ctrl-R inside a memory/role editor | Review the latest saved value/version alongside the retained draft; adopting a version does not save |
| Shift+B in Board or Agents | Review four agent admission-budget fields, then confirm with the current resource version |
| a | Pause/resume configured autonomy; does not stop in-flight work |
| q | Detach from the persistent workbench, or close a popup/standalone view; team stays running |

The **Teams** view is for consent-based teams created by permitted agents. An
empty Teams view does not mean your native sessions are missing. For everyday
pairing, press **3** for Connections, choose a recipient with **h/l** or
**Alt+←/→**, then **c**. Those owner collaboration groups share a scoped board and memory.

The roster keeps process state visible even with long project names. A native
CLI marked `exited` can still have an active coordination identity; it does not
necessarily mean the CLI failed. **Enter** or **Shift+U** opens a deliberate
relaunch review. Nothing restarts until you confirm; retained output is labeled
as last output instead of live output.

**Shift+W** can open an observer for another workspace. If this observer is
hosted inside a different tmux server, **Enter** shows an exact `agentmux attach`
command to run in a separate terminal. Observation and owner controls remain
available in the current observer; native input uses that separate terminal.
This avoids nesting tmux clients or switching unrelated attachments.

Use `o` when a preview is too short. The reader keeps the selected record and
its loaded text fixed while activity refreshes, so scrolling cannot switch to a
different result. Tasks show the result first, followed by the instruction and
provenance/reported trace. Agents shows session details and a loaded terminal
preview or unconfirmed external-bridge setup. Memory shows the selected entry,
destination author and asserted source claims, or full boundary details;
Sessions can show loaded bridge setup, and Teams/Events show full record details.
Previously loaded content remains readable offline. The reader does not fetch
older history; close and reopen it to read a refreshed record. Control and
mutation keys are inactive while reading.

After a failed send/reply, board post, memory/role save or new-session setup, inspect
current delivery/state before retrying: a lost response may follow a successful
write. Close any newer composer with Esc, then press Ctrl-R to review the retained
draft. It restores the original recipient, intent, reply reference or namespace/key;
memory/role edits retain their original version check, and new-session drafts retain
provider, project, prompt-free/coordinated profile and role. Nothing is sent automatically. Failed title edits
are restored separately with `L` on their original session.

For a memory or role version conflict, press Ctrl-R **inside that editor**. This
loads a frozen comparison of the current saved value/version and your retained
draft. Enter in that reader adopts only the displayed version and returns to
the editor; it does not save. Esc/q/o returns with the original version and
draft. After reviewing or editing your draft, a separate Enter in the editor
saves with the adopted version check. A later concurrent change still conflicts.
Offline or failed reviews retain the draft without adopting a new version.

Human input goes directly to the native CLI, including its normal approval
prompts. Broker messages do not inject keystrokes or impersonate user input.
Ask an interactive agent to read its inbox when you want it to process notes or
queued peer instructions. It does not automatically claim or execute a managed
task; managed workers provide that task-execution lifecycle.

```sh
agentmux send codex "Please read the proposed API on the team board."
agentmux board "Proposal: keep parsing and persistence separate."
agentmux inbox
agentmux reply TASK_OR_MESSAGE_ID "Please also check error handling."
agentmux reply MESSAGE_ID "A note, no task." --mode note
agentmux reply BOARD_MESSAGE_ID "For this board." --board BOARD_NAMESPACE
agentmux reply BOARD_MESSAGE_ID "Private follow-up." --to codex --mode note
```

`send`/`reply` default to `--mode auto`: a note for interactive agents and an
instruction task for managed workers. Choose `--mode note` for information or
`--mode instruction` for a task explicitly. An instruction to a native terminal
is broker work the agent must read; it never injects a native user turn. Both
forms print the record ID. They never automatically retry writes;
a transport failure can leave delivery uncertain. The lower-level `managed call`
API exposes explicit request IDs for durable retries.

`reply` accepts direct messages and tasks. Messages and instruction tasks retain
broker-validated `reply_to` and a stable `thread_id` instead of changing the text
to simulate ancestry. A shared board post has several possible audiences, so
choose `reply ID "Follow-up" --board NAMESPACE` to retain the same board, or
`reply ID "Follow-up" --to AGENT_ID` for a private follow-up to an existing reader.
Reply references cannot widen a private conversation or move a thread to a
different board. Every direct recipient must already be able to read the
referenced message; replies grant no history access. To share something with a
new audience, compose a new message explicitly rather than attaching a private
reference. Board replies are informational notes, never instruction tasks.
Empty replies are rejected before creating any message or task.

## Web app

`agentmux web --open` opens a local full-screen app; without `--open`, it prints a
private link. `--port NUMBER` selects a loopback port; by default one is allocated.
Ctrl-C stops the web process independently of the team. Never give its private
owner link to agents.

The app offers agent selection, read-only terminal previews, recent messages,
follow-ups with explicit intent, complete searchable task history, cancellation,
shared-memory inspection and versioned editing, role/profile setup, autonomy and
admission controls, and the same live permission switches as the terminal
dashboard. Board is the main focus pane. Graph shows directional message/ping/command
connections and shared board/memory access; select a connection to adjust it. Nodes
can be moved with dragging or arrow keys, and layouts persist in the browser.
Fit and zoom controls work by keyboard and on narrow screens; native terminal
input remains in tmux.
Spawned agents retain inherited delegation limits; live overlay edits currently
apply to configured agents and owner-added native/bridged sessions. The graph
displays at most 40 active agents, keeping selected identities visible; the roster
reads every broker registry page. Recent activity is bounded to 40 records per
category, while Tasks separately pages the complete broker task history. Copy
the attach command or focus an existing tmux window for native
input. Terminal emulation remains in tmux.

No external scripts, fonts, services or analytics are loaded. APIs require a
random bearer capability and validate Host/Origin. A restrictive content policy
and text-only rendering protect the owner interface from agent-authored output.
This is not a remote collaboration server.

## Live boundaries

Owner-only `control.update` overlays immutable launch configuration, with version
checks, durable request receipts and audited events. Agents cannot edit controls
or inspect another agent's permissions. Shared namespace edits cannot expose
private memory or bypass team consent. Coordinated native sessions initially
receive role, broker usage and effective-boundary context; `--native-only`
appends none of that prompt. Managed workers fetch a fresh boundary snapshot and
capture the current owner role at each task claim.

Agents query `managed call boundaries.get`; `identity` and inbox listings also
report `boundaries_version`. Interactive models receive no automatic model turn
or replacement system prompt after an owner edit. They must recheck live
permissions before coordinating; broker enforcement applies regardless of stale
context.

Removing instruction authority prevents new tasks; cancel accepted tasks
separately. Removing read access stops future board/shared-memory reads but
cannot remove information already present in history. Provider networking and
project filesystem sharing are separate process boundaries.

From a workspace-native agent's shell, the authenticated bridge is:

```sh
"$AGENTMUX_CLI" managed call identity --json '{}'
"$AGENTMUX_CLI" managed call boundaries.get --json '{}'
"$AGENTMUX_CLI" managed call message.list --json '{}'
"$AGENTMUX_CLI" managed call task.list --json '{}'
```

The native process receives its own credential/socket through its environment;
the agent should not print or locate credential contents. If the owner's native
shell policy filters out the bridge variables, report that rather than searching
owner state. These are agent-credential checks. The human's `send`, `board` and
dashboard use owner authority, so a successful owner send does not prove a peer
is permitted to send. Check the exact shared namespaces returned by
`boundaries.get` or printed by `collaborate`; the coding preset's configured
`team` and `project` names remain unshared until explicitly granted. After
isolation, test future reads of the old explicit board/memory namespace: an
unfiltered inbox may still contain the agent's own authored/direct receipts.

## Consented teams and capacity

Owner collaboration groups and consented dynamic teams have separate lifecycles.
The coding preset leaves dynamic teams and spawning disabled. A configured
managed team can enable and delegate them; `agentmux team --help` documents
create/list, metadata edits, invitations, removal/dissolution, ownership proposals,
participant acceptance and atomic invited moves. Team name/mission edits retain
the same namespace identities. An ownership proposal changes no authority until
the proposed owner accepts with its own credential. A participant move requires
its pending destination invitation and both current team versions.

`capacity.get {}` and `identity.capacity` report the caller's remaining lifetime
spawns/teams, depth, team slots, templates and admission reasons; the owner can
inspect a selected agent. A snapshot reserves no capacity. **B** in Board/Agents
reviews the selected agent's four admission-budget fields; the web uses the same
CAS broker rules. Lower-level owner `capacity.update` can edit complete agent or
session admission-budget objects and shorten the admission deadline. It cannot
refund permanently charged attempts, extend the original process deadline or
change enforced launch resource caps. These are conservative broker admissions,
not hard external provider billing limits. See [live permissions and
resources](live-permissions.md) and [dynamic teams](dynamic-teams.md).

## Limits and verification

Isolated native history is stored per agent. After restart, choose an exact
prior conversation through the native UI; uncertain managed turns are never
silently resumed. Credential snapshots are read-only: refresh externally, exit
and deliberately relaunch the affected native session if its login needs renewal.
Managed workers reload selected authentication between turns.

Interactive mode currently rejects managed conversation leases, automatic
wakeups, structured turn traces, disk capsules and allowlist egress rather than
skipping their guarantees. Bubblewrap host/none networking and process resource
limits remain available. Managed workers retain existing hardening features.
Native tool output in terminal/history is not a complete immutable broker trace.

Dashboards page the agent registry, task history and memory entries. Board/Event
activity remains a bounded recent window; these are not complete native-provider
transcripts or an unlimited activity archive.
The owner-added registry is limited to 200 definitions, and owner collaboration
groups to 128 lifetime groups. Permission overlays are limited to 4,096 explicit
edge/scope overrides. Switches
edit configured and owner-managed agents/shared namespaces; dynamic children retain bounded
inherited capabilities and dynamic teams retain consent operations.

`python3 scripts/smoke-managed-interactive.py` checks real tmux input/focus, two independent terminal clients, popup composition and
detach/cleanup, isolated terminal fixtures and broker, message/input separation,
live grant revocation/restoration and authenticated web controls. It makes no model
calls. Native model behavior requires separate provider validation.

`python3 scripts/smoke-managed-workspace.py` exercises ten live model-free terminals
in separate projects: dynamic additions, three disjoint pairs, overlapping groups,
private/shared data checks through assigned credentials, isolation without losing
native input, stable reconnects and closing one session while nine remain usable.
Research and dynamic workflows use the same observability surfaces. Agent-created
teams remain distinct from owner collaboration groups: an invitation grants no
context until the participant accepts. Team namespace access follows actual
membership, including parent-delegation and revocation fences. Membership changes
invalidate displayed permission versions immediately.

The Agent view distinguishes automatic admission state from accepted work still
executing, reports pending/used turn counts and persistent-history fences, and
shows parent/template/children for spawned workers. Events report observed actor,
subject and membership changes rather than inventing an agent's intent.

Research and legacy experiment workers run headlessly. Their consoles expose
tasks, results, reported traces and lifecycle information; they do not offer a
native tmux pane. Research adds reviewed phase-barrier controls and a separate
runner heartbeat. See [live research](research.md#observe-and-intervene-live) and
the [single-task pilot](experiments.md#observe-a-running-pilot).
