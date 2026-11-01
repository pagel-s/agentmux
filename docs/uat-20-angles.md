# Twenty-angle usability acceptance pass — 1 November 2026

Twenty reviewers examined distinct operator journeys from baseline `33f6016`.
The priorities were terminal-first native interactive work; a clear overview of
about ten everyday sessions with optional groups; observable, optionally
steerable research and autonomous teams; and a functional full-screen web
console with a pure black background and purple accents. The review looked for
confusion about identity, audience, authority, process lifetime and evidence,
as well as failed commands.

This is bounded acceptance coverage, not a claim of perfection. The reviewers
used disposable command participants, transparent provider/protocol substitutes
and explicitly synthetic registry records. They made no model/provider
generation calls and did not run a human-subject study. A command harness can
run arbitrary owner-specified code; its name alone does not prove that an
arbitrary future command will be model-free. Here, the actual test programs were
known fixtures. Counts describe separate fixtures and must not be added into a
claim about one concurrent deployment.

The earlier [usability sweep](uat-sweep.md) records prior research phase-control,
memory pagination, native-session discovery, failure and remote-machine checks.
This pass sought additional gaps rather than counting those earlier checks as
new work. The original twenty reports and private evidence were collected under
`/tmp/agentmux-uat20-CUxP3P`; those temporary artifacts are not a durable public
test bundle and may contain disposable credentials. The coverage and remaining
limits are recorded below without publishing private owner URLs or raw prompts.

## Twenty actual review angles

