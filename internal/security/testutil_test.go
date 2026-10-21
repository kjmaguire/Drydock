package security

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Fake credentials are assembled at runtime so no realistic key literal sits in the repository source.
func fakeGitHubToken() string { return "gh" + "p_" + "Z9q8W7e6R5t4Y3u2I1o0P9a8S7d6F5g4H3j2" }
func fakeAWSKey() string      { return "AK" + "IA" + "QZ7RTW4MNB5HXV3L" }
func fakeProviderKey() string { return "sk-" + "or-v1-" + "7f3a9c1e5b2d4f6a8c0e2b4d6f8a1c3e" }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)} }
func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type rig struct {
	db    *storage.DB
	bus   *events.SQLBus
	scan  *Scanner
	audit *Audit
	apr   *Approvals
	clk   *clock
}

var (
	scanOnce   sync.Once
	scanShared *Scanner
	scanErr    error
)

// sharedScanner builds the gitleaks scanner once per test binary (loading the default config is slow).
func sharedScanner(t testing.TB) *Scanner {
	t.Helper()
	scanOnce.Do(func() { scanShared, scanErr = NewScanner() })
	if scanErr != nil {
		t.Fatal(scanErr)
	}
	return scanShared
}

func newRig(t *testing.T, opts ...ApprovalOption) *rig {
	t.Helper()
	ctx := context.Background()
	db, err := storage.OpenTemp(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 256)
	t.Cleanup(func() { bus.Close(); db.Close() })
	r := &rig{db: db, bus: bus, scan: sharedScanner(t), clk: newClock()}
	r.audit = NewAudit(db, r.scan)
	opts = append([]ApprovalOption{WithApprovalClock(r.clk.Now), WithApprovalRedactor(r.scan)}, opts...)
	r.apr = NewApprovals(db, bus, r.audit, opts...)
	return r
}

func (r *rig) eventTypes(t *testing.T) []string {
	t.Helper()
	evs, err := r.bus.Replay(context.Background(), 0, events.Filter{Types: []string{"approval.*"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

func (r *rig) auditActions(t *testing.T) []string {
	t.Helper()
	rows, err := r.audit.List(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, row := range rows {
		out = append(out, row.Action)
	}
	return out
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func hasSub(s, sub string) bool { return strings.Contains(s, sub) }

func eventsFilterAll() events.Filter { return events.Filter{} }
