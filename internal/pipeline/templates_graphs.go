package pipeline

import (
	"context"
	"fmt"

	"github.com/kjmaguire/drydock/internal/workflow"
)

// The template graphs. Every loop is bounded and exhausts into the escalation (escalation.go): the team lead first, when
// the project has one, then the human, who continues (a fresh loop budget, optional guidance) or stops the task.

func (b graphBuilder) tail(extraBeforeApprove []workflow.NodeID) {
	if b.pr {
		b.prTail(extraBeforeApprove)
		return
	}
	td, g, n, esc := b.td, b.g, b.td.loops(), NodeEscalate
	g.Add(
		workflow.AgentTask("work", td.implement()).With("agent", "work"),
		workflow.Route("guard", Guard(td.Deps, td.projectFn())),
		workflow.Check("verify", td.verifyTDD()),
		workflow.Route("finalize", Finalize(td.Deps)),
		workflow.Route("sync", Sync(td.Deps, td.projectFn())),
		workflow.AgentTask("review", Review(td.Deps)).With("agent", "review"),
		&workflow.Node{ID: "approve", Kind: workflow.KindRequest, Handler: Approve(td.Deps)},
		workflow.Route("changes", Changes(td.Deps)),
		workflow.Route("merge", Merge(td.Deps, td.projectFn())).Effect(EffectMerge),
		workflow.Route(NodeAbort, Abort(td.Deps)),
	)
	g.Edge("work", "guard").
		Edge("guard", "verify").
		EdgeWhen("verify", "finalize", "pass").
		Loop("verify", "work", "fail", n, esc).
		Edge("finalize", "sync").
		EdgeWhen("sync", "review", RouteCurrent).
		EdgeWhen("sync", "merge", RouteApproved).
		Loop("sync", "verify", RouteChanged, n, esc).
		Loop("sync", "work", RouteConflict, n, esc).
		Loop("merge", "sync", RouteStale, n, esc).
		Loop("merge", "verify", RouteChanged, n, esc)
	next := workflow.NodeID("approve")
	if len(extraBeforeApprove) > 0 {
		next = extraBeforeApprove[0]
	}
	g.EdgeWhen("review", next, "ship").
		Loop("review", "work", "revise", n, esc).
		EdgeWhen("review", esc, "limit").
		EdgeWhen("review", esc, RouteStuck).
		EdgeWhen("approve", "merge", "approved").
		EdgeWhen("approve", "changes", "rejected").
		Loop("changes", "work", "", n, esc).
		Loop("merge", "merge", "answered", 3, esc)
	b.back["merge"] = "sync"
}

// prTail is the pull request variant of the tail: after the review the branch is pushed and a PR opened (push_pr), CI
// runs (ci: a failure goes back to the worker), the PR's review comments are read (pr_feedback: a change request goes back
// to the worker), then the template's extra steps, then the human's approval, and the merge goes through GitHub (merge).
// The same nodes as the default workflow's pr_flow variant (IterateUntilVerifiedPR).
func (b graphBuilder) prTail(extraBeforeApprove []workflow.NodeID) {
	td, g, n, esc := b.td, b.g, b.td.loops(), NodeEscalate
	g.Add(
		workflow.AgentTask("work", td.implement()).With("agent", "work"),
		workflow.Route("guard", Guard(td.Deps, td.projectFn())),
		workflow.Check("verify", td.verifyTDD()),
		workflow.Route("finalize", Finalize(td.Deps)),
		workflow.AgentTask("review", Review(td.Deps)).With("agent", "review"),
		workflow.Route("push_pr", PushPR(td.Deps, td.PR)).Effect(EffectPush),
		workflow.Check("ci", CI(td.Deps, td.PR)),
		workflow.Route("pr_feedback", PRFeedback(td.Deps, td.PR)),
		&workflow.Node{ID: "approve", Kind: workflow.KindRequest, Handler: Approve(td.Deps)},
		workflow.Route("changes", Changes(td.Deps)),
		workflow.Route("merge", MergePR(td.Deps, td.PR)).Effect(EffectMergePR),
		workflow.Route(NodeAbort, Abort(td.Deps)),
	)
	next := workflow.NodeID("approve")
	if len(extraBeforeApprove) > 0 {
		next = extraBeforeApprove[0]
	}
	g.Edge("work", "guard").
		Edge("guard", "verify").
		EdgeWhen("verify", "finalize", "pass").
		Loop("verify", "work", "fail", n, esc).
		Edge("finalize", "review")
	b.prSteps("review", next)
	g.EdgeWhen("approve", "merge", "approved").
		EdgeWhen("approve", "changes", "rejected").
		Loop("changes", "work", "", n, esc).
		Loop("merge", "merge", "answered", 3, esc)
	b.back["merge"] = "approve"
}

