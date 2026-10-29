package providers

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
)

// Models that need data retention (decision P112, docs/model-routing.md s8 "Fallback specifics"). Anthropic's Covered Models
// require 30-day data retention; an organization that has not enabled it gets 400 invalid_request_error "In order to access
// this model, your organization or workspace must have data retention enabled." Such a model cannot take any turn of that
// organization, so the router leaves it out (router.FilterRetention) until a person clears the mark: after enabling retention
// in the provider's console (the model then keeps prompts and outputs for 30 days, which a private route allows: the provider
// may not train on them), or to try again. A private turn then goes to a model that keeps no data.

// RetentionBlock is one model a provider refused for want of data retention.
type RetentionBlock struct {
	ProviderID string `json:"providerId"`
	Model      string `json:"model"`
	Source     string `json:"source"` // api | agent
	Message    string `json:"message"`
	SeenAt     int64  `json:"seenAt"`
}

// RetentionRefusal is what a mark says the provider answered. The provider's own text is not stored: an error body can echo
// parts of the request.
const RetentionRefusal = "the provider refused the model: the organization or workspace must have data retention enabled"

// RetentionAdvice is what a person reads about a blocked model.
const RetentionAdvice = "the provider says this model needs data retention enabled for your organization: enable it in the provider's console (the model then keeps prompts and outputs for 30 days) or pick another model; clear it on the Routing page to try again"

// MarkRetentionRequired records that providerID refused model for want of data retention. message says what happened, in
// Drydock's words (RetentionRefusal), never the provider's error body. It reports whether the mark is new.
func (r *Registry) MarkRetentionRequired(ctx context.Context, providerID, model, source, message string) (bool, error) {
	if providerID == "" || model == "" {
		return false, nil
	}
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 300 {
		message = message[:300] + "..."
	}
	var fresh bool
	err := r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO model_retention_blocks(provider_id,model,source,message,seen_at) VALUES(?,?,?,?,?)
			ON CONFLICT(provider_id,model) DO NOTHING`, providerID, model, source, message, storage.Now())
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		fresh = n > 0
		return nil
	})
	if err == nil && fresh {
		r.emit(ctx, "provider.retention_required", map[string]any{"providerId": providerID, "model": model, "source": source})
	}
	return fresh, err
}

// RetentionBlocked reports whether model of providerID is marked as needing data retention.
func (r *Registry) RetentionBlocked(ctx context.Context, providerID, model string) bool {
	var n int
	_ = r.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM model_retention_blocks WHERE provider_id=? AND model=?`, providerID, model).Scan(&n)
	return n > 0
}

// RetentionBlocks lists the marks, newest first.
func (r *Registry) RetentionBlocks(ctx context.Context) ([]RetentionBlock, error) {
	rows, err := r.db.Reader().QueryContext(ctx, `SELECT provider_id,model,source,message,seen_at FROM model_retention_blocks ORDER BY seen_at DESC, provider_id, model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RetentionBlock{}
	for rows.Next() {
		var b RetentionBlock
		if err := rows.Scan(&b.ProviderID, &b.Model, &b.Source, &b.Message, &b.SeenAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ClearRetentionBlock removes a mark (a person enabled retention, or wants the model tried again). It reports whether there
// was one.
func (r *Registry) ClearRetentionBlock(ctx context.Context, providerID, model string) (bool, error) {
	var n int64
	err := r.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM model_retention_blocks WHERE provider_id=? AND model=?`, providerID, model)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err == nil && n > 0 {
		r.emit(ctx, "provider.retention_cleared", map[string]any{"providerId": providerID, "model": model, "at": time.Now().UTC().UnixMilli()})
	}
	return n > 0, err
}
