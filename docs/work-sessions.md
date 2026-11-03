# Interactive work sessions

Start with `amx start` from your project directory after installing and
logging in to Claude Code and Codex. It opens a workbench for the default
workspace `work`; select a session and press Enter to type into its native CLI.
F1 returns to the workbench. A workspace contains sessions and an owner
coordination service (the broker).

For a coding request that runs through implementation, fresh reviews and checks
without a persistent terminal workspace, use [`amx work`](working.md). Its
saved patch is applied explicitly. [`amx flow`](workflows.md) provides advanced
comparisons, missions and handoffs; those runs do not join this workspace or
inherit its permissions automatically.

Use `interaction: interactive` per agent for a native terminal; omit it for
managed task execution. Both modes share the coordination broker and its permissions.
Each agent has one terminal/worker lock. Changing interaction mode requires a
new launch definition/run rather than a second process sharing a conversation.

v0.13.0 includes `amx context`, `amx task` / `tasks`, exact task opening
with `task ID --open`, `amx observe`, the searchable running-workspace chooser and direct
session task history in Actions. See [installation](getting-started/installation.md)
and explicitly upgrade an older workspace controller before using its new controls.

## Inspect running work

```sh
amx observe --session work
amx observe --session work --watch --json
amx task TASK_ID --open --session work
```

The operational view separates broker availability, ownership-checked worker
panes, live leases, worker check-ins, native event metadata and adapter readiness.
Counts include all retained tasks; bounded attention keeps old failures and
uncertain attempts visible. Silence alone leaves model progress unknown.
Use the printed exact task command for results, native receipts, traces and
reviewed recovery. See [observability](observability.md) for freshness, JSONL,
partial observations and the separate evidence used by coding runs and research.

## Recognize sessions throughout the day

Use a task title such as “Fix login redirect” to recognize a conversation. Titles
are editable and may repeat; the permanent session ID remains visible and is
used for every attach, message, permission and collaboration operation.

```sh
amx add --harness claude --project ~/projects/api --title "Fix login redirect"
# The command prints a generated stable ID; choosing a name is optional.
amx label SESSION_ID "Investigate expired login cookie"
amx label SESSION_ID ""  # Clear the title without changing the session.
amx sessions
```

The workbench and web app show title, project, provider and ID. Session details
also distinguish workspace sessions from connected external terminals and show
creation/start times and tmux/TTY locations when available. Untitled sessions use
a project/provider fallback. No task description is inferred from prompts or
transcripts. Two sessions with the same title remain separate identities.

In the terminal press Ctrl+P (or `/`) to find a session by task, project,
provider, ID or terminal location. Enter opens the exact native terminal; Tab
selects it in the workbench; Esc cancels. Discovered terminals open details
for their original terminal instead. `L` edits the selected task
title. `N` asks for an optional title and reviews a new generated ID using the
selected session's provider, project and coordinated/native-only choice. In the
web app use **Find a session**, **Rename**, and **Sessions**. Titles are labels,
not role instructions; editing one never restarts the native conversation.
When adding a session, `--harness` selects Claude Code, Codex, Pi or prompt-free
native Antigravity;
`--title` labels the human task, while `--role` supplies optional instructions
to the agent at launch.

For a pristine native launch, use `amx add --harness claude --native-only`.
This appends no Agentmux prompt or role and preserves the native CLI's local
configuration. Broker credentials remain available for coordination you request
manually; listing/attaching works normally. It cannot be combined with `--role`.
Native Antigravity is available with `--harness antigravity --native-only`; its
direct CLI does not expose a verified appended-system-role interface. Use the
Pi bridge when you want injected Agentmux context with that provider.

