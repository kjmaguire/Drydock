package pipeline_test

import (
	"context"
	"sync"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The template role turns run on the model the router put in the node's context (decision P63), as work and review turns
// do: the pm's spec, the tests written first, the architect's design and the security review. The router here is a TEST
// DOUBLE middleware that routes each role to a model named after it; the core's real router is tested in internal/core.

// modelLog is a TEST DOUBLE adapter wrapper: it records the model of every session an agent starts, then starts it on the
// real (fake-agent) adapter.
type modelLog struct {
	agents.Adapter
	mu  sync.Mutex
	got map[string][]string
}

func (m *modelLog) Start(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (agents.Session, error) {
	m.mu.Lock()
	m.got[spec.AgentID] = append(m.got[spec.AgentID], spec.Model)
	m.mu.Unlock()
	return m.Adapter.Start(ctx, pm, spec)
}

func (m *modelLog) models(agentID string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.got[agentID]...)
}

// routeByRole installs the TEST DOUBLE router: every agent_task node runs on "routed-<role>" with a cheap tier.
func routeByRole(e *tpl) *modelLog {
	ml := &modelLog{Adapter: e.td.Runtime.Adapters["claude"], got: map[string][]string{}}
	e.td.Runtime.Adapters["claude"] = ml
	e.eng.Use(workflow.KindAgentTask, func(next workflow.NodeHandler) workflow.NodeHandler {
		return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			return next(pipeline.WithRoutedModel(ctx, pipeline.RoutedModel{Model: "routed-" + in.Meta["agent"], Tier: "cheap", DecisionID: "dec_test"}), in)
		}
	})
	return ml
}

func wantModels(t *testing.T, ml *modelLog, agentID string, want string) {
	t.Helper()
	got := ml.models(agentID)
	if len(got) == 0 {
		t.Errorf("%s did not run", agentID)
	}
	for _, m := range got {
		if m != want {
			t.Errorf("%s ran on %v, want %s", agentID, got, want)
			return
		}
	}
}

// The Full Stack SaaS template: the pm, the architect, the test writer and the worker each run on their routed model.
func TestTemplateRoleTurnsRunOnTheRoutedModel(t *testing.T) {
	sc := featureScripts()
	sc[aArch] = fakeagent.Script{Default: []fakeagent.Step{say("Touch calc.go only; add Multiply next to Add.")}}
	e := newTpl(t, sc, tplOpts{})
	ml := routeByRole(e)
	roles := featureRoles()
	roles[pipeline.RoleArchitect] = aArch
	id := e.start(pipeline.WorkflowSaaS, roles)
	e.await(id, workflow.StatusWaitingHuman)
	if err := e.respond(id, "approved", "human:kyle", `{}`); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusWaitingHuman)
	if p := e.pending(id); p.NodeID != "approve" {
		t.Fatalf("pending %+v", p)
	}
	wantModels(t, ml, aPM, "routed-"+pipeline.RolePM)
	wantModels(t, ml, aArch, "routed-"+pipeline.RoleArchitect)
	wantModels(t, ml, aQA, "routed-"+pipeline.RoleTests)
	wantModels(t, ml, aWork, "routed-work")
	wantModels(t, ml, aReview, "routed-review")
}

// The Security template: the security reviewer runs on its routed model, and its review record names that model (it used
// to name the agent's configured model whatever ran).
func TestTheSecurityReviewRunsOnTheRoutedModelAndRecordsIt(t *testing.T) {
	sc := map[string]fakeagent.Script{
		aWork:   {Default: []fakeagent.Step{say("nothing to do")}},
		aSec:    shipReviewer(),
		aReview: shipReviewer(),
	}
	e := newTpl(t, sc, tplOpts{title: "Security audit", desc: "Audit the repository for leaked secrets."})
	ml := routeByRole(e)
	id := e.start(pipeline.WorkflowSecurity, map[string]string{pipeline.RoleSecurity: aSec}, "secrets")
	e.await(id, workflow.StatusWaitingHuman)
	if p := e.pending(id); p.NodeID != "approve" {
		t.Fatalf("pending %+v", p)
	}
	wantModels(t, ml, aSec, "routed-"+pipeline.RoleSecurity)
	rv, _ := e.tasks.Reviews(context.Background(), e.task.ID)
	if len(rv) != 1 || rv[0].Reviewer != aSec || rv[0].Model != "routed-"+pipeline.RoleSecurity {
		t.Fatalf("reviews %+v", rv)
	}
}
