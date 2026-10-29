package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
)

// A person's own agent CLIs (decision P115, docs/connections.md "As built: Antigravity and custom CLIs"): manifests in
// <app data>/adapters/custom/<id>.yaml, validated strictly (manifests.ParseUserCustom), driven by the generic headless runner,
// added and removed on the Agents page. Each carries its own acknowledgement (gate custom-<id>), which the person gives on the
// Connection before an agent can be made on it (CreateAgent), as for Antigravity.

func init() {
	RegisterExtension(Extension{Name: "customcli", Start: startCustomCLI})
}

// customDir is where a person's own CLIs are kept.
func (c *Core) customDir() string { return filepath.Join(c.cfg.DataDir, "adapters", "custom") }

// builtinAdapter reports whether id is an embedded manifest's (a person's CLI can never take or remove one).
func builtinAdapter(id string) bool {
	set, err := manifests.Embedded()
	if err != nil {
		return true
	}
	_, ok := set.Get(id)
	return ok
}

// CustomCLIs is connection.custom.list: the folder and every file in it, with why one was not loaded.
type CustomCLIs struct {
	Dir   string                 `json:"dir"`
	Files []manifests.CustomFile `json:"files"`
}

func (c *Core) customCLIs() CustomCLIs {
	_, files := manifests.LoadUserCustom(c.customDir())
	if files == nil {
		files = []manifests.CustomFile{}
	}
	return CustomCLIs{Dir: c.customDir(), Files: files}
}

// customTemplate is the embedded template made ready for an id: its id, name and gate set, the rest to fill in.
func customTemplate(id string) (string, error) {
	b, err := manifests.CustomTemplate()
	if err != nil {
		return "", err
	}
	s := string(b)
	if id = strings.TrimSpace(id); id != "" {
		s = strings.Replace(s, "id: custom,", "id: "+id+",", 1)
		s = strings.Replace(s, "display_name: Custom CLI (template)", "display_name: "+id, 1)
		s = strings.Replace(s, "gate: {id: custom-runs-arbitrary-code-v1,", "gate: {id: "+manifests.UserGateID(id)+",", 1)
	}
	return s, nil
}

func startCustomCLI(ctx context.Context, c *Core) (map[string]Method, func(), error) {
	ms, files := manifests.LoadUserCustom(c.customDir())
	for _, f := range files {
		if f.Error != "" {
			c.logf("custom CLI %s not loaded: %s", f.ID, f.Error)
		}
	}
	for _, m := range ms {
		a, err := c.Connections.Registry.AddCustom(m, builtinAdapter)
		if err != nil {
			c.logf("custom CLI %s not loaded: %v", m.Meta.ID, err)
			continue
		}
		c.RegisterAdapter(m.Meta.ID, a)
	}
	audit := func(ctx context.Context, action, subject string, detail any) {
		if k := c.Knowledge(); k != nil {
			k.audit(ctx, action, subject, detail)
		}
	}
	methods := map[string]Method{
		"connection.custom.list": bind(func(ctx context.Context, _ struct{}) (any, error) { return c.customCLIs(), nil }),
		"connection.custom.template": bind(func(ctx context.Context, p struct {
			ID string `json:"id"`
		}) (any, error) {
			s, err := customTemplate(p.ID)
			return map[string]string{"yaml": s}, err
		}),
		// a person adds their own CLI: the manifest is checked strictly, kept as a file, and its Connection appears (gated)
		"connection.custom.add": bind(func(ctx context.Context, p struct {
			YAML string `json:"yaml"`
		}) (any, error) {
			if c.Scanner != nil && c.Scanner.Redact(p.YAML) != p.YAML {
				return nil, errors.New("the manifest looks like it holds a secret: keep keys in your environment and pass the variable on by name (Settings), not in the manifest")
			}
			m, err := manifests.ParseUserCustom([]byte(p.YAML))
			if err != nil {
				return nil, err
			}
			id := m.Meta.ID
			if builtinAdapter(id) {
				return nil, fmt.Errorf("%q is a built-in CLI's id: choose another", id)
			}
			c.mu.Lock()
			_, taken := c.adapters[id]
			c.mu.Unlock()
			if taken {
				return nil, fmt.Errorf("a CLI with the id %q exists already: remove it first or choose another id", id)
			}
			if err := os.MkdirAll(c.customDir(), 0o700); err != nil {
				return nil, err
			}
			path := filepath.Join(c.customDir(), id+".yaml")
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, []byte(p.YAML), 0o600); err != nil {
				return nil, err
			}
			if err := os.Rename(tmp, path); err != nil {
				_ = os.Remove(tmp)
				return nil, err
			}
			a, err := c.Connections.Registry.AddCustom(m, builtinAdapter)
			if err != nil {
				_ = os.Remove(path)
				return nil, err
			}
			c.RegisterAdapter(id, a)
			audit(ctx, "connection.custom_added", id, map[string]any{"id": id, "program": m.Binaries.Native, "argv": m.Transports.Headless.Argv})
			c.publish(ctx, "connection.custom_added", "", map[string]any{"adapter": id, "by": c.Human()})
			return c.Connections.Check(ctx, id)
		}),
		"connection.custom.remove": bind(func(ctx context.Context, p struct {
			ID string `json:"id"`
		}) (any, error) {
			if builtinAdapter(p.ID) {
				return nil, fmt.Errorf("%q is a built-in CLI: it cannot be removed", p.ID)
			}
			var n int
			if err := c.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE adapter=? AND archived_at=0`, p.ID).Scan(&n); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if n > 0 {
				return nil, fmt.Errorf("%d %s run on %s: give them another CLI or remove them first", n, plural(n, "agent", "agents"), p.ID)
			}
			if err := c.Connections.Registry.RemoveCustom(p.ID, builtinAdapter); err != nil {
				return nil, err
			}
			c.mu.Lock()
			delete(c.adapters, p.ID)
			c.mu.Unlock()
			if err := os.Remove(filepath.Join(c.customDir(), p.ID+".yaml")); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			// a CLI added later under this id is a new program: it is acknowledged again
			gate := manifests.UserGateID(p.ID)
			_ = c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, gateKey+gate)
				return err
			})
			c.Connections.Forget(gate)
			audit(ctx, "connection.custom_removed", p.ID, map[string]any{"id": p.ID})
			c.publish(ctx, "connection.custom_removed", "", map[string]any{"adapter": p.ID, "by": c.Human()})
			return c.customCLIs(), nil
		}),
	}
	return methods, nil, nil
}
