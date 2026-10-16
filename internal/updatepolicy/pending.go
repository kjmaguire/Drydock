package updatepolicy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
)

// A pre-update backup is kept out of pruning until the new version has run healthy. Nothing ever said so: Prepare's result was
// thrown away, Confirm had no caller, and every update left a database-sized backup behind for good (audit 2026-10-16, M13, L5).
// The update that is waiting to be proven is remembered in settings, so the next start of the app, which is the new version,
// can end the exemption.

const pendingKey = "update.pending"

// Pending is an update that has been staged and not yet proven.
type Pending struct {
	Prepared Prepared `json:"prepared"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	At       int64    `json:"at"` // unix seconds
}

// MaxPending is how long an update that never proves itself keeps its backup out of pruning: the person had that long to roll
// back, and a backup should not outlive the question.
const MaxPending = 30 * 24 * time.Hour

// Record remembers a staged update. An earlier one that was never proven is superseded: its backup is released, because a newer
// pre-update backup exists.
func Record(ctx context.Context, db *storage.DB, p Prepared, from, to string, now time.Time) error {
	if prev, ok := loadPending(ctx, db); ok {
		_ = Confirm(prev.Prepared)
	}
	b, _ := json.Marshal(Pending{Prepared: p, From: from, To: to, At: now.Unix()})
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, pendingKey, string(b))
		return err
	})
}

func loadPending(ctx context.Context, db *storage.DB) (Pending, bool) {
	var raw string
	if db.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, pendingKey).Scan(&raw) != nil {
		return Pending{}, false
	}
	var p Pending
	if json.Unmarshal([]byte(raw), &p) != nil || p.Prepared.Backup.Path == "" {
		return Pending{}, false
	}
	return p, true
}

// ConfirmHealthy ends the backup exemption of a pending update once the running version is the one it staged (the caller runs it
// after the app has been healthy for a while), or after MaxPending whatever is running. It reports whether a backup was released.
// An update that did not take (the old version is still running) keeps its backup, for the rollback.
func ConfirmHealthy(ctx context.Context, db *storage.DB, running string, now time.Time) (bool, error) {
	p, ok := loadPending(ctx, db)
	if !ok {
		return false, nil
	}
	took := false
	if c, err := CompareVersions(running, p.To); err == nil && c >= 0 {
		took = true
	}
	if !took && now.Sub(time.Unix(p.At, 0)) < MaxPending {
		return false, nil
	}
	if err := Confirm(p.Prepared); err != nil {
		return false, fmt.Errorf("release the pre-update backup: %w", err)
	}
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, pendingKey)
		return err
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return true, err
	}
	return true, nil
}
