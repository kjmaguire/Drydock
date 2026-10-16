package router

import (
	"context"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/providers"
)

// Catalog is the part of *providers.Registry the router reads.
type Catalog interface {
	Get(ctx context.Context, id string) (providers.Provider, error)
	List(ctx context.Context) ([]providers.Provider, error)
	Model(ctx context.Context, providerID, model string) (providers.Model, error)
	Models(ctx context.Context, providerID string) ([]providers.Model, error)
	EffectiveDataClass(ctx context.Context, p providers.Provider, model string) providers.DataClass
}

// Availability is the part of *providers.Monitor the router reads.
type Availability interface {
	Status(ctx context.Context, id string) (providers.Status, error)
}

// QuotaView is the part of *providers.QuotaLedger the router reads. The router only checks quota; it never takes a
// slot (the llm.Wrapper reserves at call time).
type QuotaView interface {
	Check(ctx context.Context, provider, model string) (ok bool, retryAt time.Time)
	RemainingPct(ctx context.Context, provider, model string) float64
}

// CandidateSource lists the candidates for a request.
type CandidateSource interface {
	Candidates(ctx context.Context, req RouteRequest) ([]Candidate, error)
}

// RegistrySource builds candidates from the provider registry, the health monitor and the quota ledger. Agent-native
// routes (subscription CLIs, whose quota comes from CLI signals) are not registry providers; the Agent Runtime adds
// them through Extra.
type RegistrySource struct {
	Catalog Catalog
	Avail   Availability // nil: every provider counts as healthy
	Quota   QuotaView    // nil: quota unknown and not blocking
	Extra   CandidateSource
}

// blendedPerM is the price per million tokens at the default 3:1 input:output weight (docs/model-routing.md s5,
// a design assumption).
func blendedPerM(m providers.Model) float64 { return (3*m.InputPerM + m.OutputPerM) / 4 }

// Candidates returns one candidate per catalog model of every registered provider.
func (s *RegistrySource) Candidates(ctx context.Context, req RouteRequest) ([]Candidate, error) {
	ps, err := s.Catalog.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, p := range ps {
		ms, err := s.Catalog.Models(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			out = append(out, s.candidate(ctx, req, p, m))
		}
	}
	if s.Extra != nil {
		extra, err := s.Extra.Candidates(ctx, req)
		if err != nil {
			return nil, err
		}
		out = append(out, extra...)
	}
	return out, nil
}

// CandidateFor builds the candidate for one (provider, model); the model need not be in the catalog (a static
// assignment may name a model the listing does not show), in which case the price and capabilities are unknown.
func (s *RegistrySource) CandidateFor(ctx context.Context, req RouteRequest, providerID, model string) (Candidate, error) {
	p, err := s.Catalog.Get(ctx, providerID)
	if err != nil {
		return Candidate{}, err
	}
	m, err := s.Catalog.Model(ctx, providerID, model)
	if err != nil {
		// Not in the catalog: a deliberate static assignment. Capabilities are assumed (tools), the price is unknown.
		m = providers.Model{ProviderID: providerID, ID: model, Tier: p.Tier, DataClass: p.DataClass, SupportsTools: true}
	}
	return s.candidate(ctx, req, p, m), nil
}

func (s *RegistrySource) candidate(ctx context.Context, req RouteRequest, p providers.Provider, m providers.Model) Candidate {
	tier := m.Tier
	if tier == "" {
		tier = p.Tier
	}
	c := Candidate{Provider: ProviderID(p.ID), Model: m.ID, Tier: tier, DataClass: s.Catalog.EffectiveDataClass(ctx, p, m.ID), Kind: p.Kind,
		Enabled: p.Enabled, PrivateEligible: p.PrivateEligible() || p.DataClass != providers.DataPrivate, SupportsTools: m.SupportsTools,
		ResponsesValidated: !p.ResponsesValidatedAt.IsZero(), Healthy: true, QuotaOK: true, QuotaRemainingPct: -1,
		MaxConcurrent: p.MaxConcurrent, ProbePassed: m.ProbeStatus == "pass", ProbeFailure: probeFailure(m.ProbeStatus), Context: m.ContextLength, PriceKnown: m.PriceKnown(),
		BlendedPerM: blendedPerM(m), PriceUpdatedAt: m.PriceUpdatedAt}
	if s.Avail != nil {
		if st, err := s.Avail.Status(ctx, p.ID); err == nil {
			c.Healthy, c.Concurrent = st.Available, st.InUse
		}
	}
	if s.Quota != nil {
		c.QuotaOK, _ = s.Quota.Check(ctx, p.ID, m.ID)
		c.QuotaRemainingPct = s.Quota.RemainingPct(ctx, p.ID, m.ID)
	}
	if usd, ok := providers.PriceCost(m, p.Flavor, providers.Usage{Input: req.ContextTokens, Output: outputBudget(req)}); ok {
		c.EstCostUSD = &usd
	}
	return c
}

// probeFailure is the capability failure recorded by the tool-call probe ("fail:no_tool_call" -> "no_tool_call"), or "" for a
// pass, no probe, or a probe that could not run ("fail:error:..."): only a model that answered wrongly is excluded.
func probeFailure(status string) string {
	mode, ok := strings.CutPrefix(status, "fail:")
	if !ok || strings.HasPrefix(mode, "error") {
		return ""
	}
	return mode
}
