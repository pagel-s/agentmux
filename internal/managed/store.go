// Package managed implements an opt-in local coordination service. It is not
// the legacy tmux socket API: all agent operations authenticate independently.
package managed

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentmux/internal/managedconfig"
	_ "modernc.org/sqlite"
)

const ownerID = "$owner"

type Credential struct {
	AgentID    string `json:"agent_id"`
	InstanceID string `json:"instance_id"`
	Generation int    `json:"generation"`
	Socket     string `json:"socket"`
	Token      string `json:"token"`
}

type actor struct {
	ID, Instance   string
	Generation     int
	researchRunner bool
}

func (a actor) owner() bool { return a.ID == ownerID }

type Store struct {
	db                  *sql.DB
	cfg                 managedconfig.Config
	dir                 string
	mu                  sync.Mutex
	closed              bool
	stagedPaths         []string
	researchHeartbeatAt string
}

type queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const schema = `
CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS credentials (
 actor TEXT PRIMARY KEY, instance TEXT NOT NULL, generation INTEGER NOT NULL,
 token_hash TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS records (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL,
 id TEXT NOT NULL UNIQUE, actor TEXT NOT NULL DEFAULT '',
 target TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT '', data TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS records_kind_seq ON records(kind,seq);
CREATE INDEX IF NOT EXISTS records_target ON records(kind,target);
CREATE INDEX IF NOT EXISTS records_scope ON records(kind,scope);
CREATE TABLE IF NOT EXISTS requests (
 actor TEXT NOT NULL, request_id TEXT NOT NULL, digest TEXT NOT NULL,
 response TEXT NOT NULL, PRIMARY KEY(actor,request_id)
);
`

func SocketPath(dir string) string { return filepath.Join(dir, "broker.sock") }
func CredentialPath(dir, id string) string {
	if id == ownerID || id == "" {
		return filepath.Join(dir, "owner.json")
	}
	return filepath.Join(dir, "credentials", id+".json")
}

// Init creates a new private state directory. Existing state is never replaced.
// Only credential hashes enter SQLite; token files are mode 0600.
func Init(dir string, cfg managedconfig.Config) (*Store, error) {
	cfg = cfg.Normalized()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if info, e := os.Lstat(abs); e == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("state must be a dedicated directory")
		}
		entries, e := os.ReadDir(abs)
		if e != nil || len(entries) != 0 {
			return nil, errors.New("state directory already exists and is not empty")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	if err = os.Chmod(abs, 0700); err != nil {
		return nil, err
	}
	if err = os.Mkdir(filepath.Join(abs, "credentials"), 0700); err != nil {
		return nil, err
	}
	if err = os.Mkdir(filepath.Join(abs, "workspaces"), 0700); err != nil {
		return nil, err
	}
	for id, a := range cfg.Agents {
		if a.Workdir == "" {
			a.Workdir = filepath.Join(abs, "workspaces", id)
		}
		if !filepath.IsAbs(a.Workdir) {
			return nil, fmt.Errorf("agent %s workdir must be resolved to an absolute path", id)
		}
		if err = os.MkdirAll(a.Workdir, 0700); err != nil {
			return nil, err
		}
		cfg.Agents[id] = a
	}
	db, err := openDB(abs)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, cfg: cfg, dir: abs}
	failed := true
	defer func() {
		if failed {
			db.Close()
		}
	}()
	if _, err = db.Exec(schema); err != nil {
		return nil, err
	}
	data, _ := json.Marshal(cfg)
	if _, err = db.Exec("INSERT INTO metadata(key,value) VALUES('config',?),('schema','1'),('created_at',?)", string(data), now()); err != nil {
		return nil, err
	}
	ids := []string{ownerID}
	for id := range cfg.Agents {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, err = s.rotate(id); err != nil {
			return nil, err
		}
		if id != ownerID && cfg.Agents[id].Autonomy.Enabled {
			state, e := loadAutonomy(context.Background(), s.db, id)
			if e != nil {
				return nil, e
			}
			if e = saveAutonomy(context.Background(), s.db, state); e != nil {
				return nil, e
			}
		}
	}
	failed = false
	return s, nil
}

