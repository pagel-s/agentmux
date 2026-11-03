# Contributing to Agentmux

Agentmux is licensed under the [MIT License](LICENSE). Contributions are
provided under the same license.

Start with the [first-result guide](docs/getting-started/first-result.md), then
read the guide for the area you want to change. The managed runtime and
workbench are experimental. Keep new behavior small enough to explain and
verify independently.

## Work on a change

1. Create a branch from current `main`. Describe the user-visible problem and
   the expected result before expanding the implementation.
2. Keep everyday terminal sessions, managed teams and coding workflows distinct.
   Broker permissions do not confer filesystem isolation or native approvals.
3. Preserve existing work, credentials and running sessions. Refuse ambiguous
   state and retain diagnostic evidence instead of silently retrying a mutation.
4. Update help and the relevant guide when behavior changes. Keep the default
   help and first-result path short; put advanced details in topic help.
5. Add a regression when a failure could lose work, weaken a boundary, accept
   stale evidence, or report false success. Prefer an independent observable
   outcome to an assertion that repeats implementation logic.

Do not put provider credentials, native histories, private repositories or
generated run evidence in a patch or issue. See [security reporting](SECURITY.md).

## Build and validate

Requirements: Go 1.24+, Git, Make, Python 3 and Node.js. Terminal tests need
tmux. Full isolation tests need a supported Bubblewrap and working Linux
namespaces; requested isolation must never silently downgrade.

```sh
make build
./bin/amx demo
go test -race -count=1 -timeout=20m ./...
go vet ./...
node --test cmd/agentmux/workweb/app.test.cjs
python3 scripts/test-install-release.py
python3 scripts/test-check-release.py
python3 scripts/test-smoke-release.py
python3 scripts/test-publish-release.py
python3 scripts/test-compare-workflows.py
```

The Makefile selects a compatible toolchain under `.tools/` when available.
Use `make test-sandbox` for sandbox/workflow changes. See
[workflow validation](docs/workflow-validation.md) for conditional integrations,
failure injection and recovery checks. State which integrations ran and which
were unavailable; a skipped integration is not a passing boundary test.

Model calls require a deliberately selected provider, disposable project,
finite deadline and explicit opt-in. Routine contributions and CI should use
the deterministic fixtures. Comparative productivity claims require the
[evaluation protocol](docs/evaluation.md).

## Refresh documentation screenshots

Capture the real app after changing visible controls. The capture tool needs
Linux, tmux, Git and DejaVu Sans Mono fonts.
Use a private Python environment for browser and terminal-rendering dependencies:

```sh
make build
amx_docs_env="$(mktemp -d)"
python3 -m venv "$amx_docs_env"
"$amx_docs_env/bin/pip" install playwright pillow rich
"$amx_docs_env/bin/playwright" install chromium
"$amx_docs_env/bin/python" scripts/capture-docs.py --binary bin/amx
```

This creates disposable command-only workspaces and a scripted research trial;
both use explicitly trusted local execution. It starts no models and uses no
live user workspace. It stages all captures
before updating `docs/assets/guide/`. The adjacent `capture-provenance.json`
records the source, fixture conditions and image hashes. Inspect every image
and its caption; refresh each Markdown image's `?v=` cache key to the first
12 characters of its recorded SHA256, then run `mkdocs build --strict` before
publishing. Never use generated UI illustrations as evidence of actual app controls.

## Send a pull request

Explain the concrete problem, resulting behavior and relevant validation.
Include reproduction steps for a fix, and identify any change to persisted
state, compatibility or trust boundaries. Keep unrelated cleanup separate.
Passing checks support review; they do not establish arbitrary task correctness
or production security.

For release work, use the [release procedure](docs/releasing.md). Never move a
published version tag or replace published assets to repair a release.
