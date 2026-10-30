package pipeline

import (
	"context"
	"fmt"

	"github.com/kjmaguire/drydock/internal/agents/responder"
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
// The graphs (templates_graphs.go) reuse the existing nodes (Prepare, Verify, Finalize, Review, Approve, Changes, Merge) and
// add the spec gate, the test-first nodes and the security scan. Nothing in a template merges without a human's approval.

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
	// Level is the model routing level the template declares for its tasks' agent turns (docs/model-routing.md s3, decision
	// P127): a default, between a task's own level and its project's; "" declares none.
	Level string `json:"level,omitempty"`
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
		Roles:       []RoleNeed{{RoleSecurity, true, "reviews the change for security"}},
		// fixing security findings is a premium-only class of brief s6's ladder: premium work and review unless the task says
		// otherwise
		Level: "quality"},
	{Name: "full-stack-saas", Title: "Full Stack SaaS", Workflow: WorkflowSaaS, SpecGate: true, TestFirst: true,
		Description: "The Feature template plus an optional architect design step, the e2e check and a preview of the result when the project enables them, and a security scan before human approval.",
		Steps:       []string{"spec (PM)", "human accepts the spec", "design (architect, optional)", "tests first (red)", "implement (green)", "checks + e2e", "independent review", "security scan", "preview", "human approval", "merge"},
		Roles:       []RoleNeed{{RolePM, true, "writes the spec"}, {RoleTests, false, "writes the tests first (falls back to the worker)"}, {RoleArchitect, false, "writes a short design (skipped when there is no architect)"}}},
	{Name: "design-review", Title: "Design Review", Workflow: WorkflowDesignReview,
		Description: "The architect, the worker and the reviewer discuss how to build the task in its channel (at most 3 rounds) before anyone changes code. A design they agree on goes to the worker; without one, you are asked the question they ended on and your answer is the design. Then checks, an independent review and human approval.",
		Steps:       []string{"design discussion (architect, worker, reviewer)", "you settle it if they do not agree", "implement", "checks", "independent review", "human approval", "merge"},
		Roles:       []RoleNeed{{RoleArchitect, true, "leads the design discussion with the worker and the reviewer"}}},
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
	// PR is the pull request host for the templates' pr_flow variants (their names end in PRSuffix). nil: only the local
	// merge variants are registered.
	PR PRSource
	// Discuss holds a bounded group discussion (responder.Responder.Discuss) for the Design Review template, and TaskRoom
	// names a task's channel, where it is held. The core sets both; nil: the template's discussion fails, saying why.
	Discuss  func(ctx context.Context, d responder.Discussion) (responder.DiscussionResult, error)
	TaskRoom func(ctx context.Context, taskID string) (string, error)
}

// PRSuffix names a template's pull request variant: after the review the branch is pushed and a PR opened, CI failures and
// change requests go back to the worker, a human approves, and the merge goes through GitHub.
const PRSuffix = "-pr"

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
	gs := []*workflow.Graph{td.FeatureGraph(), td.BugGraph(), td.SecurityGraph(), td.SaaSGraph(), td.DesignReviewGraph()}
	if td.PR != nil {
		gs = append(gs, td.featureGraph(true), td.bugGraph(true), td.securityGraph(true), td.saasGraph(true), td.designReviewGraph(true))
	}
	return gs
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
	pr bool
	// back maps the node a loop exhausted from to where the work resumes after a continue (see addEscalation)
	back map[workflow.NodeID]workflow.NodeID
}

func newBuilder(td TemplateDeps, g *workflow.Graph, pr bool) graphBuilder {
	return graphBuilder{td: td, g: g, pr: pr, back: map[workflow.NodeID]workflow.NodeID{}}
}

// finish adds the escalation (team lead, then the human; continue or stop) and the entry point.
func (b graphBuilder) finish() *workflow.Graph {
	addEscalation(b.g, b.td.Deps, b.back)
	return b.g.Start("prepare")
}

func graphName(name string, pr bool) string {
	if pr {
		return name + PRSuffix
	}
	return name
}