Change a coordinated session's role with `amx role SESSION_ID "New role"`,
or press **V** in the terminal workbench. The role editor uses version checking
and preserves a failed draft. **Ctrl-R** reviews the latest role; Enter adopts
only the reviewed version before a separate save. Managed workers capture the
current role when claiming the next task. Native conversations stay running and
receive an informational inbox notice; `managed call identity` returns the
current role and `role_version`. Manual mode leaves it in the broker inbox;
Review/Auto can deliver an attributed role/boundary notice through the native
adapter. Roles grant no broker permissions. In the quick **N** flow, **Ctrl+O**
opens advanced provider, project, coordinated/native-only and role settings;
Actions also exposes **Advanced new session**. The quick review explicitly shows
the provider-default model and trusted local host execution. It does not copy
an existing model override or sandbox; use explicit CLI launch flags for those
choices.

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
amx start --project /path/to/project
# In another terminal:
amx attach claude
amx attach codex
amx dashboard
amx web --open
```

`start --detach` starts without attachment. Commands accept `--session NAME` or
`--state DIRECTORY`. Inside an owned tmux attachment, an omitted selector uses
that verified workspace; outside, ordinary commands default to `work`.
`start` always defaults to `work` when no selector is given. These are alternative selectors
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

Use `amx relaunch AGENT_ID` to restart one exited workspace-owned native
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
amx relaunch codex --native-launcher "$(command -v codex)"
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

`amx workspaces` lists named workspaces and remembered custom state paths.
Use `--json` for structured inventory or `--register DIR` to add an older custom
location. **Ctrl+W** (also **Shift+W**) opens the workspace chooser. From an
interactive attachment, Enter hands off to that workspace while preserving the
previous workbench and drafts. A standalone observer shows an exact attach
command when native input needs a different tmux server. The web Sessions view
lists workspace commands. Identical names remain distinct by state
path. A failed or exhausted live check reports unknown instead of inventing a
stopped process; opening an unknown workspace attempts observation without starting
agents.

The current directory does not select a workspace for later commands. Outside
an owned attachment, if you used `start --session api`, keep using `--session api`, including on `add`,
`sessions`, `connect`, `attach`, `web` and `down`. For a second workspace:

```sh
amx start --session billing --project /path/to/billing
# Later, from any directory:
amx attach codex --session billing
amx dashboard --session billing
```

In v0.13.0, `amx context --session billing` explains the target,
selection source and runtime status without attaching or starting agents.
Bare `amx` / `amx a` outside an attachment uses a sole running workspace or
offers a running-workspace chooser. In that chooser, `/` or Ctrl+F starts a
name/path/status filter, arrows or Ctrl+N/P choose a result, Ctrl+U clears and
Enter opens its exact state path. This selection does not change the default
for later ID-based commands in an unrelated shell.

If `start` reports a different saved project, use `attach --session NAME` to
reopen that workspace, or choose a fresh name for the new project. If you only
detached, attach again; no restart is needed. After `down`, running the original
`start` command launches the saved definition again. Choose prior conversations
through each provider's native history controls; relaunching does not promise
automatic conversation resumption. See [recovery](traces-and-recovery.md) for
broker/task uncertainty and upgrades.

For project directories, shell splits, multiple agents in one tmux window,
normal tmux keys and last-session reattachment, see
[terminal folders and layouts](terminal-workflow.md). Tab switches sidebar/content
focus; ↑/↓ navigates that focused area and ←/→ changes views.

Tasks in both consoles browse complete retained broker history. In the terminal,
`PgDn` loads older records, `PgUp` returns toward newer records and `f` searches
instructions, results or IDs; an empty search clears the filter. Web Tasks offers
search, recipient/state filters and Load older. These pages do not include human
turns typed only into a native provider's own conversation UI.

Current source builds also expose direct CLI inspection:

```sh
amx tasks --to codex --query "login" --limit 25 --session billing
amx task TASK_ID --session billing
amx task TASK_ID --open --session billing
```

`tasks` pages retained history newest first; its **More** command preserves the
workspace and filters. `task --open` loads the exact task directly, even when
it is older than the first page or its recipient is retired. It shows the
current task/version, instruction, result and delivery/recovery evidence.
**Ctrl+K** opens recovery actions when available; confirmation keeps the exact
task/version. Opening never retries, resolves, cancels or approves a task.
Terminal/web Actions offers **Tasks for selected session** to reset old filters
and open that recipient's history. Web attention rows also offer **Inspect tasks**.

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
amx add backend --harness claude --project ~/projects/backend
amx add review --harness codex --project ~/projects/frontend --role "Review changes"
amx collaborate backend review
amx board "Use the group's API proposal as context."
amx isolate review
amx remove backend
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
**Groups & teams → Owner collaboration groups**; select the exact board in the Board
view or the exact memory namespace in Memory.

```sh
# Assume api, api-review, billing and billing-review are registered IDs.
amx collaborate api api-review
amx collaborate billing billing-review
# Replace GROUP_ID with the first command's printed Group value:
amx board "API proposal ready for review" --group GROUP_ID
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
amx managed setup --preset coding --output team.yaml
amx start --config team.yaml --session my-team
```

For an idle managed review team, select `--preset reviewer-pair`; for coordinator,
analyst and critic roles select `--preset research-team`. These preserve explicit
authentication and sandbox choices, provide a shared board/memory, and queue no
initial tasks. They do not enable automatic wakeups, spawning or dynamic-team
authority. Use the research runner for a bounded experimental study and its
preregistration/analysis rather than treating a role preset as a research design.

## Existing sessions

These commands use an already running Agentmux workspace. If you have none,
start one first with `amx start --detach`. The independently started native
terminal remains in its original window throughout discovery and connection.

```sh
amx sessions
amx connect OBSERVATION_ID --title "Review login changes"
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
Use `amx remove REGISTERED_ID` to disconnect that bridge; `isolate` keeps
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
coordination context. See [provider compatibility](provider-compatibility.md)
for supported routes and separate startup, inference and tool checks.