// prSteps wires review -> push_pr -> ci -> pr_feedback -> next, with the loops back to the worker.
func (b graphBuilder) prSteps(review, next workflow.NodeID) {
	g, n, esc := b.g, b.td.loops(), NodeEscalate
	g.EdgeWhen(review, "push_pr", "ship").
		Loop(review, "work", "revise", n, esc).
		EdgeWhen(review, esc, "limit").
		EdgeWhen(review, esc, RouteStuck).
		Edge("push_pr", "ci").
		EdgeWhen("ci", "pr_feedback", "pass").
		Loop("ci", "work", "fail", n, esc).
		EdgeWhen("ci", esc, "limit").
		EdgeWhen("pr_feedback", next, "clean").
		Loop("pr_feedback", "work", "revise", n, esc).
		EdgeWhen("pr_feedback", esc, "limit")
}

func (td TemplateDeps) projectFn() func(ctx context.Context, projectID string) (ProjectInfo, error) {
	if td.Project != nil {
		return td.Project
	}
	return func(context.Context, string) (ProjectInfo, error) {
		return ProjectInfo{}, fmt.Errorf("pipeline: the template has no project resolver")
	}
}

// specGate wires prepare -> spec -> check -> human review -> accept, and returns the node the accepted spec continues to.
func (b graphBuilder) specGate(next workflow.NodeID) {
	td, g, esc := b.td, b.g, NodeEscalate
	g.Add(
		workflow.AgentTask("spec", td.specDraft()).With("agent", RolePM),
		workflow.Route("speccheck", td.specCheck()),
		&workflow.Node{ID: "specreview", Kind: workflow.KindRequest, Handler: td.specReview()},
		workflow.Route("specaccept", td.specAccept()),
		workflow.Route("specrevise", td.specRevise()),
	)
	g.Edge("prepare", "spec").
		Edge("spec", "speccheck").
		EdgeWhen("speccheck", "specreview", "valid").
		Loop("speccheck", "spec", "invalid", 3, esc).
		EdgeWhen("specreview", "specaccept", "approved").
		EdgeWhen("specreview", "specrevise", "rejected").
		Loop("specreview", "specreview", "answered", 3, esc).
		EdgeWhen("specaccept", next, "ok").
		EdgeWhen("specaccept", "specrevise", "revise").
		Loop("specrevise", "spec", "", 5, esc)
	// a spec loop that ran out resumes at the spec, never past the gate
	for _, n := range []workflow.NodeID{"speccheck", "specreview", "specrevise"} {
		b.back[n] = "spec"
	}
}

// testFirst wires tests -> scope check -> red check -> work (the implementation).
func (b graphBuilder) testFirst(id workflow.NodeID, bug bool) {
	td, g := b.td, b.g
	g.Add(
		workflow.AgentTask(id, td.writeTests(bug)).With("agent", RoleTests),
		workflow.Route("testscope", td.testScope(!bug)),
		workflow.Check("red", td.redCheck()),
		workflow.Request("redlimit", workflow.RequestSpec{Kind: "question", Title: "The tests do not fail before the implementation: a human needs to decide (approve to build without red evidence)"}),
	)
	g.Edge(id, "testscope").
		EdgeWhen("testscope", "red", "ok").
		Loop("testscope", id, "badscope", 3, NodeEscalate).
		EdgeWhen("red", "work", "red").
		Loop("red", id, "notred", 3, "redlimit").
		EdgeWhen("redlimit", "work", "approved").
		EdgeWhen("redlimit", "work", "answered").
		EdgeWhen("redlimit", NodeAbort, "rejected")
	b.back["testscope"] = id
}

// FeatureGraph is the Feature template: the spec gate, tests first, implementation, review, human approval.
func (td TemplateDeps) FeatureGraph() *workflow.Graph { return td.featureGraph(false) }

func (td TemplateDeps) featureGraph(pr bool) *workflow.Graph {
	g := workflow.NewGraph(graphName(WorkflowFeature, pr)).Add(workflow.Route("prepare", Prepare(td.Deps)))
	b := newBuilder(td, g, pr)
	b.specGate("tests")
	b.testFirst("tests", false)
	b.tail(nil)
	return b.finish()
}

// BugGraph is the Bug template: reproduce with a failing test first, then fix.
func (td TemplateDeps) BugGraph() *workflow.Graph { return td.bugGraph(false) }

