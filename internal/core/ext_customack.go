package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
)

// The acknowledgement of a person's own CLI is bound to the bytes of its manifest file (decision P117). The file says which
// program runs, with which arguments, and how its output is read, so a change to it is a different program as far as the
// acknowledgement goes. The SHA-256 of the file a person acknowledged is kept beside the acknowledgement; whenever the file is
// looked at again (the Agents page lists the CLIs, a Connection is checked, a person acknowledges, any session of the CLI starts)
// a file whose bytes changed is loaded again if it still validates, and the acknowledgement is taken back until a person gives
// it again. A file that no longer loads, or is gone, takes it back too. An acknowledgement given before this was kept has no
// hash and is asked again once.

// customAckKey + id holds the SHA-256 of the manifest file a person acknowledged.
const customAckKey = "customcli.ack_sha256."

func (c *Core) setCustomLoaded(id, sum string) {
	c.customMu.Lock()
	defer c.customMu.Unlock()
	if c.customLoaded == nil {
		c.customLoaded = map[string]string{}
	}
	if sum == "" {
		delete(c.customLoaded, id)
		return
	}
	c.customLoaded[id] = sum
}

func (c *Core) customLoadedSums() map[string]string {
	c.customMu.Lock()
	defer c.customMu.Unlock()
	out := make(map[string]string, len(c.customLoaded))
	for k, v := range c.customLoaded {
		out[k] = v
	}
	return out
}

// customOnDisk is what recheckCustom found for one CLI: the hash of its file when it loads, else why it does not.
type customOnDisk struct {
	Sum   string
	Error string
}

// recheckCustom compares the manifest file of every person's CLI the core loaded (only: just that one) with the bytes it was
// built from and the bytes a person acknowledged. A changed file that still validates is loaded again, so what a person is asked
// to acknowledge is what runs. It returns what it found per CLI.
func (c *Core) recheckCustom(ctx context.Context, only string) map[string]customOnDisk {
	loaded := c.customLoadedSums()
	if only != "" {
		if _, ok := loaded[only]; !ok {
			return nil
		}
	}
	ms, files := manifests.LoadUserCustom(c.customDir())
	byID := map[string]*manifests.Manifest{}
	for _, m := range ms {
		byID[m.Meta.ID] = m
	}
	disk := map[string]customOnDisk{}
	for _, f := range files {
		disk[f.ID] = customOnDisk{Sum: f.SHA256, Error: f.Error}
	}
	out := map[string]customOnDisk{}
	for id, sum := range loaded {
		if only != "" && id != only {
			continue
		}
		d, ok := disk[id]
		if !ok {
			d = customOnDisk{Error: "the manifest file is gone"}
		}
		if d.Error == "" && d.Sum != sum {
			if err := c.reloadCustom(byID[id]); err != nil {
				d = customOnDisk{Error: err.Error()}
			} else {
				c.setCustomLoaded(id, d.Sum)
				c.logf("custom CLI %s: its manifest file changed on disk and was loaded again", id)
			}
		}
		out[id] = d
		c.reconcileCustomAck(ctx, id, d)
	}
	return out
}

// reloadCustom replaces the registered adapter of a person's CLI with one built from m (the file as it is now). Sessions that
// run keep the adapter they started with.
func (c *Core) reloadCustom(m *manifests.Manifest) error {
	if m == nil {
		return errors.New("the manifest file could not be read again")
	}
	reg := c.Connections.Registry
	old, _ := reg.Set.Get(m.Meta.ID)
	if err := reg.RemoveCustom(m.Meta.ID, builtinAdapter); err != nil {
		return err
	}
	a, err := reg.AddCustom(m, builtinAdapter)
	if err != nil {
		if old != nil {
			if back, berr := reg.AddCustom(old, builtinAdapter); berr == nil {
				c.RegisterAdapter(old.Meta.ID, back)
			}
		}
		return err
	}
	c.RegisterAdapter(m.Meta.ID, a)
	return nil
}

// reconcileCustomAck takes the acknowledgement of a person's CLI back when the file it was given for is not the file there now.
func (c *Core) reconcileCustomAck(ctx context.Context, id string, d customOnDisk) {
	gate := manifests.UserGateID(id)
	var acked, ackSum string
	_ = c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, gateKey+gate).Scan(&acked)
	_ = c.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, customAckKey+id).Scan(&ackSum)
	same := d.Error == "" && ackSum != "" && ackSum == d.Sum
	switch {
	case acked == "1" && !same:
		if err := c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, gateKey+gate)
			return err
		}); err != nil {
			c.logf("take back the acknowledgement of %s: %v", id, err)
			return
		}
		c.Connections.Reask(gate)
		why := "its manifest file changed on disk"
		switch {
		case d.Error != "":
			why = "its manifest file no longer loads: " + d.Error
		case ackSum == "":
			why = "it was acknowledged before Drydock kept which manifest file was acknowledged"
		}
		detail := map[string]any{"adapter": id, "gate": gate, "acknowledged": shortSum(ackSum), "now": shortSum(d.Sum), "why": why}
		if k := c.Knowledge(); k != nil {
			k.audit(ctx, "connection.custom_changed", id, detail)
		}
		c.publish(ctx, "connection.custom_changed", "", detail)
	case acked != "1" && ackSum != "" && !same:
		c.Connections.Reask(gate) // after a restart: still owed again, and the page still says why
	}
}

func shortSum(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// acknowledgeCustom binds the acknowledgement of a person's CLI (gate custom-<id>) to the manifest file as it is now. It refuses
// when the file does not load, so nobody acknowledges a program Drydock cannot read. ok is false for any other gate.
func (c *Core) acknowledgeCustom(ctx context.Context, gateID string) (sum string, ok bool, err error) {
	id, isCustom := strings.CutPrefix(gateID, "custom-")
	if !isCustom {
		return "", false, nil
	}
	found := c.recheckCustom(ctx, id)
	d, loaded := found[id]
	if !loaded {
		return "", false, nil // not a CLI the core loaded (a gate id of no adapter): an ordinary acknowledgement
	}
	if d.Error != "" {
		return "", true, fmt.Errorf("%s cannot be acknowledged: %s. Fix the file (or remove the CLI and add it again), then acknowledge it", id, d.Error)
	}
	return d.Sum, true, nil
}

// startGate is Runtime.StartGate: no session of a gated CLI starts before a person acknowledged the gate, and a person's own
// CLI's manifest file is looked at again first, so a file changed since the acknowledgement stops the run (decision P117).
func (c *Core) startGate(ctx context.Context, cfg *rt.Config) error {
	if custom, _ := c.customMCP(cfg.Adapter); custom {
		c.recheckCustom(ctx, cfg.Adapter)
	}
	if c.Connections == nil || c.Connections.Registry == nil {
		return nil
	}
	m, ok := c.Connections.Registry.Set.Get(cfg.Adapter)
	if !ok || m.Gate == nil || !m.Gate.Required || c.Connections.Acked(m.Gate.ID) {
		return nil
	}
	if c.Connections.Reasked(m.Gate.ID) {
		return fmt.Errorf("%s's manifest is not the one you acknowledged (it changed on disk): read it again and acknowledge it on the Agents page before it runs", cfg.Adapter)
	}
	return fmt.Errorf("%s needs your acknowledgement first (%s): %s", cfg.Adapter, m.Gate.ID, strings.TrimSpace(m.Gate.Text))
}
