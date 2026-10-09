package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/providers"
)

// Provider detail and editing (docs/ui.md section 9): one provider with its served models and their prices and probe results,
// its quota windows (configured, learned from response headers, or polled from OpenRouter's key endpoint), and the edits a
// human makes: the provider's settings, a replaced API key, a model's price, a quota limit the provider does not publish.
// Every edit is a human action from the UI; the API key is written to the secret store and never returned.

func init() {
	RegisterExtension(Extension{Name: "providerdetail", After: []string{"economics"}, Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			"provider.detail": bind(func(ctx context.Context, p struct {
				ID         string `json:"id"`
				RefreshKey bool   `json:"refreshKey"`
			}) (any, error) {
				return c.ProviderDetail(ctx, p.ID, p.RefreshKey)
			}),
			"provider.update":   bind(func(ctx context.Context, p ProviderEdit) (any, error) { return c.UpdateProvider(ctx, p) }),
			"provider.setPrice": bind(func(ctx context.Context, p PriceEdit) (any, error) { return nil, c.SetModelPrice(ctx, p) }),
			"provider.setQuota": bind(func(ctx context.Context, p QuotaEdit) (any, error) { return nil, c.SetQuota(ctx, p) }),
		}, nil, nil
	}})
}

// ProviderDetailView is one provider in full.
type ProviderDetailView struct {
	Provider      providers.Provider       `json:"provider"`
	Models        []providers.Model        `json:"models"`
	Quota         []QuotaWindowView        `json:"quota"`
	OpenRouterKey *providers.OpenRouterKey `json:"openRouterKey,omitempty"`
	// KeyError says why the OpenRouter key could not be read, when a refresh was asked for.
	KeyError string `json:"keyError,omitempty"`
}

// QuotaWindowView is a quota window as the UI shows it. Limits are never invented: a window exists only when it was
// configured by the human, learned from a provider's headers or read from its API.
type QuotaWindowView struct {
	Model     string  `json:"model,omitempty"`
	Kind      string  `json:"kind"`
	Limit     float64 `json:"limit"`
	Used      float64 `json:"used"`
	ResetAt   int64   `json:"resetAt,omitempty"`
	Source    string  `json:"source"`
	UpdatedAt int64   `json:"updatedAt"`
}

// ProviderDetail returns a provider with its models and quota; refreshKey polls OpenRouter's key endpoint first.
func (c *Core) ProviderDetail(ctx context.Context, id string, refreshKey bool) (ProviderDetailView, error) {
	p, err := c.Providers.Get(ctx, id)
	if err != nil {
		return ProviderDetailView{}, err
	}
	out := ProviderDetailView{Provider: p, Models: []providers.Model{}, Quota: []QuotaWindowView{}}
	if ms, err := c.Providers.Models(ctx, id); err == nil {
		out.Models = append(out.Models, ms...)
	}
	ledger := economicsLedger(c)
	if refreshKey && ledger != nil {
		if strings.Contains(strings.ToLower(p.BaseURL), "openrouter.ai") {
			k, err := ledger.PollOpenRouter(ctx, id)
			if err != nil {
				out.KeyError = err.Error()
			} else {
				out.OpenRouterKey = &k
			}
		} else {
			out.KeyError = "only OpenRouter publishes its key limits; other providers' limits come from their response headers or from what you configure"
		}
	}
	if ledger != nil {
		if ws, err := ledger.Snapshot(ctx, id); err == nil {
			for _, w := range ws {
				v := QuotaWindowView{Model: w.Model, Kind: string(w.Kind), Limit: w.Limit, Used: w.Used, Source: string(w.Source), UpdatedAt: w.UpdatedAt.UnixMilli()}
				if !w.ResetAt.IsZero() {
					v.ResetAt = w.ResetAt.UnixMilli()
				}
				out.Quota = append(out.Quota, v)
			}
		}
	}
	return out, nil
}

func economicsLedger(c *Core) *providers.QuotaLedger {
	econMu.Lock()
	defer econMu.Unlock()
	if e := econByCore[c]; e != nil {
		return e.ledger
	}
	return nil
}

// ProviderEdit changes a provider's settings. A nil field is left as it is; APIKey replaces the stored key when not empty.
type ProviderEdit struct {
	ID            string   `json:"id"`
	Name          *string  `json:"name"`
	BaseURL       *string  `json:"baseUrl"`
	Tier          *string  `json:"tier"`
	DataClass     *string  `json:"dataClass"`
	MaxConcurrent *int     `json:"maxConcurrent"`
	HourlyCost    *float64 `json:"hourlyCost"`
	APIKey        string   `json:"apiKey"`
}

