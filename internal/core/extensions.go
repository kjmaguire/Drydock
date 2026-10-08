package core

import (
	"context"
	"sync"
)

// An Extension adds a feature area (budgets, packs, templates, integrations...) to the core without editing the files that
// wire the base application. Each area lives in its own internal/core/ext_<name>.go and registers itself from init().
// Start runs after the base services are up; the methods it returns join the registry the UI and tests call, and Close (if
// non-nil) runs on shutdown, before the database closes.
type Extension struct {
	Name  string
	Start func(ctx context.Context, c *Core) (methods map[string]Method, closeFn func(), err error)
}

var (
	extMu sync.Mutex
	exts  []Extension
)

// RegisterExtension is called from init() in the extension's file.
func RegisterExtension(e Extension) {
	extMu.Lock()
	defer extMu.Unlock()
	exts = append(exts, e)
}

// startExtensions runs every registered extension and records its methods and shutdown hook.
func (c *Core) startExtensions(ctx context.Context) error {
	extMu.Lock()
	list := append([]Extension(nil), exts...)
	extMu.Unlock()
	c.extMethods = map[string]Method{}
	for _, e := range list {
		m, closeFn, err := e.Start(ctx, c)
		if err != nil {
			c.logf("extension %s unavailable: %v", e.Name, err)
			continue
		}
		for k, v := range m {
			if _, dup := c.extMethods[k]; dup {
				c.logf("extension %s: method %s already registered, skipped", e.Name, k)
				continue
			}
			c.extMethods[k] = v
		}
		if closeFn != nil {
			c.extClose = append(c.extClose, closeFn)
		}
	}
	return nil
}
