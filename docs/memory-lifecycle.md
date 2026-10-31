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
