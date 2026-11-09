package core

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Where and how big the person left the Drydock window (G8). The Windows shell (app/window_windows.go) keeps it as it changes and
// when the window closes, and opens the next start there. Only the window's place is kept: nothing of what it shows.

const settingWindow = "ui.window"

// WindowPlacement is the window's bounds in device-independent pixels (the screen's coordinates, so a second monitor left of the
// main one has negative X), as the window last stood un-maximised, and whether it was maximised.
type WindowPlacement struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximised bool `json:"maximised"`
}

// WindowPlacement is the kept placement; false when none is kept, or what is kept does not read or has no size.
func (c *Core) WindowPlacement(ctx context.Context) (WindowPlacement, bool) {
	var v string
	if err := c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, settingWindow).Scan(&v); err != nil {
		return WindowPlacement{}, false
	}
	var p WindowPlacement
	if json.Unmarshal([]byte(v), &p) != nil || p.Width <= 0 || p.Height <= 0 {
		return WindowPlacement{}, false
	}
	return p, true
}

// SetWindowPlacement keeps the placement for the next start. A placement without a size is not kept.
func (c *Core) SetWindowPlacement(ctx context.Context, p WindowPlacement) error {
	if p.Width <= 0 || p.Height <= 0 {
		return nil
	}
	v, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingWindow, string(v))
		return err
	})
}
