# Managed runtime preview validation

This preview adds an opt-in v2 managed runtime alongside the original tmux
workspace flow. It is intended for developer teams and bounded research runs.
The design's eight follow-up areas have concrete implementations and recorded
checks; limitations below are part of the supported scope.

| Area | Delivered behavior | Evidence |
| --- | --- | --- |
| Setup | Interactive/spec-based role, harness, endpoint, auth and sandbox setup; private config output | Setup/config tests; [guide](managed-setup.md) |
| Providers | Claude, Codex, Pi and Antigravity adapters; local Antigravity-to-Pi bridge; explicit compatible endpoint metadata | Native provider checks and [provider matrix](provider-validation.md) |
| Hardening | Exact destination proxy, aggregate admission accounting, capped workspace/history/tmp filesystems, safe checkpoint recovery | Actual proxy-bypass rejection, kernel ENOSPC, checkpoint and budget integration tests; [boundaries](hardening.md) |
| Operation | Between-turn credential reload, explicit interruption handling, native history generations, owner recovery, versioned installer | Two-turn filtered credential test; 30-turn/six-restart/crash soak; [recovery](traces-and-recovery.md) |
| Memory | TTL, provenance, tombstones, scoped lexical/vector retrieval, explicit cross-run CAS import/export | ACL/expiry/receipt/CAS/vector/archive regression tests; [guide](memory-lifecycle.md) |
| Research | Factorial preregistration, scarce resources, trust exchange, pooling, dynamic populations and consented teams | Command-only environment checks and a fixed 64-task native feasibility pilot; [methods](research-study.md) and [results](research-pilot-results.md) |
| Observability/scale | Scoped activity/status, bounded authenticated local traces, explicit SSH workers and restart | Agent-bound trace tamper tests, actual two-machine scoped handoff/resume, 8/32/128 logical-agent broker measurements; [results](remote-workers.md) |
| Review/release | Independent automated review with defect regressions, flagged race suite, cross-platform packaging and checksum-verified installation | Full source/tests in the review branch; four Linux/macOS architecture packages |

The final local suite ran `go test -race ./...` with managed, experiment,
Bubblewrap, disk/proxy integration and repeated-round research flags enabled.
`go vet ./...`, formatting and installer safety checks also passed. Additional
tests verify actual cgroup controls and escaped-descendant cleanup. Command-line
acceptance checks cover shared boards/memory, hierarchy, revocation, spawning,
team consent/history and restart without replay. GitHub CI runs its supported
subset and reports unavailable kernel sandbox capabilities explicitly.

The independent automated review found and fixed owner-spawn budget ancestry,
memory tombstone byte accounting, private receipt actor matching, provenance
checksum coverage, authenticated trace ownership and trace relabeling issues.
Remote deployment and release installer paths received separate review. This
is development validation, not an external security certification.

Provider-native billing and total token usage are not hard-capped by declared
admission allowances. HTTPS paths remain opaque. Retention is logical, without
secure erasure of backups/provider data. Native adapter traces exclude hidden
provider state and unreported activity; remote trace collection is unsupported.
SSH placement is explicit, without an automatic distributed scheduler. The
soak is bounded rather than a multi-day availability claim. The native pilot
is underpowered and includes an adapter measurement confound; it cannot
establish agent personality or cooperation traits.

Raw state, credentials, native histories, trial artifacts and host details stay
private. The repository contains reproducible fixtures, sanitized methods and
measurement summaries. The pre-existing private design website is separate
from release source and artifacts.