func openDB(dir string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "state.sqlite")}
	db, err := sql.Open("sqlite", u.String()+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if err = os.Chmod(filepath.Join(dir, "state.sqlite"), 0600); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func Open(dir string) (*Store, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("state directory must be private (mode 0700)")
	}
	if _, err = os.Stat(filepath.Join(abs, "state.sqlite")); err != nil {
		return nil, err
	}
	db, err := openDB(abs)
	if err != nil {
		return nil, err
	}
	var raw, version string
	if err = db.QueryRow("SELECT value FROM metadata WHERE key='schema'").Scan(&version); err != nil || version != "1" {
		db.Close()
		return nil, errors.New("unsupported managed state schema")
	}
	if err = db.QueryRow("SELECT value FROM metadata WHERE key='config'").Scan(&raw); err != nil {
		db.Close()
		return nil, err
	}
	var cfg managedconfig.Config
	if err = json.Unmarshal([]byte(raw), &cfg); err != nil {
		db.Close()
		return nil, err
	}
	if err = cfg.Validate(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, cfg: cfg.Normalized(), dir: abs}, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.db.Close()
}

func (s *Store) Config() managedconfig.Config {
	// Return an independent copy: policy cannot be mutated through this accessor.
	data, _ := json.Marshal(s.cfg)
	var c managedconfig.Config
	_ = json.Unmarshal(data, &c)
	return c
}

// Rotate fences previous credentials for this logical agent. This is an owner
// filesystem operation, not an agent-callable API. Restart does not rotate keys.
func (s *Store) Rotate(id string) (Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != ownerID {
		if !s.knownAgent(context.Background(), s.db, id) {
			return Credential{}, errors.New("unknown agent")
		}
	}
	return s.rotate(id)
}

func (s *Store) rotate(id string) (Credential, error) {
	var generation int
	err := s.db.QueryRow("SELECT generation FROM credentials WHERE actor=?", id).Scan(&generation)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Credential{}, err
	}
	c := Credential{AgentID: id, InstanceID: randomID("instance"), Generation: generation + 1, Socket: SocketPath(s.dir), Token: randomID("token")}
	data, _ := json.MarshalIndent(c, "", "  ")
	path := CredentialPath(s.dir, id)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".credential-")
	if err != nil {
		return Credential{}, err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return Credential{}, err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return Credential{}, err
	}
	if err = tmp.Close(); err != nil {
		return Credential{}, err
	}
	// Publish the new file before activating its hash: interruption may briefly
	// reject the new token, but can never leave a revoked token working again.
	if err = os.Rename(tmp.Name(), path); err != nil {
		return Credential{}, err
	}
	_, err = s.db.Exec("INSERT INTO credentials(actor,instance,generation,token_hash) VALUES(?,?,?,?) ON CONFLICT(actor) DO UPDATE SET instance=excluded.instance,generation=excluded.generation,token_hash=excluded.token_hash", id, c.InstanceID, c.Generation, hash(c.Token))
	return c, err
}

func LoadCredential(path string) (Credential, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, err
	}
	var c Credential
	if err = strictJSON(data, &c); err != nil {
		return c, err
	}
	if c.Token == "" || c.Socket == "" {
		return c, errors.New("invalid credential file")
	}
	return c, nil
}

func (s *Store) authenticate(ctx context.Context, token string) (actor, error) {
	return authenticateWith(ctx, s.db, token)
}
func authenticateWith(ctx context.Context, q queryer, token string) (actor, error) {
	var a actor
	if token == "" || len(token) > 256 {
		return a, errors.New("invalid credential")
	}
	err := q.QueryRowContext(ctx, "SELECT actor,instance,generation FROM credentials WHERE token_hash=?", hash(token)).Scan(&a.ID, &a.Instance, &a.Generation)
	if err != nil {
		return actor{}, errors.New("invalid credential")
	}
	return a, nil
}

func randomID(prefix string) string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("secure randomness unavailable")
	}
	return prefix + "_" + hex.EncodeToString(b)
}
func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func now() string          { return time.Now().UTC().Format(time.RFC3339Nano) }

func strictJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

