# SSH workers and broker scale

`managed remote` runs one configured worker on an explicitly selected trusted
Linux amd64 SSH host. It deploys the Agentmux binary, a reduced definition with
only that agent's process/connection and accessible board names, and that
agent's own credential. It never copies owner credentials, SQLite, peer
credentials, source workspaces, dependency directories or native login caches.
The remote host can read that worker's token/data and must be trusted for them.

Start the local broker, submit a task, then run:

```bash
amx managed remote --state STATE --agent worker --host YOUR_SSH_ALIAS \
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

The remote smoke test exercises command-only board/memory handoff, credential
separation, forbidden reverse instructions and resumption of an identical
remote deployment. It makes no provider calls. Use a disposable remote directory
and retain its report to inspect the boundaries on your selected host.

The benchmark measures authenticated board/memory operations against a durable
SQLite broker. Report host/storage conditions, workload, logical client count,
latency and errors alongside any throughput measurement. Logical API clients do
not represent simultaneous native model processes or maximum cluster capacity.
Small teams remain the primary deployment target.

The bounded soak checks completed task anchors, broker restarts and an in-flight
crash. Uncertain native work requires explicit resolution without automatic
replay. Passing a finite check does not establish multi-day uptime, remote host
failure migration or availability for other deployments.