| # | Angle | Actual scenario and boundary |
| --- | --- | --- |
| 1 | Newcomer mental model | Two independent named workspaces, each with two real interactive `/bin/cat` sessions and repeated IDs/titles: start, inventory, label, group board, inbox, direct message, isolate and stop. Found legacy `list/status` recovery confusion and missing workspace context in confirmations; no cross-workspace delivery observed. |
| 2 | Web visual design | Actual browser at 1280×720 and 390×844, with ten command-agent definitions and no workers: rename, search/clear, exact selection, directional connection controls, graph and owner pair scopes. Pure black/purple presentation confirmed; identity density and the narrow graph canvas were design concerns. This was not a ten-process test. |
| 3 | Terminal design | Two real native command sessions; dashboard at 110×32 and 60×16, plus an 80×24 native attachment with a 74-column popup. Checked frozen reader, composer target, modal escape and return to the unchanged native terminal. Confirmed active Teams/Events tabs could disappear from narrow navigation. |
| 4 | Daily lifecycle | Two separate two-agent native fixtures, then a fresh fixed-candidate retest. Checked configured repeat-start, duplicate titles, generated IDs, one session exiting, removal and whole-workspace restart. Repeat-start failed initially; candidate reuse preserved the attempt and both pane PIDs and rejected a changed role. Isolated same-ID relaunch remains unavailable. |
| 5 | Multiple projects/workspaces | Two launch-to-stop journeys with the same local ID/title, separate projects, a path containing spaces, symlink aliases and cross-directory selection. Wrong-project reopen was rejected and stopping one left the other live. Conflicting `--session`/`--state` silently selected the latter before the fix. |
| 6 | Temporary topologies | Ten broker identities with assigned credentials, five disjoint pairs, an overlap and a triple. Verified nontransitive communication, scoped board/memory, isolation and selected reconnection. Disabling one overlapping group also denied a relationship supplied by another active group; this is a scope-semantics limitation, not an observed permission bypass. No native typing was exercised. |
| 7 | Messaging intent | Two broker-only journeys plus replies, using managed and interactive identities. Verified Unicode/multiple recipients, note/ping versus instruction, atomic invalid-recipient rejection and future permission revocation. Found shared-board replies becoming private instructions and blank replies becoming work through their reference prefix. |
| 8 | Knowledge workflows | Two assigned identities, small private/configured-shared/pair entries, version conflict, source reference, isolation/reconnection, export/import collision, explicit replacement, deletion and expiry. Enforcement held. Raw control responses and retained import provenance did not fully explain effective access or original source identity. |
| 9 | Research design | Two completed command-only studies: four public-goods runs with seeds 11/22 and two trust-exchange runs with seed 33; 36 real managed command tasks total. Exercised planning, paired comparisons, preregistration warning, saved status and both CSV exports. An initial infrastructure interruption was excluded. Successful trusted-local runs establish logical protocol behavior, not hostile-process isolation or model cooperation. |
| 10 | Dynamic teams and human control | Four supervised command-only runs, including real Bubblewrap workers and a spawned `/bin/cat` child that submitted a task. Exercised invitation consent/decline, stale versions, reinvitation, owner removal, new-team handoff, inherited access, parent departure, dissolution, lifetime team/spawn limits and depth/deadline inheritance. Human control used authenticated CLI, not browser controls. |
| 11 | Roles, presets and customization | Generated the coding preset through transparent Claude/Codex metadata substitutes, then launched a custom native fixture, added generated/named sessions with role/model/project choices, renamed and removed one. Captured actual launch argv/cwd: role entered the prompt, title did not; rename did not relaunch the provider substitute. No actual provider configuration loading was validated. |
| 12 | Harness/endpoint flexibility | Real command-worker turn and a Pi RPC protocol substitute against a metadata-only local HTTP fixture. Checked exact model/base selection, selected-key forwarding, unrelated environment exclusion, temporary configuration cleanup and structured completion. Negative preflight probes explained unsupported endpoint combinations. This validates adapter plumbing, not actual provider inference/tools. |
| 13 | Sandbox comprehension | Four actual command tasks across trusted local and Bubblewrap brokers/workers, with private and explicit workdirs. Local fixtures could observe owned host/peer paths; isolated fixtures saw `/workspace`, private home and hidden owner/peer paths. `network: none` retained authorized Unix broker communication. No outbound allowlist endpoint journey was performed. |
| 14 | Accessibility/input | Actual keyboard browser journeys at desktop and 375×667: search/no results/Escape, panel activation, focus retention and empty-send validation. Found missing selected-view semantics and unannounced search counts. Fixed candidate has DOM regressions; this reviewer could not reopen the browser for candidate visual verification. No real screen reader, zoom or full contrast audit. |
| 15 | Failure/recovery | Native and managed `/bin/cat` configurations: stale owner credentials, stopped broker/reconnect, corrupt owner metadata, label persistence, removal/restart and rejected sends. A submitted task survived restart without duplication and its original deadline persisted. Corrupt owner metadata produced the wrong recovery advice. No simulated mid-write transport loss. |
| 16 | Concurrent humans | Two fresh workspaces, each with two real owner-dashboard PTYs and two real native tmux attachments. Checked title CAS/draft isolation, native typing after isolation, peer detach, owner-dashboard quit and peer revocation. Independent clients survived; definite CAS rejection was incorrectly described as uncertain delivery. |
| 17 | Scale/density | Synthetic registry of 55 identities, 115 teams and 115 tasks; actual CLI/API paging and browser selection of tail IDs. Graph disclosed its 40-node cap and retained selected session/recipient. Found silent 40-task UI truncation and expensive overlapping 22-member group writes. No 55 live workers or 115 simultaneous tasks were executed. |
| 18 | Resource diagnostics | Two real Bubblewrap command journeys: pause/resume, one-turn cap, child charging to a parent subtree, pending work and an eight-second deadline. Limits held, but exhausted ancestry admission could still report ready and the unqualified pending count omitted instructions. No provider billing, in-flight cancellation or stop-under-load claim. |
| 19 | Installation/portability | Actual offline source install and local release-installer execution on Linux/amd64, including version/help/doctor, generated config validation/rendering, refusal and explicit replacement. The release archive/checksum was fabricated locally from the baseline binary. Published assets, downloads, other platforms and a new SSH deployment were not checked. |
| 20 | Artifact portability | Independently exported reviewer 9's scripted trust study; verified existing destinations were not overwritten and altered report/checkpoint evidence was rejected before export. Separately exercised private-memory collision/replacement/copy independence and absent offline traces. Retested fixed CSVs against stdout JSON. No native trace generation or third-party attestation was claimed. |

Initial enclosing-sandbox denials of Unix sockets, tmux and namespaces were
recorded as environmental failures. Reviewers used approved execution for only
their disposable fixtures where required. Requested Bubblewrap isolation did
not silently fall back to local mode. Reviewer 9 deliberately changed to a
fresh, explicitly trusted local configuration after the failed isolated attempt;
its report keeps those assurance levels separate. Reviewers stopped and reaped
their owned services/clients or used managed shutdown; no existing user sessions
were intentionally changed.

## Fixes included in this pass

- **Workspace targeting and reopening:** selection/mutation commands reject
  simultaneous `--session` and `--state` before acting, including add/connect.
  Start may still name a workspace and choose custom storage together. Repeating
  an unchanged explicit-config start reuses the live workspace; a changed
  resolved launch definition is rejected. Workspace-scoped CLI activity and
  mutation output identifies the selected state path. Reopening also rejects an
  explicit broker-sandbox mismatch; omitting the flag preserves the running mode.
  Legacy `list/status` and
  their help explain their default-tmux scope and point to everyday sessions.
