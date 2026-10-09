package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/watchdog"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Escalation: where a task's workflow goes when a loop runs out of budget or the reviewer keeps repeating itself.
//
//	<any loop exhausted> ─┐
//	review ──stuck────────┴─> escalate ──lead──> resume ──> work (or spec, tests, sync, approve: where the work resumes)
//	                            │                  ▲
//	                            └──ask──> limit ───┘ approved / answered (a human continues, with optional guidance)
//	                                        └──────rejected──> abort (the task is CANCELLED, the run ends)
//
// escalate asks the team lead first (when the project has one and its bounds allow); the lead's nudge, reassignment or split
// continues the work at once, its escalation (or no lead at all) puts the question to the human. Continuing resets every loop
// counter of the run (workflow.NodeOutput.ResetLoops) and gives the task a fresh iteration budget, so a continue is a real one.

// Node ids and routes of the escalation part of a graph.
const (
	NodeEscalate workflow.NodeID = "escalate"
	NodeLimit    workflow.NodeID = "limit"
	NodeResume   workflow.NodeID = "resume"
	NodeAbort    workflow.NodeID = "abort"

	RouteLead  = "lead"  // escalate -> resume: the team lead decided to continue
	RouteAsk   = "ask"   // escalate -> limit: a human decides
	RouteStuck = "stuck" // review -> escalate: the same REVISE feedback came back
)

// Repeated review feedback: two consecutive REVISE rounds whose feedback is at least this similar (bigram Jaccard of the
// normalized text) count as the reviewer repeating itself.
const (
	ReviseRepeatRounds = 2
	ReviseSimilarity   = 0.8
)

// iterationBudget is how many more review rounds a continue gives a task whose iteration limit was reached.
const iterationBudget = 5

// escalation is the escalate node's output; resume and limit read it.
type escalation struct {
	Trigger  string    `json:"trigger"`
	Origin   string    `json:"origin"`
	Evidence string    `json:"evidence,omitempty"`
	Lead     *leadView `json:"lead,omitempty"`
}

// leadView is the team lead's decision as the human (and resume) sees it.
type leadView struct {
	Agent     string `json:"agent"`
	Name      string `json:"name"`
	Action    string `json:"action,omitempty"`
	Message   string `json:"message,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Target    string `json:"target,omitempty"`
	Exhausted bool   `json:"exhausted,omitempty"`
	Invalid   bool   `json:"invalid,omitempty"`
}

// LimitPayload is what the human sees at the limit question.
type LimitPayload struct {
	Phase         string    `json:"phase"` // "limit"
	TaskID        string    `json:"taskId"`
	Title         string    `json:"title"`
	Trigger       string    `json:"trigger"`
	Origin        string    `json:"origin"`
	Evidence      string    `json:"evidence,omitempty"`
	Iteration     int       `json:"iteration"`
	MaxIterations int       `json:"maxIterations"`
	Lead          *leadView `json:"lead,omitempty"`
	Note          string    `json:"note"`
}

// LimitNote tells the human how to answer the limit question.
const LimitNote = `Approve (optionally with data.text: guidance for the team) to continue with a fresh loop budget; the guidance goes to the agent's next turn. Answer with data.action "stop", or reject, to stop: the task is CANCELLED and the run ends.`

func escalationTitle(trigger string) string {
	if trigger == TriggerRepeatedRevise {
		return "The reviewer keeps asking for the same change: a human needs to decide"
	}
	return "Iteration limit reached: a human needs to decide"
}

// Escalate is the escalate node: it works out why the run is here (which loop ran out, or repeated review feedback), asks
// the team lead when there is one, and routes to resume (the lead continues the work) or to the human question.
func Escalate(d Deps) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		esc := escalation{Trigger: TriggerIterationLimit}
		for _, m := range in.Inbox {
			if esc.Origin == "" {
				esc.Origin = string(m.From)
			}
			var v struct {
				Stuck    bool   `json:"stuck"`
				Feedback string `json:"feedback"`
				Verdict  string `json:"verdict"`
			}
			if json.Unmarshal(m.Value, &v) == nil {
				if v.Stuck {
					esc.Trigger = TriggerRepeatedRevise
					esc.Evidence = fmt.Sprintf("The reviewer sent the same REVISE feedback in %d consecutive rounds: %s", ReviseRepeatRounds, clip(v.Feedback, 1200))
					continue
				}
				if v.Feedback != "" && esc.Evidence == "" {
					esc.Evidence = "Last feedback: " + clip(v.Feedback, 1200)
					continue
				}
			}
			var cr workflow.CheckResult
			if json.Unmarshal(m.Value, &cr) == nil && cr.Name != "" && !cr.Passed && esc.Evidence == "" {
				esc.Evidence = fmt.Sprintf("The %q check still fails (exit %d)", cr.Name, cr.ExitCode)
				if len(cr.FailingTests) > 0 {
					esc.Evidence += ": " + strings.Join(cr.FailingTests, ", ")
				}
			}
		}
		if esc.Evidence == "" && esc.Origin != "" {
			esc.Evidence = fmt.Sprintf("The loop back from %s used up its budget.", esc.Origin)
		}
		if d.Lead != nil {
			o := d.Lead.Supervise(ctx, LeadTrigger{Kind: esc.Trigger, TaskID: t.ID, RunID: string(in.RunID), Evidence: esc.Evidence, LastText: lastWorkText(in)})
			if o.Intervened || o.Exhausted {
				esc.Lead = &leadView{Agent: o.LeadID, Name: o.LeadName, Action: o.Decision.Action, Message: o.Decision.Message, Reason: o.Decision.Reason,
					Target: o.AgentID, Exhausted: o.Exhausted, Invalid: o.Invalid}
			}
			if o.Continues() {
				b, _ := json.Marshal(esc)
				return workflow.NodeOutput{Value: b, Route: RouteLead}, nil
			}
		}
		b, _ := json.Marshal(esc)
		return workflow.NodeOutput{Value: b, Route: RouteAsk}, nil
	}
}