func (td TemplateDeps) bugGraph(pr bool) *workflow.Graph {
	g := workflow.NewGraph(graphName(WorkflowBug, pr)).Add(workflow.Route("prepare", Prepare(td.Deps)))
	b := newBuilder(td, g, pr)
	b.testFirst("repro", true)
	g.Edge("prepare", "repro")
	b.tail(nil)
	return b.finish()
}

// SaaSGraph is the Full Stack SaaS template: Feature plus a design step, e2e inside verify, a security scan and a preview
// before the human approves.
func (td TemplateDeps) SaaSGraph() *workflow.Graph { return td.saasGraph(false) }

func (td TemplateDeps) saasGraph(pr bool) *workflow.Graph {
	g := workflow.NewGraph(graphName(WorkflowSaaS, pr)).Add(workflow.Route("prepare", Prepare(td.Deps)))
	b := newBuilder(td, g, pr)
	g.Add(workflow.AgentTask("design", td.design()).With("agent", RoleArchitect))
	b.specGate("design")
	g.Edge("design", "tests")
	b.testFirst("tests", false)
	g.Add(
		workflow.Check("secscan", td.scanNode("secscan", false)),
		workflow.Route("preview", td.previewNode()),
	)
	b.tail([]workflow.NodeID{"secscan"})
	g.EdgeWhen("secscan", "preview", "pass").
		Loop("secscan", "work", "fail", td.loops(), NodeEscalate).
		Edge("preview", "approve")
	return b.finish()
}

// SecurityGraph is the Security template: scan, fix, verify, re-scan, security review, human approval.
func (td TemplateDeps) SecurityGraph() *workflow.Graph { return td.securityGraph(false) }

func (td TemplateDeps) securityGraph(pr bool) *workflow.Graph {
	n, esc := td.loops(), NodeEscalate
	g := workflow.NewGraph(graphName(WorkflowSecurity, pr)).Add(
		workflow.Route("prepare", Prepare(td.Deps)),
		workflow.Check("scan", td.scanNode("scan", true)),
		workflow.AgentTask("work", td.implement()).With("agent", "work"),
		workflow.Route("guard", Guard(td.Deps, td.projectFn())),
		workflow.Check("verify", Verify(td.Deps)),
		workflow.Check("gate", td.scanNode("gate", false)),
		workflow.Route("finalize", Finalize(td.Deps)),
		workflow.AgentTask("sreview", td.securityReview()).With("agent", RoleSecurity),
		&workflow.Node{ID: "approve", Kind: workflow.KindRequest, Handler: Approve(td.Deps)},
		workflow.Route("changes", Changes(td.Deps)),
		workflow.Route(NodeAbort, Abort(td.Deps)),
	)
	b := newBuilder(td, g, pr)
	g.Edge("prepare", "scan").
		Edge("scan", "work").
		Edge("work", "guard").
		Edge("guard", "verify").
		EdgeWhen("verify", "gate", "pass").
		Loop("verify", "work", "fail", n, esc).
		EdgeWhen("gate", "finalize", "pass").
		Loop("gate", "work", "fail", n, esc)
	if pr {
		g.Add(
			workflow.Route("push_pr", PushPR(td.Deps, td.PR)).Effect(EffectPush),
			workflow.Check("ci", CI(td.Deps, td.PR)),
			workflow.Route("pr_feedback", PRFeedback(td.Deps, td.PR)),
			workflow.Route("merge", MergePR(td.Deps, td.PR)).Effect(EffectMergePR),
		)
		g.Edge("finalize", "sreview")
		b.prSteps("sreview", "approve")
		b.back["merge"] = "approve"
	} else {
		g.Add(
			workflow.Route("sync", Sync(td.Deps, td.projectFn())),
			workflow.Route("merge", Merge(td.Deps, td.projectFn())).Effect(EffectMerge),
		)
		g.Edge("finalize", "sync").
			EdgeWhen("sync", "sreview", RouteCurrent).
			EdgeWhen("sync", "merge", RouteApproved).
			Loop("sync", "verify", RouteChanged, n, esc).
			Loop("sync", "work", RouteConflict, n, esc).
			Loop("merge", "sync", RouteStale, n, esc).
			Loop("merge", "verify", RouteChanged, n, esc).
			EdgeWhen("sreview", "approve", "ship").
			Loop("sreview", "work", "revise", n, esc).
			EdgeWhen("sreview", esc, "limit").
			EdgeWhen("sreview", esc, RouteStuck)
		b.back["merge"] = "sync"
	}
	g.EdgeWhen("approve", "merge", "approved").
		EdgeWhen("approve", "changes", "rejected").
		Loop("changes", "work", "", n, esc).
		Loop("merge", "merge", "answered", 3, esc)
	return b.finish()
}
