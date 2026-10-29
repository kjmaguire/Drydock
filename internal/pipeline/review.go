package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/git"
	"github.com/kjmaguire/drydock/internal/runfiles"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// ProjectInfo is what the merge step needs to know about the project.
type ProjectInfo struct {
	DefaultBranch string
	MergeStrategy git.MergeStrategy
	// DeleteBranchAfterMerge removes the local task branch once merged (local_merge mode).
	DeleteBranchAfterMerge bool
}

func (d Deps) emit(ctx context.Context, typ string, t tasks.Task, data map[string]any) {
	if d.Bus == nil {
		return
	}
	b, _ := json.Marshal(data)
	_, _ = d.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: t.ProjectID, TaskID: t.ID, Data: b})
}

// Verdict is a reviewer's structured answer.
type Verdict struct {
	Verdict  string `json:"verdict"` // SHIP | REVISE
	Feedback string `json:"feedback"`
}

// ParseVerdict extracts the reviewer's verdict from free text. It accepts a JSON object with a "verdict" field (the last one
// wins); without one, only a reply that is exactly the word SHIP ships. Anything else is REVISE: a review that cannot be
// understood never ships work (a first line that merely starts with "SHIP", such as "SHIPPING this would be a mistake", is
// not a verdict). Objects are found by decoding JSON at every brace, so feedback that quotes code with braces in it
// (`map[string]struct{}`) is read as written instead of turning a SHIP into an unparsed REVISE.
func ParseVerdict(text string) Verdict {
	objs := verdictObjects(text)
	for i := len(objs) - 1; i >= 0; i-- {
		v := objs[i]
		switch strings.ToUpper(strings.TrimSpace(v.Verdict)) {
		case "SHIP":
			v.Verdict = "SHIP"
			return v
		case "REVISE":
			v.Verdict = "REVISE"
			if strings.TrimSpace(v.Feedback) == "" {
				v.Feedback = "The reviewer asked for changes but gave no details; re-check the acceptance criteria and the diff."
			}
			return v
		}
	}
	if w := strings.ToUpper(strings.TrimRight(strings.TrimSpace(text), ".!")); w == "SHIP" {
		return Verdict{Verdict: "SHIP"}
	}
	return Verdict{Verdict: "REVISE", Feedback: "The review response could not be parsed as a verdict; reviewer said:\n" + truncate(text, 1500)}
}

// verdictObjects returns, in order of appearance, every JSON object in text that has a "verdict" field. Decoding starts at
// each '{'; an object that decodes is skipped as a whole, so a verdict quoted inside another verdict's feedback is not read
// twice. Only the last 64 KB are searched: the verdict is the reviewer's closing answer.
func verdictObjects(text string) []Verdict {
	if len(text) > 64<<10 {
		text = text[len(text)-64<<10:]
	}
	var out []Verdict
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text[i:]))
		var raw map[string]json.RawMessage
		if dec.Decode(&raw) != nil {
			continue
		}
		vr, ok := raw["verdict"]
		if !ok {
			continue // an object without a verdict may still contain one ({"review": {"verdict": ...}}): look inside it
		}
		var v Verdict
		if json.Unmarshal(vr, &v.Verdict) != nil {
			continue
		}
		if fb, ok := raw["feedback"]; ok {
			_ = json.Unmarshal(fb, &v.Feedback)
		}
		out = append(out, v)
		i += int(dec.InputOffset()) - 1
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...[truncated]"
}

