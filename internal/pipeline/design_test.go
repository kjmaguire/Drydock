package pipeline_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/claude"
	"github.com/kjmaguire/drydock/internal/agents/fakeagent"
	"github.com/kjmaguire/drydock/internal/agents/responder"
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/pipeline"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The Design Review template (decision P124): a bounded discussion between the architect, the worker and the reviewer in the
// task's channel (the group-collaboration node on the room's real scheduler and responder), then the work on what it ended
// in. The agents are the labelled stand-in (a real process speaking Claude's dialect, scripted per agent); git, go test, the
// engine and the room are real.

// discussing answers the design discussion's turns with say1 in round 1 and PASS after, and does other turns as rest.
func discussing(say1 string, rest ...fakeagent.Step) fakeagent.Script {
	return fakeagent.Script{Rules: []fakeagent.Rule{
		{Match: "This is round 1 of", Steps: []fakeagent.Step{say(say1)}},
		{Match: "group-discussion", Steps: []fakeagent.Step{say("PASS")}},
	}, Default: rest}
}

// withRoom gives the template its task channel (the worker, the reviewer and the architect in it, as starting the template
// does) and the room's responder for the discussion.
func (e *tpl) withRoom(arch string) *room.Service {
	e.t.Helper()
	ctx := context.Background()
	rs := room.New(e.db, e.bus, room.Options{})
	rm, err := rs.CreateTaskChannel(ctx, room.TaskChannelSpec{ProjectID: "p1", TaskID: e.task.ID, Number: e.task.Seq, Title: e.task.Title,
		Assignees: []string{e.task.Assignee}, Reviewers: []string{e.task.Reviewer}, CreatedBy: room.Human("kyle")})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := rs.AddMember(ctx, rm.ID, room.Agent(arch), pipeline.RoleArchitect, room.StateActive, room.Human("kyle")); err != nil {
		e.t.Fatal(err)
	}
	runtime := &rt.Runtime{PM: proc.New(), Bus: e.bus, DB: e.db, Adapters: map[string]agents.Adapter{"claude": &claude.Adapter{Exe: os.Args[0]}}, NudgeWait: time.Second, Grants: rs}
	r := responder.New(responder.Deps{Room: rs, Runtime: runtime, DB: e.db, Bus: e.bus,
		LookupAgent: func(ctx context.Context, id string) (responder.Agent, error) {
			var a responder.Agent
			err := e.db.Reader().QueryRowContext(ctx, `SELECT id,project_id,name,adapter,role,model FROM agents WHERE id=?`, id).Scan(&a.ID, &a.ProjectID, &a.Name, &a.Adapter, &a.Role, &a.Model)
			return a, err
		},
		Cwd: func(ctx context.Context, r room.Room) (string, error) {
			t, err := e.tasks.Get(ctx, r.TaskID)
			return t.Worktree, err
		},
		Spec: func(a responder.Agent, base agents.RunSpec) agents.RunSpec {
			l := e.logs[a.ID]
			base.Env = append(base.Env, "DRYDOCK_FAKE_SCRIPT="+l.script, "DRYDOCK_FAKE_PROMPT_LOG="+l.prompts)
			return base
		},
	}, responder.Options{})
	e.td.Discuss = r.Discuss
	e.td.TaskRoom = func(ctx context.Context, taskID string) (string, error) {
		rm, err := rs.RoomByTask(ctx, taskID)
		return rm.ID, err
	}
	if err := e.eng.RegisterNew(e.td.DesignReviewGraph()); err != nil { // the same topology, now with the room wired
		e.t.Fatal(err)
	}
	return rs
}

