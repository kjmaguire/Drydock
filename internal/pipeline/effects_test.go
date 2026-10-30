package pipeline_test

import (
	"context"
	"testing"

	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// A replay asks before every step that merges, pushes or creates tasks (P123): each such node of every task workflow carries
// what it changes, and no other node does.
func TestEveryStepThatChangesSomethingOutsideTheRunIsMarked(t *testing.T) {
	src := pipeline.PRSource(func(ctx context.Context, projectID string) (pipeline.PRConfig, error) {
		return pipeline.PRConfig{}, nil
	})
	graphs := []*workflow.Graph{
		pipeline.IterateUntilVerified(pipeline.Deps{}, nil, 5),
		pipeline.IterateUntilVerifiedPR(pipeline.Deps{}, src, 5),
		pipeline.TaskPipeline(pipeline.Deps{}, 5),
		pipeline.PlanDeps{}.PlanGraph(),
	}
	graphs = append(graphs, pipeline.TemplateDeps{PR: src}.Graphs()...)
	want := map[workflow.NodeID]bool{"merge": true, "push_pr": true, "plancreate": true}
	marked := map[workflow.NodeID]int{}
	for _, g := range graphs {
		for id, n := range g.Nodes {
			got := n.Meta[workflow.MetaEffect] != ""
			if got != want[id] {
				t.Errorf("%s: node %s marked %v, want %v", g.Name, id, got, want[id])
			}
			if got {
				marked[id]++
			}
		}
	}
	if marked["merge"] < 8 || marked["push_pr"] < 5 || marked["plancreate"] != 1 {
		t.Fatalf("marked %v", marked)
	}
}

// No shipped workflow runs a subworkflow (decision P127, docs/workflow-engine.md s14): a nested run belongs to no task, so an
// agent turn inside one would have no safe point before it and would escape the parent run's and its workflow's budgets. The
// one group discussion, Design Review's, is a node of its template's own graph, held to that workflow's budget. A template that
// nests a graph must first give the nested run the parent's task; this test is the reminder.
func TestNoShippedTemplateRunsASubworkflow(t *testing.T) {
	src := pipeline.PRSource(func(ctx context.Context, projectID string) (pipeline.PRConfig, error) {
		return pipeline.PRConfig{}, nil
	})
	graphs := []*workflow.Graph{
		pipeline.IterateUntilVerified(pipeline.Deps{}, nil, 5),
		pipeline.IterateUntilVerifiedPR(pipeline.Deps{}, src, 5),
		pipeline.TaskPipeline(pipeline.Deps{}, 5),
		pipeline.PlanDeps{}.PlanGraph(),
	}
	graphs = append(graphs, pipeline.TemplateDeps{PR: src}.Graphs()...)
	groups := 0
	for _, g := range graphs {
		for id, n := range g.Nodes {
			if n.Kind == workflow.KindSubworkflow || n.Sub != nil {
				t.Errorf("%s: node %s runs a subworkflow", g.Name, id)
			}
			if n.Kind == workflow.KindGroup {
				groups++
				if g.Name != pipeline.WorkflowDesignReview && g.Name != pipeline.WorkflowDesignReview+pipeline.PRSuffix {
					t.Errorf("%s: a discussion outside the Design Review template", g.Name)
				}
			}
		}
	}
	if groups != 2 {
		t.Fatalf("%d discussion nodes, want Design Review's two variants", groups)
	}
}
