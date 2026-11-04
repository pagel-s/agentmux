# Collaboration inside native terminals

Agentmux keeps two choices separate: **who may send an instruction**, and
**whether its recipient may receive an agent-authored native turn**. Enabling
Commands in Connections grants the first. It does not silently enable the second.
Native delivery is off by default.

New integrated Claude, Codex and Pi launches include a per-session adapter. Your
terminal remains the place to type, approve tools, interrupt work and use the
provider's normal features. The adapter does not type tmux keys, transmit unsent
drafts or edit your global configuration. Pi checks its local editor for an empty
draft before dispatch. `--native-only` disables this integration.

## Connect a pair and choose delivery

Start a new workspace with the current binary, then use a regular owner shell:

```sh
amx start --session collaboration-test --detach
amx collaborate claude codex --session collaboration-test
amx delivery codex review --session collaboration-test
amx send codex "Summarize the board proposal in two sentences." \
  --mode instruction --session collaboration-test
amx delivery codex --json --session collaboration-test
```

Read the complete pending item's text and copy its exact candidate ID from the
last command:

```sh
amx delivery codex --approve CANDIDATE_ID --session collaboration-test
amx attach codex --session collaboration-test
```

Review approves one exact item under the current policy version. A changed
policy invalidates earlier approvals. For an owner-authored test instruction,
no peer command grant is needed. To let Claude instruct Codex, enable the
direction **Claude → Codex → Commands** in Connections, then ask Claude
to use `instruct` through its authenticated broker CLI.

Choose **Auto** explicitly to receive eligible turns without each approval:

```sh
amx delivery codex auto --max-turns 20 --max-hops 4 --cooldown 1 \
  --session collaboration-test
amx delivery claude auto --session collaboration-test
```

In terminal **Connections**, the displayed recipient has its own delivery summary,
separate from the sender's Commands permission. **Shift+D** reviews that recipient's
delivery settings; **Shift+A** opens its pending-item chooser in Review mode.
These actions also appear in **Ctrl+K** Actions with the recipient's stable ID.
They keep the sender selected. In **Sessions**, the same keys apply to the selected
session. Web Connections already offers **Inspect receiver delivery**, leading to
Session detail with the same settings and pending approvals. Sender grants, receiver mode, current scopes, adapter capability, readiness and
budgets all have to allow delivery.

Choosing Auto can start **already queued eligible items**, including boundary
notices, as well as future instructions. The terminal confirmation previews the
known queue count and bounded kind/sender metadata; the queue can change. Choose Review
first when you want to inspect and approve one exact item's complete text.
Plain `amx delivery ID auto` changes only the mode: it keeps saved subscriptions,
turn/hop limits and cooldown. It does not grant Commands or reset spent turns.

## Modes and observations

| Mode | Behavior |
| --- | --- |
| Manual | Keep using the native CLI normally; inspect broker coordination when requested. No new peer turn is dispatched. |
| Review | Queue eligible items until the owner approves each exact candidate. |
| Auto | Deliver eligible items when the adapter can safely accept them, within the configured limits. |

Direct notes, pings, readable board updates and shared-memory updates are
separate subscriptions; all start off. Results of this agent's own instructions
are enabled by default. An observation is labeled as data, not an instruction
grant. Subscriptions never grant board/memory access or authority to command a
peer. Private memory is never broadcast. Current owner role and boundary notices
are delivered in Review/Auto even when note subscriptions are off, so the agent
learns its changed limits. This can include an initial boundary synchronization
turn after enabling delivery; it counts toward the turn limit.
Notices include incoming sender permissions as well as outgoing grants and
namespace access. Editing an unrelated pair does not wake other sessions whose
effective boundaries remain the same.

```sh
amx delivery codex auto --notes=true --pings=true --boards=true \
  --memory-updates=true --results=true --session collaboration-test
amx delivery codex manual --session collaboration-test
```

