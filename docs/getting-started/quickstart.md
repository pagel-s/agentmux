# Fresh-install walkthrough

This journey tests the everyday workflow: two normal native conversations,
readable session identities, opt-in communication and memory, then ending the
collaboration while keeping both terminals usable. It uses a separate workspace
named `try-agentmux`. Every owner command below selects that same workspace,
regardless of the current directory.

## 1. Install and check Agentmux

Install **v0.11.0** using the
[published binary instructions](installation.md#install-a-published-binary-recommended)
for the `amx` command without a Go toolchain. Then skip the source-build commands
below and continue with the native CLI checks in this step.

Alternatively, build from source with Git, Make, Python 3, tmux 3.2+ and Go 1.24+.
On a new checkout:

```sh
git clone https://github.com/pagel-s/agentmux.git
cd agentmux
make build
./bin/amx version
./bin/amx doctor
```

For a user-local executable:

```sh
make install
export PATH="$HOME/.local/bin:$PATH"
```

Make that PATH setting available in each new terminal; see
[installation](installation.md). Check `command -v amx` and
`amx version` if another installed copy might be selected.

The default team needs both Claude Code and Codex installed and logged in:

```sh
claude --version
codex --version
```

Open each directly once and complete its normal login/startup prompts. If a CLI
does not work outside Agentmux, resolve that before testing its Agentmux session.
Other harnesses or a single-provider team can use the
[setup wizard](../managed-setup.md) with a custom configuration.

## 2. Create a disposable project and workspace

In a regular shell outside an existing tmux attachment:

```sh
amx_demo_project="$(mktemp -d "$HOME/agentmux-playground.XXXXXX")"
git -C "$amx_demo_project" init
printf '# Agentmux playground\n' > "$amx_demo_project/README.md"
printf 'Project: %s\n' "$amx_demo_project"

amx start --session try-agentmux --project "$amx_demo_project" --detach
amx sessions --session try-agentmux
```

Expected: two registered native sessions with stable IDs `claude` and `codex`.
The native CLIs can still need their usual project-trust approval. This first
workspace uses trusted local execution to preserve normal configuration, tools,
skills, MCPs, native approval behavior and history. It does not isolate host
files. Peer collaboration and autonomous wakeups start disabled.

This journey requires a fresh, unused workspace name. If `try-agentmux` already
belongs to an earlier test, choose another name consistently in every command.
Reopening an old workspace retains its permissions and memory, so it cannot
establish the initial deny-by-default results below. A saved workspace is not
silently repointed at a different project.

## 3. Use the terminal workbench

```sh
amx label claude "Explore the project" --session try-agentmux
amx label codex "Review the approach" --session try-agentmux
amx dashboard --session try-agentmux
```

Press Tab to focus the session sidebar, select a session with ↑/↓ and press Enter. Ask it to describe the
playground without changing files. Handle native trust/tool prompts normally.
Press **F1** (or **Ctrl-b**, release, then **g**) to return to the workbench.

Try **/** to search, **Shift+L** to edit its title, **Shift+V** to edit its role,
**Shift+N** to add a session, **?** for context help, and **←/→** or **1–8** to change views. **Tab** switches sidebar/content focus;
↑/↓ navigates that focused area. Left/right works across all eight views. In Connections
and Teams, use **h/l** or **Alt+←/→** to choose the recipient or action target.
The footer follows the current view and selection; uppercase letters mean hold
Shift. Titles can change or repeat;
commands still use the stable IDs. A role update changes future managed turns
and sends a native inbox notice. In the default Manual mode it does not start
a native turn; Review/Auto can deliver updated role and boundary context.

For pairing existing sessions, use **3 Connections**, choose a recipient with
**h/l** or **Alt+←/→**, then **c**. **7 Teams** is for consent-based teams created
by agents with permission; an empty Teams view is normal for this everyday preset.

Detach with **Ctrl-b**, release, then **d**. The sessions keep running.
Runtime services are separate from the agent windows. To inspect their logs,
press **Ctrl-b**, release, then **Shift+D**. From an agent terminal, **Ctrl-b b**
opens the Board popup; from the workbench, press **1** for Board directly.

## 4. Keep both native sessions open

In one terminal window:

```sh
amx attach claude --session try-agentmux
```

In another:

```sh
amx attach codex --session try-agentmux
```

Expected: both are normal native CLI conversations. Switching sessions in one
attachment does not switch the other attachment. Each attachment also has its
own dashboard selection and drafts. Keep a separate regular shell
for owner commands in the following steps.

## 5. Verify the initial communication boundary

Paste this into Claude's **native conversation**, not the owner shell:

```text
Use "$AGENTMUX_CLI" managed call boundaries.get to inspect your current
permissions. Then attempt message.send with JSON
{"to":["codex"],"text":"Initial boundary probe"}.
Report the broker result. Do not change policy or use files to communicate.
```

Expected: Claude's agent credential receives `forbidden` for the peer message.
Use its normal shell tool and approve that specific call if the native CLI asks.
The owner CLI has broader authority: running `amx send` as the owner does
not test a peer's permission boundary.

## 6. Connect only this pair

In the owner shell:

```sh
amx collaborate claude codex --session try-agentmux
amx board "Test proposal: keep parsing separate from persistence." --session try-agentmux
```

Keep the printed **Group**, **Board** and **Memory** values. This creates a
dedicated scope for this member set and enables peer notes/pings. It contributes
no peer command authority. The original `team` and `project` namespaces do not
become shared merely because this group exists.

Ask Claude:

```text
Re-read your effective Agentmux boundaries using the authenticated CLI bridge.
Read the newly allowed group board. In its shared-memory namespace, save a new
key demo-greeting with value HELLO_FROM_CLAUDE using memory.put and version 0.
Send Codex a direct informational note telling it which namespace/key to read.
Use only the broker; do not edit files or change permissions.
```

Then ask Codex:

```text
Read your current Agentmux boundaries and broker inbox. Read the group board
and the shared-memory key demo-greeting. Report the exact saved value and send
Claude a short informational acknowledgment through the broker.
```

Expected: Codex reports `HELLO_FROM_CLAUDE`; the board and acknowledgments appear
in the workbench. Native sessions read coordination when asked; a note does not
automatically type into or wake a native conversation.

## 7. Compare notes with queued instructions

In the owner shell:

```sh
amx send codex "Informational test note; no task requested." --mode note --session try-agentmux
amx send codex "Test task: summarize the board proposal." --mode instruction --session try-agentmux
amx inbox --session try-agentmux
```

Expected: the first creates a message; the second creates a task visible in
**Tasks**. In the default Manual delivery mode, neither starts a native turn.
Open the task with **o**, search with **f**, and use **PgUp/PgDn** for history.
Return from the reader and cancel this demonstration task with **c** after
inspection, so it does not remain pending across reopening. In a managed worker
workflow, eligible queued tasks are claimed and executed instead.

### Try an approved native turn

New integrated sessions can also receive attributed instructions directly in
their native conversation. In the owner shell:

```sh
amx delivery codex review --session try-agentmux
amx send codex "Reply with one sentence about the board proposal." --mode instruction --session try-agentmux
amx delivery codex --session try-agentmux
```

Copy the exact pending candidate ID, then approve it:

```sh
amx delivery codex --approve CANDIDATE_ID --session try-agentmux
```

Expected: Codex receives the attributed turn, and Tasks shows transport progress
and a correlated result. A queued/submitted item is not yet proof of execution.
**2 Sessions → Shift+D** edits receiver delivery; **Shift+A** reviews an item.
Auto removes per-item approval while retaining limits and permissions.
Peer commands additionally require the sender's directional Commands grant.
Return to Manual with `amx delivery codex manual --session try-agentmux`.
See [native collaboration](../native-delivery.md) for provider differences,
subscriptions and upgrading a workspace whose controller predates this feature.

## 8. Use the web control center

Open an additional regular shell for the web server and keep that process open.
Leave the original owner shell available for steps 9–10:

```sh
amx web --session try-agentmux --open
```

If your desktop does not open it, use the full private URL printed by the
command. Try **Connections** for directional relationships and shared access,
**Groups & teams** for the owner group's scope, **Memory** to inspect
`demo-greeting`, and **Tasks** for instruction history. Select a session before
changing its controls; review
the exact sender, recipient and effective permission. Note, ping and instruction
authority, and namespace read/write access, are separate controls.

Expected: a black/purple observation and control app. Native typing stays in
tmux. Closing the browser does not stop the workspace; Ctrl-C stops this web
server only.

## 9. End collaboration without ending the conversations

```sh
amx isolate codex --session try-agentmux
```

Ask Codex to retry `memory.get` using the **same shared-memory namespace/key**
from step 6 and `message.list` explicitly filtered to that old board, then try
a direct note to Claude. A generic inbox can still contain authored/direct
receipts and is not the revoked-board test. Expected: the
revoked operations are denied, while direct native conversation still works.
Previously read information remains in its conversation; isolation cannot make
it forget. Accepted tasks need separate cancellation.

Reconnect when wanted:

```sh
amx collaborate claude codex --session try-agentmux
```

The same member set reuses its group and saved history. Separate explicit owner
denies survive reconnect; review/reset those in Connections/Memory if you added
them during step 8. See [live permissions](../live-permissions.md).

## 10. Add a session and test individual recovery

In the original owner shell, which still has `amx_demo_project` set:

```sh
amx add review --harness claude --project "$amx_demo_project" \
  --title "Independent second review" --session try-agentmux
amx sessions --session try-agentmux
```

The new ID `review` starts independently. Repeat `collaborate` only for the IDs
you want connected. Use `--native-only` on an addition for a prompt-free native
launch; coordination then needs explicit manual setup/instructions.

Attach to the new `review` session with `amx attach review --session
try-agentmux`, exit that native CLI normally, then detach with **Ctrl-b**, release,
then **d** to return to the owner shell. The exited pane can retain its final
output. Then test a deliberate same-ID relaunch:

In the workbench, an exited native CLI is marked `exited`; **Enter** or
**Shift+U** opens its relaunch review. A clean exit does not mean a failure.
You can use the owner shell instead:

```sh
amx relaunch review --session try-agentmux
amx attach review --session try-agentmux
```

The first command requires `review` to have exited; it refuses a live session.
Expected: a fresh native conversation under the same Agentmux ID, with peers
untouched. To choose native history deliberately use `--resume picker` or an
exact native conversation ID instead. `--resume last` means the provider's
latest applicable conversation, not an Agentmux title match.

A native CLI may exit normally after updating itself. Review and relaunch that
session in the same way; a clean exit is not a crash. New local setups follow
the selected public launcher on relaunch. For older saved definitions that
freeze a versioned executable, see the explicit launcher recovery option in
[work sessions](../work-sessions.md#start-and-attach).

## 11. Test discovery or another workspace

To observe a native CLI started outside Agentmux:

```sh
amx sessions --session try-agentmux
amx connect OBSERVED_ID --title "Existing conversation" --session try-agentmux
```

Replace `OBSERVED_ID` with a currently observed entry. Paste its printed setup
into that original native conversation; connecting does not inject it or move
the external terminal. Some generic interpreter/non-TTY processes cannot be
identified. Existing external conversations remain under your direct control.

`amx workspaces` lists named and remembered custom locations. **Ctrl+W**
(also **Shift+W**) opens the chooser. An interactive attachment switches to the
selected workspace and retains the previous workbench and drafts. A standalone
observer shows an exact attach command when native input needs another server.
For an older custom state location, use `amx workspaces --register DIRECTORY`.

## 12. Stop the test and try isolated/managed workflows

Stop the web process with Ctrl-C in its shell, then:

```sh
amx down --session try-agentmux
```

Expected: only the workspace's owned processes stop. Saved state/history and
the playground directory are retained. External bridged terminals stay alive.
You can reopen the original definition with the same `start` command/project;
choose previous conversations explicitly through native history controls.

For model-free isolation checks, return to the repository checkout and run:

```sh
python3 scripts/smoke-managed.py
python3 scripts/smoke-managed-interactive.py
```

These require Linux, a supported Bubblewrap (including `--size`) and working
user namespaces. They make no model calls and clean up their temporary runtimes.
For a real isolated native workspace, use a fresh workspace name with
`start --sandbox bubblewrap`; isolated credential snapshots and private homes
have different customization boundaries from the local workflow above.

Continue with [managed setup](../managed-setup.md) for role presets and selected
providers, [persistent/autonomous workflows](../autonomous-workflows.md) for
managed task execution, and [research](../research.md) for observable,
preregistered studies. The [work-session guide](../work-sessions.md) and
[command reference](../commands.md#everyday-work-sessions) cover the full controls.

## Existing workspace mode

The following flow is a separate, optional tutorial for the original layout compiler. It creates tmux panes
without the managed broker's authenticated board, memory or permissions.

## 1. Create a Configuration

Agentmux uses declarative YAML files to define your workspace. You can create one from scratch, but it's easier to use a built-in starter template.

```bash
# See available templates
amx templates

# Initialize a standard AI development workspace
amx init ai-dev my-workflow.yaml
```

## 2. Review the Plan

Before launching, you can ask Agentmux to show you exactly what it intends to do:

```bash
amx render my-workflow.yaml
```

You'll see a breakdown of the windows, panes, commands, and environment variables that will be created.

## 3. Launch the Session

To create the tmux session and attach to it:

```bash
amx load my-workflow.yaml
```

Welcome to your new workspace! You should see your editor on the left and a dedicated pane for your AI agent on the right.

## 4. Live Modifications

If you realize you want to change the layout or add a new pane, you don't need to kill the session.

1. Detach from tmux (`Prefix + d`).
2. Edit your configuration file (`amx edit my-workflow`).
3. Reload the session non-destructively:

```bash
amx reload my-workflow.yaml
```

For supported changes, reload reconciles the layout and updates affected panes.
Structural changes that cannot be reconciled in place can recreate the session
and interrupt its processes; inspect the rendered configuration before reloading.