## Terminal dashboard

Keys are case-sensitive: `N`, `L`, `V`, and `P` mean Shift plus that letter. For
`Ctrl-b b`, press Ctrl-b, release both keys, then press b. The same two-step
prefix applies to the other Ctrl-b bindings. Shortcuts below depend on the
active view and selected record; the footer shows the actions available there.
Use Left/Right or a view number to switch between all eight views.
Tab changes sidebar/content focus. Up/Down navigates the focused area.
Connections and Teams use h/l or Alt-Left/Alt-Right for their recipient/target.
Below 100 columns, the terminal labels **Connections** as **Links** and
**Existing terminals** as **Existing**; their view numbers and controls stay the same.
In an editor, printable characters are text;
use **Ctrl-G** for help and **Esc** to cancel before using navigation shortcuts.

| Key | Action |
| --- | --- |
| ↑/↓ or j/k | Navigate the focused sidebar or content |
| Enter | Open the focused native terminal or content reader; an exited native CLI opens a same-ID relaunch review. Outside tmux, open an independent attachment |
| Ctrl+P or / | Find by task, project, provider, ID or terminal location; Enter opens native, Tab selects here, Esc cancels |
| Shift+L | Edit the selected session's human task title; empty clears |
| Shift+V | Edit its coordinated role with CAS; prompt-free native sessions reject injected roles |
| Ctrl+K | Search and invoke Actions with exact targets and unavailable reasons |
| Shift+N | Quick new session using selected provider/project/profile; optional title, then review model default and host execution; Ctrl+O opens advanced settings |
| Ctrl+W / Shift+W | Choose another workspace; an attachment hands off interactively and retains prior drafts |
| Shift+U | Deliberately relaunch the selected exited owned native session |
| ? / Ctrl-G | Open context help outside an editor / while editing or reviewing |
| F1 / Ctrl-b g in a native session | Return to the workbench |
| Ctrl-b, then b / Shift+P / m / t in a native session | Open Board / Connections / Memory / Tasks in a popup |
| Ctrl-b, then Shift+D | Choose read-only Broker or Supervisor logs without leaving the current terminal |
| Tab / Shift-Tab | Focus sidebar or content |
| 1–8 | Select Board, Sessions, Connections, Memory, Tasks, Existing terminals, Teams, Events |
| ←/→ or Ctrl+←/→ in every view | Switch views; editors, readers and reviews keep their own arrow keys |
| [ / ] in Teams; h/l or Alt+←/→ | Choose a team; choose an exact agent for a membership action |
| i / r / d in Teams | Review invite / remove or revoke invitation / dissolve with the current version; hints follow target membership |
| e / t in Teams | Edit team name/mission / propose the selected member as owner; acceptance is separate |
| b / v in Teams | Compose to its board / inspect its shared memory as owner |
| [ / ] in Events; Home | Inspect an event; return to following the newest event |
| PgDn / PgUp / Shift+F in Board or Events | Page older / newer retained broker history / search within its scope |
| g in Events | Toggle all events versus the selected session's events |
| P / R / S / T in Events | Research pause / resume / one phase / stop, each with a reason and separate review |
| x | Revoke the selected session and descendants after review; owned shutdown is requested |
| s / b | Compose a direct message/instruction or selected shared-board note |
| + / − in Board | Select an allowed board namespace |
| Enter / Esc in composer | Submit / cancel; Alt-Enter inserts a newline |
| Ctrl-T in a direct-message/reply composer | Switch the frozen draft's note/instruction intent without changing its text or recipient |
| ←/→ / Home / End in composer | Edit the draft without changing its recipient |
| f in Board | Filter all coordination, team board, or selected agent inbox |
| [ / ] then r in Board | Select a message and reply |
| h/l or Alt+←/→ in Connections | Select the recipient for the selected sender |
| c / d in Connections | Connect selected sender/recipient in a group / make sender independent |
| c in Existing terminals | Prepare a bridge for the selected observed terminal; paste setup manually |
| m / p / i in Connections | Toggle messages / pings / commands |
| Shift+M / Shift+P / Shift+I in Connections | Reset the selected explicit override to the underlying effective policy |
| [ / ] in Memory | Select a board or memory namespace |
| r / w in Memory | Toggle read / write |
| Shift+R in Memory | Reset the selected explicit scope override; membership remains authoritative |
| v in Memory | Inspect shared entries; [ / ] selects an entry |
| e / n in memory entries | Edit an existing entry / create a key and value |
| [ / ] in Tasks | Select a task in the loaded history page |
| PgDn / PgUp / f in Tasks | Load older / newer history pages / search all task instructions, results or IDs |
| y / z in Tasks | Toggle recipient / cycle state; an explicit filter leaves a CLI-opened exact-task view |
| r / c in Tasks | Follow up / cancel |
| o in Board, Sessions, Memory, Tasks, Existing terminals, Teams or Events | Open the selected loaded content in a frozen, read-only reader |
| ↑/↓ or j/k in the reader | Scroll one line |
| PgUp / PgDn; Home / End in the reader | Scroll a page; jump to the start / end |
| Esc / q / o in the reader | Return to the previous view while keeping the observer open |
| Ctrl-R outside a composer | Restore the last failed message, board, memory, role or new-session draft for review |
| Ctrl-R inside a memory/role editor | Review the latest saved value/version alongside the retained draft; adopting a version does not save |
| Shift+B in Board or Sessions | Review four agent admission-budget fields, then confirm with the current resource version |
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