func save(ctx context.Context, q queryer, kind, id, actor, target, scope string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "INSERT INTO records(kind,id,actor,target,scope,data) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", kind, id, actor, target, scope, string(data))
	return err
}
func get(ctx context.Context, q queryer, kind, id string, out any) error {
	var data string
	if err := q.QueryRowContext(ctx, "SELECT data FROM records WHERE kind=? AND id=?", kind, id).Scan(&data); err != nil {
		return err
	}
	return json.Unmarshal([]byte(data), out)
}
func list[T any](ctx context.Context, q queryer, kind, condition string, args ...any) ([]T, error) {
	values := append([]any{kind}, args...)
	rows, err := q.QueryContext(ctx, "SELECT data FROM records WHERE kind=? "+condition, values...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []T{}
	for rows.Next() {
		var raw string
		var item T
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func count(ctx context.Context, q queryer, kind string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind=?", kind).Scan(&n)
	return n, err
}

// Reserve a bounded number of remaining lifecycle receipts/events per task.
// Retrying uncertain work must acquire fresh capacity; terminal transitions
// release their reservation. This protects cancellation at the storage limit.
func lifecycleReserve(ctx context.Context, q queryer) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE
 WHEN json_extract(data,'$.state')='proposed' THEN 3
 WHEN json_extract(data,'$.state')='running' THEN 2
 WHEN json_extract(data,'$.state')='submitted' THEN 1
 WHEN json_extract(data,'$.state')='cancelled' AND json_extract(data,'$.delivery') IN ('cancel_requested','cancel_uncertain') THEN 1
 ELSE 0 END + CASE WHEN (
 EXISTS (SELECT 1 FROM metadata WHERE key='config' AND json_extract(value,'$.agents.'||json_quote(records.target)||'.conversation')='persistent')
 OR EXISTS (SELECT 1 FROM records agent WHERE agent.kind='agent' AND agent.target=records.target AND json_extract(agent.data,'$.config.conversation')='persistent')
 ) AND NOT EXISTS (SELECT 1 FROM records c WHERE c.kind='conversation' AND json_extract(c.data,'$.task_id')=records.id)
 THEN CASE json_extract(data,'$.state') WHEN 'proposed' THEN 3 WHEN 'running' THEN 3 ELSE 0 END
 ELSE 0 END),0) FROM records WHERE kind='task'`).Scan(&n)
	if err != nil {
		return n, err
	}
	var extra int
	// An interrupted native turn retains one owner reset event/receipt slot.
	// Reset releases this reservation; it never renews general storage budgets.
	err = q.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind='conversation' AND json_extract(data,'$.state') IN ('busy','blocked')").Scan(&extra)
	if err != nil {
		return 0, err
	}
	n += extra
	err = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN EXISTS(SELECT 1 FROM records WHERE kind='agent' AND target=credentials.actor) THEN 1 ELSE 2 END),0) FROM credentials WHERE actor<>'$owner'`).Scan(&extra)
	if err != nil {
		return 0, err
	}
	n += extra
	err = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(1+json_array_length(data,'$.members')+json_array_length(data,'$.invited')),0) FROM records WHERE kind='team' AND json_extract(data,'$.state')='active'`).Scan(&extra)
	if err != nil {
		return 0, err
	}
	n += extra
	// Dispatch controls retain capacity for pause and stop even after ordinary
	// work fills storage. Resume is a fresh allocation, never a safety fallback.
	err = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE json_extract(data,'$.mode') WHEN 'running' THEN 2 WHEN 'paused' THEN 1 ELSE 0 END),0) FROM records WHERE kind='autonomy'`).Scan(&extra)
	if err != nil {
		return 0, err
	}
	n += extra
	// An active study retains slots for an audited owner stop and the runner's
	// terminal checkpoint. Safety controls do not renew ordinary work budgets.
	err = q.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE json_extract(data,'$.control.mode') WHEN 'stopped' THEN 1 ELSE 3 END),0) FROM records WHERE kind='research' AND json_extract(data,'$.progress.state') NOT IN ('completed','interrupted','stopped')`).Scan(&extra)
	if err != nil {
		return 0, err
	}
	n += extra
	// Every retained expiring entry can be pruned even if ordinary writes fill
	// the run. A batch releases at least as much capacity as its cleanup receipt.
	err = q.QueryRowContext(ctx, "SELECT count(*) FROM records WHERE kind='memory' AND COALESCE(json_extract(data,'$.deleted'),0)=0 AND COALESCE(json_extract(data,'$.expires_at'),'')<>''").Scan(&extra)
	return n + extra, err
}

type Event struct {
	ID       string         `json:"id"`
	At       string         `json:"at"`
	Type     string         `json:"type"`
	Actor    string         `json:"actor"`
	EntityID string         `json:"entity_id,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
}

func event(ctx context.Context, q queryer, a actor, kind, id string, details map[string]any) error {
	e := Event{ID: randomID("evt"), At: now(), Type: kind, Actor: a.ID, EntityID: id, Details: details}
	return save(ctx, q, "event", e.ID, a.ID, "", "owner", e)
}