// ReviewPrompt tells the reviewer exactly what to do, with the evidence attached so it does not have to find what a tool
// already found.
func ReviewPrompt(t tasks.Task, evidence []tasks.Evidence, diff string) string {
	var sb strings.Builder
	sb.WriteString("You are the independent reviewer for this task. You did not write this code.\n\n")
	sb.WriteString(taskBlock(t, false))
	if len(t.Acceptance) == 0 {
		if dod := statedDoD(evidence); dod != "" {
			sb.WriteString("\nNo acceptance criteria were given. Check the work against the task text and against the definition of done the worker stated:\n")
			sb.WriteString(quoted("The worker's definition of done", dod))
		} else {
			sb.WriteString("\nNo acceptance criteria were given and the worker stated no definition of done: check the work against the task text.\n")
		}
	}
	sb.WriteString("\nEvidence already produced by deterministic tools (do not redo it):\n")
	for _, e := range evidence {
		if e.Kind == dodKind {
			continue
		}
		status := ""
		if e.Passed != nil {
			status = map[bool]string{true: " [passed]", false: " [FAILED]"}[*e.Passed]
		}
		fmt.Fprintf(&sb, "- %s%s: %s\n", e.Kind, status, e.Summary)
	}
	sb.WriteString(diffSection(diff))
	sb.WriteString(`
Review for correctness against the task and acceptance criteria, missed edge cases, and unintended changes. Answer REVISE only for problems that affect correctness, the stated requirements or security; style preferences, extra abstraction and tests for cases that cannot happen are not reasons to revise. You may read files in the working directory. Do not modify anything.
Finish with exactly one JSON object on its own line, and nothing after it:
{"verdict":"SHIP","feedback":""}   when the change is correct and complete, or
{"verdict":"REVISE","feedback":"specific, actionable problems"}   otherwise.`)
	return sb.String()
}

// Review runs the reviewer agent (a different agent instance) on the committed work, records the verdict and moves the
// task: SHIP -> VERIFICATION (route "ship"), REVISE -> IN_PROGRESS with the feedback (route "revise").
func Review(d Deps) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		if t.Status != tasks.Review {
			return workflow.NodeOutput{}, fmt.Errorf("task is %s, not REVIEW: nothing to review (checks must pass with evidence first)", t.Status)
		}
		evidence, err := d.Tasks.Evidence(ctx, t.ID)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		var diff, commit string
		for i := len(evidence) - 1; i >= 0 && (diff == "" || commit == ""); i-- {
			switch {
			case evidence[i].Kind == "diff" && diff == "":
				b, _ := runfiles.ReadConfined(d.DataDir, evidence[i].Ref)
				diff = string(b)
			case evidence[i].Kind == "commit" && commit == "":
				commit = evidence[i].Ref
			}
		}
		cfg := d.AgentConfig(t, "review")
		applyRoute(ctx, &cfg)
		cfg.TaskID, cfg.ProjectID = t.ID, t.ProjectID
		cfg.Spec.Cwd = t.Worktree
		if cfg.AgentID == t.Assignee {
			return workflow.NodeOutput{}, errors.New("the reviewer must be a different agent instance from the assignee")
		}
		prompt := ReviewPrompt(t, evidence, diff)
		d.saveManifest(ctx, in, t, cfg.AgentID, prompt, "review", nil)
		h := d.Runtime.AgentTask(rt.TaskSpec{Config: cfg, Prompt: func(workflow.NodeInput) string { return prompt }})
		out, err := h(ctx, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		var res rt.TurnResult
		_ = json.Unmarshal(out.Value, &res)
		var cost float64
		for _, u := range res.Usage {
			cost += u.CostUSD
		}
		_, _ = d.Tasks.Patch(ctx, t.ID, tasks.Update{AddCost: cost})
		v := ParseVerdict(res.Text)
		if d.OnTurn != nil {
			summary := v.Verdict
			if v.Feedback != "" {
				summary += ": " + v.Feedback
			}
			d.OnTurn(ctx, t, cfg.AgentID, "review", summary)
		}
		if _, err := d.Tasks.AddReview(ctx, t.ID, tasks.ReviewRecord{Attempt: t.Iteration, Reviewer: cfg.AgentID, Verdict: v.Verdict, Feedback: v.Feedback, Model: cfg.Spec.Model, Commit: commit}); err != nil {
			return workflow.NodeOutput{}, err
		}
		b, _ := json.Marshal(v)
		if v.Verdict == "SHIP" {
			if _, err := d.Tasks.Transition(ctx, t.ID, tasks.Verification, tasks.TransitionOpts{By: cfg.AgentID, Reason: "reviewer returned SHIP"}); err != nil {
				return workflow.NodeOutput{}, err
			}
			return workflow.NodeOutput{Value: b, Route: "ship"}, nil
		}
		if _, err := d.Tasks.Transition(ctx, t.ID, tasks.InProgress, tasks.TransitionOpts{By: cfg.AgentID, Reason: "reviewer returned REVISE", Feedback: v.Feedback}); err != nil {
			// most often the iteration limit: hand the decision to the escalation (the team lead, then a human)
			return workflow.NodeOutput{Value: b, Route: "limit"}, nil
		}
		return d.reviseOutput(ctx, t, v, b), nil
	}
}