- **Reply intent:** shared-board replies require an explicit board or private
  destination instead of silently becoming instructions to the author. CLI and
  web reject blank/whitespace text before adding reply context. The fix does
  not add a general note/instruction reply-mode selector or structured ancestry.
- **Terminal navigation and failure wording:** narrow navigation keeps the
  active view visible with overflow cues. Definite broker rejections, including
  title-version conflicts, are identified as rejected requests; transport
  failures retain uncertainty wording and drafts remain available for review.
- **Web identity and accessibility:** stable ID/project text and graph identity
  text are larger; untitled rows avoid repeated project/provider text. View
  buttons expose their pressed state and associated panel. A dedicated status
  region announces user-driven search results without announcing every poll;
  Clear search returns focus to the persistent search field. The black/purple
  visual direction remains intact.
- **Honest task window:** the web panel says “Recent tasks,” explains that it
  shows up to the latest 40 and warns that older tasks may exist. This is a
  disclosure fix; browsing older tasks in the owner UI is still unimplemented.
- **Admission diagnostics:** read-only readiness uses the same session/ancestry
  reservation rules as the transactional claim. Otherwise eligible queued work
  that cannot fit reports halted/admission blocked instead of ready. Separate
  observation and instruction counts appear in status and both consoles; the
  old `pending` field retains its observation-count meaning for compatibility.
  Status neither reserves allowance nor renews a budget.
- **Overlapping-group write cost:** an evaluation cache is limited to the
  immutable ordered-pair authorization checks inside one control transaction,
  after its group update. It does not cache mutations or reuse policy across
  requests. Regression coverage checks policy parity, later deny changes,
  independence and atomic rollback at the override limit.
- **Portable research analysis:** existing leading CSV columns are preserved.
  Summary rows append study/hash, exclusion counts, interpretation, power
  warning and the complete `analysis_json`, including uncertainty,
  preregistration and evidence warnings. When there are no estimates, a
  metadata-only row leaves metric cells blank. Behavior JSONL/CSV gains study,
  seed/repetition/order, attempt, config hash and environment joins. Private
  output permissions, no-overwrite behavior and prompt omission remain covered.
- **Recovery/install guidance:** unreadable owner credentials identify their
  file and explain that restarting the broker will not repair it. `init` and
  `validate` have useful subcommand help. Expected installer refusals produce
  concise errors while preserving the destination; unexpected programming
  errors remain visible. The installer no longer uses `str.removeprefix` for
  optional version-prefix removal.
- **Network documentation:** the runtime guide now describes the existing
  `none`, unrestricted `host` and exact-origin `allowlist` choices and links to
  their proxy/client limitations. This corrects documentation; it does not add
  a new network implementation or prove compatibility with arbitrary clients.

The scale optimization has a bounded, reproducible measurement. With 55
synthetic identities and six overlapping 22-member scopes, the focused
production-method fixture reduced authorization reads from 8,562 to 316 for
the first edit and 7,174 to 313 for each later edit. In that local reproduction,
six edits took 62–1,624 ms before and 3.6–19.7 ms after. These are local
observations, not deployment guarantees. The original CLI's sixth request hit
the reviewer's four-second timeout; its final outcome was unknown, was never
retried and cannot be inferred from a later fresh reproduction.

## Capability proposals and remaining design limits

These are not implemented by the fixes above and should not be presented as
completed features:

- **Everyday session continuity:** deliberately relaunch an exited native
  definition under the same stable ID without restarting healthy peers, with
  explicit new-conversation versus provider-history choices. Whole-workspace
  restart and creating a new ID remain the available routes. A workspace
  inventory that discovers all named/stored workspaces also remains a proposal;
  mode labels and selector hints do not provide that inventory.
- **History and navigation:** browse/search tasks beyond the recent owner UI
  window; add terminal context help and modal-specific hints; improve mobile
  graph fit/pan guidance and the balance of readable labels versus machine IDs
  in Teams. Larger identity text is a partial readability improvement, not a
  full responsive or accessibility audit.