// lastWorkText is the final text of the run's latest work turn, bounded (from the shared state).
func lastWorkText(in workflow.NodeInput) string {
	raw, ok := in.Shared["out/work"]
	if !ok {
		return ""
	}
	if v, err := workflow.ReadRef(raw); err == nil {
		raw = v
	}
	var res rt.TurnResult
	if json.Unmarshal(raw, &res) != nil {
		return ""
	}
	return clip(strings.TrimSpace(res.Text), 1500)
}

// LimitRequest is the limit node: the question to the human, with why the run stopped and what the team lead said, if
// anything. A task that is IN_PROGRESS waits for the human (WAITING_HUMAN) meanwhile.
func LimitRequest(d Deps) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		var esc escalation
		for _, m := range in.Inbox {
			_ = json.Unmarshal(m.Value, &esc)
		}
		if esc.Trigger == "" {
			esc.Trigger = TriggerIterationLimit
		}
		if t.Status == tasks.InProgress {
			if nt, err := d.Tasks.Transition(ctx, t.ID, tasks.WaitingHuman, tasks.TransitionOpts{By: "system", Reason: escalationTitle(esc.Trigger)}); err == nil {
				t = nt
			}
		}
		p := LimitPayload{Phase: "limit", TaskID: t.ID, Title: t.Title, Trigger: esc.Trigger, Origin: esc.Origin, Evidence: esc.Evidence,
			Iteration: t.Iteration, MaxIterations: t.MaxIterations, Lead: esc.Lead, Note: LimitNote}
		payload, _ := json.Marshal(p)
		return workflow.NodeOutput{Request: &workflow.RequestSpec{Kind: "question", Title: escalationTitle(esc.Trigger), Payload: payload}}, nil
	}
}

// Resume continues a task past an escalation, after a human's answer or a team lead's decision: a human "stop" cancels the
// task; otherwise the task gets a fresh iteration budget and goes back to IN_PROGRESS, the run's loop counters reset, and
// the work resumes (at back[origin], default "work") with the guidance as readable text.
func Resume(d Deps, back map[workflow.NodeID]workflow.NodeID) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		var esc escalation
		if raw, ok := in.Shared["out/"+string(NodeEscalate)]; ok {
			_ = json.Unmarshal(raw, &esc)
		}
		from, by, guidance := "human", "", ""
		if h, ok := humanBodyOf(in); ok {
			if !isHuman(h.By) {
				return workflow.NodeOutput{}, fmt.Errorf("only a human can continue a task past a limit; got %q", h.By)
			}
			by, guidance = h.By, HumanText(h.Data)
			var ctl struct {
				Action string `json:"action"`
			}
			if json.Unmarshal(h.Data, &ctl) == nil && strings.EqualFold(strings.TrimSpace(ctl.Action), "stop") {
				return stopTask(ctx, d, t, by, "stopped by the human at "+esc.Trigger)
			}
		} else {
			if esc.Lead == nil || esc.Lead.Agent == "" {
				return workflow.NodeOutput{}, fmt.Errorf("resume needs a human answer or a team lead decision")
			}
			from, by, guidance = "lead", esc.Lead.Agent, clip(strings.TrimSpace(esc.Lead.Message), maxGuidance)
		}
		target := workflow.NodeID("work")
		if b, ok := back[workflow.NodeID(esc.Origin)]; ok {
			target = b
		}
		// the work goes back to an agent (work, spec, tests): the task is IN_PROGRESS again; a resume at sync or at the
		// approval keeps the task where it is (VERIFICATION after a SHIP)
		reopen := target != "sync" && target != "approve"
		if err := d.freshBudget(ctx, t, by, guidance, esc.Trigger, reopen); err != nil {
			return workflow.NodeOutput{}, err
		}
		if from == "human" && d.ResetWatch != nil {
			d.ResetWatch(ctx, t.ID)
		}
		out := map[string]any{"from": from, "continue": true}
		if guidance != "" {
			out["guidance"] = guidance
			if target != "work" {
				out["feedback"] = guidanceLabel(from) + " " + guidance // the spec and test writers read "feedback"
			}
		}
		d.emit(ctx, "task.continued", t, map[string]any{"trigger": esc.Trigger, "origin": esc.Origin, "by": by, "from": from, "resumeAt": target})
		b, _ := json.Marshal(out)
		return workflow.NodeOutput{Value: b, Route: string(target), ResetLoops: true}, nil
	}
}

