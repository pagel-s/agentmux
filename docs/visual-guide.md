# Agentmux visual workflow guide

Use Agentmux for a checked coding request, several native agent sessions, bounded
automation or controlled multiagent studies. The terminal is the place to work
directly with agents; the web app provides a larger view of relationships,
shared context and evidence. Coding workflows also retain private offline HTML
reports without requiring a running work-session broker or web app.

The managed runtime and workbench are experimental. This guide follows current
`main`, including the power-user shortcuts and exact-task recovery navigation.
The base `amx work` and `amx flow` journeys are also available in
[v0.13.0](getting-started/installation.md), including the inspection and workspace
shortcuts shown here.
For a first checked fixture without provider setup,
start with [`amx demo`](getting-started/first-result.md).
Native providers must be
installed and authenticated separately. A new installation does not replace
an existing workspace's pinned controller; use a fresh workspace for all current
features, or [upgrade deliberately](traces-and-recovery.md).

Screenshots were captured on **2026-11-04** from source **6edb44bbcc91**,
using **amx 0.14.0**, in disposable command-only workspaces and a scripted research trial with explicitly
trusted local execution. They show real current controls; they measure neither
model quality nor isolation. Terminal images render live tmux captures with
trailing empty rows cropped. [Capture provenance](assets/guide/capture-provenance.json)
records source, fixture conditions and image hashes. Open any image at full size.

## Choose a workflow

