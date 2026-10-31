package managed

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Lifecycle state and its recovery digest are written in the same transaction.
// An absent or damaged state must not reset a pause, budget, or native-history
// fence to its initial defaults. Legacy intact records remain readable.
func checkStateRecoveryDigest(ctx context.Context, q queryer, kind, id string, encoded []byte) error {
	var digest string
	err := q.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", "state_digest:"+kind+":"+id).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		var initialized string
		markerErr := q.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", "state_initialized:"+kind+":"+id).Scan(&initialized)
		if markerErr == nil {
			return errors.New("persisted " + kind + " recovery digest is missing; restore valid owner state")
		}
		if !errors.Is(markerErr, sql.ErrNoRows) {
			return markerErr
		}
		return nil
	}
	if err != nil {
		return err
	}
	if len(encoded) == 0 || digest != hash(string(encoded)) {
		return errors.New("persisted " + kind + " state is missing or damaged; restore valid owner state")
	}
	return nil
}

func saveStateRecoveryDigest(ctx context.Context, q queryer, kind, id string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, "INSERT INTO metadata(key,value) VALUES(?,?),(?,'1') ON CONFLICT(key) DO UPDATE SET value=excluded.value", "state_digest:"+kind+":"+id, hash(string(encoded)), "state_initialized:"+kind+":"+id)
	return err
}