// reviseOutput routes a REVISE back to the worker, or to the escalation when the reviewer repeated the same feedback.
func (d Deps) reviseOutput(ctx context.Context, t tasks.Task, v Verdict, b []byte) workflow.NodeOutput {
	if d.reviewRoute(ctx, t.ID) == RouteStuck {
		d.emit(ctx, "task.review_repeated", t, map[string]any{"rounds": ReviseRepeatRounds, "feedback": truncate(v.Feedback, 300)})
		sb, _ := json.Marshal(map[string]any{"stuck": true, "verdict": v.Verdict, "feedback": v.Feedback})
		return workflow.NodeOutput{Value: sb, Route: RouteStuck}
	}
	return workflow.NodeOutput{Value: b, Route: "revise"}
}

// ApproveSummary is the payload shown to the human at the approval step.
type ApproveSummary struct {
	TaskID   string               `json:"taskId"`
	Title    string               `json:"title"`
	Branch   string               `json:"branch"`
	Base     string               `json:"base"`
	Head     string               `json:"head,omitempty"` // the commit the human is asked to approve
	Files    []string             `json:"files"`
	Evidence []tasks.Evidence     `json:"evidence"`
	Reviews  []tasks.ReviewRecord `json:"reviews"`
	Cost     float64              `json:"costUsd"`
}

// Approve raises the human review request (Accept / Reject / Request Changes). The payload carries everything the human
// needs to approve a result without reconstructing what the agents did.
func Approve(d Deps) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		ev, _ := d.Tasks.Evidence(ctx, t.ID)
		rv, _ := d.Tasks.Reviews(ctx, t.ID)
		// the approval is for the commit the human is shown now (audit 2026-10-16, L15)
		var head string
		if repo, rerr := d.Repo(ctx, t.ProjectID); rerr == nil {
			head, _ = repo.Head(ctx, worktreeOf(t))
		}
		if head != "" {
			rememberReviewed(ctx, d, t, head)
		}
		payload, _ := json.Marshal(ApproveSummary{TaskID: t.ID, Title: t.Title, Branch: t.Branch, Base: t.BaseBranch, Head: head, Files: t.Files, Evidence: ev, Reviews: rv, Cost: t.CostUSD})
		return workflow.NodeOutput{Request: &workflow.RequestSpec{Kind: "review", Phase: "merge", Title: MergeRequestTitlePrefix + t.Title, Payload: payload}}, nil
	}
}

// approvalKind is the evidence kind that remembers a human approval and the change it was for.
const approvalKind = "approval"

// reviewedKind is the evidence kind that remembers the commit a human was asked to approve.
const reviewedKind = "review_requested"

func rememberReviewed(ctx context.Context, d Deps, t tasks.Task, head string) {
	data, _ := json.Marshal(map[string]string{"head": head})
	_, _ = d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: reviewedKind, Summary: "asked a human to review " + shortCommit(head), Data: data})
}