func TestDesignReviewTemplateBuildsTheDesignTheDiscussionAgreed(t *testing.T) {
	e := newTpl(t, map[string]fakeagent.Script{
		aArch:   discussing("Multiply belongs next to Add in calc.go; no new file."),
		aWork:   discussing("Agreed, and a table test is enough.", writeStep("calc.go", calcWithMul), say("implemented Multiply")),
		aReview: discussing("Agreed.\n[DECISION] Add Multiply(a, b int) int to calc.go beside Add, with no new file", say(`{"verdict":"SHIP","feedback":""}`)),
	}, tplOpts{template: pipeline.WorkflowDesignReview})
	rs := e.withRoom(aArch)
	id := e.start(pipeline.WorkflowDesignReview, map[string]string{pipeline.RoleArchitect: aArch})
	e.await(id, workflow.StatusWaitingHuman)
	if p := e.pending(id); p.NodeID != "approve" {
		t.Fatalf("the run went on to the approval: %+v", p)
	}

	// the discussion: in the task's channel, the architect, the worker and the reviewer in turn; the reviewer's DECISION ends
	// it in round 1 (P127), so nobody is asked for a round of passes
	rm, _ := rs.RoomByTask(context.Background(), e.task.ID)
	ms, _ := rs.Messages(context.Background(), rm.ID, 0, 200)
	var order []string
	for _, m := range ms {
		if m.Author.Kind == room.AuthorAgent && m.Type == room.TypeChat {
			order = append(order, m.Author.ID)
		}
	}
	if strings.Join(order, ",") != strings.Join([]string{aArch, aWork, aReview}, ",") {
		t.Fatalf("turns %v", order)
	}
	// the decision is the task's design, and the worker's turn carried it
	ev := e.evidence()["design"]
	if len(ev) != 1 || ev[0].Summary != "design agreed in the discussion" {
		t.Fatalf("design evidence %+v", ev)
	}
	var work string
	for _, p := range e.prompts(aWork) {
		if !strings.Contains(p, "group-discussion") {
			work = p
		}
	}
	if !strings.Contains(work, "Design notes") || !strings.Contains(work, "Add Multiply(a, b int) int to calc.go beside Add") {
		t.Fatalf("the work turn did not carry the agreed design:\n%s", work)
	}
	if err := e.respond(id, "approved", "human:kyle", ""); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusCompleted)
}

func TestDesignReviewTemplateAsksAPersonWhenTheDiscussionDoesNotAgree(t *testing.T) {
	disagree := func(s string, rest ...fakeagent.Step) fakeagent.Script {
		return fakeagent.Script{Rules: []fakeagent.Rule{{Match: "group-discussion", Steps: []fakeagent.Step{say(s)}}}, Default: rest}
	}
	e := newTpl(t, map[string]fakeagent.Script{
		aArch:   disagree("Put it in a new file mul.go."),
		aWork:   disagree("No: keep it in calc.go.", writeStep("calc.go", calcWithMul), say("implemented Multiply")),
		aReview: disagree("I have no view yet.", say(`{"verdict":"SHIP","feedback":""}`)),
	}, tplOpts{template: pipeline.WorkflowDesignReview})
	rs := e.withRoom(aArch)
	id := e.start(pipeline.WorkflowDesignReview, map[string]string{pipeline.RoleArchitect: aArch})
	e.await(id, workflow.StatusWaitingHuman)
	p := e.pending(id)
	var q pipeline.DesignQuestionPayload
	if err := json.Unmarshal(p.Spec.Payload, &q); err != nil || p.NodeID != "designq" || p.Spec.Kind != "question" || q.Phase != "design" || q.QuestionID == "" || q.Rounds != pipeline.DesignRounds {
		t.Fatalf("pending %+v %s (%v)", p, p.Spec.Payload, err)
	}
	if e.get().Status != "WAITING_HUMAN" {
		t.Fatalf("task %s", e.get().Status)
	}
	// the Inbox shows the request in place of the room's question it carries
	items, err := rs.NeedsYou(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, it := range items {
		if it.MessageID == q.QuestionID {
			t.Fatalf("the room's question is an item of its own too: %+v", it)
		}
		kinds = append(kinds, string(it.Kind))
	}
	if !strings.Contains(strings.Join(kinds, ","), "question") {
		t.Fatalf("items %v", kinds)
	}
	// an agent cannot settle it; a person's answer is the design the worker builds
	if err := e.respond(id, "answered", "human:kyle", `{"text":"Keep Multiply in calc.go next to Add."}`); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusWaitingHuman)
	if p := e.pending(id); p.NodeID != "approve" {
		t.Fatalf("%+v", p)
	}
	ev := e.evidence()["design"]
	if len(ev) != 1 || ev[0].Summary != "design settled by kyle" {
		t.Fatalf("design evidence %+v", ev)
	}
	var work string
	for _, pr := range e.prompts(aWork) {
		if !strings.Contains(pr, "group-discussion") {
			work = pr
		}
	}
	if !strings.Contains(work, "kyle settled it") || !strings.Contains(work, "Keep Multiply in calc.go next to Add.") {
		t.Fatalf("the work turn did not carry the person's design:\n%s", work)
	}
}

