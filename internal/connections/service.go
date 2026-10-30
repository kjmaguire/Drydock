package connections

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/safe"
)

// Config builds a Service.
type Config struct {
	// Manifests configures the adapter registry (manifest set, compat, ACP permission handler, terminal, scrubbed env).
	// Its Detect field is set by NewService.
	Manifests manifests.Options
	// Detection configures probes: process manager, environment, gate acknowledgements, last Test results.
	Detection Options
}

// Service ties the manifest registry to detection and Test connection.
type Service struct {
	Registry *manifests.Registry
	mu       sync.RWMutex // guards opts: gate acknowledgements, Test results and drift change while checks read them
	opts     Options
	// last is the newest Connection each check computed, by key (Last): the Inbox reads it rather than starting every CLI on
	// each of its reads (decision P130)
	lastMu sync.Mutex
	last   map[string]Checked
}

// Checked is a Connection as a check last computed it.
type Checked struct {
	Connection
	At time.Time `json:"at"`
}

// record keeps c as the newest check of its key.
func (s *Service) record(c Connection) {
	s.lastMu.Lock()
	defer s.lastMu.Unlock()
	if s.last == nil {
		s.last = map[string]Checked{}
	}
	s.last[c.Key] = Checked{Connection: c, At: time.Now()}
}

// Last returns the newest check of every Connection checked since the app started (a page that lists them, a check again, an
// agent's start or its creation), by key. A Connection nothing checked yet is absent: nothing here starts a CLI.
func (s *Service) Last() map[string]Checked {
	s.lastMu.Lock()
	defer s.lastMu.Unlock()
	out := make(map[string]Checked, len(s.last))
	for k, v := range s.last {
		out[k] = v
	}
	return out
}

// options is a snapshot of the detection options (the maps in it are replaced, never changed in place).
func (s *Service) options() Options {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.opts
}

// NewService loads the manifests and compat.yaml (embedded unless the config supplies others) and wires detection into the
// adapters the registry builds.
func NewService(cfg Config) (*Service, error) {
	s := &Service{opts: cfg.Detection}
	mo := cfg.Manifests
	if mo.WSL == nil {
		mo.WSL = cfg.Detection.WSL // one host: the adapters start in the distros detection probed
	}
	mo.Detect = func(ctx context.Context, m *manifests.Manifest) (agents.Detection, error) {
		c := Check(ctx, m, s.options())
		s.record(c)
		return c.Detection, nil
	}
	reg, err := manifests.NewRegistry(mo)
	if err != nil {
		return nil, err
	}
	s.Registry = reg
	s.opts.Compat = reg.Compat
	if mo.ClaudeExe != "" {
		cp := map[string]string{}
		for k, v := range s.opts.BinaryOverride {
			cp[k] = v
		}
		cp["claude"] = mo.ClaudeExe
		s.opts.BinaryOverride = cp
	}
	return s, nil
}

// Check computes the full Connection for one adapter id.
func (s *Service) Check(ctx context.Context, id string) (Connection, error) {
	m, ok := s.Registry.Set.Get(id)
	if !ok {
		return Connection{}, fmt.Errorf("no manifest for adapter %q", id)
	}
	c := Check(ctx, m, s.options())
	s.record(c)
	return c, nil
}

// Detect computes the Detection for one adapter id.
func (s *Service) Detect(ctx context.Context, id string) (agents.Detection, error) {
	c, err := s.Check(ctx, id)
	return c.Detection, err
}