// reviewedHead is the commit of the latest review request, "" when none was recorded (a task that was already waiting for a
// person before this was recorded is not refused for it).
func reviewedHead(ctx context.Context, d Deps, taskID string) string {
	ev, err := d.Tasks.Evidence(ctx, taskID)
	if err != nil {
		return ""
	}
	for i := len(ev) - 1; i >= 0; i-- {
		if ev[i].Kind != reviewedKind {
			continue
		}
		var raw map[string]string
		if json.Unmarshal(ev[i].Data, &raw) == nil {
			return raw["head"]
		}
		return ""
	}
	return ""
}

// reviewerCommit is the commit the latest review record says its reviewer was shown, "" when none was recorded.
func reviewerCommit(ctx context.Context, d Deps, taskID string) string {
	rs, err := d.Tasks.Reviews(ctx, taskID)
	if err != nil || len(rs) == 0 {
		return ""
	}
	return rs[len(rs)-1].Commit
}

// approvalMismatch says whether a human's answer can be taken for the branch as it is now. requested is the commit the person
// was asked about and reviewed the commit the reviewing agent was shown; "" means not recorded, which is not a mismatch. The
// commit that is not head is returned with the name of what recorded it.
func approvalMismatch(requested, reviewed, head string) (why, commit string) {
	if head == "" {
		return "", ""
	}
	if requested != "" && requested != head {
		return "request", requested
	}
	if reviewed != "" && reviewed != head {
		return "review", reviewed
	}
	return "", ""
}

func shortCommit(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func rememberApproval(ctx context.Context, d Deps, t tasks.Task, by, patch string) {
	data, _ := json.Marshal(map[string]string{"by": by, "patchId": patch})
	ok := true
	_, _ = d.Tasks.AddEvidence(ctx, t.ID, tasks.Evidence{Kind: approvalKind, Passed: &ok, Summary: "approved by " + by, Data: data})
}

// approvalFor finds a human approval of exactly this change (the same patch id). Only the merge step writes these, from the
// answer of a "human:" identity, so an agent cannot produce one.
func approvalFor(ctx context.Context, d Deps, taskID, patch string) (string, bool) {
	if patch == "" {
		return "", false
	}
	ev, err := d.Tasks.Evidence(ctx, taskID)
	if err != nil {
		return "", false
	}
	for i := len(ev) - 1; i >= 0; i-- {
		if ev[i].Kind != approvalKind {
			continue
		}
		var a struct{ By, PatchID string }
		var raw map[string]string
		if json.Unmarshal(ev[i].Data, &raw) != nil {
			continue
		}
		a.By, a.PatchID = raw["by"], raw["patchId"]
		if a.PatchID == patch && strings.HasPrefix(a.By, "human:") {
			return a.By, true
		}
	}
	return "", false
}

// Changes handles a human "request changes": the task goes back to IN_PROGRESS carrying the human's feedback.
func Changes(d Deps) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		resp := humanResponse(in)
		fb := strings.TrimSpace(resp.feedback)
		if fb == "" {
			fb = "The human reviewer rejected the change without comment; re-read the task and the diff and address the likely gaps."
		}
		if _, err := d.Tasks.Transition(ctx, t.ID, tasks.InProgress, tasks.TransitionOpts{By: resp.by, Reason: "human requested changes", Feedback: fb}); err != nil {
			return workflow.NodeOutput{}, err
		}
		b, _ := json.Marshal(map[string]string{"feedback": fb, "from": "human"})
		return workflow.NodeOutput{Value: b}, nil
	}
}

type human struct {
	decision string
	by       string
	feedback string
}

// humanResponse reads the human's answer delivered through the request port.
func humanResponse(in workflow.NodeInput) human {
	var h human
	for _, m := range in.Inbox {
		var r struct {
			Decision string          `json:"decision"`
			By       string          `json:"by"`
			Data     json.RawMessage `json:"data"`
		}
		if json.Unmarshal(m.Value, &r) != nil || r.Decision == "" {
			continue
		}
		h.decision, h.by = r.Decision, r.By
		var d struct {
			Feedback string `json:"feedback"`
			Text     string `json:"text"`
		}
		if json.Unmarshal(r.Data, &d) == nil {
			h.feedback = d.Feedback
			if h.feedback == "" {
				h.feedback = d.Text
			}
		}
		return h
	}
	return h
}

