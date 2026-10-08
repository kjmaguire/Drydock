package core

import (
	"context"
	"time"

	"github.com/kjmaguire/drydock/internal/license"
)

// Test hooks for the packaging extension. They live in a _test file so the product has no clock or key setters.

// TestSetLicenseKeys installs the license verification keys a build would embed with -ldflags.
func (c *Core) TestSetLicenseKeys(k license.Keys) {
	p := c.pkg()
	p.mu.Lock()
	p.keys = k
	p.cachedTk = ""
	p.mu.Unlock()
}

// TestSetLicenseClock replaces the clock the license state machine reads.
func (c *Core) TestSetLicenseClock(f func() time.Time) {
	p := c.pkg()
	p.mu.Lock()
	p.now = f
	p.mu.Unlock()
}

// TestReconcileLicense runs one license check now (what the 30 second loop does).
func (c *Core) TestReconcileLicense(ctx context.Context) { c.pkg().reconcile(ctx) }

// TestBackupTick runs one scheduler decision now.
func (c *Core) TestBackupTick(ctx context.Context) { c.pkg().backupTick(ctx) }

// TestSetBackupClock replaces the clock backups are stamped and scheduled with.
func (c *Core) TestSetBackupClock(f func() time.Time) {
	p := c.pkg()
	p.mu.Lock()
	p.backupClock = f
	p.mu.Unlock()
}

// TestSetBackupRetry changes how long the scheduler waits after a failed backup.
func (c *Core) TestSetBackupRetry(d time.Duration) {
	p := c.pkg()
	p.mu.Lock()
	p.retryAfter = d
	p.mu.Unlock()
}

// GatedMethodsForTest returns the registry methods that an expired license refuses, with the action each is checked as.
func GatedMethodsForTest() map[string]string {
	out := map[string]string{}
	for k, v := range gatedMethods {
		out[k] = v
	}
	return out
}

// TestLog writes through Core.logf (stderr and the scrubbed application log).
func (c *Core) TestLog(format string, a ...any) { c.logf(format, a...) }
