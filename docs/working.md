# Ask for work, inspect the result, apply it

`amx work` turns one request into a checked coding-and-review journey. Codex
implements the change, Antigravity reviews it, Codex addresses the findings,
Antigravity reviews the final revision, and the project checks run again. You
receive a saved report and a patch in a separate Git worktree. Agentmux does not
integrate that patch into your project until you explicitly apply it.

This command is available in the current source preview. Build and install this
checkout with `make install`; the earlier published binary predates this journey.

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

The command prints the result location immediately and reports phase changes
as they are recorded. The usual path is:

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
`gemini-3.8-flash-low`. This journey does not select Claude or delegate to other
models. Their availability still depends on your account and CLI versions.

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

This option preserves host filesystem, network, devices and native authentication
access. Requests to independent host services are outside its process-descendant
boundary. For filesystem and network isolation, use the existing managed capsule
workflow; this command does not silently copy native credentials into a sandbox.

Preview also shows selected file and byte counts. Snapshot admission is bounded
to 100,000 existing selected files and 512 MiB, checked before Git writes new
snapshot objects and checked again while fingerprinting. These are protective
selection bounds; trusted filters and unrelated writers are not a disk quota.

## Inspect the result

```sh
amx work status
amx work show latest
amx work list
```

These commands use the current Git project. `status` explains the recorded
phases, checks, final review, changed files and next action. `show` also prints
the hash-checked patch preview and saved HTML report path. The preview is capped
at 64 KiB, preserves code line breaks and escapes terminal control characters;
truncated previews identify the full retained patch. Use the retained artifact
or explicit apply command for integration, because displayed escapes are for
inspection. These commands do not open a browser or start a model. `list`
shows recorded runs for this project, newest first.

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

The journey uses trusted local execution. Native tools and checks retain host
authority; worktrees separate changes but are not filesystem or network
sandboxes. Review changes are checked and refused, which does not isolate a
reviewer's process from the host. Use trusted owner-authored requests and
checks, and keep the reports private because they contain task and code data.

For individual comparisons, custom mission plans, exact handoff delivery,
snapshot watchers and repeated trials, use [the advanced workflow
guide](workflows.md). The [validation record](workflow-validation.md) identifies
the actual tested build, provider selections, regressions and limits.
