package core_test

import (
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/core"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/requirements"
	"github.com/kjmaguire/drydock/internal/room"
)

// X45: a crew the app made has a "planner" (the Planner starter) or none; Propose a plan picks the planner, or falls back to
// the lead and then to anyone (the turn is read-only) and says so; an empty crew is refused with what to do.
func TestPlanProposeFindsAPlannerOnTheCrewTheAppMade(t *testing.T) {
	a, _ := openDev(t, devScript())
	repo := newGreenRepo(t)
	var p, q, empty core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	a.agent(p.ID, "Dana", "backend")
	planner := a.agent(p.ID, "Pia", "planner")
	var prop map[string]any
	a.call("plan.propose", map[string]any{"projectId": p.ID, "goal": "Add Multiply and then Divide to gocalc"}, &prop)
	if prop["plannerAgentId"] != planner.ID || prop["fallback"] != false {
		t.Fatalf("the planner role plans: %+v", prop)
	}

	a.call("project.create", map[string]any{"name": "gocalc2", "repoPath": newGreenRepo(t)}, &q)
	dana := a.agent(q.ID, "Dana", "backend")
	a.call("plan.propose", map[string]any{"projectId": q.ID, "goal": "Add Multiply and then Divide to gocalc"}, &prop)
	if prop["plannerAgentId"] != dana.ID || prop["fallback"] != true {
		t.Fatalf("with no planner anyone on the crew plans: %+v", prop)
	}
	var view struct {
		pipeline.Plan
		PlannedBy string `json:"plannedBy"`
	}
	a.waitFor("the fallback proposal", func() bool {
		a.call("plan.get", map[string]string{"planId": prop["planId"].(string)}, &view)
		return view.Status == "PROPOSED"
	})
	if view.PlannedBy != "Dana" {
		t.Fatalf("the card says who planned: %+v", view.PlannedBy)
	}

	a.call("project.create", map[string]any{"name": "empty", "repoPath": newGreenRepo(t)}, &empty)
	if err := a.callErr("plan.propose", map[string]any{"projectId": empty.ID, "goal": "g"}); err == nil || !strings.Contains(err.Error(), "no agents yet") {
		t.Fatalf("an empty crew: %v", err)
	}
}

// X48: Propose a plan from the project's requirement documents; the plan keeps them and accepting it links every created
// task to them, so the trace shows the tasks without linking each by hand.
func TestPlanFromRequirementsLinksTheAcceptedTasks(t *testing.T) {
	a, _ := openDev(t, devScript())
	repo := newGreenRepo(t)
	var p, other core.Project
	a.call("project.create", map[string]any{"name": "gocalc", "repoPath": repo}, &p)
	a.agent(p.ID, "Priya", "pm")
	a.agent(p.ID, "Dana", "backend")
	a.agent(p.ID, "Rex", "reviewer")
	var r requirements.Requirement
	a.call("requirement.add", map[string]any{"projectId": p.ID, "title": "Calculator PRD", "text": "# Calc\n\n## Arithmetic\n\nThe calculator shall multiply and divide integers.\n"}, &r)
	a.call("project.create", map[string]any{"name": "other", "repoPath": newGreenRepo(t)}, &other)
	var foreign requirements.Requirement
	a.call("requirement.add", map[string]any{"projectId": other.ID, "title": "Other PRD", "text": "# Other\n\nText.\n"}, &foreign)
	if err := a.callErr("plan.propose", map[string]any{"projectId": p.ID, "goal": "g", "requirementIds": []string{foreign.ID}}); err == nil || !strings.Contains(err.Error(), "another project") {
		t.Fatalf("a requirement of another project: %v", err)
	}

	var prop map[string]any
	a.call("plan.propose", map[string]any{"projectId": p.ID, "goal": "Add Multiply and then Divide to gocalc", "requirementIds": []string{r.ID}}, &prop)
	planID := prop["planId"].(string)
	var plan pipeline.Plan
	a.waitFor("the proposal", func() bool {
		a.call("plan.get", map[string]string{"planId": planID}, &plan)
		return plan.Status == "PROPOSED"
	})
	if len(plan.RequirementIDs) != 1 || plan.RequirementIDs[0] != r.ID {
		t.Fatalf("the plan keeps its requirements: %+v", plan.RequirementIDs)
	}
	var links []requirements.Link
	a.call("requirement.links", map[string]any{"requirementId": r.ID}, &links)
	if len(links) != 0 {
		t.Fatal("nothing is linked before the accept")
	}
	a.waitFor("the plan review in Needs you", func() bool {
		var needs []room.NeedsYouItem
		a.call("needsyou.list", map[string]string{"projectId": p.ID}, &needs)
		return len(needs) > 0
	})
	a.call("plan.accept", map[string]any{"planId": planID}, &plan)
	var tr requirements.Trace
	a.call("requirement.trace", map[string]string{"id": r.ID}, &tr)
	got := map[string]bool{}
	for _, x := range tr.Tasks {
		got[x.TaskID] = true
	}
	if len(plan.Created) != 2 || !got[plan.Created["mul"]] || !got[plan.Created["div"]] {
		t.Fatalf("the accepted tasks are traced to the requirement: created %v trace %+v", plan.Created, tr.Tasks)
	}
}
