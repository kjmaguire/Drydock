package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/security"
)

// A panic in a goroutine of the application is contained where it happens (internal/safe) and told here: the line and the stack go
// to the application log, and an app.panic event says where, so the window can show it and a support bundle has it. The text of
// the panic can hold what the code was working on, so it goes through the secret scanner first. One event per place every ten
// seconds: a loop that panics again and again must not fill the event log.

const panicEventEvery = 10 * time.Second

// installPanicReporter makes c the place contained panics are reported to.
func (c *Core) installPanicReporter() {
	var mu sync.Mutex
	last := map[string]time.Time{}
	safe.SetReporter(func(p safe.Panic) {
		msg, stack := fmt.Sprint(p.Value), p.Stack
		if len(msg) > 2000 {
			msg = msg[:2000] + "..."
		}
		var scan *security.Scanner = c.Scanner
		if scan != nil {
			msg, stack = scan.Redact(msg), scan.Redact(stack)
		}
		c.logf("PANIC contained in %s: %s\n%s", p.Name, msg, stack)
		mu.Lock()
		now := time.Now()
		quiet := now.Sub(last[p.Name]) < panicEventEvery
		if !quiet {
			last[p.Name] = now
		}
		mu.Unlock()
		if !quiet && c.Bus != nil {
			c.publish(context.Background(), "app.panic", "", map[string]any{"where": p.Name, "message": msg})
		}
	})
}
