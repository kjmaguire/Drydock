package pipeline

import (
	"context"
	"encoding/json"
	"fmt"

	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/checks"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Workflow templates (brief section 22): Full Stack SaaS, Feature, Bug, Security (each ending in human approval) and
// Iterate Until Verified (iterate-until-verified, built in review.go). Templates set defaults, never locks: every role can
// be overridden when a run starts, the settings can switch e2e and preview on or off per project, and the human can send
// the work back at every request.
//
// The graphs below reuse the existing nodes (Prepare, Verify, Finalize, Review, Approve, Changes, Merge) and add the
// spec gate, the test-first nodes and the security scan. Nothing in a template merges without a human's approval.

// Names of the registered workflows.
const (
	WorkflowIUV      = "iterate-until-verified"
	WorkflowFeature  = "feature"
	WorkflowBug      = "bug"
	WorkflowSecurity = "security"
	WorkflowSaaS     = "full-stack-saas"
)

// Template describes one template for the UI and for role resolution.
type Template struct {
	Name        string     `json:"name"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Workflow    string     `json:"workflow"`
	SpecGate    bool       `json:"specGate"`
	TestFirst   bool       `json:"testFirst"`
	Steps       []string   `json:"steps"`
	Roles       []RoleNeed `json:"roles"` // beyond the task's worker and reviewer
}

var templates = []Template{
	{Name: "iterate-until-verified", Title: "Iterate Until Verified", Workflow: WorkflowIUV,
		Description: "Work in a fresh session, deterministic checks, a different agent reviews with SHIP or REVISE, loop with only the feedback, then human approval.",
		Steps:       []string{"work", "checks", "independent review", "human approval", "merge"}},
	{Name: "feature", Title: "Feature", Workflow: WorkflowFeature, SpecGate: true, TestFirst: true,
		Description: "A PM-role agent writes a spec with Given/When/Then criteria and a human accepts it before anything is built; tests are written first and must fail; the implementation makes them pass; an independent review; human approval.",
		Steps:       []string{"spec (PM)", "human accepts the spec", "tests first (red)", "implement (green)", "checks", "independent review", "human approval", "merge"},
		Roles:       []RoleNeed{{RolePM, true, "writes the spec"}, {RoleTests, false, "writes the tests first (falls back to the worker)"}}},
	{Name: "bug", Title: "Bug", Workflow: WorkflowBug, TestFirst: true,
		Description: "Reproduce the bug with a failing test first, then fix it until that test passes; independent review; human approval.",
		Steps:       []string{"reproduce with a failing test (red)", "fix (green)", "checks", "independent review", "human approval", "merge"},
		Roles:       []RoleNeed{{RoleTests, false, "writes the reproducing test (falls back to the worker)"}}},
	{Name: "security", Title: "Security", Workflow: WorkflowSecurity,
		Description: "Scan the tree (secrets, dependencies and any scan you plug in), fix the findings, scan again, a reviewer with the security role signs off, human approval.",
		Steps:       []string{"scan", "fix findings", "checks", "re-scan", "security review", "human approval", "merge"},
		Roles:       []RoleNeed{{RoleSecurity, true, "reviews the change for security"}}},
	{Name: "full-stack-saas", Title: "Full Stack SaaS", Workflow: WorkflowSaaS, SpecGate: true, TestFirst: true,
		Description: "The Feature template plus an optional architect design step, the e2e check and a preview of the result when the project enables them, and a security scan before human approval.",
		Steps:       []string{"spec (PM)", "human accepts the spec", "design (architect, optional)", "tests first (red)", "implement (green)", "checks + e2e", "independent review", "security scan", "preview", "human approval", "merge"},
		Roles:       []RoleNeed{{RolePM, true, "writes the spec"}, {RoleTests, false, "writes the tests first (falls back to the worker)"}, {RoleArchitect, false, "writes a short design (skipped when there is no architect)"}}},
}

// Templates lists the templates.
func Templates() []Template { return append([]Template(nil), templates...) }

// TemplateByName finds a template by its name (or its workflow name).
func TemplateByName(name string) (Template, bool) {
	for _, t := range templates {
		if t.Name == name || t.Workflow == name {
			return t, true
		}
	}
	return Template{}, false
}

// TemplateInput is the run input of the template workflows. It is a superset of Input.
type TemplateInput struct {
	TaskID string            `json:"taskId"`
	Roles  map[string]string `json:"roles,omitempty"` // template role -> agent id, resolved when the run starts and frozen with it
	Scans  []string          `json:"scans,omitempty"` // security template: names of the scan checks to run (default secrets, deps)
}

// TemplateDeps are the services the template nodes need beyond Deps.
type TemplateDeps struct {
	Deps
	Project func(ctx context.Context, projectID string) (ProjectInfo, error)
	// RoleConfig builds the runtime config for a template role (pm, tests, architect, security) and the agent id the run
	// pinned for it.
	RoleConfig func(ctx context.Context, t tasks.Task, role, agentID string) (rt.Config, error)
	// Dev returns the project's development settings (nil: defaults).
	Dev func(ctx context.Context, projectID string) DevSettings
	// Scans resolves scan checks by name (nil: DefaultScans).
	Scans *ScanRegistry
	// OnInconclusive is called for a check that was skipped or errored, so the room can show it (tool member, status skipped).
	OnInconclusive func(ctx context.Context, t tasks.Task, r checks.Result)
	// Preview starts the project's preview for a task and keeps it running; the caller owns stopping it. nil: no preview.
	Preview func(ctx context.Context, t tasks.Task, dev DevSettings) (checks.PreviewResult, error)
	// MaxLoops bounds every loop edge (default 5).
	MaxLoops int
}

func (td TemplateDeps) loops() int {
	if td.MaxLoops <= 0 {
		return 5
	}
	return td.MaxLoops
}

func (td TemplateDeps) dev(ctx context.Context, projectID string) DevSettings {
	if td.Dev != nil {
		return td.Dev(ctx, projectID)
	}
	return DefaultDevSettings()
}

func (td TemplateDeps) scans() *ScanRegistry {
	if td.Scans != nil {
		return td.Scans
	}
	return DefaultScans
}

// Graphs returns every template graph (iterate-until-verified is built by IterateUntilVerified and registered by the core).
func (td TemplateDeps) Graphs() []*workflow.Graph {
	return []*workflow.Graph{td.FeatureGraph(), td.BugGraph(), td.SecurityGraph(), td.SaaSGraph()}
}

// RegisterTemplates adds the template graphs to a running engine. It uses RegisterNew, which refuses to replace a graph
// whose topology changed under runs that are in flight.
func RegisterTemplates(e *workflow.Engine, td TemplateDeps) error {
	for _, g := range td.Graphs() {
		if err := e.RegisterNew(g); err != nil {
			return fmt.Errorf("register %s: %w", g.Name, err)
		}
	}
	return nil
}

// graphBuilder keeps the wiring of the common tail of the TDD templates in one place.
type graphBuilder struct {
	td TemplateDeps
	g  *workflow.Graph
}

func (b graphBuilder) tail(extraBeforeApprove []workflow.NodeID) {
	td, g, n := b.td, b.g, b.td.loops()
	g.Add(
		workflow.AgentTask("work", td.implement()).With("agent", "work"),
		workflow.Route("guard", Guard(td.Deps, td.projectFn())),
		workflow.Check("verify", td.verifyTDD()),
		workflow.Route("finalize", Finalize(td.Deps)),
		workflow.Route("sync", Sync(td.Deps, td.projectFn())),
		workflow.AgentTask("review", Review(td.Deps)).With("agent", "review"),
		&workflow.Node{ID: "approve", Kind: workflow.KindRequest, Handler: Approve(td.Deps)},
		workflow.Route("changes", Changes(td.Deps)),
		workflow.Route("merge", Merge(td.Deps, td.projectFn())),
		workflow.Request("limit", workflow.RequestSpec{Kind: "question", Title: "Iteration limit reached: a human needs to decide"}),
		workflow.Route("abort", td.abort()),
	)
	g.Edge("work", "guard").
		Edge("guard", "verify").
		EdgeWhen("verify", "finalize", "pass").
		Loop("verify", "work", "fail", n, "limit").
		Edge("finalize", "sync").
		EdgeWhen("sync", "review", RouteCurrent).
		EdgeWhen("sync", "merge", RouteApproved).
		Loop("sync", "verify", RouteChanged, n, "limit").
		Loop("sync", "work", RouteConflict, n, "limit").
		Loop("merge", "sync", RouteStale, n, "limit")
	next := workflow.NodeID("approve")
	if len(extraBeforeApprove) > 0 {
		next = extraBeforeApprove[0]
	}
	g.EdgeWhen("review", next, "ship").
		Loop("review", "work", "revise", n, "limit").
		EdgeWhen("review", "limit", "limit").
		EdgeWhen("approve", "merge", "approved").
		EdgeWhen("approve", "changes", "rejected").
		Loop("changes", "work", "", n, "limit").
		Loop("merge", "merge", "answered", 3, "limit").
		EdgeWhen("limit", "work", "approved").
		EdgeWhen("limit", "work", "answered").
		EdgeWhen("limit", "abort", "rejected")
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
	td, g := b.td, b.g
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
		Loop("speccheck", "spec", "invalid", 3, "limit").
		EdgeWhen("specreview", "specaccept", "approved").
		EdgeWhen("specreview", "specrevise", "rejected").
		Loop("specreview", "specreview", "answered", 3, "limit").
		EdgeWhen("specaccept", next, "ok").
		EdgeWhen("specaccept", "specrevise", "revise").
		Loop("specrevise", "spec", "", 5, "limit")
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
		Loop("testscope", id, "badscope", 3, "limit").
		EdgeWhen("red", "work", "red").
		Loop("red", id, "notred", 3, "redlimit").
		EdgeWhen("redlimit", "work", "approved").
		EdgeWhen("redlimit", "work", "answered").
		EdgeWhen("redlimit", "abort", "rejected")
}

// FeatureGraph is the Feature template: the spec gate, tests first, implementation, review, human approval.
func (td TemplateDeps) FeatureGraph() *workflow.Graph {
	g := workflow.NewGraph(WorkflowFeature).Add(workflow.Route("prepare", Prepare(td.Deps)))
	b := graphBuilder{td, g}
	b.specGate("tests")
	b.testFirst("tests", false)
	b.tail(nil)
	return g.Start("prepare")
}

// BugGraph is the Bug template: reproduce with a failing test first, then fix.
func (td TemplateDeps) BugGraph() *workflow.Graph {
	g := workflow.NewGraph(WorkflowBug).Add(workflow.Route("prepare", Prepare(td.Deps)))
	b := graphBuilder{td, g}
	b.testFirst("repro", true)
	g.Edge("prepare", "repro")
	b.tail(nil)
	return g.Start("prepare")
}

// SaaSGraph is the Full Stack SaaS template: Feature plus a design step, e2e inside verify, a security scan and a preview
// before the human approves.
func (td TemplateDeps) SaaSGraph() *workflow.Graph {
	g := workflow.NewGraph(WorkflowSaaS).Add(workflow.Route("prepare", Prepare(td.Deps)))
	b := graphBuilder{td, g}
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
		Loop("secscan", "work", "fail", td.loops(), "limit").
		Edge("preview", "approve")
	return g.Start("prepare")
}

// SecurityGraph is the Security template: scan, fix, verify, re-scan, security review, human approval.
func (td TemplateDeps) SecurityGraph() *workflow.Graph {
	n := td.loops()
	g := workflow.NewGraph(WorkflowSecurity).Add(
		workflow.Route("prepare", Prepare(td.Deps)),
		workflow.Check("scan", td.scanNode("scan", true)),
		workflow.AgentTask("work", td.implement()).With("agent", "work"),
		workflow.Route("guard", Guard(td.Deps, td.projectFn())),
		workflow.Check("verify", Verify(td.Deps)),
		workflow.Check("gate", td.scanNode("gate", false)),
		workflow.Route("finalize", Finalize(td.Deps)),
		workflow.Route("sync", Sync(td.Deps, td.projectFn())),
		workflow.AgentTask("sreview", td.securityReview()).With("agent", RoleSecurity),
		&workflow.Node{ID: "approve", Kind: workflow.KindRequest, Handler: Approve(td.Deps)},
		workflow.Route("changes", Changes(td.Deps)),
		workflow.Route("merge", Merge(td.Deps, td.projectFn())),
		workflow.Request("limit", workflow.RequestSpec{Kind: "question", Title: "Iteration limit reached: a human needs to decide"}),
		workflow.Route("abort", td.abort()),
	)
	g.Edge("prepare", "scan").
		Edge("scan", "work").
		Edge("work", "guard").
		Edge("guard", "verify").
		EdgeWhen("verify", "gate", "pass").
		Loop("verify", "work", "fail", n, "limit").
		EdgeWhen("gate", "finalize", "pass").
		Loop("gate", "work", "fail", n, "limit").
		Edge("finalize", "sync").
		EdgeWhen("sync", "sreview", RouteCurrent).
		EdgeWhen("sync", "merge", RouteApproved).
		Loop("sync", "verify", RouteChanged, n, "limit").
		Loop("sync", "work", RouteConflict, n, "limit").
		Loop("merge", "sync", RouteStale, n, "limit").
		EdgeWhen("sreview", "approve", "ship").
		Loop("sreview", "work", "revise", n, "limit").
		EdgeWhen("sreview", "limit", "limit").
		EdgeWhen("approve", "merge", "approved").
		EdgeWhen("approve", "changes", "rejected").
		Loop("changes", "work", "", n, "limit").
		Loop("merge", "merge", "answered", 3, "limit").
		EdgeWhen("limit", "work", "approved").
		EdgeWhen("limit", "work", "answered").
		EdgeWhen("limit", "abort", "rejected")
	return g.Start("prepare")
}

// abort cancels the task when a human rejects a "limit" question: nothing further runs.
func (td TemplateDeps) abort() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		h := humanResponse(in)
		by := h.by
		if len(by) < 6 || by[:6] != "human:" {
			return workflow.NodeOutput{}, fmt.Errorf("abort needs a human decision; got %q", h.by)
		}
		if _, err := td.Tasks.Transition(ctx, t.ID, tasks.Cancelled, tasks.TransitionOpts{By: by, Reason: "stopped by the human at a limit"}); err != nil {
			return workflow.NodeOutput{}, err
		}
		b, _ := json.Marshal(map[string]any{"cancelled": true, "by": by})
		return workflow.NodeOutput{Value: b}, nil
	}
}
