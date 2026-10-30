package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The turn that asks the outgoing agent for a handoff (docs/token-control.md rule 6 and section 3, decision P125). When a task's
// next work turn replaces a session that could still answer (its context is full, or the turn moves to another model), Drydock
// first resumes that session for one short turn of its own: a small budget of new tokens and a time limit, read-only, and a
// prompt that asks only for a handoff written with the handoff tool. What the agent writes is then checked as any agent handoff
// is (VerifyAgentHandoff). Drydock writes the handoff itself, and records why, when the agent is not asked (a crash, a watchdog
// stop, a CLI that cannot resume, no handoff tool, no room left in the session), does not answer in time, ends its turn without a
// handoff, answers badly (the check rejects it), or goes over the budget. Since P128 the outgoing agent is asked too when another
// agent takes the task over (resumed with its own config, before the new agent starts), and no agent is asked when the handoff
// it left during its turn already passes Drydock's checks.

// Bounds of the handoff turn.
const (
	// HandoffTurnTokens is the turn's budget: the tokens it may add beyond what the CLI reads from its cache (input it reports
	// apart from cache reads, cache writes and output). A handoff of 8 KB is about 2,000 tokens; a resumed session whose cache is
	// warm reads its context from the cache.
	HandoffTurnTokens = 4000
	// HandoffTurnTimeout is how long the turn may take before Drydock stops it and writes the handoff itself.
	HandoffTurnTimeout = 2 * time.Minute
	// HandoffTurnRoom is how much room, in tokens, the outgoing session's context window must have left for the turn.
	HandoffTurnRoom = 4000
)

// Outcomes of a handoff request (HandoffRequest.Outcome).
const (
	HandoffAnswered   = "answered"    // the agent wrote a handoff during the turn (it is then checked)
	HandoffNoAnswer   = "no_answer"   // asked, and no handoff came: the time ran out, the turn failed or ended without one
	HandoffOverBudget = "over_budget" // not asked for lack of room, or the turn used more than its budget
	HandoffNotAsked   = "not_asked"   // the outgoing session cannot or should not be asked
)

