package connections

import (
	"context"
	"fmt"
	"sort"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
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
	opts     Options
}

// NewService loads the manifests and compat.yaml (embedded unless the config supplies others) and wires detection into the
// adapters the registry builds.
func NewService(cfg Config) (*Service, error) {
	s := &Service{opts: cfg.Detection}
	mo := cfg.Manifests
	mo.Detect = func(ctx context.Context, m *manifests.Manifest) (agents.Detection, error) {
		return Detect(ctx, m, s.opts)
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
	return Check(ctx, m, s.opts), nil
}

// Detect computes the Detection for one adapter id.
func (s *Service) Detect(ctx context.Context, id string) (agents.Detection, error) {
	c, err := s.Check(ctx, id)
	return c.Detection, err
}

// DetectAll checks every manifest, in id order.
func (s *Service) DetectAll(ctx context.Context) []Connection {
	ids := s.Registry.Set.IDs()
	sort.Strings(ids)
	out := make([]Connection, 0, len(ids))
	for _, id := range ids {
		c, _ := s.Check(ctx, id)
		out = append(out, c)
	}
	return out
}

// Acknowledge records that the user acknowledged a gate. The caller persists the acknowledgement; this only affects Usable.
func (s *Service) Acknowledge(gateID string) {
	cp := map[string]bool{gateID: true}
	for k, v := range s.opts.Acked {
		cp[k] = v
	}
	s.opts.Acked = cp
}

// RecordTest stores the outcome of a Test connection so a failure degrades the Connection until the next passing Test.
func (s *Service) RecordTest(id string, r Report) {
	cp := map[string]TestOutcome{}
	for k, v := range s.opts.Last {
		cp[k] = v
	}
	cp[id] = TestOutcome{OK: r.OK, Reason: r.FailureReason(), Version: r.CLIVersion}
	s.opts.Last = cp
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
