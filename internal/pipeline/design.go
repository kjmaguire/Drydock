package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kjmaguire/drydock/internal/agents/responder"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The Design Review template (decision P124, brief s4's "group collaboration with bounded debate"). Before anything is
// built, the architect, the worker and the reviewer hold a bounded discussion in the task's channel: the group-collaboration
// node (responder.GroupNodeWith, docs/workflow-engine.md s14), every turn a read-only agent turn granted by the room's
// scheduler, at most DesignRounds rounds. A DECISION one of them proposed stands as the design the worker builds; a
// discussion that ends without one asks a person the question it ended on (designq), and the person's answer is the design.
// Either way the design is the task's "design" evidence, which every implementation pass carries as design notes; then the
// common tail (work, checks, an independent review, the person's approval, the merge).

// WorkflowDesignReview is the Design Review template's workflow.
const WorkflowDesignReview = "design-review"

// DesignRounds bounds the design discussion.
const DesignRounds = 3

// DesignReviewed reports whether a workflow (local or pull request variant) holds the design discussion before the work.
func DesignReviewed(workflowName string) bool {
	return strings.TrimSuffix(workflowName, PRSuffix) == WorkflowDesignReview
}

// DesignQuestionPayload is the payload of the request a design discussion that did not agree raises. QuestionID is the
// room's QUESTION the discussion ended on: the Inbox shows this request in its place (room.NeedsYou).
type DesignQuestionPayload struct {
	Phase      string `json:"phase"` // "design"
	TaskID     string `json:"taskId"`
	Question   string `json:"question"`
	QuestionID string `json:"questionId"`
	RoomID     string `json:"roomId"`
	ThreadID   string `json:"threadId"`
	Rounds     int    `json:"rounds"`
	Note       string `json:"note"`
}

// DesignReviewGraph is the Design Review template: the design discussion, then the work.
func (td TemplateDeps) DesignReviewGraph() *workflow.Graph { return td.designReviewGraph(false) }

func (td TemplateDeps) designReviewGraph(pr bool) *workflow.Graph {
	g := workflow.NewGraph(graphName(WorkflowDesignReview, pr)).Add(
		workflow.Route("prepare", Prepare(td.Deps)),
		responder.GroupNodeWith("discuss", td.discuss, td.designDiscussion),
		&workflow.Node{ID: "designq", Kind: workflow.KindRequest, Handler: td.designQuestion()},
		workflow.Route("designed", td.designed()),
	)
	b := newBuilder(td, g, pr)
	b.tail(nil)
	g.Edge("prepare", "discuss").
		EdgeWhen("discuss", "designed", responder.OutcomeDecision).
		EdgeWhen("discuss", "designq", responder.OutcomeQuestion).
		EdgeWhen("designq", "designed", "answered").
		EdgeWhen("designq", "designed", "approved").
		EdgeWhen("designq", NodeAbort, "rejected").
		Edge("designed", "work")
	return b.finish()
}

// discuss holds the discussion through TemplateDeps.Discuss, which the core connects to the room's responder once it exists.
func (td TemplateDeps) discuss(ctx context.Context, d responder.Discussion) (responder.DiscussionResult, error) {
	if td.Discuss == nil {
		return responder.DiscussionResult{}, errors.New("pipeline: agents cannot discuss in this build (no responder is connected)")
	}
	return td.Discuss(ctx, d)
}