// Merge performs the human-approved merge into the base branch (the human's approval is the authorization; only a
// "human:" identity can accept), completes the task, and removes the worktree when it is clean. A merge conflict parks
// the task for a human with the conflicting files.
func Merge(d Deps, project func(ctx context.Context, projectID string) (ProjectInfo, error)) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		t, err := taskFromInput(ctx, d, in)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		h := humanResponse(in)
		repo, err := d.Repo(ctx, t.ProjectID)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		pi, err := project(ctx, t.ProjectID)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		target := baseTarget(t, pi)
		w := worktreeOf(t)
		head, _ := repo.Head(ctx, w)
		patch, _ := repo.PatchID(ctx, w, target)
		stale := func(behind int) (workflow.NodeOutput, error) {
			m := map[string]any{"target": target, "behind": behind}
			d.emit(ctx, "task.stale_at_merge", t, m)
			b, _ := json.Marshal(m)
			return workflow.NodeOutput{Value: b, Route: RouteStale}, nil
		}
		// The branch is not at the commit the person was shown: what was checked and reviewed is not what would land. Back to the
		// checks, then the review and the question again (RouteChanged, as when the base was brought in).
		changedSinceReview := func(reviewed, now string) (workflow.NodeOutput, error) {
			if t.Status != tasks.InProgress {
				if _, err := d.Tasks.Transition(ctx, t.ID, tasks.InProgress, tasks.TransitionOpts{By: "system", Reason: "the branch changed after the review was asked for; the checks run again", Resync: true}); err != nil {
					return workflow.NodeOutput{}, err
				}
			}
			m := map[string]any{"target": target, "reviewed": reviewed, "now": now}
			d.emit(ctx, "task.changed_since_review", t, m)
			b, _ := json.Marshal(m)
			return workflow.NodeOutput{Value: b, Route: RouteChanged}, nil
		}
		switch {
		case strings.HasPrefix(h.by, "human:"):
			// the human answered a request that showed one commit, after a reviewer was shown one; if the branch is somewhere else
			// now, the answer is not about this change: go back, check and review again
			if why, got := approvalMismatch(reviewedHead(ctx, d, t.ID), reviewerCommit(ctx, d, t.ID), head); why != "" {
				return changedSinceReview(got, head)
			}
			// remember exactly which change the human approved, so that bringing the base branch in later does not need a second
			// approval when the change itself is the same
			if h.decision == "approved" && patch != "" {
				rememberApproval(ctx, d, t, h.by, patch)
			}
		default:
			// reached again after the base moved (sync): the approval stands only for the very same change
			by, ok := approvalFor(ctx, d, t.ID, patch)
			if !ok {
				return workflow.NodeOutput{}, fmt.Errorf("merge requires an approval by a human; got %q", h.by)
			}
			h.by = by
		}
		// Another task may have merged since the checks ran: what was checked and approved is then not what would land. Go back
		// and bring the base in, run the checks again and ask again, instead of merging an unchecked combination.
		if behind, berr := repo.Behind(ctx, w, target); berr == nil && behind > 0 {
			return stale(behind)
		}
		// The same two checks again under the repository's merge lock: another task approved in the same second passed the check
		// above too, and only one of them can land on this base (audit 2026-10-16, M14).
		commit, err := repo.MergeChecked(ctx, w, target, pi.MergeStrategy, "Merge "+t.Title+" (Drydock "+t.ID+")", git.MergeCheck{Head: head, UpToDate: true})
		if err != nil {
			var se *git.StaleBaseError
			if errors.As(err, &se) {
				return stale(se.Behind)
			}
			var he *git.ChangedSinceReviewError
			if errors.As(err, &he) {
				return changedSinceReview(he.Reviewed, he.Now)
			}
			var ce *git.ConflictError
			if errors.As(err, &ce) {
				p, _ := json.Marshal(map[string]any{"conflicts": ce.Files, "branch": t.Branch, "target": target})
				return workflow.NodeOutput{Request: &workflow.RequestSpec{Kind: "question", Title: "Merge conflict: resolve or rebase " + t.Branch, Payload: p}}, nil
			}
			return workflow.NodeOutput{}, err
		}
		d.emit(ctx, "git.commit_created", t, map[string]any{"commit": commit, "merged": t.Branch, "into": target})
		if _, err := d.Tasks.Transition(ctx, t.ID, tasks.Completed, tasks.TransitionOpts{By: h.by, Reason: "approved and merged"}); err != nil {
			return workflow.NodeOutput{}, err
		}
		cleaned := false
		if err := repo.DeleteWorktree(ctx, w, false); err == nil {
			cleaned = true
			d.emit(ctx, "git.worktree_deleted", t, map[string]any{"path": w.Path})
			if pi.DeleteBranchAfterMerge {
				_ = repo.DeleteBranch(ctx, w.Branch, false)
			}
		} // a dirty worktree is kept and flagged, never deleted silently
		b, _ := json.Marshal(map[string]any{"commit": commit, "merged": true, "worktreeRemoved": cleaned})
		return workflow.NodeOutput{Value: b}, nil
	}
}

