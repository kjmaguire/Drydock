package core

import (
	"context"
	"time"

	"github.com/kjmaguire/drydock/internal/license"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/scan"
	"github.com/kjmaguire/drydock/internal/workflow"
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

// TestStoreScanReport stores a finished security scan report the way the security extension does when a scan ends.
func (c *Core) TestStoreScanReport(ctx context.Context, rep scan.Report) error {
	return (&securityExt{c: c}).store(ctx, target{Key: rep.ProjectID, ProjectID: rep.ProjectID}, rep)
}

// TestMaybeRecommend runs the check a completed task triggers: whether the project asks for recommendations again by itself.
func (c *Core) TestMaybeRecommend(ctx context.Context, projectID string) {
	c.maybeRecommend(ctx, projectID)
}

// TestRecommendPrompt returns the prompt a recommendation turn for the project would get now.
func (c *Core) TestRecommendPrompt(ctx context.Context, projectID string) (string, error) {
	w, err := c.recommendWorld(ctx, projectID)
	if err != nil {
		return "", err
	}
	return c.recommendPrompt(ctx, projectID, w, nil), nil
}

// TestRouteNode runs the routing middleware of agent_task nodes (routeTurn) on in, in place of a real node: the node it wraps
// only reports the routed model the middleware put in its context (none: the turn runs the agent's configured model).
func (c *Core) TestRouteNode(ctx context.Context, in workflow.NodeInput) (pipeline.RoutedModel, bool, error) {
	econMu.Lock()
	e := econByCore[c]
	econMu.Unlock()
	var got pipeline.RoutedModel
	var ok bool
	_, err := e.routeTurn(func(ctx context.Context, _ workflow.NodeInput) (workflow.NodeOutput, error) {
		got, ok = pipeline.RoutedFrom(ctx)
		return workflow.NodeOutput{}, nil
	})(ctx, in)
	return got, ok, err
}