Returning to Manual stops future dispatch and requests safe cancellation of the
active peer delivery where the provider supports it. It does not interrupt an
unrelated human turn. Isolation revokes future peer access and delivery eligibility;
data already read remains known. Inspect active tasks and cancellation status too.

An instruction result returns to its issuer through a correlated task/result
channel; this needs no reverse `message.send` or Commands grant. A native result
turn still requires the issuer's **Returned results** subscription, its own
Review/Auto delivery policy and adapter readiness. In Manual, inspect the recorded
result with `amx task TASK_ID` or the agent-authenticated `task.get` operation.
Result notices do not recursively generate result notices. Native turns count
toward a cumulative per-agent turn limit, causal hop limits and any configured broker admission budget.
Raising `max_turns` explicitly permits more turns; changing modes does not reset it.
These are admission controls, not hard caps on provider billing or human CLI use.

## What the status means

| Phase | Evidence |
| --- | --- |
| Pending | Current queue record; no new native turn is confirmed. Earlier attempts may have run: inspect receipts and history, especially after retry. |
| Claimed / submitting | One exact attempt is reserved. |
| Submitted | Transport accepted the message, or the inbox write completed; model execution is not yet confirmed. |
| Running | Correlated native events or an explicit receiver acknowledgment confirm this delivery started. |
| Completed | Correlated settlement or an explicit receiver completion includes the result. The broker task is submitted for owner verification. |
| Failed / cancelled | The adapter has evidence of failure or cancellation. |
| Uncertain | Outcome cannot be confirmed. Agentmux does not automatically resend it. Inspect the terminal and resolve the task before another turn. |

Permission revocation is checked again when claiming and when the adapter checks
active delivery. Cancellation cannot undo model context or tool effects already
produced. A restarted broker never assumes an in-flight native turn is safe to retry.

A successful `instruct` call confirms broker admission. A `proposed` task or
`pending` delivery confirms queueing, not execution; inspect correlated receipts
and history, especially after retries because prior attempts may have run. It does
not identify the receiver's mode or prove why dispatch is blocked. Agents can
inspect only their **own** `native.status`. Ask the owner to inspect another recipient with
`amx delivery ID --json --state /exact/workspace/state`. Integrated launch context
includes these instructions and owner commands even when the project repository
contains no Agentmux documentation. `amx delivery --help` is available anywhere.

## Adapter capabilities

| Harness | Native path | Current boundaries |
| --- | --- | --- |
| Codex | Private app-server and native queue, retaining the normal TUI | Exact thread/turn correlation, queued deletion and peer-turn interruption. Owner developer instructions stay intact; Agentmux appends separate context to new, resumed and forked conversations. CLI profiles and preconfigured remote transports retain manual coordination. |
| Pi | One per-invocation extension | Waits for idle, trusted project, empty editor and no approval/pending input; settles only on the correlated agent settlement. |
| Claude Code | Own-child SessionStart hook and native session inbox | Inbox submission alone is unconfirmed. Injected content asks the receiver to acknowledge and finish the exact delivery through its scoped CLI. A model may fail to do so. Already queued messages cannot be retracted; draft/approval preservation remains explicitly unverified in capability status. Requires a supported native inbox and enabled hooks. |
| Antigravity / arbitrary commands | Manual coordination | No verified idle-wakeup adapter. Antigravity can instead be used through Pi's provider bridge. |

Codex refreshes its separate Agentmux developer-context entry once per selected
native conversation per bridge invocation; that entry persists in native history.
Unsupported native transports keep the owner's configuration and terminal;
Agentmux role-context append is unavailable in that fallback.

When human input steers a Codex peer turn, answers its native question, or joins
Pi's settled activity, Agentmux returns an explicit withheld-result notice
instead of sharing output that may answer the human's private input. Unsent
drafts do not suppress normal results. Claude's completion remains an explicit
receiver acknowledgment rather than automatic native event correlation.

