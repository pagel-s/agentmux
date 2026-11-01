# Memory lifecycle and retrieval

Private memory belongs to one authenticated agent. Named shared memory retains
exact namespace read/write ACLs; no retrieval or transfer operation adds access.
Task/native conversation history and this explicit memory store are separate.

```yaml
memory_policy:
  default_ttl_seconds: 86400
  max_ttl_seconds: 604800
  max_total_bytes: 67108864
```

Omit the default TTL for non-expiring memory. `memory.put` accepts `namespace`,
`key`, `value`, the current CAS `version`, optional `ttl_seconds`, `tags`, and
`sources`. Source references can identify a message/task the writer can see or
an explicit external HTTP(S) URI plus optional SHA256. The broker never fetches
external URIs and does not certify the truth of agent-authored claims. Created
and updated times, author, content digest and previous digest record provenance.

Expired entries disappear from reads and retrieval immediately. `memory.head`
returns their current version/state without returning expired content. Updates
require that version; `memory.delete` and owner `memory.prune` retain versioned
tombstones, preventing stale writers from recreating an earlier CAS version.
Deletion/pruning clears stored payloads and historical idempotency-response
payloads. This is logical retention, not physical secure erasure of SQLite
pages/WAL, backups, exported archives, native histories or provider systems.
The allocation ceiling bounds current serialized memory records, not the whole
SQLite file or every copy of content elsewhere.

Use the agent's own credential for private memory. The owner's `private`
namespace is separate; owner credentials do not impersonate an agent.
The export/import/search/index/prune commands below use the live broker.
Selecting `--state STATE --agent coder` loads that agent's credential from the
owner-controlled state directory; it does not change its API permissions.

```bash
agentmux managed memory export --state STATE --agent coder \
  --namespace private --output /private/coder-memory.json
agentmux managed memory import --state NEW_STATE --agent coder \
  --namespace private --input /private/coder-memory.json
agentmux managed memory search --state NEW_STATE --agent coder \
  --namespace private --query 'parser architecture'
agentmux managed memory prune --state STATE
```

Exports are exclusive mode-0600 files, bounded to 64 MiB/10000 entries. Imports
validate the archive and perform per-key CAS writes into an explicitly selected
authorized namespace. Existing keys require `--replace`; conflict resolution is
never implicit. Imports preserve remaining TTL, skip expired values, record an
import source, and can resume safely after partial completion. They retain the
destination writer's identity rather than impersonating the source author.
Each imported entry carries structured `sources` claims: `source_session`,
`source_agent` (the exporter), `source_namespace`, `source_author` (the original
entry's writer), `source_key`, `source_version`, the complete `archive_sha256`
and the imported value's `sha256`. These remain available on the destination
entry without the original archive. They are asserted source metadata, not an
authenticated source identity or a grant of source-namespace access. Re-exporting
the entry retains its source claims inside the archive; a subsequent import
records that immediate archive's identities.
Existing v1 import sources without the newer identity fields remain readable.
An entry imported by an older version can be enriched by reimporting its original
archive with `--replace`; enrichment still uses the destination's current CAS
version rather than silently changing an existing entry.
An archive checksum detects accidental content changes; it is not a signature
establishing a trusted source. Protect archive files as private data.

Lexical/tag search is local and deterministic. Semantic retrieval is optional:
`memory.index` stores a finite vector bound to the current content version and
an exact embedding model ID. Changing content invalidates its index. Cosine
search compares only matching model IDs and vector dimensions. The index and
search paths enforce the same namespace ACLs as ordinary reads/writes.

```bash
agentmux managed memory index --state STATE --agent coder --namespace private \
  --embedding-base-url http://127.0.0.1:8000/v1 --embedding-model EXACT_MODEL \
  --allow-provider-calls --max-entries 100
agentmux managed memory search --state STATE --agent coder --namespace private \
  --query 'parser architecture' --embedding-base-url http://127.0.0.1:8000/v1 \
  --embedding-model EXACT_MODEL --allow-provider-calls
```

These owner-side commands send selected authorized text to the explicit
embedding endpoint. Supply `--api-key-env NAME` when needed. No provider is
selected or contacted implicitly; calls, body sizes, timeouts and vector
dimensions are bounded. This is retrieval over explicit memory, not an automatic
long-term summary of everything agents observe.

## Value-only edits and retrieval evidence

Use `memory.patch` with namespace, key, value and expected version to change
only a live value. It requires both read and write access and preserves sources,
tags and exact absolute expiry. `memory.put` is full replacement and accepts an
explicit `expires_at`; imports retain source deadlines and skip expired entries.
Owner `exposure.list` queries retained authenticated retrieval metadata, including
reader, namespace/key, version, digest, time and route. Owner inspection and
forbidden attempts do not count as agent exposure. The bounded ledger records
data returned to an agent, not whether a model understood it.
