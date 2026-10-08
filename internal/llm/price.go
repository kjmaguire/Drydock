package llm

import (
	"context"

	"github.com/kjmaguire/drydock/internal/providers"
)

// Documented cache price multipliers, used when a catalog row has an input price but no cache prices
// (docs/token-control.md s9; Anthropic read 0.1x and 5-minute write 1.25x, Google read 0.25x). Other providers
// get no assumed discount: the full input price, so the estimate errs high.
func cacheMultipliers(f providers.Flavor) (read, write float64) {
	switch f {
	case providers.FlavorAnthropic:
		return 0.1, 1.25
	case providers.FlavorGemini:
		return 0.25, 1
	}
	return 1, 1
}

// PriceCost computes the estimated cost of u from a catalog row. ok is false when the row has no usable price.
func PriceCost(m providers.Model, flavor providers.Flavor, u Usage) (usd float64, ok bool) {
	if !m.PriceKnown() {
		return 0, false
	}
	readMul, writeMul := cacheMultipliers(flavor)
	cacheRead, cacheWrite := m.CacheReadPerM, m.CacheWritePerM
	if cacheRead == 0 {
		cacheRead = m.InputPerM * readMul
	}
	if cacheWrite == 0 {
		cacheWrite = m.InputPerM * writeMul
	}
	uncached := u.Input - u.Cached - u.CacheWrite
	if uncached < 0 {
		uncached = 0
	}
	usd = (float64(uncached)*m.InputPerM + float64(u.Cached)*cacheRead + float64(u.CacheWrite)*cacheWrite + float64(u.Output)*m.OutputPerM) / 1e6
	return usd, true
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
