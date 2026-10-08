package providers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
)

// UpsertModels writes listing results into model_catalog. Prices of rows whose price_source is "user" are never
// overwritten (the user's value wins, docs/token-control.md s9). Models the listing no longer contains are kept.
func (r *Registry) UpsertModels(ctx context.Context, providerID string, models []Model) error {
	now := storage.Now()
	return r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`INSERT INTO model_catalog(provider_id,model,context_length,supports_tools,supports_reasoning,
			quality_tier,data_class,tier,input_price_per_m,output_price_per_m,cached_price_per_m,price_updated_at,max_output,
			supported_parameters,is_free,cache_write_price_per_m,price_source,source,fetched_at)
			VALUES(?,?,?,?,?,'unknown',?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(provider_id,model) DO UPDATE SET
			 context_length=excluded.context_length, supports_tools=excluded.supports_tools,
			 supports_reasoning=excluded.supports_reasoning, data_class=excluded.data_class, tier=excluded.tier,
			 max_output=excluded.max_output, supported_parameters=excluded.supported_parameters, is_free=excluded.is_free,
			 source=excluded.source, fetched_at=excluded.fetched_at,
			 input_price_per_m=CASE WHEN model_catalog.price_source='user' THEN model_catalog.input_price_per_m ELSE excluded.input_price_per_m END,
			 output_price_per_m=CASE WHEN model_catalog.price_source='user' THEN model_catalog.output_price_per_m ELSE excluded.output_price_per_m END,
			 cached_price_per_m=CASE WHEN model_catalog.price_source='user' THEN model_catalog.cached_price_per_m ELSE excluded.cached_price_per_m END,
			 cache_write_price_per_m=CASE WHEN model_catalog.price_source='user' THEN model_catalog.cache_write_price_per_m ELSE excluded.cache_write_price_per_m END,
			 price_updated_at=CASE WHEN model_catalog.price_source='user' THEN model_catalog.price_updated_at ELSE excluded.price_updated_at END,
			 price_source=CASE WHEN model_catalog.price_source='user' THEN 'user' ELSE excluded.price_source END`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, m := range models {
			if m.ID == "" {
				continue
			}
			ps := m.PriceSource
			if ps == "" {
				ps = "catalog"
			}
			src := m.Source
			if src == "" {
				src = "listing"
			}
			params := m.SupportedParameters
			if params == nil {
				params = []string{}
			}
			pj, _ := json.Marshal(params)
			if _, err := stmt.Exec(providerID, m.ID, m.ContextLength, b2i(m.SupportsTools), b2i(m.SupportsReasoning),
				string(m.DataClass), string(m.Tier), m.InputPerM, m.OutputPerM, m.CacheReadPerM, now, m.MaxOutput, string(pj),
				b2i(m.IsFree), m.CacheWritePerM, ps, src, now); err != nil {
				return err
			}
		}
		return nil
	})
}

const modelCols = `provider_id,model,context_length,max_output,supports_tools,supports_reasoning,data_class,tier,is_free,
input_price_per_m,output_price_per_m,cached_price_per_m,cache_write_price_per_m,price_source,price_updated_at,supported_parameters,
source,fetched_at,probe_status,probe_at`

func scanModel(s scanner) (Model, error) {
	var m Model
	var tools, reasoning, free int
	var pu, fa, pa int64
	var params string
	err := s.Scan(&m.ProviderID, &m.ID, &m.ContextLength, &m.MaxOutput, &tools, &reasoning, &m.DataClass, &m.Tier, &free,
		&m.InputPerM, &m.OutputPerM, &m.CacheReadPerM, &m.CacheWritePerM, &m.PriceSource, &pu, &params, &m.Source, &fa,
		&m.ProbeStatus, &pa)
	if err != nil {
		return Model{}, err
	}
	m.SupportsTools, m.SupportsReasoning, m.IsFree = tools != 0, reasoning != 0, free != 0
	m.PriceUpdatedAt, m.FetchedAt, m.ProbeAt = fromMS(pu), fromMS(fa), fromMS(pa)
	_ = json.Unmarshal([]byte(params), &m.SupportedParameters)
	return m, nil
}

// Model returns one catalog row.
func (r *Registry) Model(ctx context.Context, providerID, model string) (Model, error) {
	m, err := scanModel(r.db.Reader().QueryRowContext(ctx, `SELECT `+modelCols+` FROM model_catalog WHERE provider_id=? AND model=?`, providerID, model))
	if errors.Is(err, sql.ErrNoRows) {
		return Model{}, fmt.Errorf("%w: model %s on provider %s", ErrNotFound, model, providerID)
	}
	return m, err
}

// Models lists a provider's catalog rows ordered by id.
func (r *Registry) Models(ctx context.Context, providerID string) ([]Model, error) {
	rows, err := r.db.Reader().QueryContext(ctx, `SELECT `+modelCols+` FROM model_catalog WHERE provider_id=? ORDER BY model`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Model
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetUserPrice records a user-edited price (USD per million tokens); catalog refreshes never overwrite it.
func (r *Registry) SetUserPrice(ctx context.Context, providerID, model string, in, out, cacheRead, cacheWrite float64) error {
	now := storage.Now()
	var n int64
	err := r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE model_catalog SET input_price_per_m=?, output_price_per_m=?, cached_price_per_m=?,
			cache_write_price_per_m=?, price_source='user', price_updated_at=? WHERE provider_id=? AND model=?`,
			in, out, cacheRead, cacheWrite, now, providerID, model)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: model %s on provider %s", ErrNotFound, model, providerID)
	}
	return nil
}

// SetProbeStatus records the outcome of a tool-call probe for a model (docs/model-routing.md s5).
func (r *Registry) SetProbeStatus(ctx context.Context, providerID, model, status string, at time.Time) error {
	return r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE model_catalog SET probe_status=?, probe_at=? WHERE provider_id=? AND model=?`, status, ms(at), providerID, model)
		return err
	})
}

// EffectiveDataClass is the class a request to (provider, model) must be treated as: trainable when either the
// provider or the catalog row says so (an OpenRouter ":free" model is trainable even on a private aggregator route).
func (r *Registry) EffectiveDataClass(ctx context.Context, p Provider, model string) DataClass {
	if p.DataClass == DataTrainable {
		return DataTrainable
	}
	if p.Flavor == FlavorOpenRouter && (strings.HasSuffix(model, ":free") || model == "openrouter/free") {
		return DataTrainable // free OpenRouter endpoints may train or publish (MP A.10); true even before the catalog is fetched
	}
	if m, err := r.Model(ctx, p.ID, model); err == nil && m.DataClass == DataTrainable {
		return DataTrainable
	}
	return p.DataClass
}
