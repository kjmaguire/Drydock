package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents"

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
	// a person's own CLI is acknowledged for its manifest file as it is now (decision P117, ext_customack.go)
	sum, custom, err := c.acknowledgeCustom(ctx, gateID)
	if err != nil {
		return err
	}
	err = c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,'1') ON CONFLICT(key) DO UPDATE SET value='1'`, gateKey+gateID); err != nil {
			return err
		}
		if custom {
			_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
				customAckKey+strings.TrimPrefix(gateID, "custom-"), sum)
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	c.Connections.Acknowledge(gateID)
	data := map[string]any{"gate": gateID, "by": c.Human()}
	if custom {
		data["manifest"] = shortSum(sum)
	}
	c.publish(ctx, "connection.gate_acknowledged", "", data)
	return nil
}

// TestConnection runs the Test connection steps for one adapter. With prompt it also sends one tiny prompt on the adapter's
// cheapest model (it uses the human's account: a fraction of a cent); without it nothing that costs tokens runs. The outcome
// is kept (it survives a restart): a pass clears "version untested" for exactly that CLI version, a failure degrades it.
func (c *Core) TestConnection(ctx context.Context, adapterID string, prompt bool) (connections.Report, error) {
	return c.TestConnectionIn(ctx, adapterID, "", prompt)
}

// TestConnectionIn is TestConnection for the install inside the WSL distro distro ("" is the native install). Each install
// keeps its own result (decision P41).
func (c *Core) TestConnectionIn(ctx context.Context, adapterID, distro string, prompt bool) (connections.Report, error) {
	o := connections.TestOptions{Distro: distro}
	if prompt {
		o.Prompt, o.Model = true, cheapestModel(adapterID, c.adapters[adapterID])
	}
	rep, err := c.Connections.TestConnection(ctx, adapterID, o)
	if err == nil {
		key := rep.Key
		if key == "" {
			key = adapterID
		}
		c.Connections.RecordTest(key, rep)
		out := connections.TestOutcome{OK: rep.OK, Reason: rep.FailureReason(), Version: rep.CLIVersion}
		if b, jerr := json.Marshal(out); jerr == nil {
			if werr := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, testKey+key, string(b))
				return err
			}); werr != nil {
				c.logf("keep the Test connection result: %v", werr)
			}
		}
		if rep.OK {
			if _, derr := c.DB.Writer().ExecContext(ctx, `DELETE FROM settings WHERE key=?`, driftKey+adapterID); derr != nil {
				c.logf("clear the recorded drift: %v", derr)
			}
		}
	}
	return rep, err
}

const testKey = "connection.test."

// loadTestOutcomes reads the kept Test connection results, by adapter id.
func (c *Core) loadTestOutcomes(ctx context.Context) map[string]connections.TestOutcome {
	out := map[string]connections.TestOutcome{}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT key, value FROM settings WHERE key LIKE ?`, testKey+"%")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		var o connections.TestOutcome
		if rows.Scan(&k, &v) == nil && json.Unmarshal([]byte(v), &o) == nil {
			out[strings.TrimPrefix(k, testKey)] = o
		}
	}
	return out
}

// cheapestModel is the model Test connection's tiny prompt uses: Claude's haiku, else the first model the adapter lists ("" when
// it lists none: the prompt step then says so).
func cheapestModel(adapterID string, ad agents.Adapter) string {
	if adapterID == "claude" {
		return "haiku"
	}
	if ad != nil {
		if ms := ad.Capabilities().Models; len(ms) > 0 {
			return ms[0]
		}
	}
	return ""
}