// A person who rejects the question stops the task.
func TestDesignReviewTemplateRejectedQuestionStopsTheTask(t *testing.T) {
	e := newTpl(t, map[string]fakeagent.Script{
		aArch:   {Default: []fakeagent.Step{say("A new file.")}},
		aWork:   {Default: []fakeagent.Step{say("No new file.")}},
		aReview: shipReviewer(),
	}, tplOpts{template: pipeline.WorkflowDesignReview})
	e.withRoom(aArch)
	id := e.start(pipeline.WorkflowDesignReview, map[string]string{pipeline.RoleArchitect: aArch})
	e.await(id, workflow.StatusWaitingHuman)
	if err := e.respond(id, "rejected", "human:kyle", ""); err != nil {
		t.Fatal(err)
	}
	e.await(id, workflow.StatusCompleted)
	if st := e.get().Status; st != "CANCELLED" {
		t.Fatalf("task %s", st)
	}
	for _, pr := range e.prompts(aWork) {
		if !strings.Contains(pr, "group-discussion") {
			t.Fatal("a work turn ran")
		}
	}
}

// The template's graph: a group node after prepare whose two routes lead to the design (or the person's question), and nothing
// reaches the work but the design step; the catalog names the architect it needs.
func TestDesignReviewTemplateGraphAndCatalog(t *testing.T) {
	td := pipeline.TemplateDeps{}
	for _, g := range []*workflow.Graph{td.DesignReviewGraph()} {
		if err := g.Validate(); err != nil {
			t.Fatal(err)
		}
		if g.Nodes["discuss"].Kind != workflow.KindGroup {
			t.Fatalf("discuss is %s", g.Nodes["discuss"].Kind)
		}
		into, out := map[string][]string{}, map[string][]string{}
		for _, ed := range g.Edges {
			into[string(ed.To)] = append(into[string(ed.To)], string(ed.From)+":"+ed.When)
			out[string(ed.From)] = append(out[string(ed.From)], string(ed.To)+":"+ed.When)
		}
		if got := strings.Join(out["prepare"], " ") + " | " + strings.Join(out["designed"], " "); got != "discuss: | work:" {
			t.Fatalf("out of prepare | designed: %s", got)
		}
		if got := strings.Join(into["designed"], " "); got != "discuss:decision designq:answered designq:approved" {
			t.Fatalf("into designed: %s", got)
		}
		if got := strings.Join(into["designq"], " "); got != "discuss:question" {
			t.Fatalf("into designq: %s", got)
		}
	}
	tpl, ok := pipeline.TemplateByName("design-review")
	if !ok || tpl.Workflow != pipeline.WorkflowDesignReview || len(tpl.Roles) != 1 || tpl.Roles[0].Role != pipeline.RoleArchitect || !tpl.Roles[0].Required {
		t.Fatalf("%+v", tpl)
	}
	if !pipeline.DesignReviewed("design-review-pr") || pipeline.DesignReviewed("feature") {
		t.Fatal("DesignReviewed")
	}
}