// HandoffRequest is what came of asking the outgoing agent for a handoff. It is recorded in the session decision (the work
// node's output), in Drydock's handoff artifact, the turn's manifest and the agent.session event.
type HandoffRequest struct {
	Asked   bool   `json:"asked"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
	// Agent is the outgoing agent (another agent than the next turn's when it takes the task over, P128), Session the outgoing
	// session asked, Model the model it ran on.
	Agent   string `json:"agent,omitempty"`
	Session string `json:"session,omitempty"`
	Model   string `json:"model,omitempty"`
	// Tokens are the new tokens the turn used, against Budget; TimeoutS is its time limit in seconds.
	Tokens   int64   `json:"tokens,omitempty"`
	Budget   int     `json:"budget"`
	TimeoutS int     `json:"timeoutS"`
	CostUSD  float64 `json:"costUsd,omitempty"`
	// Handoff is the handoff the agent wrote during the turn (answered).
	Handoff string `json:"handoff,omitempty"`
	// StartedAt is when the turn started (Unix milliseconds): a handoff the agent wrote during it is newer.
	StartedAt int64 `json:"startedAt,omitempty"`
}

// AskForHandoff decides whether the outgoing session of a handoff decision is asked for a handoff, and why not. canResume is
// whether the outgoing agent's CLI can continue a stored session, hasTool whether its turns have the handoff tool.
func AskForHandoff(dec SessionDecision, prev *WorkOutput, canResume, hasTool bool) (ask bool, outcome, why string) {
	switch {
	case dec.Action != SessionHandoff:
		return false, HandoffNotAsked, "no session is replaced"
	case prev == nil || prev.SessionID == "":
		return false, HandoffNotAsked, "there is no outgoing session"
	case dec.Trigger == HandoffInterrupted:
		return false, HandoffNotAsked, "the previous attempt was cut off: Drydock writes the handoff after a crash"
	case dec.Trigger == HandoffWatchdog:
		return false, HandoffNotAsked, "the watchdog stopped the outgoing session: Drydock writes the handoff of a stopped session"
	case dec.Trigger == HandoffResumeFailed:
		return false, HandoffNotAsked, "the outgoing session could not be resumed"
	case !canResume:
		return false, HandoffNotAsked, "the outgoing agent's CLI cannot continue a stored session, so it cannot be asked"
	case !hasTool:
		return false, HandoffNotAsked, "the outgoing agent has no handoff tool here (no MCP gateway, or the project does not let agents act)"
	}
	used, size, _ := prev.Occupancy()
	if size <= 0 && prev.Estimate != nil {
		used, size = prev.Estimate.Used, prev.Estimate.Size
	}
	if size > 0 && size-used < HandoffTurnRoom {
		return false, HandoffOverBudget, fmt.Sprintf("the outgoing session has %d tokens of its context window left, less than the %d the handoff turn needs", max(size-used, 0), HandoffTurnRoom)
	}
	return true, "", ""
}

// handoffTurnPrompt asks the outgoing session for a handoff and nothing else.
func handoffTurnPrompt(dec SessionDecision, criteria bool) string {
	why := strings.TrimSuffix(dec.Reason, ": a fresh session seeded with a handoff artifact, never the transcript")
	ask := ""
	if criteria {
		ask = ", each acceptance criterion you consider met with what shows it (criteria: id, met, evidence)"
	}
	return "Drydock phase: handoff\n" +
		"This session is about to be replaced by a fresh one (" + why + "). Before it ends, record where the work stands with the handoff tool " +
		"(action create): summary, done, remaining, decisions, the files you changed, commands of note, each check you ran with whether it passed" + ask +
		", and next_step. Drydock checks the handoff against the working folder and the recorded check results before the fresh session gets it. " +
		"Do not change any file and do not run anything else. Answer with the handoff's id and nothing more.\n"
}

func (d Deps) handoffTurnTokens() int {
	if d.HandoffTurnTokens > 0 {
		return d.HandoffTurnTokens
	}
	return HandoffTurnTokens
}

func (d Deps) handoffTurnTimeout() time.Duration {
	if d.HandoffTurnTimeout > 0 {
		return d.HandoffTurnTimeout
	}
	return HandoffTurnTimeout
}

// hasHandoffTool reports whether the agent's work turns get the handoff tool.
func (d Deps) hasHandoffTool(ctx context.Context, t tasks.Task, agentID string) bool {
	if d.MCPStatus == nil {
		return false
	}
	st := d.MCPStatus(ctx, t, agentID, "work")
	if !st.Attached {
		return false
	}
	for _, tool := range st.Tools {
		if tool == "handoff" {
			return true
		}
	}
	return false
}

// requestHandoff asks the outgoing session for a handoff in a bounded turn, when it can be asked, and says what came of it.
// cfg is the next turn's config. The outgoing session is resumed on the model the previous turn ran on, by the same agent; when
// another agent takes the task over (P128), by the outgoing agent, with its own config, before the new agent's session starts.
func (d Deps) requestHandoff(ctx context.Context, in workflow.NodeInput, t tasks.Task, cfg rt.Config, prev *WorkOutput, dec SessionDecision) HandoffRequest {
	r := HandoffRequest{Budget: d.handoffTurnTokens(), TimeoutS: int(d.handoffTurnTimeout() / time.Second)}
	hc := cfg
	if prev != nil {
		r.Session, r.Model = prev.SessionID, prev.Model
		if dec.Trigger == HandoffAgentChange && prev.Agent != "" && d.AgentConfig != nil {
			hc = d.outgoingConfig(t, prev.Agent)
			if hc.Adapter != prev.Adapter {
				r.Agent, r.Outcome, r.Reason = hc.AgentID, HandoffNotAsked, "the outgoing agent no longer runs the CLI its session was on, so the session cannot be resumed"
				return r
			}
		}
	}
	r.Agent = hc.AgentID
	ask, outcome, why := AskForHandoff(dec, prev, d.canResume(hc), d.hasHandoffTool(ctx, t, hc.AgentID))
	if !ask {
		r.Outcome, r.Reason = outcome, why
		return r
	}
	// a handoff the outgoing agent left during its turn that passes Drydock's checks is missing nothing: no turn is needed for it
	if c := d.checkAgentHandoff(ctx, t, prev, HandoffArtifact{}); c != nil && c.Accepted {
		r.Outcome, r.Reason = HandoffNotAsked, "the outgoing session's own handoff "+c.ID+" from its turn passed Drydock's checks, so nothing is missing to ask for"
		return r
	}
	hc.Spec.Model = prev.Model
	_, _, totals := prev.Occupancy()
	hc.Spec.ResumeID, hc.Spec.UsageBaseline = prev.SessionID, totals
	hc.Spec.PermissionMode, hc.Spec.ReadOnly = "plan", true
	prompt := handoffTurnPrompt(dec, len(t.Acceptance) > 0)
	r.Asked, r.StartedAt = true, storage.Now()
	tctx, cancel := context.WithTimeout(ctx, d.handoffTurnTimeout())
	out, err := d.Runtime.AgentTask(rt.TaskSpec{Config: hc, Prompt: func(workflow.NodeInput) string { return prompt }})(tctx, in)
	timedOut := errors.Is(tctx.Err(), context.DeadlineExceeded)
	cancel()
	var res rt.TurnResult
	_ = json.Unmarshal(out.Value, &res)
	for _, u := range res.Usage {
		r.Tokens += u.InputTokens + u.CacheWriteTokens + u.OutputTokens
		r.CostUSD += u.CostUSD
	}
	if r.CostUSD > 0 && d.Tasks != nil {
		_, _ = d.Tasks.Patch(ctx, t.ID, tasks.Update{AddCost: r.CostUSD})
	}
	written := d.handoffWrittenSince(ctx, t.ID, hc.AgentID, r.StartedAt)
	switch {
	case ctx.Err() != nil:
		r.Outcome, r.Reason = HandoffNoAnswer, "the run was stopped while the outgoing session was asked"
	case timedOut:
		r.Outcome, r.Reason = HandoffNoAnswer, fmt.Sprintf("the outgoing session did not answer within %d s, so Drydock stopped the turn", r.TimeoutS)
	case r.Tokens > int64(r.Budget):
		r.Outcome, r.Reason = HandoffOverBudget, fmt.Sprintf("the handoff turn used %d new tokens, more than its budget of %d, so what it wrote is not used", r.Tokens, r.Budget)
	case written != "":
		r.Outcome, r.Handoff, r.Reason = HandoffAnswered, written, "the outgoing session wrote handoff "+written
	case err != nil:
		r.Outcome, r.Reason = HandoffNoAnswer, "the handoff turn failed: "+clip(oneLine(err.Error()), 200)
	case !res.Started:
		r.Outcome, r.Reason = HandoffNoAnswer, "the outgoing session could not be resumed"
	default:
		r.Outcome, r.Reason = HandoffNoAnswer, "the outgoing session ended its turn without writing a handoff"
	}
	if r.Outcome != HandoffAnswered {
		r.Handoff = written
	}
	return r
}

// outgoingConfig is the work config of the agent a task is moving away from: the task as it was assigned to it.
func (d Deps) outgoingConfig(t tasks.Task, agentID string) rt.Config {
	ot := t
	ot.Assignee = agentID
	oc := d.AgentConfig(ot, "work")
	oc.TaskID, oc.ProjectID = t.ID, t.ProjectID
	oc.Spec.Cwd = t.Worktree
	return oc
}

// handoffWrittenSince is the newest handoff an agent wrote with the handoff tool for a task since a time (” when none).
func (d Deps) handoffWrittenSince(ctx context.Context, taskID, agentID string, since int64) string {
	if d.Tasks == nil || d.Tasks.DB == nil {
		return ""
	}
	var id string
	_ = d.Tasks.DB.Reader().QueryRowContext(ctx, `SELECT id FROM handoffs WHERE task_id=? AND agent_id=? AND reason='agent' AND created_at>=?
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, taskID, agentID, since).Scan(&id)
	return id
}

// requestNote is the manifest line of a handoff request.
func requestNote(r HandoffRequest) string {
	switch {
	case !r.Asked:
		return "Drydock did not ask the outgoing session for a handoff: " + r.Reason
	case r.Outcome == HandoffAnswered:
		return fmt.Sprintf("Drydock asked the outgoing session %s for a handoff (a budget of %d new tokens and %d s; it used %d): %s", r.Session, r.Budget, r.TimeoutS, r.Tokens, r.Reason)
	}
	return fmt.Sprintf("Drydock asked the outgoing session %s for a handoff (a budget of %d new tokens and %d s) and wrote its own: %s", r.Session, r.Budget, r.TimeoutS, r.Reason)
}