**Ctrl+W** (also **Shift+W**) opens the workspace chooser even while a draft or
review is open. An interactive `amx attach` hands off between tmux servers using
its outer client; other attachments remain where they are, and returning
restores the previous workbench and draft. A standalone observer instead
shows an exact attach command for native input in another server.

Use `o` when a preview is too short. The reader keeps the selected record and
its loaded text fixed while activity refreshes, so scrolling cannot switch to a
different result. Tasks show the result first, followed by the instruction and
provenance/reported trace. The Sessions view shows session details and a loaded terminal
preview or unconfirmed external-bridge setup. Memory shows the selected entry,
destination author and asserted source claims, or full boundary details;
Existing terminals can show loaded bridge setup, and Teams/Events show full record details.
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
In the default Manual mode, ask an interactive agent to read its broker inbox.
For integrated Claude, Codex and Pi sessions, choose Review or Auto in terminal
**Sessions** or web **Session detail**, or use `amx delivery ID review|auto` to
enable attributed native turns.
Sender Commands permission and receiver delivery mode are separate. See
[native delivery](native-delivery.md) for approvals, subscriptions, lifecycle
evidence, limits and provider-specific capabilities.

```sh
amx send codex "Please read the proposed API on the team board."
amx board "Proposal: keep parsing and persistence separate."
amx inbox
amx reply TASK_OR_MESSAGE_ID "Please also check error handling."
amx reply MESSAGE_ID "A note, no task." --mode note
amx reply BOARD_MESSAGE_ID "For this board." --board BOARD_NAMESPACE
amx reply BOARD_MESSAGE_ID "Private follow-up." --to codex --mode note
```