// DetectAll checks every manifest, in id order, then the installs inside WSL distros (DetectWSL). The checks run side by
// side: each starts its CLI for the version and the sign-in (a Node CLI takes most of a second on Windows), so one after
// another the Agents page waited for the sum of them.
func (s *Service) DetectAll(ctx context.Context) []Connection {
	ids := s.Registry.Set.IDs()
	sort.Strings(ids)
	out := make([]Connection, len(ids))
	var inWSL []Connection
	var wg sync.WaitGroup
	wg.Add(len(ids) + 1)
	for i, id := range ids {
		safe.Go("connections.Service.DetectAll", func() {
			defer wg.Done()
			out[i], _ = s.Check(ctx, id)
		})
	}
	safe.Go("connections.Service.DetectAll", func() {
		defer wg.Done()
		inWSL = s.DetectWSL(ctx)
	})
	wg.Wait()
	return append(out, inWSL...)
}

// Acknowledge records that the user acknowledged a gate. The caller persists the acknowledgement; this only affects Usable.
func (s *Service) Acknowledge(gateID string) {
	s.mu.Lock()
	cp := map[string]bool{gateID: true}
	for k, v := range s.opts.Acked {
		cp[k] = v
	}
	s.opts.Acked = cp
	s.opts.Reasked = without(s.opts.Reasked, gateID)
	s.mu.Unlock()
}

// Acked reports whether a gate is acknowledged now (the check every session start makes, decision P117).
func (s *Service) Acked(gateID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.opts.Acked[gateID]
}

// Reask takes an acknowledgement back because what was acknowledged changed (a person's own CLI's manifest file changed on
// disk, decision P117): the gate is owed again, and its status says why.
func (s *Service) Reask(gateID string) {
	s.mu.Lock()
	s.opts.Acked = without(s.opts.Acked, gateID)
	cp := map[string]bool{gateID: true}
	for k, v := range s.opts.Reasked {
		cp[k] = v
	}
	s.opts.Reasked = cp
	s.mu.Unlock()
}

// Reasked reports whether a gate is owed again because what was acknowledged changed.
func (s *Service) Reasked(gateID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.opts.Reasked[gateID]
}

// without is a copy of m without key (the maps in Options are replaced, never changed in place).
func without(m map[string]bool, key string) map[string]bool {
	cp := map[string]bool{}
	for k, v := range m {
		if k != key {
			cp[k] = v
		}
	}
	return cp
}

// Forget drops an acknowledgement (a person's own CLI was removed: adding one with that id again asks again).
func (s *Service) Forget(gateID string) {
	s.mu.Lock()
	s.opts.Acked = without(s.opts.Acked, gateID)
	s.opts.Reasked = without(s.opts.Reasked, gateID)
	s.mu.Unlock()
}

// RecordTest stores the outcome of a Test connection so a failure degrades the Connection until the next passing Test.
func (s *Service) RecordTest(id string, r Report) {
	s.mu.Lock()
	cp := map[string]TestOutcome{}
	for k, v := range s.opts.Last {
		cp[k] = v
	}
	cp[id] = TestOutcome{OK: r.OK, Reason: r.FailureReason(), Version: r.CLIVersion}
	s.opts.Last = cp
	s.mu.Unlock()
	if r.OK {
		s.SetDrift(id, nil)
	}
}

// SetDrift records drift seen in an adapter's live output (nil clears it), so the Connection shows degraded until a Test passes.
func (s *Service) SetDrift(id string, d *DriftRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := map[string]DriftRecord{}
	for k, v := range s.opts.Drift {
		if k != id {
			cp[k] = v
		}
	}
	if d != nil {
		cp[id] = *d
	}
	s.opts.Drift = cp
}

// Drift returns the drift recorded for an adapter since its last passing Test.
func (s *Service) Drift(id string) (DriftRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.opts.Drift[id]
	return d, ok
}

// TestConnection runs Test connection for adapterID on a default Service (embedded manifests and compat.yaml, the real process
// manager). Use Service.TestConnection to carry gate acknowledgements, a permission handler or recorded results.
func TestConnection(ctx context.Context, adapterID string, o TestOptions) (Report, error) {
	svc, err := NewService(Config{})
	if err != nil {
		return Report{}, err
	}
	return svc.TestConnection(ctx, adapterID, o)
}
