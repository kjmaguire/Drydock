package connections

import (
	"context"
	"fmt"
	"sort"
	"sync"

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
		return Detect(ctx, m, s.options())
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
	return Check(ctx, m, s.options()), nil
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
