package workflow_test

import (
	"context"
	"strings"
	"testing"

	wf "github.com/kjmaguire/drydock/internal/workflow"
)

// The engine names its workflows (a workflow budget names one, P118), and a group node is a node like any other: its output
// goes where its route says.
func TestWorkflowsAreListedAndAGroupNodeRoutesItsOutcome(t *testing.T) {
	e := openEnv(t, t.TempDir())
	got := make(chan string, 1)
	g := wf.NewGraph("zeta").
		Add(wf.Group("discuss", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
			return wf.NodeOutput{Value: val("keep the parser"), Route: "decision"}, nil
		}),
			wf.Route("adopt", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
				got <- string(in.Inbox[0].Value)
				return wf.NodeOutput{}, nil
			}),
			wf.Route("ask", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) {
				got <- "asked"
				return wf.NodeOutput{}, nil
			})).
		EdgeWhen("discuss", "adopt", "decision").EdgeWhen("discuss", "ask", "question").Start("discuss")
	if g.Nodes["discuss"].Kind != wf.KindGroup {
		t.Fatalf("kind %q", g.Nodes["discuss"].Kind)
	}
	for _, gr := range []*wf.Graph{g, wf.Sequential("alpha", wf.Route("only", func(ctx context.Context, in wf.NodeInput) (wf.NodeOutput, error) { return wf.NodeOutput{}, nil }))} {
		if err := e.eng.Register(gr); err != nil {
			t.Fatal(err)
		}
	}
	if names := strings.Join(e.eng.Workflows(), ","); names != "alpha,zeta" {
		t.Fatalf("workflows %s", names)
	}
	run, err := e.eng.Start(context.Background(), "zeta", nil, wf.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e.eng, run, wf.StatusCompleted)
	if v := <-got; v != `"keep the parser"` {
		t.Fatalf("the next node got %s", v)
	}
}