type Message struct {
	ID         string      `json:"id"`
	Kind       string      `json:"kind"`
	From       string      `json:"from"`
	To         []string    `json:"to,omitempty"`
	Board      string      `json:"board,omitempty"`
	Text       string      `json:"text"`
	CreatedAt  string      `json:"created_at"`
	TaskID     string      `json:"task_id,omitempty"`
	Provenance *Provenance `json:"provenance,omitempty"`
}
type Task struct {
	ID        string          `json:"id"`
	MessageID string          `json:"message_id"`
	From      string          `json:"from"`
	To        string          `json:"to"`
	Text      string          `json:"text"`
	State     string          `json:"state"`
	Delivery  string          `json:"delivery"`
	Version   int             `json:"version"`
	Result    string          `json:"result,omitempty"`
	Usage     json.RawMessage `json:"usage,omitempty"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	Trace     *TraceSummary   `json:"trace,omitempty"`
	Origin    *TaskOrigin     `json:"origin,omitempty"`
}

// Persist lease fields separately from public task JSON. They are never part
// of status, audit exports, task lists, or another agent's task view.
type taskRecord struct {
	Task
	BudgetCharge *BudgetCharge `json:"budget_charge,omitempty"`
	LeaseToken   string        `json:"lease_token,omitempty"`
	Instance     string        `json:"instance,omitempty"`
	Expires      int64         `json:"expires,omitempty"`
}

func loadTask(ctx context.Context, q queryer, id string) (taskRecord, error) {
	var t taskRecord
	err := get(ctx, q, "task", id, &t)
	return t, err
}
func saveTask(ctx context.Context, q queryer, t taskRecord) error {
	return save(ctx, q, "task", t.ID, t.From, t.To, "", t)
}

type Memory struct {
	Namespace       string         `json:"namespace"`
	Key             string         `json:"key"`
	Value           string         `json:"value"`
	Version         int            `json:"version"`
	Author          string         `json:"author"`
	UpdatedAt       string         `json:"updated_at"`
	CreatedAt       string         `json:"created_at,omitempty"`
	ExpiresAt       string         `json:"expires_at,omitempty"`
	ExpiresUnixNano int64          `json:"expires_unix_nano,omitempty"`
	Deleted         bool           `json:"deleted,omitempty"`
	Tags            []string       `json:"tags,omitempty"`
	Sources         []MemorySource `json:"sources,omitempty"`
	SHA256          string         `json:"sha256,omitempty"`
	PreviousSHA256  string         `json:"previous_sha256,omitempty"`
	Embedding       []float64      `json:"embedding,omitempty"`
	EmbeddingModel  string         `json:"embedding_model,omitempty"`
}

// RecoverInflight never blindly repeats a potentially accepted harness turn.
// An operator must explicitly resolve uncertain delivery before another claim.
func (s *Store) RecoverInflight(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tasks, err := list[taskRecord](ctx, tx, "task", "AND json_extract(data,'$.delivery') IN ('dispatching','cancel_requested')")
	if err != nil {
		return err
	}
	for _, t := range tasks {
		t.Delivery = "uncertain"
		if t.State == "cancelled" {
			t.Delivery = "cancel_uncertain"
		}
		t.Version++
		t.UpdatedAt = now()
		t.LeaseToken = ""
		t.Instance = ""
		t.Expires = 0
		if err = saveTask(ctx, tx, t); err != nil {
			return err
		}
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM records").Scan(&n); err != nil {
			return err
		}
		reserved, err := lifecycleReserve(ctx, tx)
		if err != nil {
			return err
		}
		if n+reserved < s.cfg.Limits.MaxRecords {
			if err = event(ctx, tx, actor{ID: ownerID}, "delivery."+t.Delivery, t.ID, nil); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func contains(items []string, value string) bool {
	for _, v := range items {
		if v == value {
			return true
		}
	}
	return false
}
func (s *Store) canSeeMessage(ctx context.Context, q queryer, a actor, m Message) bool {
	if a.owner() || m.From == a.ID || contains(m.To, a.ID) {
		return true
	}
	if m.Board == "" || !s.namespace(ctx, q, a.ID, m.Board, false, false) {
		return false
	}
	var seq int64
	if err := q.QueryRowContext(ctx, "SELECT seq FROM records WHERE id=?", m.ID).Scan(&seq); err != nil {
		return false
	}
	return seq > s.boardFloor(ctx, q, a.ID, m.Board)
}
func canSeeTask(a actor, t Task) bool { return a.owner() || t.From == a.ID || t.To == a.ID }
func validKey(s string) bool          { return s != "" && len(s) <= 128 && !strings.ContainsAny(s, "\x00\r\n") }
