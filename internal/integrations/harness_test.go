package integrations_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git/host"
	"github.com/kjmaguire/drydock/internal/integrations"
	"github.com/kjmaguire/drydock/internal/providers"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

const human = "human:kyle"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

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

type logBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuf) logf(format string, a ...any) {
	l.mu.Lock()
	fmt.Fprintf(&l.b, format+"\n", a...)
	l.mu.Unlock()
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type harness struct {
	t     *testing.T
	dir   string
	db    *storage.DB
	bus   *events.SQLBus
	cred  *providers.MemoryCredentialStore
	vault *providers.Vault
	scan  *security.Scanner
	appr  *security.Approvals
	svc   *integrations.Service
	// gh is the GitHub API the service reaches (a host.HTTPAPI on the GitHub fixture), nil = gh not installed
	gh   host.API
	clk  *clock
	logs *logBuf
	mod  func(*integrations.Options)

	mu    sync.Mutex
	tasks []integrations.TaskInput
}

// baseTime is "now" for every test: far enough from 2026-10 fixtures to never matter, fixed so tests are deterministic.
var baseTime = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func newHarness(t *testing.T, mod func(*integrations.Options)) *harness {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.OpenTemp(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewSQLBus(db, 1024)
	scan, err := security.NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	cred := providers.NewMemoryCredentialStoreForTests()
	h := &harness{t: t, dir: dir, db: db, bus: bus, cred: cred, vault: providers.NewVault(cred, db), scan: scan, clk: &clock{t: baseTime}, logs: &logBuf{}, mod: mod}
	h.appr = security.NewApprovals(db, bus, security.NewAudit(db, scan), security.WithApprovalClock(h.clk.Now), security.WithApprovalRedactor(scan))
	h.build()
	t.Cleanup(func() {
		h.svc.Close()
		bus.Close()
		db.Close()
	})
	return h
}

func (h *harness) build() {
	o := integrations.Options{
		DB: h.db, Bus: h.bus, Vault: h.vault, Scanner: h.scan, Approvals: h.appr,
		BodyDir: h.dir + "/integrations", Now: h.clk.Now, Logf: h.logs.logf, Jitter: func() float64 { return 1 },
		RegisterSecret: func(name, v string) { _ = h.scan.RegisterSecret(name, v) }, UnregisterSecret: h.scan.UnregisterSecret,
		// the fixtures are local: the documented budgets are tested on their own (ratelimit_test.go), not slept through here
		RateLimits: generousLimits,
		GitHub: func(context.Context) (host.API, error) {
			if h.gh == nil {
				return nil, fmt.Errorf("gh is not installed")
			}
			return h.gh, nil
		},
		ProjectRepo: func(_ context.Context, projectID string) (string, error) { return "acme/" + projectID, nil },
		ProjectExists: func(ctx context.Context, id string) error {
			if id == "" {
				return fmt.Errorf("no project")
			}
			return nil
		},
		CreateTask: func(ctx context.Context, in integrations.TaskInput) (string, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.tasks = append(h.tasks, in)
			return fmt.Sprintf("task_%d", len(h.tasks)), nil
		},
	}
	if h.mod != nil {
		h.mod(&o)
	}
	svc, err := integrations.New(o)
	if err != nil {
		h.t.Fatal(err)
	}
	h.svc = svc
}

// restart simulates quitting and reopening the app: a new Service over the same database and vault.
func (h *harness) restart() {
	h.svc.Close()
	h.build()
}

// generousLimits lets tests talk to the fixtures at full speed.
var generousLimits = map[string]integrations.RateLimit{
	integrations.KindLinear: {Requests: 100000, Per: time.Second, Burst: 100000},
	integrations.KindNotion: {Requests: 100000, Per: time.Second, Burst: 100000},
	integrations.KindSentry: {Requests: 100000, Per: time.Second, Burst: 100000},
	integrations.KindAikido: {Requests: 100000, Per: time.Second, Burst: 100000},
}

func (h *harness) taskList() []integrations.TaskInput {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]integrations.TaskInput(nil), h.tasks...)
}

// connect connects a kind with a token against a fixture and assigns it to project prj1 with cfg.
func (h *harness) connect(kind, token, baseURL string, cfg map[string]string) integrations.ConnectionView {
	h.t.Helper()
	v, err := h.svc.Connect(context.Background(), human, integrations.ConnectReq{Kind: kind, ProjectID: "prj1", Token: token, BaseURL: baseURL, Config: cfg})
	if err != nil {
		h.t.Fatalf("connect %s: %v", kind, err)
	}
	return v
}

func (h *harness) sync(id string, o integrations.SyncOpts) integrations.SyncResult {
	h.t.Helper()
	r, err := h.svc.Sync(context.Background(), id, o)
	if err != nil {
		h.t.Fatalf("sync: %v", err)
	}
	return r
}

func (h *harness) events(prefix string) []events.Event {
	h.t.Helper()
	evs, err := h.bus.Replay(context.Background(), 0, events.Filter{Types: []string{prefix}}, 100000)
	if err != nil {
		h.t.Fatal(err)
	}
	return evs
}

func (h *harness) allEventsJSON() string {
	h.t.Helper()
	evs, err := h.bus.Replay(context.Background(), 0, events.Filter{}, 100000)
	if err != nil {
		h.t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range evs {
		b.WriteString(e.Type)
		b.Write(e.Data)
		b.WriteByte('\n')
	}
	return b.String()
}

func (h *harness) items(id string) []integrations.Item {
	h.t.Helper()
	its, err := h.svc.Items(context.Background(), integrations.ItemFilter{IntegrationID: id, Limit: 500})
	if err != nil {
		h.t.Fatal(err)
	}
	return its
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

func (h *harness) count(q string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.db.Reader().QueryRow(q, args...).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}
