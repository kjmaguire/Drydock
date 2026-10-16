package llm

import (
	"context"

	"github.com/kjmaguire/drydock/internal/providers"
)

// PriceCost computes the estimated cost of u from a catalog row (see providers.PriceCost). ok is false when the row
// has no usable price.
func PriceCost(m providers.Model, flavor providers.Flavor, u Usage) (usd float64, ok bool) {
	return providers.PriceCost(m, flavor, u)
}

// costOf prices a response: the provider's own figure when it reported one, otherwise the catalog price table,
// otherwise unknown (never silently zero).
func (w *Wrapper) costOf(ctx context.Context, p providers.Provider, requested string, r Response) Cost {
	if r.ReportedCostUSD != nil {
		return Cost{USD: *r.ReportedCostUSD, Source: CostReported, Known: true}
	}
	for _, id := range []string{r.ModelUsed, requested} {
		if id == "" {
			continue
		}
		if m, err := w.reg.Model(ctx, p.ID, id); err == nil {
			if usd, ok := PriceCost(m, p.Flavor, r.Usage); ok {
				return Cost{USD: usd, Source: CostEstimated, Known: true}
			}
		}
	}
	return Cost{Source: CostUnknown}
}
