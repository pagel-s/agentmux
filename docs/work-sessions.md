# Interactive work sessions

Use `interaction: interactive` per agent for a native terminal; omit it for
managed task execution. Both modes share the coordination broker and its ACLs.
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
`--state DIRECTORY`; the default session is `work`. Do not reuse a saved session
for a different project or definition. `down` stops owned agents and broker in
order, retaining their data. Detaching or closing a dashboard leaves them running.
Running `start` again in the same project attaches to its running default team.
Use `managed restart --state DIRECTORY` for explicit restart.

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
history after reconnect/restart. Other groups do not gain access. Commands remain
disabled until explicitly granted in Connections. `board` selects the only active
group automatically; with multiple groups use `--group ID` or `--board NAMESPACE`.
`isolate A` revokes both directions of peer coordination, shared scopes and group
membership, and fences future delegated spawning/team formation. It leaves native
input running. It does not cancel already accepted tasks or erase previous context.
These owner-managed groups are distinct from consent-based agent-created research
teams; neither operation bypasses those teams' membership rules.

Each `attach` opens an independent tmux client view. You can keep Claude in one
terminal window and Codex in another; switching one does not move the other.
Detaching removes that view, not the agent. Session IDs remain distinct from the
workspace name, tmux panes, and provider-native conversation IDs.

To review/edit a configured team before launch:

```sh
agentmux managed setup --preset coding --output team.yaml
agentmux start --config team.yaml --session my-team
```

## Existing sessions

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
Transport failures never trigger automatic registration retries. Explicit reconnect
with the same name safely recovers an uncertain registration and preserves identity.

Without `--project`, preset setup creates separate workspaces. JSON setup specs
accept `interaction` and optional `workdir` per agent. Pi interactive sessions
support configured compatible endpoints and selected authentication. Direct
interactive Antigravity role injection is unverified; use its Pi bridge.

## Terminal dashboard

| Key | Action |
| --- | --- |
| ↑/↓ or j/k | Select agent |
| Enter | Focus its tracked terminal; attach when outside that tmux server |
| / | Find by task, project, provider, ID or external terminal location |
| L | Edit the selected session's human task title; empty clears |
| F1 / Ctrl-b g in a native session | Return to the workbench |
| Ctrl-b b / P / m / t | Open Board / Connections / Memory / Tasks in a popup |
| Tab / Shift-Tab or 1–8 | Switch Board, Agents, Connections, Memory, Tasks, Sessions, Teams, Events |
| [ / ] in Teams; ← / → | Choose a team; choose an exact agent for a membership action |
| i / r / d in Teams | Review invite / remove or revoke invitation / dissolve with the current version |
| b / v in Teams | Compose to its board / inspect its shared memory as owner |
| [ / ] in Events; Home | Inspect an event; return to following the newest event |
| P / R / S / T in Events | Research pause / resume / one phase / stop, each with a reason and separate review |
| N / x | Add a native session / close the selected registered session |
| s / b | Compose a direct message/instruction or selected shared-board note |
| + / − in Board | Select an allowed board namespace |
| Enter / Esc in composer | Submit / cancel; Alt-Enter inserts a newline |
| ←/→ / Home / End in composer | Edit the draft without changing its recipient |
| f in Board | Filter all coordination, team board, or selected agent inbox |
| [ / ] then r in Board | Select a message and reply |
| ←/→ in Connections | Select the recipient for the selected sender |
| c / d in Connections | Connect selected sender/recipient in a group / make sender independent |
| c in Sessions | Prepare a bridge for the selected observed terminal; paste setup manually |
| m / p / i in Connections | Toggle messages / pings / commands |
| [ / ] in Memory | Select a board or memory namespace |
| r / w in Memory | Toggle read / write |
| v in Memory | Inspect shared entries; [ / ] selects an entry |
| e / n in memory entries | Edit an existing entry / create a key and value |
| [ / ] in Tasks | Select a recent task |
| r / c in Tasks | Follow up / cancel |
| a | Pause/resume configured autonomy; does not stop in-flight work |
| q | Detach from the persistent workbench, or close a popup/standalone view; team stays running |

Human input goes directly to the native CLI, including its normal approval
prompts. Broker messages do not inject keystrokes or impersonate user input.
Ask an interactive agent to read its inbox when you want it to process notes or
queued peer instructions.

```sh
agentmux send codex "Please read the proposed API on the team board."
agentmux board "Proposal: keep parsing and persistence separate."
agentmux inbox
agentmux reply TASK_OR_MESSAGE_ID "Please also check error handling."
```

`send`/`reply` choose a note for interactive agents and an instruction task for
managed workers, printing the record ID. They never automatically retry writes;
a transport failure can leave delivery uncertain. The lower-level `managed call`
API exposes explicit request IDs for durable retries.

## Web app

`agentmux web --open` opens a local full-screen app; without `--open`, it prints a
private link. `--port NUMBER` selects a loopback port; by default one is allocated.
Ctrl-C stops the web process independently of the team. Never give its private
owner link to agents.

The app offers agent selection, read-only terminal previews, recent messages,
follow-ups, task results/cancellation, shared-memory inspection and versioned
editing, autonomy controls, and the same live permission switches as the terminal
dashboard. Board is the main focus pane. Graph shows directional message/ping/command
connections and shared board/memory access; select a connection to adjust it. Nodes
can be moved with dragging or arrow keys, and layouts persist in the browser.
Spawned agents retain inherited delegation limits; live overlay edits currently
apply to configured agents and owner-added native/bridged sessions. The graph displays at most 40 active agents, while
the roster loads up to 200. Recent activity is bounded to 40 records per category. Copy the attach command or focus an existing tmux window for native
input. Terminal emulation remains in tmux.

No external scripts, fonts, services or analytics are loaded. APIs require a
random bearer capability and validate Host/Origin. A restrictive content policy
and text-only rendering protect the owner interface from agent-authored output.
This is not a remote collaboration server.

## Live boundaries

Owner-only `control.update` overlays immutable launch configuration, with version
checks, durable request receipts and audited events. Agents cannot edit controls
or inspect another agent's permissions. Shared namespace edits cannot expose
private memory or bypass team consent. Initial native system context includes
role, broker usage and effective boundaries; managed workers fetch a fresh
snapshot before each turn.

Agents query `managed call boundaries.get`; `identity` and inbox listings also
report `boundaries_version`. Interactive models receive no automatic model turn
or replacement system prompt after an owner edit. They must recheck live
permissions before coordinating; broker enforcement applies regardless of stale
context.

Removing instruction authority prevents new tasks; cancel accepted tasks
separately. Removing read access stops future board/shared-memory reads but
cannot remove information already present in history. Provider networking and
project filesystem sharing are separate process boundaries.

## Limits and verification

Isolated native history is stored per agent. After restart, choose an exact
prior conversation through the native UI; uncertain managed turns are never
silently resumed. Credential snapshots are read-only: refresh externally and
restart a terminal if its login needs renewal.

Interactive mode currently rejects managed conversation leases, automatic
wakeups, structured turn traces, disk capsules and allowlist egress rather than
skipping their guarantees. Bubblewrap host/none networking and process resource
limits remain available. Managed workers retain existing hardening features.
Native tool output in terminal/history is not a complete immutable broker trace.

Dashboards show up to 200 agents and bounded recent activity. The underlying API
supports pagination; these consoles do not browse arbitrary-size histories.
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