// UpdateProvider applies a human's edit. The registry validates the result (URL and TLS rules, tier and class values).
func (c *Core) UpdateProvider(ctx context.Context, e ProviderEdit) (providers.Provider, error) {
	p, err := c.Providers.Get(ctx, e.ID)
	if err != nil {
		return providers.Provider{}, err
	}
	if e.Name != nil {
		if strings.TrimSpace(*e.Name) == "" {
			return providers.Provider{}, errors.New("a provider needs a name")
		}
		p.Name = strings.TrimSpace(*e.Name)
	}
	if e.BaseURL != nil {
		p.BaseURL = strings.TrimSpace(*e.BaseURL)
	}
	if e.Tier != nil {
		p.Tier = providers.Tier(*e.Tier)
	}
	if e.DataClass != nil {
		p.DataClass = providers.DataClass(*e.DataClass)
	}
	if e.MaxConcurrent != nil {
		if *e.MaxConcurrent < 0 || *e.MaxConcurrent > 1000 {
			return providers.Provider{}, errors.New("max concurrent agents must be between 0 (unlimited) and 1000")
		}
		p.MaxConcurrent = *e.MaxConcurrent
	}
	if e.HourlyCost != nil {
		if *e.HourlyCost < 0 || math.IsNaN(*e.HourlyCost) || math.IsInf(*e.HourlyCost, 0) {
			return providers.Provider{}, errors.New("the hourly cost must be zero or more")
		}
		p.HourlyCost = *e.HourlyCost
	}
	return c.Providers.Update(ctx, p, strings.TrimSpace(e.APIKey))
}

// PriceEdit sets a model's price in USD per million tokens. Catalog refreshes never overwrite it afterwards.
type PriceEdit struct {
	ProviderID     string  `json:"providerId"`
	Model          string  `json:"model"`
	InputPerM      float64 `json:"inputPerM"`
	OutputPerM     float64 `json:"outputPerM"`
	CacheReadPerM  float64 `json:"cacheReadPerM"`
	CacheWritePerM float64 `json:"cacheWritePerM"`
}

// SetModelPrice records a human-edited price.
func (c *Core) SetModelPrice(ctx context.Context, p PriceEdit) error {
	for name, v := range map[string]float64{"input": p.InputPerM, "output": p.OutputPerM, "cache read": p.CacheReadPerM, "cache write": p.CacheWritePerM} {
		if v < 0 || v > 10000 || math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("the %s price must be between 0 and 10000 USD per million tokens", name)
		}
	}
	return c.Providers.SetUserPrice(ctx, p.ProviderID, p.Model, p.InputPerM, p.OutputPerM, p.CacheReadPerM, p.CacheWritePerM)
}

// QuotaEdit configures a quota window the provider does not publish (Gemini's per-model limits, for example: decision D13).
type QuotaEdit struct {
	ProviderID string  `json:"providerId"`
	Model      string  `json:"model"`
	Kind       string  `json:"kind"`
	Limit      float64 `json:"limit"`
}

// SetQuota configures a window. The human's configured limit replaces an earlier configured one.
func (c *Core) SetQuota(ctx context.Context, q QuotaEdit) error {
	ledger := economicsLedger(c)
	if ledger == nil {
		return errors.New("the quota ledger is not running")
	}
	if _, err := c.Providers.Get(ctx, q.ProviderID); err != nil {
		return err
	}
	switch providers.WindowKind(q.Kind) {
	case providers.WindowRPM, providers.WindowRPD, providers.WindowTPM, providers.WindowTPD, providers.WindowITPM, providers.WindowOTPM, providers.WindowCredits:
	default:
		return fmt.Errorf("unknown quota window %q", q.Kind)
	}
	if !(q.Limit > 0) || math.IsInf(q.Limit, 0) {
		return errors.New("a quota limit must be a positive number")
	}
	return ledger.Configure(ctx, providers.Window{ProviderID: q.ProviderID, Model: strings.TrimSpace(q.Model), Kind: providers.WindowKind(q.Kind), Limit: q.Limit,
		Source: providers.SourceConfigured, UpdatedAt: time.Now().UTC()})
}