`send`/`reply` default to `--mode auto`: a note for interactive agents and an
instruction task for managed workers. Choose `--mode note` for information or
`--mode instruction` for a task explicitly. An instruction to a native terminal
stays queued in Manual mode; Review/Auto can deliver it through the native adapter. Both
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

`amx web --open` opens a local full-screen app; without `--open`, it prints a
private link. `--port NUMBER` selects a loopback port; by default one is allocated.
Ctrl-C stops the web process independently of the team. Never give its private
owner link to agents.

The app offers agent selection, read-only terminal previews, recent messages,
follow-ups with explicit intent, searchable retained message/event/task history, cancellation,
shared-memory inspection and versioned editing, role/profile setup, autonomy and
admission controls, and the same live permission switches as the terminal
dashboard. Board is the main focus pane. **Connections** shows directional message/ping/command
connections and shared board/memory access; select a connection to adjust it.
In v0.13.0, enabled connections automatically group connected
nodes. Denied preview edges do not affect arrangement. Dragging or **Shift+Arrow**
pins a node; its position persists locally for that workspace and study attempt,
including after topology changes. Unpinned nodes arrange around pins after a drag.
**Auto arrange** releases pins and refits; **Fit** only adjusts the viewport.
Ordinary refreshes, title changes and selection do not rearrange an unchanged
visible topology. Arrangement does not write permissions or establish cooperation.
Fit and zoom controls work by keyboard and on narrow screens; native terminal
input remains in tmux.
Spawned agents retain inherited delegation limits; live overlay edits currently
apply to configured agents and owner-added native/bridged sessions. The graph
displays at most 40 active agents, keeping selected identities visible; the roster
reads every broker registry page. Recent activity snapshots are bounded to 40
records per category; Board, Events and Tasks separately search/page retained
broker history. Current source Actions opens the selected session's task history,
and attention rows offer **Inspect tasks** for historical recipients too. Copy
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
managed team can enable and delegate them; `amx team --help` documents
create/list, metadata edits, invitations, removal/dissolution, ownership proposals,
participant acceptance and atomic invited moves. Team name/mission edits retain
the same namespace identities. An ownership proposal changes no authority until
the proposed owner accepts with its own credential. A participant move requires
its pending destination invitation and both current team versions.

`capacity.get {}` and `identity.capacity` report the caller's remaining lifetime
spawns/teams, depth, team slots, templates and admission reasons; the owner can
inspect a selected agent. A snapshot reserves no capacity. **Shift+B** in Board/Sessions
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

Dashboards page the agent registry, retained message/event/task history and
memory entries. Board/Event searches and pages are scoped at the broker before
pagination. An older controller without those history APIs exposes only a
labeled partial recent snapshot, never a complete search or older page.
Retained broker history is not a native-provider transcript or an unlimited
archive of external activity.
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

Terminal **Sessions** and web **Session detail** distinguish automatic admission
state from accepted work still executing, report pending/used turn counts and
persistent-history fences, and show parent/template/children for spawned workers. Events report observed actor,
subject and membership changes rather than inventing an agent's intent.

Research and legacy experiment workers run headlessly. Their consoles expose
tasks, results, reported traces and lifecycle information; they do not offer a
native tmux pane. Research adds reviewed phase-barrier controls and a separate
runner heartbeat. See [live research](research.md#observe-and-intervene-live) and
the [single-task pilot](experiments.md#observe-a-running-pilot).

The [control center guide](usability-control-center.md) describes reviewed
shutdown, scoped history, attention, exact group disconnect, retirement,
exposure receipts and continuous study observation. Existing `--view agents`
and `--view sessions` names remain compatible for registered-session detail
and discovered terminals respectively.