// freshBudget gives the task another round of iterations and, when reopen is set, moves it back to IN_PROGRESS.
func (d Deps) freshBudget(ctx context.Context, t tasks.Task, by, guidance, trigger string, reopen bool) error {
	if t.Iteration+1 >= t.MaxIterations {
		if err := d.Tasks.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE tasks SET max_iterations=?, updated_at=?, version=version+1 WHERE id=?`, t.Iteration+1+iterationBudget, storage.Now(), t.ID)
			return err
		}); err != nil {
			return err
		}
	}
	if !reopen {
		return nil
	}
	switch t.Status {
	case tasks.Review, tasks.Verification, tasks.WaitingHuman, tasks.Blocked:
		fb := guidance
		if fb == "" {
			fb = "Continue for another round: re-read the task and the last feedback and finish it."
		}
		if _, err := d.Tasks.Transition(ctx, t.ID, tasks.InProgress, tasks.TransitionOpts{By: by, Reason: "continued past " + trigger, Feedback: fb}); err != nil {
			return err
		}
	}
	return nil
}

// stopTask cancels the task and halts the run (no edge is followed after it).
func stopTask(ctx context.Context, d Deps, t tasks.Task, by, reason string) (workflow.NodeOutput, error) {
	if !t.Status.Terminal() {
		if _, err := d.Tasks.Transition(ctx, t.ID, tasks.Cancelled, tasks.TransitionOpts{By: by, Reason: reason}); err != nil {
			return workflow.NodeOutput{}, err
		}
	}
	b, _ := json.Marshal(map[string]any{"cancelled": true, "by": by})
	return workflow.NodeOutput{Value: b, Halt: true}, nil
}

// Abort cancels the task when a human rejects a limit question: nothing further runs.
func Abort(d Deps) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		h := humanResponse(in)
		if !isHuman(h.by) {
			return workflow.NodeOutput{}, fmt.Errorf("abort needs a human decision; got %q", h.by)
		}
		return stopTask(ctx, d, t, h.by, "stopped by the human at a limit")
	}
}

// addEscalation adds escalate, limit, resume and abort to a graph. back maps the node a loop exhausted from to where the work
// resumes after a continue (default "work").
func addEscalation(g *workflow.Graph, d Deps, back map[workflow.NodeID]workflow.NodeID) {
	g.Add(
		workflow.Route(NodeEscalate, Escalate(d)),
		&workflow.Node{ID: NodeLimit, Kind: workflow.KindRequest, Handler: LimitRequest(d)},
		workflow.Route(NodeResume, Resume(d, back)),
	)
	if _, ok := g.Nodes[NodeAbort]; !ok {
		g.Add(workflow.Route(NodeAbort, Abort(d)))
	}
	g.EdgeWhen(NodeEscalate, NodeLimit, RouteAsk).
		EdgeWhen(NodeEscalate, NodeResume, RouteLead).
		EdgeWhen(NodeLimit, NodeResume, "approved").
		EdgeWhen(NodeLimit, NodeResume, "answered").
		EdgeWhen(NodeLimit, NodeAbort, "rejected")
	targets := map[workflow.NodeID]bool{"work": true}
	for _, to := range back {
		targets[to] = true
	}
	var ids []string
	for to := range targets {
		ids = append(ids, string(to))
	}
	sort.Strings(ids)
	for _, to := range ids {
		g.EdgeWhen(NodeResume, workflow.NodeID(to), to)
	}
}

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// normalizeFeedback lowercases review feedback and drops punctuation and spacing differences.
func normalizeFeedback(s string) string {
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

// FeedbackSimilar reports whether two review feedbacks say the same thing (normalized bigram Jaccard >= ReviseSimilarity).
func FeedbackSimilar(a, b string) bool {
	a, b = normalizeFeedback(a), normalizeFeedback(b)
	if a == "" || b == "" {
		return false
	}
	return a == b || watchdog.Jaccard(a, b) >= ReviseSimilarity
}

// RepeatedRevise reports whether the latest ReviseRepeatRounds reviews are all REVISE with near-identical feedback.
func RepeatedRevise(reviews []tasks.ReviewRecord) bool {
	if len(reviews) < ReviseRepeatRounds {
		return false
	}
	last := reviews[len(reviews)-ReviseRepeatRounds:]
	for i, r := range last {
		if r.Verdict != "REVISE" {
			return false
		}
		if i > 0 && !FeedbackSimilar(last[i-1].Feedback, r.Feedback) {
			return false
		}
	}
	return true
}

// reviewRoute decides where a REVISE goes: back to the worker, or to the escalation when the reviewer repeats itself.
func (d Deps) reviewRoute(ctx context.Context, taskID string) string {
	if rv, err := d.Tasks.Reviews(ctx, taskID); err == nil && RepeatedRevise(rv) {
		return RouteStuck
	}
	return "revise"
}
