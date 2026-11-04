# Ask for work, inspect the result, apply it

`amx work` turns one request into a checked coding-and-review journey. Codex
implements the change, Antigravity reviews it, Codex addresses the findings,
Antigravity reviews the final revision, and the project checks run again. You
receive a saved report and a patch in a separate Git worktree. Agentmux does not
integrate that patch into your project until you explicitly apply it.

Available in [Agentmux v0.13.0](getting-started/installation.md). To inspect a
checked fixture before configuring providers, start with
[`amx demo`](getting-started/first-result.md).

[![Actual offline report from the model-free demo, showing saved checks and the retained change](assets/guide/checked-work-report.jpg?v=9e8c23859bf8)](getting-started/first-result.md)

The report above comes from `amx demo`, a deterministic fixture with no model
calls. For your own task, the daily sequence is:

| Step | Command | What happens |
| --- | --- | --- |
| Preview | `amx work "Change" -- CHECK` | Review the plan without starting models |
| Run | Add `--allow-models` before `--` | Implement, review and check in a retained worktree |
| Inspect | `amx work show RUN_ID --open` | Read fresh report evidence and the patch |
| Apply | `amx work apply RUN_ID` | Recheck and apply the selected result explicitly |

v0.13.0 includes run-ID listing and report opening. The base journey also works
in v0.12.0; see [inspection](#inspect-the-result) for the older report route.

The five execution phases retain checks and review evidence. Failed or uncertain
work remains available to inspect; it does not become an approved application.

## Preview your first request

From an existing Git project with an initial commit:

```sh
amx work "Fix the login redirect" -- go test ./...
```

This is a preview. It explains the five phases, exact check command, deadline,
planned native turns and private result location. It does not start models,
create worktrees or write a run catalog. Go projects can omit the command to
use `go test ./...`; npm projects with an existing test script can use `npm test`.
If both apply or no supported check can be identified, choose the check
explicitly after `--`.

The check is literal executable argv, not a shell command. For example:

```sh
amx work "Correct the parser" -- python3 -m pytest tests/test_parser.py
amx work "Handle empty input" -- npm test
```

Pipes, redirects and `&&` are ordinary arguments after `--`; they are never
silently interpreted by a shell. Use an owner-maintained check script when a
check needs several commands.

Preflight checks the Git project, executable availability, check selection and
output paths. It cannot establish that provider accounts are signed in. Install
and sign in to the Codex and Antigravity CLIs before executing a request.

## Start the work

Add `--allow-models` before `--`:

```sh
amx work "Fix the login redirect" --allow-models -- go test ./...
```

Without full isolation, the command prints the result location immediately
and reports phase changes as they are recorded. Isolated CLI runs defer stdout
and stderr until completion so selected credentials can be checked before
publication. Use an explicit `--output` path to locate retained evidence during
such a run. The usual path is:

1. Implement the requested change and run the declared check.
2. Review the implementation without changing its checked content.
3. Address the review findings and check the changes.
4. Review the exact final revision.
5. Run final acceptance checks and retain the patch.

Final review must explicitly approve the revision. An empty review, malformed
review, review edits, remaining requested changes or failed acceptance checks
stop the journey and remain visible in the result. A model saying “finished”
does not replace review approval and executable checks.

Default native selections are Codex `gpt-6.1-sol` and Antigravity
`gemini-3.8-flash-low`. `--coder` and `--reviewer` explicitly select another
supported profile, including Claude. Availability depends on your account and
CLI versions. Each review starts a new native session with the owner’s task,
acceptance criteria and current files. Implementation narrative, earlier review
findings and earlier approvals are excluded from that review’s input.
Workflow profiles that request native resume, continue or transcript selection
are rejected before admission. Codex workflow turns also use ephemeral sessions.

Normally the plan uses four native turns; completed implementation-check
failures can use up to six with the default two correction attempts. Native
turns can contain multiple provider requests, so this is not a monetary cap.
Transport failures and uncertain turns are not automatically replayed.

Useful controls are:

```sh
amx work "Fix the redirect" --timeout 8m --check-timeout 2m --attempts 2 --allow-models -- go test ./...
amx work "Fix the redirect" --dry-run --allow-models -- go test ./...
amx work "Fix the redirect" --repo /path/to/project --output /private/new-run --allow-models -- go test ./...
amx work "Fix the redirect" --containment pid-namespace --allow-models -- go test ./...
```

The overall deadline is 1 second to 24 hours. Per-check deadlines are 1 second
to 10 minutes. `--attempts` is 1–5 and applies separately to the two coding
phases. `--dry-run` always previews, including when `--allow-models` is present.

### Require process containment

On Linux, `--containment pid-namespace` requires Bubblewrap and a working user/PID
namespace boundary. Execution probes that boundary before starting workflow
work; an unavailable boundary fails without falling back. Native tools, checks,
Git filters, integration and freshness checks inherit the recorded requirement.
A descendant that creates another session or process group remains in the PID
namespace and is terminated with it. The default `process-group` policy retains
the ordinary process-group cleanup behavior.

This option alone preserves host filesystem, network, devices and native
authentication access. Requests to independent host services are outside its
process-descendant boundary. Use the full isolation option below to restrict
filesystem and network authority too.

Preview also shows selected file and byte counts. Snapshot admission is bounded
to 100,000 existing selected files and 512 MiB, checked before raw working-content
snapshot objects are written and checked again while fingerprinting. These are protective
selection bounds; trusted filters and unrelated writers are not a disk quota.

### Private snapshots and full isolation

New runs default to `--storage private`. Snapshot objects live in the private
run directory, and every candidate has an independent Git object store without
alternates or shared hardlinks. Capturing and running a task leaves the source’s
index, objects, refs and Git configuration unchanged. Explicit application uses
a persistent `.git/agentmux-apply.lock` coordination file while preserving the
source index, objects, refs and configuration. `--storage shared` explicitly
selects the legacy source-object-store behavior; full isolation requires private
storage. Deleting a private run removes its retained copies, but cannot erase
filesystem backups or independently copied evidence.

On Linux, `--isolation bubblewrap` requires real user, PID, mount and network
namespaces and capped private HOME and temporary filesystems. Required probes
fail closed. Native tools see their selected candidate workspace, system runtime
files and explicit `--read-only-path` dependencies. Reviewer workspaces are
read-only. Checks can create build output in their assigned worktree, receive
no provider credentials or network, and fail verification if selected files change.
The default network policy is `none`; provider turns need the exact inference
origins you deliberately grant:

```sh
amx work "Fix the redirect" --isolation bubblewrap --network allowlist \
  --coder codex --reviewer claude \
  --allow-origin https://YOUR-CODEX-INFERENCE-ORIGIN \
  --allow-origin https://YOUR-CLAUDE-INFERENCE-ORIGIN \
  --auth coder=/private/codex-auth.json --auth reviewer=/private/claude-auth.json \
  --read-only-path /opt/selected-native-runtime --allow-models -- /usr/bin/python3 -m unittest
```

Replace the origin placeholders with the actual origins needed by your selected
profiles. Grants match exact scheme, host and port; redirects to another origin
need their own grant. HTTPS is inspected by a trusted per-turn proxy: an
ephemeral public CA is mounted read-only, each HTTP/1.1 Host or HTTP/2 authority
is checked, and upstream certificates and hostnames are verified. The signing
key stays in controller memory. Certificate-pinned, mutual-TLS or custom-trust
clients may fail closed; opaque tunnels and HTTP upgrades are refused. Runtime directories and executable dependencies outside
the system runtime must be selected explicitly. Do not grant a broad home or
credential directory as a runtime dependency. Selected credential files must
remain outside the source project and exposed dependencies. Supported fields
are filtered into a sealed anonymous, read-only handoff for that candidate;
the original login file and ambient login directories are not mounted. This
selection does not establish that a provider account or endpoint is usable.

With full isolation the default Antigravity reviewer uses `antigravity-bridge`:
an explicit Gemini model runs through a bounded host inference bridge, while
Pi performs the model’s tools inside the isolated workspace. The host bridge
does not execute model tools or export Antigravity login files. Its inference
requests are counted and capped separately from native turns. See the
[advanced guide](workflows.md) for plan fields and direct provider selection.

The Go controller remains trusted: it reads source inputs, runs the selected
host inference adapter and admits explicit source application after its checks.
Independent host services and unrelated host writers are outside its authority.
HOME and temporary filesystems default to 64 MiB each. Workspace growth has no
hard disk quota; selection bounds and post-run evidence limits do not provide
one. Keep reports private because they contain task and code data.

## Inspect the result

```sh
amx work status
amx work show latest
amx work list --limit 10
amx work show RUN_ID --open
```

These commands use the current Git project. `status` explains the recorded
phases, checks, final review, changed files and next action. `show` also prints
the hash-checked patch preview and saved HTML report path. The preview is capped
at 64 KiB, preserves code line breaks and escapes terminal control characters;
truncated previews identify the full retained patch. Use the retained artifact
or explicit apply command for integration, because displayed escapes are for
inspection. Ordinary inspection starts no model and opens no browser. `list`
shows stable run IDs for this project, newest first. Omit `--limit` to retain
the full list; an explicit limit accepts 1–1024.

In v0.13.0, `show RUN_ID --open` renders a fresh private,
script-free HTML report from the selected run's saved evidence and asks the
system browser to open it. Use an explicit ID, directory or `latest`; it
resolves that selector once and never starts another run. Existing `report.html`
is not opened. The generated file path is printed and retained even if the
browser cannot open it. `--open` and `--json` cannot be combined. Stable IDs
and `--limit` are included in v0.13.0.

Use an explicit directory or run ID to inspect another recorded attempt:

```sh
amx work show /private/run-directory --repo /path/to/project
amx work status RUN_ID --repo /path/to/project
```

A run belonging to another project is refused. The catalog cannot redirect an
application into a different repository.

By default, private run state lives under
`$XDG_STATE_HOME/agentmux/workflows`, or
`~/.local/state/agentmux/workflows`. `--catalog DIR` selects another private
catalog; use the same option to list or resolve its latest run. Explicit result
directories are indexed too. Outputs never overwrite an existing run.
For an explicit `--output`, its parent directory must already exist; omitting
the option lets Agentmux prepare its private catalog hierarchy for you.

`--json` prints structured output on stdout. Phase progress remains on stderr:

```sh
amx work status latest --json
amx work "Fix the redirect" --json --allow-models -- go test ./...
```

## Apply only the result you inspected

```sh
amx work apply latest
```

Application is explicit. It rechecks the original source, final result,
verification and patch, and refuses stale or incompatible work. It does not
commit, push, publish or merge conflicts. Your staging remains unchanged;
inspect your source diff before committing.

If unrelated work changed the source or candidate since the run, keep both
results and reconcile deliberately. An application with uncertain effects
reports whether changes may already be present; inspect before retrying.

### Recover an interrupted application

```sh
amx work status latest
amx work recover latest
```

Before admitting source mutation, Agentmux saves an uncertain application intent.
After applying and checking the source, it finalizes that same record. Changed
files and directory entries are synced before a confirmed receipt is saved.
Unsupported directory durability fails closed. The application lock survives in
an admitted Git writer if its controller dies, and releases when the writer
exits. A stale coordination filename by itself does not hold the kernel lock.

Recovery takes the same lock and compares source HEAD and exact content with the
recorded starting and final snapshots. An exact match can resolve the saved
intent as `not_applied` or `applied`; divergent or uninspectable content remains
uncertain. Recovery updates existing records, never reapplies a patch or undoes
source files. Damaged history blocks application until inspected. Settle unrelated
writers first; Agentmux's lock coordinates Agentmux operations, not every host
application. Filesystem and hardware must honor their sync guarantees.

## Return after interruption

Ctrl+C cancels active work and retains its recorded partial outcome. A valid
run is registered during execution and again after completion so it remains
discoverable through `status` and `list`. The printed result directory provides
a direct route if catalog registration was unavailable.

Recorded `running` is not proof that a process is still alive. After a hard
process or machine interruption, inspect its evidence before starting a new
attempt. This command does not automatically resume an uncertain provider turn
or undo external side effects. A failed final review remains available for
inspection; it is not silently treated as approval.

## Execution and evidence

Without `--isolation bubblewrap`, native tools and checks use trusted local
execution with host authority. Private Git stores separate retained data; they
do not themselves confine a process. With full isolation, the recorded policy is
required again for inspection, verification, recovery and application helpers.
Unavailable boundaries remain visible as failures. The trusted controller and
owner-selected runtime dependencies are part of that boundary.

For individual comparisons, custom mission plans, exact handoff delivery,
snapshot watchers and repeated trials, use [the advanced workflow
guide](workflows.md). The [validation guide](workflow-validation.md) explains
how to check your build, selected providers and isolation boundaries.
