# Your first checked result

Start with a deterministic fixture before configuring providers or using a real
project. [Install Agentmux v0.14.0](installation.md), then run from any directory:

```sh
amx demo
```

After installation, this command needs Git and a POSIX shell. It needs no Python,
tmux, provider CLI, account or API key and makes no model calls.

## What happens

The demo creates a new private temporary directory containing a disposable Git
repository. It runs a deterministic candidate through Agentmux's workflow
engine, checks the candidate independently, retains a patch and report, then
applies the checked patch **only to the generated repository**. A final byte
comparison verifies the fixture result. Your current project is not the target.

Success means the workflow check and fixture application verification passed.
Look for:

```text
Success: checked patch applied; fixture file independently verified.
```

The command prints the repository, report and patch locations. Retained files
let you inspect what happened after the command exits.

This is a fixture, not an AI coding task. It demonstrates local workflow
mechanics; it does not measure model quality or certify sandbox isolation.

## Inspect the evidence

Open the printed `run/report.html` in a browser and inspect the checked change.
The report is a local file; no running web server is needed. The neighboring
`run/report.json` retains machine-readable evidence. The command also prints
the exact commands for reading the run from the terminal:

```sh
amx flow status /printed/demo-directory/run
amx flow explain /printed/demo-directory/run
```

Use the actual printed path in place of `/printed/demo-directory`. Confirm that
the candidate passed its independent check, inspect the saved patch, and compare
the resulting `repository/hello.txt` with that change. Its final contents are:

```text
Agentmux demo: checked change applied.
```

Reading these records starts no models and applies no additional change.

Each `amx demo` invocation creates a fresh directory. To choose where to retain
it, pass an absolute path that does not yet exist:

```sh
amx demo --output /path/to/new-agentmux-demo
```

Keep that directory while inspecting the evidence. Delete it when finished;
it contains only this run's fixture and retained files.

If `demo` is unknown, check `command -v amx` and `amx version`, then follow the
[upgrade instructions](installation.md#install-a-published-binary-recommended).
If Git or the shell is missing, install the named dependency and rerun. A failed
demo is not a passing result: retain the printed diagnostics and files when
reporting the failure.

## Move to a live coding task

Install and sign in to the native CLIs you want to use. Trusted-local setup
selects installed tools and shows the coder/reviewer in its preview; Codex plus
Claude is the usual pair when both are installed.
In a Git project with an initial commit, preview a specific task with its
executable acceptance check:

```sh
amx work "Fix the login redirect" -- go test ./...
```

Replace the request and `go test ./...` with your task and check. This preview
starts no models and writes no run files. It shows the selected providers,
phases, deadlines and result location.

When the preview is suitable, explicitly permit provider calls:

```sh
amx work "Fix the login redirect" --allow-models -- go test ./...
amx work status
amx work show latest
```

Provider calls can incur charges. Agentmux retains the implementation, fresh
reviews, executable checks and patch in a separate worktree. Inspect the report
and patch before choosing `amx work apply latest`; that command changes your
project. A failed check or unapproved review does not become an accepted result.

See [work on a task](../working.md) for provider choices, deadlines, failed or
interrupted runs and optional Linux isolation. For several interactive native
conversations instead, follow the [short native-session quickstart](quickstart.md).
