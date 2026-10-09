package pipeline

import (
	"context"

	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
)

// RoutedModel is the model the model router chose for one agent turn (internal/core/turnrouting.go). The routing middleware
// puts it in the node's context; the work, implementation and review nodes apply it over the agent's configured model.
type RoutedModel struct {
	Model      string
	Effort     string
	Tier       string
	DecisionID string
}

type routedKey struct{}

// WithRoutedModel returns ctx carrying the turn's routed model.
func WithRoutedModel(ctx context.Context, r RoutedModel) context.Context {
	return context.WithValue(ctx, routedKey{}, r)
}

// RoutedFrom returns the routed model of the turn, if the router chose one.
func RoutedFrom(ctx context.Context) (RoutedModel, bool) {
	r, ok := ctx.Value(routedKey{}).(RoutedModel)
	return r, ok && r.Model != ""
}

// applyRoute puts the routed model (and effort and tier) into the turn's config.
func applyRoute(ctx context.Context, cfg *rt.Config) {
	r, ok := RoutedFrom(ctx)
	if !ok {
		return
	}
	cfg.Spec.Model = r.Model
	if r.Effort != "" {
		cfg.Spec.Effort = r.Effort
	}
	if r.Tier != "" {
		cfg.Tier = r.Tier
	}
}
