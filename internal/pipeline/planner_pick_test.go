package pipeline_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// X45: the crews the app makes have a "planner" (the Planner starter) or no planner at all; a plan still has someone to draft
// it, in the order pm or planner, architect, lead, anyone, and the lookup says when it fell back.
func TestPickPlannerFallsBackToTheLeadThenAnyone(t *testing.T) {
	m := func(id, role string) pipeline.TeamMember { return pipeline.TeamMember{ID: id, Role: role} }
	cases := []struct {
		name     string
		team     []pipeline.TeamMember
		want     string
		fallback bool
	}{
		{"the Planner starter's role", []pipeline.TeamMember{m("b", "backend"), m("p", "planner")}, "p", false},
		{"pm before architect", []pipeline.TeamMember{m("a", "architect"), m("p", "Product Manager")}, "p", false},
		{"architect", []pipeline.TeamMember{m("b", "backend"), m("a", "architect")}, "a", false},
		{"the lead", []pipeline.TeamMember{m("b", "backend"), m("l", "team lead")}, "l", true},
		{"anyone", []pipeline.TeamMember{m("b", "backend"), m("r", "reviewer")}, "b", true},
		{"nobody", nil, "", false},
	}
	for _, c := range cases {
		got, fb := pipeline.PickPlanner(c.team)
		if got != c.want || fb != c.fallback {
			t.Errorf("%s: got %q fallback %v, want %q %v", c.name, got, fb, c.want, c.fallback)
		}
	}
	if !pipeline.RoleMatches("Planner", pipeline.RolePM) || !pipeline.RoleMatches("planning", pipeline.RolePM) || pipeline.NormalizePlanRole("planner") != "pm" {
		t.Fatal("planner is a pm role")
	}
}

func TestClipRequirementsKeepsWholeCharacters(t *testing.T) {
	short := "Integers only."
	if pipeline.ClipRequirements(short) != short {
		t.Fatal("a short text is kept as it is")
	}
	long := strings.Repeat("é", 40<<10) // 80 KiB
	got := pipeline.ClipRequirements(long)
	if len(got) > 64<<10 || !utf8.ValidString(got) || !strings.HasSuffix(got, "longer than a plan holds)") {
		t.Fatalf("len %d valid %v", len(got), utf8.ValidString(got))
	}
}

// X48: a plan made from requirement documents keeps their ids, and accepting it links every created task to each of them as
// the human who accepted; nothing is linked before the accept.
func TestPlanTeamAcceptLinksTheRequirementsItWasMadeFrom(t *testing.T) {
	var mu sync.Mutex
	var links []string
	pe := newPlanEnv(t, fakeagent.Script{Default: []fakeagent.Step{say(planJSON(nil))}}, func(pd *pipeline.PlanDeps) {
		pd.LinkRequirement = func(_ context.Context, by, reqID, taskID string) error {
			mu.Lock()
			defer mu.Unlock()
			links = append(links, by+"|"+reqID+"|"+taskID)
			return nil
		}
	})
	pi, err := pipeline.NewPlanFrom(context.Background(), pe.db, "p1", "Add multiplication and division", "## PRD\n\nIntegers only.", aPM, []string{"req_a", "req_b"})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := pipeline.GetPlan(context.Background(), pe.db, pi.PlanID)
	if strings.Join(stored.RequirementIDs, ",") != "req_a,req_b" {
		t.Fatalf("the plan keeps its requirements: %+v", stored.RequirementIDs)
	}
	if _, err := pipeline.NewPlanFrom(context.Background(), pe.db, "p1", "g", "", aPM, make([]string, 21)); err == nil {
		t.Fatal("too many documents are refused")
	}
	id, err := pe.eng.Start(context.Background(), pipeline.WorkflowPlan, pi, workflow.StartOptions{ProjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	pe.await(id, workflow.StatusWaitingHuman)
	mu.Lock()
	n := len(links)
	mu.Unlock()
	if n != 0 {
		t.Fatal("nothing is linked before the accept")
	}
	if err := pe.respond(id, "approved", "human:kyle", `{"accept":["mul","div"]}`); err != nil {
		t.Fatal(err)
	}
	pe.await(id, workflow.StatusCompleted)
	stored, _ = pipeline.GetPlan(context.Background(), pe.db, pi.PlanID)
	mu.Lock()
	defer mu.Unlock()
	if len(links) != 4 {
		t.Fatalf("two tasks times two documents: %v", links)
	}
	for _, k := range []string{"mul", "div"} {
		for _, r := range []string{"req_a", "req_b"} {
			want := "human:kyle|" + r + "|" + stored.Created[k]
			found := false
			for _, l := range links {
				found = found || l == want
			}
			if !found {
				t.Fatalf("missing %s in %v", want, links)
			}
		}
	}
}

// X48 review: a trace link that fails at accept (the requirement was archived or removed after Propose) is kept on the plan row,
// so the accepted plan can say which tasks are not linked; the links that worked are made, and a plan that linked all keeps none.
func TestPlanTeamAcceptKeepsTheLinksItCouldNotMake(t *testing.T) {
	pe := newPlanEnv(t, fakeagent.Script{Default: []fakeagent.Step{say(planJSON(nil))}}, func(pd *pipeline.PlanDeps) {
		pd.LinkRequirement = func(_ context.Context, _, reqID, _ string) error {
			if reqID == "req_gone" {
				return errors.New("requirement req_gone not found")
			}
			return nil
		}
	})
	pi, err := pipeline.NewPlanFrom(context.Background(), pe.db, "p1", "Add multiplication and division", "", aPM, []string{"req_a", "req_gone"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := pe.eng.Start(context.Background(), pipeline.WorkflowPlan, pi, workflow.StartOptions{ProjectID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	pe.await(id, workflow.StatusWaitingHuman)
	if err := pe.respond(id, "approved", "human:kyle", `{"accept":["mul","div"]}`); err != nil {
		t.Fatal(err)
	}
	pe.await(id, workflow.StatusCompleted)
	stored, err := pipeline.GetPlan(context.Background(), pe.db, pi.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "ACCEPTED" || len(stored.Unlinked) != 2 {
		t.Fatalf("both tasks are unlinked from req_gone: %+v", stored.Unlinked)
	}
	for _, u := range stored.Unlinked {
		if u.RequirementID != "req_gone" || (u.TaskID != stored.Created["mul"] && u.TaskID != stored.Created["div"]) {
			t.Fatalf("unlinked %+v created %v", u, stored.Created)
		}
	}
}