// designDiscussion is the discussion of a task: in its channel, between the architect the run pinned, the worker and the
// reviewer (each once, in that order), on the task as a person wrote it.
func (td TemplateDeps) designDiscussion(ctx context.Context, in workflow.NodeInput) (responder.Discussion, error) {
	t, err := taskFromInput(ctx, td.Deps, in)
	if err != nil {
		return responder.Discussion{}, err
	}
	if td.TaskRoom == nil {
		return responder.Discussion{}, errors.New("pipeline: the task's channel cannot be found in this build")
	}
	roomID, err := td.TaskRoom(ctx, t.ID)
	if err != nil {
		return responder.Discussion{}, fmt.Errorf("the design discussion is held in the task's channel: %w", err)
	}
	var agents []string
	for _, id := range []string{td.templateInput(in).Roles[RoleArchitect], t.Assignee, t.Reviewer} {
		if id != "" && !containsStr(agents, id) {
			agents = append(agents, id)
		}
	}
	return responder.Discussion{RoomID: roomID, Topic: DesignTopic(t, td.taskBrief(ctx, t)), Agents: agents, Rounds: DesignRounds, TaskID: t.ID}, nil
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// DesignTopic is what the design discussion is about: the task as a person wrote it and what the discussion must end in.
func DesignTopic(t tasks.Task, brief string) string {
	var b strings.Builder
	b.WriteString("Agree on how to build this task before anyone changes code. Say how you would build it and what worries you, answer the others, and keep it short. ")
	b.WriteString("When you agree, one of you writes the agreed design on a line that starts with [DECISION]: the approach, the files it touches and what it leaves out. ")
	b.WriteString("Say PASS when you have nothing to add.\n\n")
	fmt.Fprintf(&b, "Task: %s\n", t.Title)
	if d := strings.TrimSpace(t.Description); d != "" {
		b.WriteString(clip(d, 2000) + "\n")
	}
	if len(t.Acceptance) > 0 {
		b.WriteString("\nAcceptance criteria:\n")
		for _, a := range t.Acceptance {
			b.WriteString("- " + a + "\n")
		}
	}
	if brief = strings.TrimSpace(brief); brief != "" {
		b.WriteString("\nThe project, as its person describes it:\n" + clip(brief, 1500) + "\n")
	}
	return strings.TrimSpace(b.String())
}

func discussionOf(in workflow.NodeInput) (responder.DiscussionResult, bool) {
	for _, m := range in.Inbox {
		var r responder.DiscussionResult
		if json.Unmarshal(m.Value, &r) == nil && r.Outcome != "" && r.ThreadID != "" {
			return r, true
		}
	}
	if v, ok := in.Shared["out/discuss"]; ok {
		var r responder.DiscussionResult
		if raw, err := workflow.ReadRef(v); err == nil && json.Unmarshal(raw, &r) == nil && r.Outcome != "" {
			return r, true
		}
	}
	return responder.DiscussionResult{}, false
}

// designQuestion puts the question a discussion that did not agree ended on to a person; the task waits for them.
func (td TemplateDeps) designQuestion() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		res, ok := discussionOf(in)
		if !ok {
			return workflow.NodeOutput{}, errors.New("design question without a discussion")
		}
		q := strings.TrimSpace(res.Question)
		if q == "" {
			q = "The design discussion ended without an agreed design. How should this task be built?"
		}
		if cur, err := td.Tasks.Get(ctx, t.ID); err == nil && cur.Status == tasks.InProgress {
			if _, err := td.Tasks.Transition(ctx, t.ID, tasks.WaitingHuman, tasks.TransitionOpts{By: "system", Reason: "waiting for a person to settle the design"}); err != nil {
				return workflow.NodeOutput{}, err
			}
		}
		payload, _ := json.Marshal(DesignQuestionPayload{Phase: "design", TaskID: t.ID, Question: clip(q, 4000), QuestionID: res.QuestionID, RoomID: res.RoomID,
			ThreadID: res.ThreadID, Rounds: res.Rounds,
			Note: "Answer with the design to build (it goes to the worker as the design notes), approve to build on what the discussion said, or reject to stop the task."})
		return workflow.NodeOutput{Request: &workflow.RequestSpec{Kind: "question", Phase: "design", Title: "Settle the design of " + t.Title + ": " + firstLineOf(q, 200), Payload: payload}}, nil
	}
}

func firstLineOf(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clip(strings.TrimSpace(s), n)
}

// designed records the design the work builds on as the task's design evidence: the discussion's decision, or the question
// it ended on with the person's answer. Only a person's answer is taken.
func (td TemplateDeps) designed() workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, td.Deps, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		res, _ := discussionOf(in)
		var notes, summary string
		if h, ok := answerOf(in); ok {
			if !isHuman(h.By) {
				return workflow.NodeOutput{}, fmt.Errorf("the design can only be settled by a person; got %q", h.By)
			}
			answer := HumanText(h.Data)
			if answer == "" {
				notes = fmt.Sprintf("The design discussion (%d rounds) did not agree. It asked: %s\n\n%s approved building on what the discussion said (thread %s in the task's channel).",
					res.Rounds, res.Question, personOf(h.By), res.ThreadID)
			} else {
				notes = fmt.Sprintf("The design discussion (%d rounds) did not agree. It asked: %s\n\n%s settled it:\n%s", res.Rounds, res.Question, personOf(h.By), answer)
			}
			summary = "design settled by " + personOf(h.By)
			if cur, err := td.Tasks.Get(ctx, t.ID); err == nil && cur.Status == tasks.WaitingHuman {
				if _, err := td.Tasks.Transition(ctx, t.ID, tasks.InProgress, tasks.TransitionOpts{By: h.By, Reason: "the design is settled"}); err != nil {
					return workflow.NodeOutput{}, err
				}
			}
		} else {
			if res.Outcome != responder.OutcomeDecision || strings.TrimSpace(res.Decision) == "" {
				return workflow.NodeOutput{}, errors.New("no design was agreed and no person settled it")
			}
			notes = fmt.Sprintf("Agreed in the design discussion (%d rounds, thread %s in the task's channel); agents proposed it, so check it against the task:\n%s",
				res.Rounds, res.ThreadID, strings.TrimSpace(res.Decision))
			summary = "design agreed in the discussion"
		}
		path := filepath.Join(td.evidenceDir(in), fmt.Sprintf("design-%d.md", in.Step))
		if err := os.WriteFile(path, []byte(notes+"\n"), 0o644); err != nil {
			return workflow.NodeOutput{}, err
		}
		if err := td.addEvidenceOnce(ctx, t.ID, tasks.Evidence{Kind: "design", Summary: summary, Ref: path}); err != nil {
			return workflow.NodeOutput{}, err
		}
		b, _ := json.Marshal(map[string]any{"design": clip(notes, 4000)})
		return workflow.NodeOutput{Value: b}, nil
	}
}

// answerOf is the person's answer to the design question (the result the discussion delivers has a "decision" too).
func answerOf(in workflow.NodeInput) (humanBody, bool) {
	for _, m := range in.Inbox {
		var h humanBody
		if m.From == "designq" && json.Unmarshal(m.Value, &h) == nil && h.Decision != "" {
			return h, true
		}
	}
	return humanBody{}, false
}

func personOf(by string) string {
	if n := strings.TrimPrefix(by, "human:"); n != "" {
		return n
	}
	return "A person"
}
