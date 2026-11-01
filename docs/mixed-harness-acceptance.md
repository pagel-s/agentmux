# Mixed harness sandbox acceptance

This documents one explicit three-worker integration scenario and its
reproduction requirements. Its recorded 31 October 2026 run is summarized in
[provider validation](provider-validation.md#what-the-developer-workflows-established).
The later [provider acceptance](provider-acceptance.md) separates additional
connectivity, endpoint and native-startup checks from this complete coding
workflow. For everyday interactive sessions, start with [work sessions](work-sessions.md).

`scripts/smoke-managed-models.py` runs one real Claude lead turn, one real Codex
coding turn, and one Pi turn against an already running local model. The broker
and all three workers use separate Bubblewrap sandboxes. The script creates a
private temporary team and uses the normal managed supervisor and tmux panes.
It never downloads a model or starts a model server.

This is an integration acceptance scenario, not a cooperation experiment or a
model-quality benchmark. The lead uses a supplied coordination helper. Pi
receives the submitted source and reported test evidence in its instruction and
produces an analysis-only review; its tools are disabled. A nonempty Pi response
establishes that the local inference path ran, not that its verdict was correct.

## Requirements and reproduction

Use Linux with working unprivileged Bubblewrap namespaces, tmux, Python 3.9+,
Git, and an Agentmux binary built from the current source. Install Claude Code,
Codex CLI, and the maintained `@earendil-works/pi-coding-agent` package. The
installed CLIs must pass `managed doctor` capability checks. Claude needs the
native `Read,Write,Edit,Bash` tool allowlist used by this task; the managed adapter
retains its safety and permission flags.

This scenario explicitly sets the Codex agent's `codex_sandbox: external`.
Codex then delegates native tool isolation to the enforced outer Bubblewrap
namespace. This option requires the Codex harness, `sandbox: bubblewrap`, and a
real wrapper; local workers cannot select it. The native CLI's
`--sandbox danger-full-access` selects that external-boundary contract inside
the already isolated process. It adds no approval-bypass flag and grants no
access to unmounted host files or credentials. Ordinary Codex configurations
retain native `workspace-write` sandboxing by default, with no automatic
fallback. On the validation host, native tools could not create their additional
nested user namespace; actual nongenerative native tool execution passed under
the explicit external policy. No host security policy was changed.

Claude and Codex need existing login credential files. By default the script
uses `~/.claude/.credentials.json` and `~/.codex/auth.json`. Each must be an owned
regular private `0600` file, not a symlink. Managed workers receive a private
read-only native-auth snapshot at the expected sandbox path; the original source
and the rest of the host home are not mounted.

First exercise the independent verifier without any model calls:

```sh
python3 scripts/smoke-managed-models.py --check-fixtures
```

The verifier must reject the broken fixture and accept the reference. It runs
submitted Python in a fresh network-disabled Bubblewrap sandbox without broker
or native credentials. Its tests remain outside participant workspaces.

Start a local OpenAI-compatible chat-completions endpoint separately. Confirm
the exact model ID with its `/v1/models` response, then explicitly opt into the
real turns. For an endpoint already listening on port 34501 with model ID
`cached-qwen-1.5b`:

```sh
python3 scripts/smoke-managed-models.py \
  --binary ./bin/agentmux \
  --allow-models \
  --pi-base-url http://127.0.0.1:34501/v1 \
  --pi-model cached-qwen-1.5b \
  --bounded-resources \
  --keep-output
```

The URL and model name above are an example of an already running local service;
the script does not provision that service. It rejects non-loopback endpoints.
The local model must support Pi's text-output completion path. Tool calling is
not required. Claude and Codex use their native remote services with explicit
`network: host`; this is not an outbound network allowlist.

Use `--claude-bin`, `--codex-bin`, and `--pi-bin` for executable locations, and
`--claude-auth-file` / `--codex-auth-file` for credential sources. Optional
`--claude-model` and `--codex-model` select explicit native models. The script
discovers standard executable symlinks, Node, installed package code, and npm
optional dependencies without mounting a whole home directory. A Node-based Pi
launcher uses the canonical Node binary and real package entrypoint so a sandbox
file bind does not change package-relative module resolution. Native executable
arguments also use their canonical paths. Standalone Codex receives only its
specific sibling code-mode host, bundled Bubblewrap/ripgrep, and package metadata;
the containing `.codex` directory is not exposed. Add an exact
`--dependency reviewer=/absolute/package/path` (or `lead=` / `coder=`) when an
installation needs additional read-only runtime code. Broad paths remain
subject to managed boundary validation. The handwritten
[sandboxed coding example](https://github.com/pagel-s/agentmux/blob/main/examples/managed/sandboxed-coding.yaml) shows the
equivalent owner-configured boundaries; its placeholder paths need replacement.

`--bounded-resources` requests per-worker 2 GiB memory, one CPU, 256 processes,
1,024 open files, and 120 CPU seconds. Memory/CPU/process-tree limits require a
working delegated user-systemd cgroup scope; an unavailable enforcement path
fails closed. `--memory-mib`, `--cpu-percent`, `--processes`, `--open-files`, and
`--cpu-seconds` override individual values. The overall deadline defaults to 600
seconds (`--timeout`, range 60–1,200). Two cloud tasks describe the requested task
count, not a token or monetary spending guarantee. Native harnesses can make
multiple model requests within a turn. No model task is automatically retried.

## What a successful run establishes

1. An owner-submitted lead task remains pending with the same ID and version
   across a controlled isolated-broker restart before any worker claims it.
   The owner does not resubmit the instruction. Readiness uses an authenticated
   identity response, so a stale Unix socket is insufficient.
2. The actual Claude lead invokes authenticated operations for the shared board,
   project memory, and subordinate instructions. The Codex coder edits a small
   Python function, runs development tests, and publishes its complete source,
   SHA-256 digest, and reported test evidence into a scoped memory handoff.
3. Reverse coder-to-lead instruction and private-board access are denied.
   The local reviewer cannot read the handoff namespace directly. It receives
   the bounded artifact through the lead's authorized instruction instead.
4. Before each real native turn, a trusted read-only launch helper checks the
   actual authenticated identity, private HOME/workspace, own bridge paths,
   hidden host sentinel/state/owner credential, hidden peer broker/native
   credentials, hidden original auth sources, omitted owner environment secret,
   and configured CPU-time/open-file limits. Nongenerative help/version/login
   metadata probes bypass this task-specific helper check.
5. The owner separately runs immutable normalization, type-error, generator,
   nonmutation, and fresh-output checks over the submitted artifact in an additional,
   credential-free verifier sandbox. Only that oracle can mark the coder task
   verified. The review model's own claims do not determine acceptance.

## Reports and cleanup

Standard output is a JSON summary with task counts, boundary checks, resource
requests, oracle outcomes, artifact/review digests and byte counts, and reported
native usage when available. It excludes source code, raw model responses, raw
native diagnostics, and credential contents. Reported usage is an observation,
not a billing guarantee. Use `--report /new/private/report.json` to save a new
`0600` summary file.

The private temporary directory contains the exact generated configuration,
broker database, workspace files, model outputs in task records, private logs,
and a `0600` owner-operation JSONL audit. Those artifacts are sensitive despite
the sanitized summary; do not publish the directory. The audit captures broker
coordination, not a complete native prompt/tool/network trace.

The `finally` cleanup stops the manually started broker and the managed
supervisor. Successful artifacts are removed unless `--keep-output` is set.
Failures or unconfirmed cleanup retain the private directory for inspection and
print its location; no automatic rerun can duplicate uncertain model work.
The already running local model server remains the owner's responsibility.
