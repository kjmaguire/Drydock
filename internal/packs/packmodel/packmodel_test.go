package packmodel

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/llm"
	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/router"
)

// TEST DOUBLE: a router that refuses every request with a fixed BlockedError, to pin how a refusal reads. The routed path
// itself is tested in internal/core against the real rule router and a protocol-fixture provider.
type refusingRouter struct{ err error }

func (r refusingRouter) Decide(context.Context, router.RouteRequest) (router.RoutingDecision, error) {
	return router.RoutingDecision{}, r.err
}
func (r refusingRouter) Next(context.Context, router.RoutingDecision, router.Cause) (router.RoutingDecision, error) {
	return router.RoutingDecision{}, r.err
}
func (refusingRouter) Explain(router.RoutingDecision) string { return "" }

func TestARefusalReadsAsNoModelOrTheRoutersReason(t *testing.T) {
	ctx := context.Background()
	req := packs.AnswerRequest{Instruction: packs.AnswerInstruction, Question: "q", Sources: []packs.AnswerSource{{N: 1, Text: "t"}}}
	// nothing set up at all
	if _, err := (Answerer{}).Answer(ctx, req); !errors.Is(err, packs.ErrNoModel) {
		t.Fatalf("%v", err)
	}
	llmNone := func() llm.Client { return nil }
	if _, err := (Answerer{Models: Models{Router: func() router.ModelRouter { return refusingRouter{} }, LLM: llmNone}}).Answer(ctx, req); !errors.Is(err, packs.ErrNoModel) {
		t.Fatalf("no client: %v", err)
	}
	var cl llm.Client = (*llm.Wrapper)(nil) // any non-nil client: the router refuses before it is called
	for reason, want := range map[*router.BlockedError]error{
		{Reason: "no candidate providers or models are registered"}:                                                                              packs.ErrNoModel,
		{Reason: "no candidate passes the invariants (data class, health, quota, capability)", Alternatives: []router.Alternative{{Model: "m"}}}: packs.ErrRouteBlocked,
		{Reason: "the project budget is used up", Budget: true}:                                                                                  packs.ErrRouteBlocked,
	} {
		m := Models{Router: func() router.ModelRouter { return refusingRouter{err: reason} }, LLM: func() llm.Client { return cl }}
		_, err := (Answerer{Models: m}).Answer(ctx, req)
		if !errors.Is(err, want) {
			t.Fatalf("%s: %v", reason.Reason, err)
		}
		if want == packs.ErrRouteBlocked && !strings.Contains(err.Error(), reason.Reason) {
			t.Fatalf("the router's reason is kept: %v", err)
		}
		if _, err := (Summarizer{Models: m}).Summarize(ctx, packs.Digest{PackID: "p"}, false); !errors.Is(err, want) {
			t.Fatalf("summarizer %s: %v", reason.Reason, err)
		}
	}
}