| What you want | Start here | Keep control through |
| --- | --- | --- |
| One coding change with implementation, review and checks | [Checked coding change](#ask-for-a-checked-coding-change) | Preview, finite turns, fresh review, report and explicit apply |
| Compare alternative implementations or build a custom mission | [Advanced workflow plans](#compare-alternatives-or-build-a-mission) | Pinned snapshots, external checks, retained patches and freshness |
| Several independent Claude or Codex conversations | [Everyday sessions](#everyday-sessions) | Native terminals, titles, session switcher |
| Ten sessions with just a few collaborating pairs | [Temporary collaboration](#temporary-collaboration) | Exact groups, directional permissions, receiver modes |
| A concrete end-to-end collaboration example | [Write a poem together](#write-a-poem-together) | Review delivery, Tasks, Board, native terminals |
| Reusable team knowledge | [Private and shared memory](#private-and-shared-memory) | Exact namespaces, versions and retrieval evidence |
| A team working from an initial goal | [Managed and autonomous work](#managed-and-autonomous-work) | Task outcomes, wake subscriptions and finite limits |
| Agents recruiting or forming their own teams | [Dynamic teams and recruitment](#dynamic-teams-and-recruitment) | Approved templates, invitations, consent, capacity |
| Cooperation versus competition experiments | [Research and competition](#research-and-competition) | Frozen design, phase controls, objective outcomes |
| More agents or another machine | [Scale and remote workers](#scale-and-remote-workers) | Admission limits, scoped views, explicit SSH placement |

## Ask for a checked coding change

From a Git project with an initial commit and the selected native CLIs installed,
preview your request:

```sh
amx work "Fix the login redirect" -- go test ./...
```

The preview shows the check, phases, provider profiles, deadline, maximum turns
and private result location. It starts no models and creates no run files.
Replace `go test ./...` with the executable and arguments that verify your task.
The text after `--` is literal argv; use an owner-maintained script for shell logic.

[![Actual offline report from amx demo with retained check evidence and a reviewable change](assets/guide/checked-work-report.jpg?v=d3cdf4a8e036)](assets/guide/checked-work-report.jpg?v=d3cdf4a8e036)

This is a real report from the deterministic `amx demo` fixture, with no model
calls. The practical sequence for your own task is **preview → run → inspect →
explicitly apply**; the report keeps the checked content and outcome visible.

When the preview is suitable, explicitly permit provider execution:

```sh
amx work "Fix the login redirect" --allow-models -- go test ./...
amx work status
amx work show latest
```

The default profiles are Codex `gpt-6.1-sol` for implementation and Antigravity
`gemini-3.8-flash-low` for review; `--coder` and `--reviewer` choose another
supported profile deliberately. Each review starts a fresh native session with
the task, acceptance criteria and current files, excluding implementation chat
and earlier review conclusions. A final explicit approval and successful checks
are required before application. A failed check, tool denial, malformed review
or uncertain transport remains visible in the retained outcome.

In v0.13.0, copy a stable run ID from `amx work list --limit 10`
and use `amx work show RUN_ID --open` to generate and open a fresh private report.
The command stays bound to that selected run. In v0.12.0, open the saved
`report.html` path printed by `show`. Reports are separate from the live owner
app launched by `amx web`; inspection starts no models. After reviewing the
patch and evidence, apply deliberately:

```sh
amx work apply latest
```

Application rechecks the source, checked revision, final approval and patch. It
refuses stale results and leaves committing or pushing to you. After an
interrupted application, inspect `amx work status` and use `amx work recover latest`
to resolve the journal against exact source content; recovery does not replay
the patch. Interrupted provider work is never automatically replayed.

Private Git snapshots separate retained data; the default trusted local tools
still have host filesystem and network authority. On Linux,
`--isolation bubblewrap` additionally requires filesystem, PID and network
namespaces with no weaker fallback. Tools see the assigned worktree and selected
runtime dependencies; reviewer workspaces are read-only. Checks get no provider
credentials or network. Native inference needs exact allowed origins and
explicit selected credentials; isolated Antigravity uses host inference with
Pi tools inside the capsule. See [work on a task](working.md) for a complete
isolated command, provider prerequisites, budgets and operational boundaries.

## Compare alternatives or build a mission

Use [`amx flow`](workflows.md) when you need an owner-authored plan rather than
the default coding journey. Start with the
[complete command-only example](workflows.md#try-the-complete-journey-without-model-calls)
in a disposable Git project. It runs real local commands without model calls,
checks two independent candidates and retains an offline report.

| Workflow | Result to inspect |
| --- | --- |
| `flow run` | Alternatives from the same snapshot with separate worktrees and checks |
| `flow mission` | Bounded dependency steps, configured review gates and final acceptance checks |
| `flow check` | Verification pinned to the checked content; keep its sibling `.git` fingerprint store |
| `flow watch` | A bounded review for each eligible changed snapshot; unchanged content does not retrigger |
| `flow lab` | Repetitions against one starting snapshot with rotated candidate order |
| `flow explain` | Saved inputs, patch integrity and current freshness |
| `flow handoff` | A private Markdown brief; saved-brief delivery respects receiver permissions and mode |

Command plans and checks execute owner-selected code even without
`--allow-models`; that flag admits provider turns. Keep output directories outside
the source and retain the worktrees, selection manifests and evidence stores
together. `flow apply` is always a separate source mutation; `flow recover`
resolves an interrupted application without replay. See [advanced workflows](workflows.md)
for strict plan fields, isolated execution and exact saved-brief delivery.

## Navigate the terminal and web app

[![Terminal workbench with session identities on the left and coordination on the right](assets/guide/terminal-board.jpg?v=e301fabd23e6)](assets/guide/terminal-board.jpg?v=e301fabd23e6)

The left column selects a **session**. **Tab** moves focus between the sidebar
and content; the focused header is purple. Its task title can change; its Agentmux
ID stays stable. The top row selects a **view** of that session or workspace.
You do not need to unselect a session before choosing another one.

| Action | Terminal control |
| --- | --- |
| Select a different session | **Tab** to the session sidebar, then **Up/Down** |
| Navigate messages, tasks, memory or teams | **Tab** to content, then **Up/Down**; **Enter** reads a selected item |
| Find a task, provider, project or ID | **Ctrl+P**, type a query, then **Up/Down** |
| Enter the selected native CLI | **Enter** with the session sidebar focused, or from **Ctrl+P**; **F1** returns |
| Select a search result without entering its CLI | **Tab** in the session switcher |
| Leave a search, editor or review | **Esc** |
| Change views | **Left/Right** or **1–8** outside an editor |
| Find an action without remembering its key | **Ctrl+K** |
| Add a session or change its title or role | **Shift+N**, **Shift+L**, **Shift+V** |
| Choose another workspace | **Ctrl+W** or **Shift+W** |
| See help for the current context | **?**; **Ctrl+G** while editing or reviewing |
| Open the board from a native terminal | **Ctrl+b**, release, then **b** |
| Detach while keeping sessions alive | **Ctrl+b**, release, then **d** |

Uppercase shortcuts mean hold Shift. In an editor, letters and arrows edit the
draft; cancel or save it before using workbench navigation. The footer always
describes the current context. In **Connections**, **[** focuses **SENDER** in
the sidebar and **]** focuses **RECIPIENT**. **Tab** or **Shift+Tab** switches
between them; **Up/Down** changes only the focused identity. Its label is purple
and shows the arrow hint. Focus changes preserve both selections and permissions.
**h/l** or **Alt+Left/Right** also chooses a recipient. **Left/Right** still changes views.

[![Searchable terminal session switcher distinguishes two poem sessions by title and stable ID](assets/guide/terminal-switcher.jpg?v=b853fbe7fa87)](assets/guide/terminal-switcher.jpg?v=b853fbe7fa87)

Search results show titles, IDs, projects and providers. **Enter** opens a
workspace-owned native terminal; an external observation opens its details.
**Tab** keeps you in the current workbench view and changes its selected session.

[![Searchable Actions menu with shortcuts and exact session targets](assets/guide/terminal-actions.jpg?v=6087783816df)](assets/guide/terminal-actions.jpg?v=6087783816df)

Use **Ctrl+K** when unsure what to press. Actions opens the appropriate editor
or review, with the target and unavailable reasons shown before a change.
**Tasks for selected session** clears previous task filters and opens that
recipient's retained history. **Attention** enables the attention view;
the **!** shortcut toggles it.

Open the web control center with:

```sh
amx web -s daily --open
```

[![Web board filtered to a specific collaboration pair](assets/guide/web-board.jpg?v=92dda425664d)](assets/guide/web-board.jpg?v=92dda425664d)

The web sidebar selects a session; the main tabs expose its controls and the
workspace's records. **Board** filters communication, **Connections** controls
relationships, **Memory** edits shared knowledge, and **Tasks** shows deliveries
and outcomes. **Session detail** exposes role, native delivery and capacity.
**Groups & teams** distinguishes owner groups from consented teams. **Research**
appears when observing a research attempt. **Focus in tmux** returns native work
to the terminal. The web owner view can inspect records that an individual
participant is not authorized to read.

For project folders, shell splits, multiple agents in one tmux window and
`amx a` reattachment, see [terminal workflow](terminal-workflow.md).

## Everyday sessions

Start from an existing project directory, with Claude Code and Codex logged in:

```sh
amx up -s daily -p "$PWD" -d
amx name claude "Fix login redirect" -s daily
amx name codex "Review authentication changes" -s daily
amx a -s daily
```

The default coding workspace starts native Claude and Codex sessions. Select one
and press Enter, then use its normal prompt, tools, approvals, skills and MCPs.
F1 returns to the workbench. Peer collaboration and automatic delivery start off.

Add a new conversation for a new problem, without disturbing the others:

```sh
amx new billing --harness claude -p /path/to/billing \
  --title "Investigate retry failures" -s daily
amx new --harness codex -p /path/to/api \
  --title "Review cache invalidation" -s daily
amx ls -s daily
```

Omitting the name generates a stable ID. Copy that ID from the output for later
commands. Shift+N is the quick terminal flow and inherits the selected provider
and project. Its review also shows the provider-default model and trusted local
host execution; model overrides and sandboxes are not copied. A title identifies the human task;
a role supplies agent instructions and grants no permissions.

One workspace can hold unrelated projects. Two terminal windows can each run
`amx a SESSION_ID -s daily`: they have independent navigation and drafts while
sharing the underlying native conversations. Ctrl+W switches workspaces from an
owned attachment and preserves its previous dashboard. Switching workspaces
does not connect their brokers or share their data.

### Bring in a session started elsewhere

```sh
amx ls -s daily
amx connect OBSERVATION_ID --title "Existing API investigation" -s daily
```

Choose an observed terminal ID from the inventory, then paste the printed helper
setup into that original conversation. Discovery does not read prompts or send
input. Registration prepares coordination; it does not prove the agent has read
the setup. External terminals stay in their original window and use a manual
bridge rather than receiving a retrofitted native delivery adapter. Some wrapper
processes cannot be discovered reliably; use `amx new` for those cases.

The easiest arrangement for several collaborating pairs is one coordination
workspace containing those sessions or bridges. `link` takes IDs from that
workspace; it is not a cross-workspace broker federation command.

### Choose harness and isolation

| Session choice | Setup path |
| --- | --- |
| Claude Code or Codex | Native local launch preserves the installed CLI's normal configuration and history |
| Pi with a hosted provider | Select the provider, model and authentication through managed setup |
| Pi with Ollama or vLLM | Configure the exact compatible endpoint and model; verify context and tool support |
| Native Antigravity | Use `--harness antigravity --native-only`; coordination remains manual |
| A prompt-free native session | `--native-only` omits Agentmux context and injected role; broker access can still be used manually |
| An isolated worker or terminal | Select Bubblewrap and explicitly provision the required dependencies and credentials |

For local model endpoints, start with the
[Pi setup specification](https://github.com/pagel-s/agentmux/blob/main/examples/managed/setup-local-pi.json)
and [endpoint setup guide](managed-setup.md). Edit the endpoint and exact model
to match your server. A successful metadata preflight is not a successful tool
or inference test. Sandbox configuration and native approvals are separate;
choosing a local model does not provide isolation by itself.

## Temporary collaboration

Suppose `api`, `api-review`, `poet` and `editor` are registered session IDs:

```sh
amx link api api-review -s daily
amx link poet editor -s daily
```

Each exact member set gets a dedicated board and shared-memory namespace. Save
the printed **Group**, **Board** and **Memory** values. The API pair does not gain
access to the poem pair's scope. With several groups, target one explicitly:

```sh
amx board "The API proposal is ready." --group GROUP_ID -s daily
amx send api-review "Read the proposal when useful." --mode note -s daily
```

[![Owner collaboration groups show exact members board and memory namespaces and group controls](assets/guide/web-groups.jpg?v=7faa7d74bfb1)](assets/guide/web-groups.jpg?v=7faa7d74bfb1)

A board post is visible to that board's authorized readers. A direct note has
explicit recipients. An everyone-readable board is possible when configured
with that audience; linking a pair does not create a workspace-wide public board.
A session belonging to two groups can carry information between them, so avoid
overlapping membership when testing independent information boundaries.
In web Board, choose **Shared boards**, select the exact board and click
**Search** to apply that scope.

[![Terminal Connections view labels sender and recipient and explains note ping and command permissions](assets/guide/terminal-connections.jpg?v=1d51cfd74306)](assets/guide/terminal-connections.jpg?v=1d51cfd74306)

[![Web relationship graph with directional permission and receiver-delivery explanations](assets/guide/web-connections.jpg?v=0fd3a75200c7)](assets/guide/web-connections.jpg?v=0fd3a75200c7)

Use **Connections → List view** to search an exact pair when the graph is busy;
the list pages 50 entries at a time and supports arrows and Enter. Inspect the
direction shown in the controls before changing it. The graph offers an overview
of effective permissions; permission explanations remain the authority.

Enabled session connections and enabled shared-access
links automatically group connected nodes. Denied preview edges do not affect
placement. Drag a node, or focus it and use **Shift+Arrow**, to pin its position.
Pins persist in this browser for the selected workspace and study attempt, and
stay in place when connections change. Other nodes arrange around the pins after
you finish dragging. **Auto arrange** releases all pins and fits the new layout;
**Fit** changes the viewport without releasing them. Selection, title changes
and ordinary refreshes preserve the layout when the visible topology is unchanged.
Placement never changes permissions. Direction arrows still identify sender and
recipient; proximity does not establish that agents actually cooperated.

The graph shows at most 40 active sessions. Use the roster and paginated
**List view** for larger workspaces. Automatic grouping and **Auto arrange** are
also available in v0.12.0.

### Separate permission from delivery

| Control | What it allows | What it does not imply |
| --- | --- | --- |
| Messages | A note in the recipient's inbox | A native turn will start |
| Pings | An authorized notification | The receiver subscribes to pings |
| Commands | Creation of an instruction task in this direction | Receiver approval, readiness or native execution |
| Board or memory access | Read or write the exact namespace | Access to another scope or permission to command a peer |
| Receiver Manual | Human-led use and manual inbox reads | Automatic native dispatch |
| Receiver Review | Approval of each exact eligible candidate | Approval of every future message |
| Receiver Auto | Eligible turns within configured limits | Unlimited turns or a hard provider billing cap |

For example, to let `poet` request work from `editor`:

```sh
amx allow poet editor commands -s daily
amx mode editor review -s daily
```

Integrated Claude, Codex and Pi sessions support verified native delivery paths.
The screenshot's command-only fixture correctly reports that it has no native
adapter. Native Antigravity and external bridges retain manual coordination;
use the Pi bridge for the supported Antigravity integration path. Consult the
[native delivery matrix](native-delivery.md#adapter-capabilities) for provider-specific limits.

Notes, pings, board changes and memory changes have separate subscriptions.
Enabling a subscription never grants namespace access. Roles and boundary
changes reach eligible Review/Auto receivers as attributed notices; an initial
boundary synchronization can be the first pending candidate.

### End just the intended collaboration

```sh
amx mode editor manual -s daily
amx deny poet editor commands -s daily
amx unlink poet editor -s daily
```

`unlink` removes that exact group's contribution. Other groups or explicit grants
may still allow communication. **Make independent** / `amx isolate editor`
revokes that session's peer coordination more broadly and leaves native input
running. Isolation also permanently removes consented memberships and dissolves
teams that session owns, affecting their other members; review the listed effects.
Cancel already accepted tasks separately. Previously received context remains known.

## Write a poem together

This example uses two ordinary native sessions and owner-reviewed delivery.
Choose a fresh workspace name and run from an existing project directory:

```sh
amx up -s poem-guide -p "$PWD" -d
amx name codex "Poem: writer and editor" -s poem-guide
amx name claude "Poem: second stanza" -s poem-guide
amx link codex claude -s poem-guide
amx allow codex claude commands -s poem-guide
amx mode claude review --max-turns 6 --max-hops 4 -s poem-guide
amx mode codex review --max-turns 6 --max-hops 4 -s poem-guide
amx a codex -s poem-guide
```

1. In Codex, enter: **“Use your authenticated Agentmux tools to collaborate with
   Claude on a poem called The Second Cup. Write an opening stanza about an
   evening window and rain. Post the draft to our shared board, then use
   `instruct` to ask agent `claude` for an original four-line second stanza.
   Keep attribution. Assemble the poem after its result arrives.”**
2. Press F1. In **2 Sessions**, select Claude, use **Shift+A** to read the exact
   pending candidate and review its approval. Approve the intended instruction;
   a boundary notice may appear first. Web **Session detail** offers the same
   inbox review. Commands alone would leave this waiting in Review mode.
3. Enter Claude's native terminal to watch or interact. The adapter starts an
   attributed peer turn when ready; it does not insert tmux keystrokes or send
   your unsent draft. **Tasks** should advance beyond pending once the native
   transport accepts it.
4. When Claude settles, review the correlated result notice for Codex too.
   Results are subscribed by default, but Codex's Review mode still requires
   approval. Return to Codex to see it assemble the poem. The result does not
   grant Claude permission to command Codex.
5. Inspect the board, the task's result and both native conversations. A transport
   receipt alone is not the finished poem or owner verification of its quality.

From a separate owner shell, `amx mode claude -j -s poem-guide` lists status and
pending candidates. After reading one, approve its exact ID with
`amx mode claude --approve CANDIDATE_ID -s poem-guide`. Repeat for Codex's result
candidate. If you prefer automatic eligible deliveries after trying Review,
explicitly change both receivers to Auto with suitable finite limits.

When finished, set both receivers to Manual and unlink the pair. Both native
conversations remain available. `amx down -s poem-guide` instead stops the
workspace's owned processes and retains their saved data.

## Private and shared memory

[![Shared memory editor shows the selected pair namespace an agreed plan and its version](assets/guide/web-memory.jpg?v=355480b85d69)](assets/guide/web-memory.jpg?v=355480b85d69)

Private memory belongs to one authenticated agent. Shared memory belongs to an
explicit namespace with separate read/write access. Neither is an automatic
copy of a native transcript. Linking sessions gives them a shared scope; they
still need to write useful entries and read them through their tools.

In the web app, select the session, open **Memory**, choose the group's namespace,
then **Load** and select a key. Review its value and version before saving. In the
terminal, **4 Memory** exposes shared boundaries and **o** opens the selected
scope. Value-only edits of live entries preserve metadata and expiry; conflicting
edits retain the draft until you explicitly review the latest version.

A useful handoff is a short `agreed-plan` entry containing decisions, remaining
checks and artifact locations. Ask the agents to cite the source messages or
tasks when writing it. Agents use namespace `private` with their own credential
for personal notes. The owner's private namespace is separate from theirs.

**Who received context?** shows retained authenticated retrieval evidence. It
records which version was returned, not understanding or use. TTL, deletion,
export/import, provenance and optional semantic search are described in
[memory lifecycle](memory-lifecycle.md). Removing access does not erase a copy
already received in a native conversation.

## Read the evidence

| Surface | What it establishes |
| --- | --- |
| Native terminal | The provider conversation you can directly read, steer or interrupt |
| Board and replies | Broker-visible content, authors, audiences and reply ancestry |
| Connections | Current effective permissions and their sources, rather than what an agent remembers |
| Tasks and attention | Recorded delivery, results, failures, approvals and uncertainty |
| Memory and exposure | Explicit stored versions and authenticated retrieval receipts |
| Groups, teams and capacity | Membership, pending consent, parentage, effective access and remaining admissions |
| Research and archives | Protocol state, phase controls, population, interventions and retained study evidence |

These views do not reveal hidden model reasoning or all provider-side activity.
The graph describes permissions, not a claim that two agents cooperated.
Task settlement is distinct from owner verification, and a retrieved memory is
distinct from understanding it. For configured managed turns,
[trace inspection](traces-and-recovery.md) adds adapter-visible execution evidence;
it is not a complete recording of everything a model or remote service did.

### Open the exact task before recovery

v0.13.0 prints a copyable open command after `amx task TASK_ID`:

```sh
amx task TASK_ID --open -s daily
```

[![Terminal Tasks view pinned to one exact task with its version, evidence and recovery controls](assets/guide/terminal-task-recovery.jpg?v=0e9ed39b4995)](assets/guide/terminal-task-recovery.jpg?v=0e9ed39b4995)

The view reads that exact record, including older tasks and retired recipients;
it never substitutes a newer task that mentions the ID. Inspect the instruction,
result and delivery evidence. **Ctrl+K** exposes available recovery actions;
each action reviews the exact task/version before confirmation. Opening the
view does not retry or resolve anything. Check the prior process and side
effects before choosing retry.

[![Web Tasks opened for a selected session with exact recipient filtering and retained history](assets/guide/web-session-tasks.jpg?v=a62a57b2a38b)](assets/guide/web-session-tasks.jpg?v=a62a57b2a38b)

In terminal or web Actions, **Tasks for selected session** goes directly to a
recipient's retained history. Web attention rows also have **Inspect tasks**,
including historical recipients outside the active roster. Reading history
changes no recovery decision.

## Managed and autonomous work

Choose native interactive sessions when you want to type throughout the day.
Choose managed workers for queued task turns, persistent managed conversations
and bounded wakeups. Managed worker output is observable, but it is not an
always-open provider TUI. These modes can share the same broker and policy.

Use the setup wizard to review a role preset before launching:

```sh
amx managed setup --preset reviewer-pair --session review-team \
  --output review-team.yaml
amx start --config review-team.yaml -s review-team -d
amx ls -s review-team
amx send TARGET_ID "Review the implementation and report specific findings." \
  --mode instruction -s review-team
```

Replace `TARGET_ID` with the registered ID shown by setup or `ls`. The
`research-team` role preset is a coordinator/analyst/critic starting point, not
an experimental design. Presets do not automatically enable wakeups, spawning
or permission to form teams.

For ongoing autonomy, explicitly configure conversation persistence, permitted
command edges, and wake subscriptions to selected boards, notes or pings.
Set finite turn, hop, backlog, concurrency and runtime limits. A message-triggered
observation is attributed data, not a new owner instruction. See
[persistent workflows](autonomous-workflows.md) for the configuration.

[![Tasks view shows complete retained counts and attention for pending and failed deliveries](assets/guide/web-tasks.jpg?v=19ad3ea7cd2a)](assets/guide/web-tasks.jpg?v=19ad3ea7cd2a)

Observe **Tasks** for complete retained counts, attention filters, exact
recipients, results and adapter evidence. Search or load older pages rather than
assuming the visible rows are the complete history. Human-only native turns
are not broker tasks. A paused worker stops future launches; inspect or cancel
an already running task separately.

## Dynamic teams and recruitment

[![Consented team details show its creator current members effective access and membership version](assets/guide/web-teams.jpg?v=26ca0a62c499)](assets/guide/web-teams.jpg?v=26ca0a62c499)

Two mechanisms serve different purposes:

| Mechanism | Who decides membership | Use it for |
| --- | --- | --- |
| Owner collaboration group | You select the exact session IDs with `link` | Effortless temporary pairs or owner-arranged teams |
| Consented agent team | An authorized creator invites; each participant accepts | Agent self-organization and observable team formation |

Teams are off unless configured. Joining creates access to that team's board
and current shared memory, not permission to instruct its members. Pending
invitations grant no access. Since-join board history is the default; current
memory can include material written before a participant joined.

From a source checkout, start this finite, model-free fixture to try the team
operations below. It uses an isolated broker, isolated workers `lead` and `peer`,
and a permitted helper template. Working Linux Bubblewrap is required:

```sh
./bin/amx managed up examples/managed/bounded-teams.yaml \
  --state /tmp/amx-team-demo --broker-sandbox bubblewrap
./bin/amx dashboard --state /tmp/amx-team-demo
```

The owner can create a team and invite participants:

```sh
amx team create "API investigation" --state /tmp/amx-team-demo
amx team invite TEAM_ID lead --state /tmp/amx-team-demo
amx team invite TEAM_ID peer --state /tmp/amx-team-demo
```

Replace `TEAM_ID` with the ID returned by `team create`. Each participant
separately consents through its own tools and credential.
The owner console does not accept invitations on an agent's behalf. Teams can
be renamed, dissolved, or transferred through a proposal plus the new owner's
acceptance. An invited non-owner member can consent to an atomic move between
teams. Reviews retain exact IDs and versions.

Recruitment is another explicit permission. An agent can request only approved
spawn templates and delegate a bounded subset of its own rights. Children get
new identities and isolated workspaces; parent, depth and remaining capacity
stay visible. A role called “leader” cannot grant itself these powers.

The fixture's command agents do not independently recruit or accept invitations. Follow
[dynamic teams and bounded spawning](dynamic-teams.md) for the authenticated
spawn/invite/join calls or replace the fixtures with provisioned model harnesses.
The ordinary indefinite interactive preset disables delegated spawning; use an
explicit finite managed configuration for this workflow.

## Research and competition

Use the research runner to study how behavior changes under information,
incentives, team permission and population policies. It supplies private
observations, phase barriers and objective mechanics for `public-goods-v1` and
`trust-exchange-v1`.

### Inspect the protocol before using models

From the source checkout, with supported Linux Bubblewrap and Python 3:

```sh
amx research plan examples/research/public-goods.yaml
amx research run examples/research/public-goods.yaml \
  --output /tmp/amx-study-demo --start-paused
```

Choose a new output path. `plan` starts no participants and reports the matrix
and task count. This bundled run uses scripted command participants and makes
no model calls. `run` stays in its terminal; observe it from another terminal:

```sh
amx research watch /tmp/amx-study-demo --follow
amx research study /tmp/amx-study-demo
# Use the exact STATE printed by the runner:
amx dashboard --state STATE --view research
amx web --state STATE --open
```

The bundled smoke has short deadlines, so prepare the observer terminal first.
For slower exploration, make a separate manifest and team with explicitly larger
trial, phase and runtime limits; use a new output path. Pausing does not extend
any of those deadlines.

[![Live research console shows one completed coordination phase a paused decision boundary and reasoned intervention controls](assets/guide/web-research.jpg?v=8318adf0735d)](assets/guide/web-research.jpg?v=8318adf0735d)

The research screenshots show a one-condition demo. The bundled public-goods
smoke plans six trials across its conditions and seeds.

The console shows trial/condition, phase, admitted population, task outcomes,
deadlines, runner heartbeat and requested versus acknowledged controls. Web
**Follow this study across trials** tracks the next attempt. Terminal **8 Events**
contains research controls and study details. Overall study status and archived
history remain inspectable after individual brokers stop.

Read the current control version before an intervention:

```sh
amx research status /tmp/amx-study-demo
amx research step /tmp/amx-study-demo --version CONTROL_VERSION \
  --reason "Inspect team formation after one phase"
```

Replace `CONTROL_VERSION` with `result.research.control.version`; read it again
before another change. **Step** admits one phase. **Pause** and **Stop** take
effect at phase barriers; already admitted work may finish. **Resume** releases
a live pause, not a terminated runner. `--start-paused` pauses every new attempt;
it also takes precedence over a carried unused step. Deadlines and budgets keep
counting while paused.

[![Research intervention form requires an action reason and review before applying](assets/guide/web-research-control.jpg?v=b454120a775f)](assets/guide/web-research-control.jpg?v=b454120a775f)

### Design cooperation and competition comparisons

| Treatment | Relevant design choice |
| --- | --- |
| Shared success versus individual incentives | `reward_mode: shared`, `individual` or `winner` |
| Available communication | `visibility: isolated`, `board` or `direct` |
| Permission to form private teams | `teams: true` in a direct condition |
| Voluntary team reward pooling | `team_rewards: pool`; eligible members must independently choose the pool |
| Role or behavioral prompt treatments | Named `role_assignments` and `personality_profiles` |
| Changing population through recruitment | Explicit `population.mode: dynamic` and approved bounded templates |

Use [roles-personality.yaml](https://github.com/pagel-s/agentmux/blob/main/examples/research/roles-personality.yaml)
to inspect a declared role/profile design, and
[dynamic-population.yaml](https://github.com/pagel-s/agentmux/blob/main/examples/research/dynamic-population.yaml)
for bounded recruitment. Dynamic children become research participants only at
the next-round boundary with the required entry-credit transfer. Team formation
and population growth are separate controls.

Hold the population, tools, models and other conditions constant when testing
one factor, or declare a complete factorial design and balanced order. The
public-goods smoke changes several factors to test mechanics; it cannot isolate
their causal effects. Prompt profiles are treatments, not established personality
traits. One trajectory cannot establish a stable cooperative or competitive
characteristic.

For native model studies, provision a separate team, review the planned turn
count, declare preregistration and a positive model-task budget, then explicitly
use `--allow-models`. Supported limits count admissions or native tasks, not a
hard external dollar cap. See [research design and analysis](research.md).

[![A scripted research team contains Alice and Bob while Charlie remains outside it](assets/guide/web-research-teams.jpg?v=e9910402cec4)](assets/guide/web-research-teams.jpg?v=e9910402cec4)

The demo above exercises actual invitation, acceptance and namespace access.
Its Alice/Bob coalition is scripted; measuring model self-organization requires
model participants making those decisions themselves under the declared policy.

### Observe freely and record steering

Read-only inspection does not mark a trial assisted. Phase controls and owner
changes to messages, memory, permissions, teams or tasks are recorded as
interventions. Default estimates exclude those assisted runs. You can still
interact deliberately; keep that exploratory trajectory distinct from an
unassisted comparison.

```sh
amx research history /tmp/amx-study-demo --type team.join --limit 100
amx research analyze /tmp/amx-study-demo \
  --csv /tmp/amx-study-summary.csv \
  --behavior-csv /tmp/amx-study-behavior.csv
```

Use new CSV paths. The private archive preserves messages, membership changes,
tasks, memory and retrieval evidence. Behavior CSV exposes objective decision
fields without raw prompts. Analysis reports incomplete blocks, invalid actions,
execution failures and other quality limits. Repeated worlds, counterbalancing
and adequate independent blocks matter more than an attractive single run.

## Scale and remote workers

For a busy workday, keep clear titles and project identities, use Ctrl+P or web
search, and create small scopes instead of one everyone-to-everyone channel.
Use Tasks attention filters and connection List view as the roster grows.
Remove exited sessions deliberately; retire revoked IDs to release active
definition slots while preserving attribution and history. `amx archive ID`
is the short name for retirement; it does not stop an active session first.

Choose isolated profiles when filesystem boundaries matter. Trusted local
sessions preserve normal host tools, skills and MCPs. Bubblewrap uses private
homes, selected credentials and explicit dependencies; arbitrary host
configuration is not inherited. An isolated broker can still communicate with
separate agent sandboxes through its authenticated socket. Isolation failures
do not fall back to trusted local execution.

Managed remote workers use explicit trusted Linux SSH placement:

```sh
amx managed remote --state STATE --agent WORKER_ID --host SSH_ALIAS \
  --remote-dir /tmp/amx-unique-remote-worker --once
```

Provision the remote harness, dependencies and selected credentials separately.
The command forwards authenticated broker access, not the owner's credentials
or source tree. It is not an automatic cluster scheduler or host-failure migration
service. See [SSH workers and broker scale](remote-workers.md).

Current validation includes larger registries and broker actor workloads;
those are not measurements of hundreds of simultaneous native model sessions.
Small teams remain the primary deployment target. Explicit concurrency, lifetime
spawn/team, runtime and resource limits apply as you grow.

## Diagnose a stalled workflow

| What you see | Check next |
| --- | --- |
| Commands allowed, nothing appears in the native CLI | Receiver Manual/Review mode, pending approval, adapter support, readiness and limits in Session detail |
| A note or ping stays in the inbox | Its subscription and receiver mode; permission alone does not wake a session |
| A task says submitted | Inspect native settlement and its result; transport acceptance is not completion |
| Delivery is uncertain or its lease expired | Open `amx task TASK_ID --open -s NAME`, inspect the old process and task evidence, then review an exact fail/retry decision |
| Unlinking leaves an edge allowed | Inspect other active groups and explicit owner overrides |
| A provider updated and exited | Inspect final output; use Shift+U or `amx relaunch ID` with an explicit native resume choice |
| Complete history is unavailable on an old controller | The roster can remain usable with partial snapshots; use a fresh current workspace for full backend features |
| Broker or runner health is unknown | Inspect evidence and logs; a responsive broker does not prove a runner or model is active |

Use the [control center reference](usability-control-center.md),
[fresh-install walkthrough](getting-started/quickstart.md) and
[trace and recovery guide](traces-and-recovery.md) for exact recovery steps.
Closing an observer leaves work running. `amx remove ID` revokes and closes an
owned session; removing an external bridge leaves its original terminal alone.
`amx down -s NAME` stops the workspace's owned processes and retains saved data.
