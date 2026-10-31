package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Credential rotation uses a separate Store in the CLI. Authentication inside
// the write transaction must therefore establish the SQLite snapshot that
// fences any later mutation; the process-local Store mutex is insufficient.
func TestCredentialRotationFencesPreviouslyAuthenticatedWriteSnapshot(t *testing.T) {
	store := testStore(t, testConfig())
	external, err := Open(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	old := credential(t, store, "alice")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	authenticated, err := authenticateWith(ctx, tx, old.Token)
	if err != nil || authenticated.Instance != old.InstanceID {
		t.Fatalf("initial snapshot did not authenticate the old instance: %v", err)
	}

	// No timing assumptions or goroutines: the independent writer commits
	// rotation strictly after the old transaction's authentication read.
	current, err := external.Rotate("alice")
	if err != nil {
		t.Fatalf("rotation could not commit beside the read snapshot: %v", err)
	}
	if current.Generation != old.Generation+1 || current.InstanceID == old.InstanceID {
		t.Fatal("rotation did not advance the credential instance")
	}
	stale := Message{ID: "rotation-stale-message", Kind: "board", From: authenticated.ID, Board: "team", Text: "must not be published", CreatedAt: now()}
	err = save(ctx, tx, "message", stale.ID, authenticated.ID, "", "board:team", stale)
	if err == nil {
		if commitErr := tx.Commit(); commitErr == nil {
			t.Fatal("revoked credential snapshot published a mutation after rotation")
		}
	} else {
		var snapshotError *sqlite.Error
		if !errors.As(err, &snapshotError) || snapshotError.Code() != sqlite3.SQLITE_BUSY_SNAPSHOT {
			t.Fatalf("expected stale WAL snapshot to reject writer promotion, got %v", err)
		}
	}
	_ = tx.Rollback()
	var unused Message
	if err := get(ctx, store.db, "message", stale.ID, &unused); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale transaction left a durable effect: %v", err)
	}

	args := json.RawMessage(`{"board":"team","text":"authorized after rotation"}`)
	if _, problem := store.Call(ctx, old.Token, Request{Operation: "board.post", RequestID: "rotation-old-write", Args: args}); problem == nil || problem.Code != "unauthorized" {
		t.Fatalf("old token was not fenced on a new call: %v", problem)
	}
	if _, problem := store.Call(ctx, current.Token, Request{Operation: "board.post", RequestID: "rotation-new-write", Args: args}); problem != nil {
		t.Fatalf("new instance could not write: %v", problem)
	}
	if count, err := count(ctx, store.db, "message"); err != nil || count != 1 {
		t.Fatalf("expected only the new instance's mutation, got count=%d error=%v", count, err)
	}
}
