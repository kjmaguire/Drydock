package core

import (
	"context"
	"database/sql"
	"strings"

	"github.com/kjmaguire/drydock/internal/connections"
)

const gateKey = "gate."

// loadGateAcks reads the acknowledgements the human gave to Connection gates (for example the Antigravity terms notice).
func (c *Core) loadGateAcks(ctx context.Context) (map[string]bool, error) {
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT key FROM settings WHERE key LIKE 'gate.%' AND value='1'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[strings.TrimPrefix(k, gateKey)] = true
	}
	return out, rows.Err()
}

// AcknowledgeGate records that the human accepted a gate's notice. Only a human action reaches this method.
func (c *Core) AcknowledgeGate(ctx context.Context, gateID string) error {
	err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,'1') ON CONFLICT(key) DO UPDATE SET value='1'`, gateKey+gateID)
		return err
	})
	if err != nil {
		return err
	}
	c.Connections.Acknowledge(gateID)
	c.publish(ctx, "connection.gate_acknowledged", "", map[string]any{"gate": gateID, "by": c.Human()})
	return nil
}

// TestConnection runs the Test connection steps for one adapter. It can start the real CLI and may cost a few cents.
func (c *Core) TestConnection(ctx context.Context, adapterID string) (connections.Report, error) {
	rep, err := c.Connections.TestConnection(ctx, adapterID, connections.TestOptions{})
	if err == nil {
		c.Connections.RecordTest(adapterID, rep)
	}
	return rep, err
}