In an all-native interactive workspace, the supervisor keeps healthy terminals
open through a broker outage and recovers only its owned broker with bounded
backoff. Sandboxed native workers use a private stable socket relay so a replaced
broker socket remains reachable. In-flight work stays uncertain until explicit
owner resolution; it is never replayed automatically. Managed/mixed workspaces
retain their existing fail-closed shutdown behavior. Owner shutdown and configured
runtime deadlines still take precedence over recovery.

## Check native delivery

Installed native TUI tests use loopback/offline fake providers to exercise draft
preservation, idle wakeup, permissions and completion receipts. Pi requires the
`agent_settled` lifecycle event; older or unverifiable Pi versions keep the
original native invocation with manual coordination. Inspect the adapter's
reported capabilities for your installed CLI.

These checks make no paid model requests. They verify the selected native
integration, not every provider/account configuration. Delivery status does not
show that a model understood an instruction or that its result is correct.

```sh
make build
AGENTMUX_INTEGRATION_BINARY="$PWD/bin/amx" \
  AGENTMUX_CODEX_NATIVE_INTEGRATION=1 go test -race ./internal/nativebridge/codex
AGENTMUX_CLAUDE_NATIVE_INTEGRATION=1 go test -race ./internal/nativebridge/claude
AGENTMUX_PI_NATIVE_LIVE=1 go test -race ./internal/nativebridge/pi
python3 internal/nativebridge/pi/testdata/native_broker.py --binary bin/amx
python3 internal/nativebridge/pi/testdata/native_broker.py --binary bin/amx --sandbox bubblewrap
python3 internal/nativebridge/pi/testdata/native_broker.py --binary bin/amx --sandbox bubblewrap --recovery
```

These require their installed CLIs. The full Pi workflow uses isolated profiles
and an offline in-process provider; its sandbox fixture explicitly permits host
networking but makes no external model request.

## Existing sessions and upgrades

An existing external session can join broker communication through `connect`,
but that does not retrofit a native transport into its running CLI. An older
owned session needs an explicit relaunch with the updated controller. Installing
Agentmux does not replace a running pinned controller or restart your agents.

For a first trial, use the separate `collaboration-test` workspace above. When
ready to upgrade an older workspace, finish or preserve its native work, inspect
uncertain tasks, and deliberately restart it with `amx managed restart
--state /absolute/workspace/state`. Whole-workspace restart stops its owned
sessions; choose history through explicit per-agent relaunch when resumption is
needed. Never start a second worker against the same conversation.

Launch YAML can preconfigure the initial delivery policy:

```yaml
agents:
  reviewer:
    harness: pi
    interaction: interactive
    sandbox: local
    native_delivery:
      mode: review
      max_turns: 20
      max_hops: 4
      cooldown_seconds: 1
      notes: false
      pings: false
      boards: false
      memory_updates: false
      results: true
```

Live owner edits overlay this policy without rewriting the saved launch definition.
When supplying `native_delivery` explicitly in YAML, set `results: true` if you
want returned results; omitted boolean fields in that object are false.
Agent-only bridge operations are `native.register`, `native.peek`, `native.claim`
and `native.event`. Owner controls are `native.configure` and `native.approve`;
`native.status` exposes scoped evidence without credentials or task leases.

Human-steered Codex turns continue when delivery is changed to Manual; the
terminal remains the place to interrupt them explicitly. Cancellation of an
untouched peer turn retains its original thread/turn identity after a native
conversation switch. The control center provides exact-version uncertain-task
fail/retry reviews; a retry is a new execution. External manual bridges do not
become native delivery adapters through relaunch.

Pi can remain `submitting` if a draft appears before injection. A preexisting
draft blocks readiness before submission begins. `submitted` is recorded only
after the native transport accepts the message; receipt retries never inject it
again. These phases describe transport evidence, not whether
the model understood or completed the instruction.
