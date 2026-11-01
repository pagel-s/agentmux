# SSH workers and broker scale

`managed remote` runs one configured worker on an explicitly selected trusted
Linux amd64 SSH host. It deploys the Agentmux binary, a reduced definition with
only that agent's process/connection and accessible board names, and that
agent's own credential. It never copies owner credentials, SQLite, peer
credentials, source workspaces, dependency directories or native login caches.
The remote host can read that worker's token/data and must be trusted for them.

Start the local broker, submit a task, then run:

```bash
agentmux managed remote --state STATE --agent worker --host YOUR_SSH_ALIAS \
  --remote-dir /tmp/my-agentmux-worker --once
```

The directory must be new and absolute with an existing canonical parent; it
is created privately and retained for inspection/history. The worker requires
Bubblewrap and its native harness/dependencies on the remote machine. Python 3
and SSH Unix-socket forwarding are required. No root privileges or public
broker port are used. The owner's local process keeps the SSH session open;
disconnect/owner cancellation stops the remote worker's process group. Broker
lease checks stop native execution when communication cannot be confirmed.

An SSH remote Unix-socket forward exposes only the authenticated local broker
socket inside the remote private state. Each sandbox still receives only its
own token. `network: none` therefore permits broker coordination without
providing ordinary IP connectivity. Provider traffic needs an explicit host or
allowlist network policy separately.

Use `--layout layout.json` for explicit host-specific paths:

```json
{
  "workdir": "/tmp/my-agentmux-worker/state/workspaces/worker",
  "read_only_paths": ["/opt/my-native-harness"],
  "auth_file": "/private/remote-native-credential.json"
}
```

The optional `command` array changes argv only for a command harness. Layouts
cannot change the role, model, policy or broker admission limits. Dependencies
and native credentials must be provisioned separately; selecting `auth_file`
does not copy a local login. Default workspaces are dedicated remote paths.
Disk capsules require their exact state-owned workspace layout.
The broker remains authoritative after deployment: managed claims capture the
current owner role, and live permission/admission overlays apply centrally.
Changing a remote layout is not a way to override those controls. Filtered
native file credentials are reloaded between managed turns from the selected
remote source; this does not synchronize a local owner's later login refresh.

To restart an identical deployment, add `--resume`. It validates private owned
directories and exact binary/definition/credential hashes before execution,
preserving workspace and native session files. Changes to credentials, binary,
definition or layout require an explicit new deployment/migration; they are
never overwritten implicitly. The central session deadline is not extended.
Use owner task/conversation recovery before resuming uncertain work.

Remote tracing is rejected when `trace.enabled` is selected: the local broker
cannot authenticate a remote trace through a local file path. A central trace
collector is a future extension; this command does not claim that coverage.
Portable signed trace bundles support moving already captured local evidence
for offline verification; they do not add remote capture or collection.
See [traces and recovery](traces-and-recovery.md).
Spawning remains centrally bounded, with the existing local supervisor creating
approved child workers. This is explicit SSH placement, not an automatic
multi-host cluster scheduler or host failure migration service.

## Reproducible validation

```bash
python3 scripts/smoke-managed-remote.py --host YOUR_SSH_ALIAS \
  --remote-dir /tmp/amx-unique-check --output /tmp/remote-result.json
python3 scripts/benchmark-managed.py --output /tmp/broker-benchmark.json
python3 scripts/soak-managed.py --output /tmp/soak-result.json
```

On 2026-10-31 the cross-machine command-only check passed with an isolated local
broker, an isolated local lead and an isolated remote worker. Board/memory
handoff, peer/owner credential isolation and forbidden reverse instruction were
verified; the final check also restarted the identical deployment with
`--resume`, and all three tasks received owner verification. One local trace
was anchored and verified. No provider/model calls were made. Initial deployment
plus remote restart and two remote tasks took about 23 seconds in the final run.

The local broker benchmark used a 32-logical-CPU Linux x86_64 machine. Every
agent ran 20 iterations of `board.post`, private `memory.put` and board
`message.list`, through authenticated HTTP over a Unix socket to a sandboxed
SQLite broker. All operations succeeded:

| Logical agents | Operations | Ops/sec | p50 ms | p95 ms | p99 ms |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 8 | 480 | 712.87 | 10.964 | 17.272 | 18.893 |
| 32 | 1920 | 803.86 | 27.637 | 120.549 | 162.941 |
| 128 | 7680 | 428.01 | 250.481 | 628.191 | 701.964 |

The same script also ran directly on a separate 128-logical-CPU Linux x86_64
host, after stopping the validation inference server, with zero errors:

| Logical agents | Operations | Ops/sec | p50 ms | p95 ms | p99 ms |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 8 | 480 | 167.59 | 49.980 | 72.363 | 137.378 |
| 32 | 1920 | 131.91 | 248.600 | 413.934 | 444.559 |
| 128 | 7680 | 77.10 | 1596.019 | 3121.154 | 3483.363 |

More logical CPUs did not improve this serialized, durable workload. Host and
storage conditions were not isolated experimentally; these two single runs do
not identify the cause of their performance difference.

These are one-run workload measurements, including SQLite commits. They show
contention at 128 API actors; they do not measure 128 simultaneous native model
processes, remote network latency, repeated confidence intervals or maximum
cluster capacity. Small teams remain the primary deployment target.

An earlier bounded soak passed 30 verified command turns in about 94 seconds, six clean
restarts with the original deadline, verification of each completed trace, and
one forced in-flight broker crash. The interrupted native process stopped,
delivery remained uncertain without automatic replay, and the owner resolved
it as failed. This is recovery evidence, not a multi-day uptime guarantee.
The later 250-turn/50-restart local soak is recorded separately in
[release validation](release-validation.md#follow-up-hardening-checks-2026-10-31).
The SSH and benchmark numbers above are historical measurements of those runs,
not throughput or availability guarantees for a current deployment.
