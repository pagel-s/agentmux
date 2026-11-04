# Validate workflows

Use these checks to validate an Agentmux build and the workflow features you
intend to use. Model-free checks exercise process control, private Git storage,
verification and recovery without loading provider credentials.

## Build and regression checks

Run from a source checkout with Go 1.24 or newer, Python 3, Git and Node.js.
Terminal integration tests also require tmux.

```sh
make build
go test -race -count=1 -timeout=20m ./...
go vet ./...
node cmd/agentmux/workweb/app.test.cjs
python3 scripts/test-validate-workflows.py
python3 scripts/test-soak-workflows.py
```

Conditional integrations report skips when their prerequisites are absent.
A passing default suite does not establish that those integrations ran.

## Observation and reporting

Current source includes model-free regressions for owner observation, task
progress and native readiness:

```sh
go test -race ./internal/managed -run 'TestObservation|TestTaskProgress|TestNativeReadiness' -count=1
go test -race ./cmd/agentmux -run 'TestObserve|TestNativeObservation' -count=1
node --test cmd/agentmux/workweb/app.test.cjs
```

These check bounded full-history summaries, exact attempt/credential fences,
separate event and worker timestamps, restart-unknown progress, unavailable
components and read-only inspection. They make no model calls. They do not
prove provider acceptance, useful model progress or elapsed production use.
See [observability](observability.md) for operational commands and interpretation.

## Model-free workflow journeys

Choose a new output directory for each run. The validator creates disposable
repositories and checks source preservation, durable reports, verification and
explicit application behavior.

```sh
validation_root=$(mktemp -d)
python3 scripts/validate-workflows.py --binary bin/amx --output "$validation_root/flow"
python3 scripts/validate-workflows.py --binary bin/amx --output "$validation_root/work" --guided
```

Inspect `validation.json`: its completion state and case results are the run
authority. `diagnostic.json` describes case progress and is not a substitute
for successful finalization. An interrupted process or missing final record
does not establish success.

Use `--storage shared` only to check explicit legacy compatibility. New
workflows should use the default private stores.

## Required Linux isolation

Full workflow isolation requires Linux, Bubblewrap with capped tmpfs support,
and working user/PID namespaces. Requested isolation fails closed when the
host cannot provide it. Process-group tests alone do not validate filesystem
or network isolation.

```sh
make test-sandbox

isolation_root=$(mktemp -d)
python3 scripts/validate-workflows.py --binary bin/amx --output "$isolation_root/flow" \
  --storage private --isolation bubblewrap --containment pid-namespace
python3 scripts/validate-workflows.py --binary bin/amx --output "$isolation_root/work" \
  --guided --storage private --isolation bubblewrap --containment pid-namespace
```

The workflow oracle and cleanup regressions also have an explicit real-CLI
isolation mode. A private evidence parent retains integration records for
every outcome:

```sh
evidence_root=$(mktemp -d)
AGENTMUX_SANDBOX_INTEGRATION=1 \
AGENTMUX_INTEGRATION_BINARY="$PWD/bin/amx" \
AGENTMUX_WORKFLOW_TEST_EVIDENCE_ROOT="$evidence_root" \
  python3 scripts/test-soak-workflows.py
```

Keep evidence private: it can contain repository contents, patches, task
instructions and process details. Review artifacts before sharing them.

## Fault and recovery soak

The model-free soak checks interrupted turns, verification failures, uncertain
application and recovery, source/staging preservation, cleanup and evidence
publication. By default each iteration exercises every scenario class.

```sh
soak_root=$(mktemp -d)
python3 scripts/soak-workflows.py --binary bin/amx --output "$soak_root/run" \
  --iterations 10 --duration-seconds 1800 \
  --storage private --isolation bubblewrap --containment pid-namespace
```

Inspect `soak.json` for `state`, `completed`, iteration counts, assertions and
coverage exclusions. A selected scenario subset validates only that subset.
Cleanup observations cover the recorded process trees. Worker containment
also depends on the required namespaces; unrelated host services remain
outside that boundary.

## Live provider checks

Provider tests require an explicit profile, selected authentication, a disposable
repository and bounded execution. Review the validator's `--help` before using
`--native` or a native comparison: these options can make billable requests.
Run only the provider routes you have configured and intend to use.

Recorded protocol fixtures establish parser behavior for those frames. Native
CLI startup establishes startup behavior. An actual verified tool task supplies
stronger evidence for that profile, version and task; none guarantees future
provider compatibility or arbitrary task correctness.

See [provider compatibility](provider-compatibility.md),
[guided coding](working.md), and [advanced workflows](workflows.md) for setup.

## Interpret the boundaries

- The Go controller and any selected host inference adapter remain trusted.
- Private Git stores isolate run evidence; explicit shared storage retains
  legacy source-store behavior.
- Bubblewrap constrains selected workers and checks. Independent host writers
  and services remain outside its authority.
- Selection bounds are not a hard workspace disk quota. Native deadlines and
  admission limits do not guarantee a monetary cap; use provider-side controls.
- Recovery resolves existing application evidence. It does not replay a patch
  or infer a missing outcome.
- Exact revision checks and fresh reviews provide task-specific evidence.
  They do not prove universal correctness.

Use the [security model](security-model.md) and [hardening guide](hardening.md)
when selecting the boundaries for a deployment.