// IterateUntilVerified is the full loop of brief section 22: work in a fresh session, deterministic checks, an
// independent review (SHIP or REVISE), human approval, human-approved merge. A worker's claim of completion never ends the
// loop: only passing checks plus SHIP plus a human do.
func IterateUntilVerified(d Deps, project func(ctx context.Context, projectID string) (ProjectInfo, error), maxLoops int) *workflow.Graph {
	if maxLoops <= 0 {
		maxLoops = 5
	}
	esc := NodeEscalate
	g := workflow.NewGraph("iterate-until-verified").
		Add(
			workflow.Route("prepare", Prepare(d)),
			workflow.AgentTask("work", Work(d)).With("agent", "work"),
			workflow.Route("guard", Guard(d, project)),
			workflow.Check("verify", Verify(d)),
			workflow.Route("finalize", Finalize(d)),
			workflow.Route("sync", Sync(d, project)),
			workflow.AgentTask("review", Review(d)).With("agent", "review"),
			&workflow.Node{ID: "approve", Kind: workflow.KindRequest, Handler: Approve(d)},
			workflow.Route("changes", Changes(d)),
			workflow.Route("merge", Merge(d, project)).Effect(EffectMerge),
		).
		Edge("prepare", "work").
		Edge("work", "guard").
		Edge("guard", "verify").
		EdgeWhen("verify", "finalize", "pass").
		Loop("verify", "work", "fail", maxLoops, esc).
		Edge("finalize", "sync").
		EdgeWhen("sync", "review", RouteCurrent).
		EdgeWhen("sync", "merge", RouteApproved).
		Loop("sync", "verify", RouteChanged, maxLoops, esc).
		Loop("sync", "work", RouteConflict, maxLoops, esc).
		Loop("merge", "sync", RouteStale, maxLoops, esc).
		Loop("merge", "verify", RouteChanged, maxLoops, esc).
		EdgeWhen("review", "approve", "ship").
		Loop("review", "work", "revise", maxLoops, esc).
		EdgeWhen("review", esc, "limit").
		EdgeWhen("review", esc, RouteStuck).
		EdgeWhen("approve", "merge", "approved").
		EdgeWhen("approve", "changes", "rejected").
		Loop("changes", "work", "", maxLoops, esc).
		Loop("merge", "merge", "answered", 3, esc)
	addEscalation(g, d, map[workflow.NodeID]workflow.NodeID{"merge": "sync"})
	return g.Start("prepare")
}
