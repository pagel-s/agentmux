package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
)

// MemorySource is asserted provenance, not a claim that the source is true.
// Broker references must be visible at write time; external URIs are never fetched.
type MemorySource struct {
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	URI    string `json:"uri,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	// Imported identities are archive claims. Author on Memory remains the
	// authenticated destination writer; these fields never authorize access.
	SourceSession   string `json:"source_session,omitempty"`
	SourceAgent     string `json:"source_agent,omitempty"`
	SourceNamespace string `json:"source_namespace,omitempty"`
	SourceAuthor    string `json:"source_author,omitempty"`
	SourceKey       string `json:"source_key,omitempty"`
	SourceVersion   int    `json:"source_version,omitempty"`
	ArchiveSHA256   string `json:"archive_sha256,omitempty"`
}
type memoryInput struct {
	Namespace      string         `json:"namespace"`
	Key            string         `json:"key"`
	Value          string         `json:"value"`
	Version        int            `json:"version"`
	Limit          int            `json:"limit"`
	After          string         `json:"after"`
	TTLSeconds     *int           `json:"ttl_seconds"`
	ExpiresAt      *string        `json:"expires_at"`
	Tags           []string       `json:"tags"`
	Sources        []MemorySource `json:"sources"`
	Query          string         `json:"query"`
	Embedding      []float64      `json:"embedding"`
	EmbeddingModel string         `json:"embedding_model"`
}

func memoryState(m Memory, at time.Time) string {
	if m.Version == 0 {
		return "missing"
	}
	if m.Deleted {
		return "deleted"
	}
	if m.ExpiresAt != "" {
		expires, err := time.Parse(time.RFC3339Nano, m.ExpiresAt)
		if err != nil || !at.Before(expires) {
			return "expired"
		}
	}
	return "live"
}

func (s *Store) memoryReplay(ctx context.Context, q queryer, a actor, r Request, raw json.RawMessage) (json.RawMessage, *Fault) {
	if r.Operation != "memory.put" && r.Operation != "memory.patch" && r.Operation != "memory.delete" && r.Operation != "memory.index" {
		return raw, nil
	}
	var in memoryInput
	if err := strictJSON(r.Args, &in); err != nil {
		return nil, invalid(err)
	}
	scope, p := s.memoryScope(ctx, q, a, in.Namespace, true)
	if p != nil {
		return nil, p
	}
	if r.Operation == "memory.patch" {
		// Preserving and returning prior metadata is also a read. Receipts
		// cannot restore that visibility after the owner revokes read access.
		if _, p := s.memoryScope(ctx, q, a, in.Namespace, false); p != nil {
			return nil, p
		}
	}
	if r.Operation != "memory.put" && r.Operation != "memory.patch" {
		return raw, nil
	}
	var prior struct {
		Entry Memory `json:"entry"`
	}
	if err := json.Unmarshal(raw, &prior); err != nil {
		return nil, internal(err)
	}
	var current Memory
	if err := get(ctx, q, "memory", "memory_"+hash(scope+"\x00"+in.Key), &current); err != nil {
		return nil, fault("not_found", "memory receipt refers to unavailable content")
	}
	if memoryState(current, time.Now()) != "live" || current.Version != prior.Entry.Version {
		return nil, fault("not_found", "memory mutation was already recorded; its content has expired, been deleted or superseded")
	}
	return raw, nil
}

func (s *Store) memoryScope(ctx context.Context, q queryer, a actor, namespace string, write bool) (string, *Fault) {
	if namespace == "private" {
		if a.owner() {
			return "", fault("invalid", "owner must select an agent credential for private memory")
		}
		return "private:" + a.ID, nil
	}
	if !s.namespaceExists(ctx, q, namespace, true) || (!a.owner() && !s.namespace(ctx, q, a.ID, namespace, true, write)) {
		return "", forbidden()
	}
	return namespace, nil
}

func (s *Store) memoryOperation(ctx context.Context, q queryer, a actor, r Request) (any, *Fault) {
	var in memoryInput
	if r.Operation == "memory.patch" {
		// A value edit cannot silently reset lifetime or discard asserted sources.
		var patch struct {
			Namespace string `json:"namespace"`
			Key       string `json:"key"`
			Value     string `json:"value"`
			Version   int    `json:"version"`
		}
		if err := strictJSON(r.Args, &patch); err != nil {
			return nil, invalid(err)
		}
		in.Namespace, in.Key, in.Value, in.Version = patch.Namespace, patch.Key, patch.Value, patch.Version
	} else if err := strictJSON(r.Args, &in); err != nil {
		return nil, invalid(err)
	}
	if r.Operation == "memory.prune" {
		return s.pruneMemory(ctx, q, a, in)
	}
	write := r.Operation == "memory.put" || r.Operation == "memory.patch" || r.Operation == "memory.delete" || r.Operation == "memory.index"
	scope, problem := s.memoryScope(ctx, q, a, in.Namespace, write)
	if problem != nil {
		return nil, problem
	}
	if r.Operation == "memory.patch" {
		if _, p := s.memoryScope(ctx, q, a, in.Namespace, false); p != nil {
			return nil, p
		}
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 100
	}
	if r.Operation == "memory.list" || r.Operation == "memory.search" {
		if r.Operation == "memory.search" {
			return s.searchMemory(ctx, q, scope, in)
		}
		var after int64
		if in.After != "" {
			if !validKey(in.After) {
				return nil, fault("invalid", "invalid memory cursor")
			}
			if err := q.QueryRowContext(ctx, "SELECT seq FROM records WHERE kind='memory' AND scope=? AND id=?", scope, "memory_"+hash(scope+"\x00"+in.After)).Scan(&after); err != nil {
				return nil, fault("invalid", "invalid memory cursor")
			}
		}
		// Filter in SQL so an expired page cannot hide a later live entry.
		items, err := list[Memory](ctx, q, "memory", "AND scope=? AND seq>? AND COALESCE(json_extract(data,'$.deleted'),0)=0 AND (COALESCE(json_extract(data,'$.expires_at'),'')='' OR json_extract(data,'$.expires_unix_nano')>?) ORDER BY seq LIMIT ?", scope, after, time.Now().UnixNano(), in.Limit)
		if err != nil {
			return nil, internal(err)
		}
		page := []Memory{}
		cursor := in.After
		size := 0
		for _, m := range items {
			m.Embedding = nil
			raw, _ := json.Marshal(m)
			if size+len(raw) > maxWireBytes/2 {
				break
			}
			size += len(raw)
			page = append(page, m)
			cursor = m.Key
		}
		return map[string]any{"entries": page, "cursor": cursor}, nil
	}
	if !validKey(in.Key) {
		return nil, fault("invalid", "key must have 1–128 characters without line breaks")
	}
	id := "memory_" + hash(scope+"\x00"+in.Key)
	var m Memory
	err := get(ctx, q, "memory", id, &m)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, internal(err)
	}
	state := memoryState(m, time.Now())
	if r.Operation == "memory.head" {
		return map[string]any{"key": in.Key, "version": m.Version, "state": state, "sha256": m.SHA256, "expires_at": m.ExpiresAt}, nil
	}
	if r.Operation == "memory.get" {
		if state != "live" {
			return nil, fault("not_found", "memory entry is missing, expired or deleted; memory.head exposes its CAS version")
		}
		m.Embedding = nil
		return map[string]any{"entry": m}, nil
	}
	if in.Version != m.Version {
		return nil, fault("conflict", "memory version changed; use memory.head for expired/deleted entries")
	}
	if r.Operation == "memory.index" {
		if state != "live" {
			return nil, fault("not_found", "only live memory may be indexed")
		}
		if err := validateEmbedding(in.Embedding, in.EmbeddingModel); err != nil {
			return nil, invalid(err)
		}
		m.Embedding = append([]float64(nil), in.Embedding...)
		m.EmbeddingModel = in.EmbeddingModel
		if problem := s.memoryAllocation(ctx, q, id, m); problem != nil {
			return nil, problem
		}
		if err := save(ctx, q, "memory", id, a.ID, "", scope, m); err != nil {
			return nil, internal(err)
		}
		if err := event(ctx, q, a, "memory.index", id, map[string]any{"version": m.Version, "dimensions": len(m.Embedding), "model": m.EmbeddingModel}); err != nil {
			return nil, internal(err)
		}
		return map[string]any{"key": m.Key, "version": m.Version, "indexed": true, "dimensions": len(m.Embedding)}, nil
	}
	if r.Operation == "memory.delete" {
		if m.Version == 0 {
			return nil, fault("not_found", "memory entry not found")
		}
		m.Value = ""
		m.Tags = nil
		m.Sources = nil
		m.Embedding = nil
		m.EmbeddingModel = ""
		m.Deleted = true
		m.ExpiresAt = ""
		m.ExpiresUnixNano = 0
		m.PreviousSHA256 = m.SHA256
		m.SHA256 = ""
		m.Version++
		m.Author = a.ID
		m.UpdatedAt = now()
	} else if r.Operation == "memory.put" || r.Operation == "memory.patch" {
		if len(in.Value) > s.cfg.Limits.MaxMessageBytes {
			return nil, fault("invalid", "memory value exceeds max_message_bytes")
		}
		if r.Operation == "memory.patch" {
			if state != "live" {
				return nil, fault("not_found", "only live memory may be patched")
			}
			in.Tags, in.Sources, in.ExpiresAt = m.Tags, m.Sources, &m.ExpiresAt
		} else if problem := s.validateMemoryMetadata(ctx, q, a, in); problem != nil {
			return nil, problem
		}
		if in.ExpiresAt != nil && in.TTLSeconds != nil {
			return nil, fault("invalid", "choose expires_at or ttl_seconds")
		}
		policy := s.cfg.MemoryPolicy.Normalized()
		ttl := policy.DefaultTTLSeconds
		if in.TTLSeconds != nil {
			ttl = *in.TTLSeconds
		}
		if ttl < 0 || ttl > policy.MaxTTLSeconds {
			return nil, fault("invalid", "ttl_seconds exceeds owner memory policy")
		}
		created := m.CreatedAt
		if created == "" {
			created = now()
		}
		expires := ""
		var expiresNanos int64
		if in.ExpiresAt != nil {
			expires = *in.ExpiresAt
			if expires != "" {
				expiry, err := time.Parse(time.RFC3339Nano, expires)
				if err != nil {
					return nil, fault("invalid", "expires_at must be an RFC3339 timestamp")
				}
				remaining := time.Until(expiry)
				if remaining <= 0 {
					return nil, fault("expired", "memory expiry has elapsed")
				}
				if r.Operation != "memory.patch" && remaining > time.Duration(policy.MaxTTLSeconds)*time.Second {
					return nil, fault("invalid", "expires_at exceeds owner memory policy")
				}
				expiresNanos = expiry.UnixNano()
			}
		} else if ttl > 0 {
			expiry := time.Now().Add(time.Duration(ttl) * time.Second).UTC()
			expires = expiry.Format(time.RFC3339Nano)
			expiresNanos = expiry.UnixNano()
		}
		m = Memory{Namespace: in.Namespace, Key: in.Key, Value: in.Value, Version: m.Version + 1, Author: a.ID, CreatedAt: created, UpdatedAt: now(), ExpiresAt: expires, ExpiresUnixNano: expiresNanos, Tags: append([]string(nil), in.Tags...), Sources: append([]MemorySource(nil), in.Sources...), SHA256: hash(in.Value), PreviousSHA256: m.SHA256}
		m.Provenance, err = s.messageProvenance(ctx, q, a, id)
		if err != nil {
			return nil, internal(err)
		}
		if m.Provenance.ParentTaskID == "" {
			m.Provenance = nil
		}
		if problem := s.memoryAllocation(ctx, q, id, m); problem != nil {
			return nil, problem
		}
	} else {
		return nil, fault("unsupported", "unsupported memory operation")
	}
	if r.Operation == "memory.delete" {
		if err := redactMemoryReceipts(ctx, q, scope, m.Key, m.Version); err != nil {
			return nil, internal(err)
		}
	}
	// Absolute archive expiry and patch lifetime are checked again at the write
	// boundary, after provenance and allocation work, so neither can revive data.
	if (r.Operation == "memory.put" || r.Operation == "memory.patch") && m.ExpiresAt != "" && memoryState(m, time.Now()) != "live" {
		return nil, fault("expired", "memory expiry has elapsed")
	}
	if err := save(ctx, q, "memory", id, a.ID, "", scope, m); err != nil {
		return nil, internal(err)
	}
	if err := event(ctx, q, a, r.Operation, id, map[string]any{"namespace": scope, "version": m.Version, "sha256": m.SHA256, "expires_at": m.ExpiresAt}); err != nil {
		return nil, internal(err)
	}
	m.Embedding = nil
	return map[string]any{"entry": m}, nil
}

func (s *Store) validateMemoryMetadata(ctx context.Context, q queryer, a actor, in memoryInput) *Fault {
	if len(in.Tags) > 16 || len(in.Sources) > 16 {
		return fault("invalid", "memory permits at most 16 tags and 16 source references")
	}
	seen := map[string]bool{}
	for _, tag := range in.Tags {
		if len(tag) < 1 || len(tag) > 64 || strings.IndexFunc(tag, unicode.IsControl) >= 0 || seen[tag] {
			return fault("invalid", "tags must be distinct nonempty strings of at most 64 bytes without controls")
		}
		seen[tag] = true
	}
	for _, source := range in.Sources {
		if len(source.SHA256) != 0 && (len(source.SHA256) != 64 || strings.Trim(source.SHA256, "0123456789abcdef") != "") {
			return fault("invalid", "source sha256 must be lowercase hex")
		}
		if problem := validateImportedClaims(source); problem != nil {
			return problem
		}
		switch source.Kind {
		case "message":
			var m Message
			if get(ctx, q, "message", source.ID, &m) != nil || !s.canSeeMessage(ctx, q, a, m) {
				return fault("not_found", "source message is not visible")
			}
		case "task":
			t, err := loadTask(ctx, q, source.ID)
			if err != nil || !canSeeTask(a, t.Task) {
				return fault("not_found", "source task is not visible")
			}
		case "external":
			u, err := url.Parse(source.URI)
			if len(source.URI) > 2048 || err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
				return fault("invalid", "external source requires an http(s) URI without embedded credentials")
			}
		case "import":
			if len(source.ID) < 1 || len(source.ID) > 128 || source.SHA256 == "" {
				return fault("invalid", "import provenance requires source run id and value hash")
			}
		default:
			return fault("invalid", "unsupported memory source kind")
		}
	}
	return nil
}

func (s *Store) memoryAllocation(ctx context.Context, q queryer, id string, m Memory) *Fault {
	encoded, _ := json.Marshal(m)
	var used int64
	// Reserve bounded tombstone growth before admitting live content. Deletion
	// must remain possible at capacity even when author/version/time metadata
	// makes a small entry's tombstone larger than the original live record.
	if err := q.QueryRowContext(ctx, "SELECT COALESCE(SUM(length(CAST(data AS BLOB))+CASE WHEN COALESCE(json_extract(data,'$.deleted'),0)=0 THEN 128 ELSE 0 END),0) FROM records WHERE kind='memory' AND id<>?", id).Scan(&used); err != nil {
		return internal(err)
	}
	reserve := int64(0)
	if !m.Deleted {
		reserve = 128
	}
	if used+int64(len(encoded))+reserve > s.cfg.MemoryPolicy.Normalized().MaxTotalBytes {
		return fault("limit", "memory byte allocation exhausted; prune expired entries or archive this run")
	}
	return nil
}

func validateEmbedding(values []float64, model string) error {
	if len(values) < 1 || len(values) > 4096 || len(model) < 1 || len(model) > 128 || strings.IndexFunc(model, unicode.IsControl) >= 0 {
		return errors.New("embedding requires 1–4096 finite dimensions and an exact model identifier")
	}
	norm := 0.0
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e6 {
			return errors.New("embedding values must be finite and bounded")
		}
		norm += v * v
	}
	if norm == 0 {
		return errors.New("embedding must have nonzero norm")
	}
	return nil
}

type memoryHit struct {
	Entry  Memory  `json:"entry"`
	Score  float64 `json:"score"`
	Method string  `json:"method"`
}

func words(text string) map[string]bool {
	out := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		if len(out) < 256 {
			out[word] = true
		}
	}
	return out
}
func (s *Store) searchMemory(ctx context.Context, q queryer, scope string, in memoryInput) (any, *Fault) {
	if len(in.Query) > 4096 || len(in.Tags) > 16 {
		return nil, fault("invalid", "query or tags exceed search bounds")
	}
	if len(in.Embedding) > 0 {
		if err := validateEmbedding(in.Embedding, in.EmbeddingModel); err != nil {
			return nil, invalid(err)
		}
	} else if strings.TrimSpace(in.Query) == "" && len(in.Tags) == 0 {
		return nil, fault("invalid", "search requires query, tags or an embedding")
	}
	items, err := list[Memory](ctx, q, "memory", "AND scope=? AND COALESCE(json_extract(data,'$.deleted'),0)=0 AND (COALESCE(json_extract(data,'$.expires_at'),'')='' OR json_extract(data,'$.expires_unix_nano')>?) ORDER BY seq", scope, time.Now().UnixNano())
	if err != nil {
		return nil, internal(err)
	}
	query := words(in.Query)
	hits := []memoryHit{}
	for _, m := range items {
		match := true
		for _, tag := range in.Tags {
			if !contains(m.Tags, tag) {
				match = false
			}
		}
		if !match {
			continue
		}
		score := 0.0
		method := "lexical"
		if len(in.Embedding) > 0 {
			if m.EmbeddingModel != in.EmbeddingModel || len(m.Embedding) != len(in.Embedding) {
				continue
			}
			method = "cosine"
			dot, a, b := 0.0, 0.0, 0.0
			for i, v := range in.Embedding {
				dot += v * m.Embedding[i]
				a += v * v
				b += m.Embedding[i] * m.Embedding[i]
			}
			if a == 0 || b == 0 {
				continue
			}
			score = dot / math.Sqrt(a*b)
		} else {
			tokens := words(m.Key + " " + strings.Join(m.Tags, " ") + " " + m.Value)
			for word := range query {
				if tokens[word] {
					score++
				}
			}
			if len(query) > 0 {
				score /= float64(len(query))
				if score == 0 {
					continue
				}
			} else {
				score = 1
			}
		}
		m.Embedding = nil
		hits = append(hits, memoryHit{m, score, method})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Entry.Key < hits[j].Entry.Key
		}
		return hits[i].Score > hits[j].Score
	})
	if len(hits) > in.Limit {
		hits = hits[:in.Limit]
	}
	size := 0
	bounded := []memoryHit{}
	for _, hit := range hits {
		raw, _ := json.Marshal(hit)
		if size+len(raw) > maxWireBytes/2 {
			break
		}
		bounded = append(bounded, hit)
		size += len(raw)
	}
	return map[string]any{"hits": bounded, "scanned": len(items)}, nil
}

func (s *Store) pruneMemory(ctx context.Context, q queryer, a actor, in memoryInput) (any, *Fault) {
	if !a.owner() {
		return nil, forbidden()
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 100
	}
	rows, err := q.QueryContext(ctx, "SELECT id,scope,data FROM records WHERE kind='memory' AND COALESCE(json_extract(data,'$.deleted'),0)=0 AND COALESCE(json_extract(data,'$.expires_at'),'')<>'' AND json_extract(data,'$.expires_unix_nano')<=? ORDER BY seq LIMIT ?", time.Now().UnixNano(), in.Limit)
	if err != nil {
		return nil, internal(err)
	}
	type expired struct {
		ID, Scope string
		M         Memory
	}
	items := []expired{}
	for rows.Next() {
		var item expired
		var raw string
		if err := rows.Scan(&item.ID, &item.Scope, &raw); err != nil {
			rows.Close()
			return nil, internal(err)
		}
		if err := json.Unmarshal([]byte(raw), &item.M); err != nil {
			rows.Close()
			return nil, internal(err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, internal(err)
	}
	if err := rows.Close(); err != nil {
		return nil, internal(err)
	}
	for _, item := range items {
		m := item.M
		m.Value = ""
		m.Tags = nil
		m.Sources = nil
		m.Embedding = nil
		m.EmbeddingModel = ""
		m.Deleted = true
		m.Version++
		m.UpdatedAt = now()
		m.PreviousSHA256 = m.SHA256
		m.SHA256 = ""
		if err := save(ctx, q, "memory", item.ID, a.ID, "", item.Scope, m); err != nil {
			return nil, internal(err)
		}
		if err := redactMemoryReceipts(ctx, q, item.Scope, m.Key, m.Version); err != nil {
			return nil, internal(err)
		}
	}
	if err := event(ctx, q, a, "memory.prune", "", map[string]any{"pruned": len(items)}); err != nil {
		return nil, internal(err)
	}
	return map[string]any{"pruned": len(items)}, nil
}

func redactMemoryReceipts(ctx context.Context, q queryer, scope, key string, version int) error {
	namespace := scope
	condition := ""
	args := []any{}
	if strings.HasPrefix(scope, "private:") {
		namespace = "private"
		condition = " AND substr(actor,1,instr(actor,'/')-1)=?"
		args = append(args, strings.TrimPrefix(scope, "private:"))
	}
	values := []any{namespace, key, version}
	values = append(values, args...)
	_, err := q.ExecContext(ctx, `UPDATE requests SET response=json_set(response,'$.entry.value','','$.entry.embedding',json('null'),'$.entry.tags',json('[]'),'$.entry.sources',json('[]'),'$.content_unavailable',json('true')) WHERE json_extract(response,'$.entry.namespace')=? AND json_extract(response,'$.entry.key')=? AND json_extract(response,'$.entry.version')<=?`+condition, values...)
	return err
}