- **Overlapping permission semantics:** decide whether removing one group
  should remove only that group's contribution or also write a global
  relationship deny. Current explicit edge controls can deny a pair even while
  both remain in another active group. The performance fix does not change
  those semantics. Raw control responses should distinguish stored overrides
  from effective scopes and describe exactly which shared namespaces isolation
  and reconnection restore. Reconnecting a pair does not automatically undo
  isolation's denies on configured shared knowledge.
- **Knowledge provenance:** retain original source agent/namespace/session and
  digest as structured imported claims on the destination entry. The current
  archive carries that information, but the imported entry alone has an import
  marker/checksum and the correct destination author. Checksums establish
  integrity, not trusted source identity; copied exports can outlive deletion
  of the original. A portable authenticated trace-bundle workflow also remains
  a proposal.
- **Messaging:** expose note versus instruction intent before direct-reply
  submission and retain structured reply ancestry. The existing native-inbox
  versus managed-instruction distinction remains; blocking ambiguous board
  replies does not implement a general conversation threading model.
- **Research design:** preregistration still requires
  `primary_outcome: total_social_welfare` and one or two binary factors drawn
  from `reward_mode`, `visibility`, `teams` and `team_rewards`. Team-formation
  primary outcomes, role/personality treatments, condition-specific role
  assignment and counterbalancing need an explicit design/estimator extension.
  Tiny unregistered deterministic studies can still have degenerate intervals
  without a dedicated scripted-evidence/tiny-sample warning. Carrying all
  existing analysis into CSV does not create new inferential safeguards.
- **Dynamic team planning:** richer remaining-capacity summaries, participant
  explanations for inherited access denials and clearer reinvitation recovery.
  Ownership transfer, live team rename/mission change and atomic membership
  moves were not implemented; the reviewed handoff used a new team and separate
  invitations/acceptances. Lifetime allocations remain consumed after team
  dissolution. Admission diagnostics do not add live budget editing or provider
  hard billing caps.
- **Native/custom configuration:** a prompt-free native add mode, live role
  editing and more managed presets remain product choices. Empty `--role`
  still includes Agentmux coordination instructions. Interactive native
  configuration and isolated headless execution have different inheritance
  boundaries. Actual arbitrary endpoints/providers, Antigravity role injection
  and imported skills/MCP behavior were not established by protocol substitutes.

## Validation and what it establishes

After the fixes, the coordinator reported a passing full uncached Go race suite
using `go test -race -count=1 ./...`, with
`AGENTMUX_MANAGED_INTEGRATION`, `AGENTMUX_EXPERIMENT_INTEGRATION`,
`AGENTMUX_RESEARCH_INTEGRATION` and `AGENTMUX_SANDBOX_INTEGRATION` enabled.
`AGENTMUX_TEST_BINARY` and `AGENTMUX_INTEGRATION_BINARY` selected the frozen
candidate, and `AGENTMUX_TMUX_DEBUG` retained failure diagnostics. The 62 frontend
regression scenarios and seven release-installer tests also passed:

```sh
node cmd/agentmux/workweb/app.test.cjs
python3 scripts/test-install-release.py
```

Targeted follow-ups included a real configured repeat-start preserving native
processes, an independent review of the admission diagnostics, a fresh
overlapping-group production-method reproduction and export of the same
completed scripted study through the fixed CLI. Fixed CSV `analysis_json`
matched the complete stdout analysis, including warnings; behavior rows retained
their actual block/attempt/config/environment identity. Invalid completed
evidence was rejected rather than rescored or exported.

Final review also exercised actual local and Bubblewrap brokers: both rejected
an opposing explicit sandbox mode, accepted matching or omitted modes, and
preserved their running attempt. The coordinator's final browser acceptance used
ten real command-only native PTYs in ten separate project directories. Searching
the tenth stable ID, selecting it, clearing search and opening Graph retained the
correct identity. Keyboard activation retained panel focus; view buttons exposed
one active state, search had a polite status region, identity metadata rendered
at 11px and the body background was `rgb(0, 0, 0)`. The recent-task panel disclosed
its 40-record window. A 760×640 check had no page-level horizontal overflow;
Graph retained its own scrollable canvas. Screenshots were retained privately,
the temporary viewport was reset, and the fixture was stopped.

Automated DOM tests are distinct from manual browser acceptance and real
assistive-technology use. No new remote-machine deployment, published-release
download, macOS/arm64 run, multi-day operation or actual provider reliability
test occurred in this twenty-angle pass. The results support the tested
orchestration, permission, artifact and operator workflows. They do not establish
model cooperation, natural self-organization, personalities, hidden reasoning,
causal treatment effects, hostile-agent safety in local mode or unlimited scale.
