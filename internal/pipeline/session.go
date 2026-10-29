package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	"github.com/kjmaguire/drydock/internal/checks"
	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/tasks"
	"github.com/kjmaguire/drydock/internal/watchdog"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Session policy of a task's work turns (brief section 5, docs/token-control.md rules 5 and 6, docs/model-routing.md s8).
//
// Between two work turns of the same run, Drydock decides whether the next turn continues the previous turn's CLI session
// (--resume) or starts a fresh one. The decision is taken only when a turn starts, from what the previous turn reported when it
// ended, so a turn is never cut in the middle. A fresh session that follows another session of the task is seeded with a handoff
// artifact that Drydock writes deterministically (no extra agent turn): the task and its acceptance criteria, what the previous
// session said it did, the diff stat of the worktree, the failing checks and the open questions. It never gets the transcript.

// ContextHandoffPct is the context occupancy (percent of the model's context window) at or above which the next turn of a task
// does not continue the session but starts a fresh one seeded with a handoff artifact.
const ContextHandoffPct = 70

// MaxHandoffBytes bounds one stored handoff artifact (token-control s3: about 1,500 tokens, enforced at write time).
const MaxHandoffBytes = 8 << 10

// Session actions.
const (
	SessionFresh    = "fresh"    // a new session with the task and the latest feedback (nothing to continue or hand off)
	SessionContinue = "continue" // the previous turn's session continues (--resume)
	SessionHandoff  = "handoff"  // a new session seeded with a handoff artifact of the previous one
)

// Why a fresh session was seeded with a handoff artifact (SessionDecision.Trigger, handoffs.reason).
const (
	HandoffContextPressure = "context_pressure" // the previous turn ended at or above ContextHandoffPct occupancy
	HandoffEscalation      = "escalation"       // the router escalated the turn to a different model
	HandoffModelChange     = "model_change"     // the turn runs on a different model for another reason
	HandoffAgentChange     = "agent_change"     // another agent (or CLI) takes the task over
	HandoffInterrupted     = "interrupted"      // the previous attempt of this turn was cut off (the app stopped)
	HandoffWatchdog        = "watchdog"         // the watchdog paused the previous turn and a human let the work go on
	HandoffResumeFailed    = "resume_failed"    // the session to continue could not be resumed
)

// SessionDecision is what the session policy decided for one work turn. It is recorded in the turn's output, its context
// manifest and an agent.session event.
type SessionDecision struct {
	Action  string `json:"action"`
	Trigger string `json:"trigger,omitempty"`
	Reason  string `json:"reason"`
	// PrevSession is the previous turn's CLI session (the one continued, or the one handed off).
	PrevSession  string  `json:"prevSession,omitempty"`
	ContextUsed  int64   `json:"contextUsed,omitempty"`
	ContextSize  int64   `json:"contextSize,omitempty"`
	OccupancyPct float64 `json:"occupancyPct,omitempty"`
	ThresholdPct int     `json:"thresholdPct"`
	// Compactions is how many times the CLI compacted the previous turn's context (a handoff for context pressure).
	Compactions int    `json:"compactions,omitempty"`
	FromModel   string `json:"fromModel,omitempty"`
	ToModel     string `json:"toModel,omitempty"`
	Handoff     string `json:"handoff,omitempty"` // the handoff artifact's id, for SessionHandoff
}

// SessionPlan is what the next turn will be.
type SessionPlan struct {
	Agent, Adapter, Model string
	// CanResume is true when the agent's CLI can continue a stored session (Capabilities.SessionResume) and this session keeps
	// what it does (no --no-session-persistence).
	CanResume bool
	// Escalated is true when the router asked for a handoff artifact for this turn (RoutedModel.Handoff).
	Escalated bool
	// Interrupted is true for a retry of an attempt the app cut off (workflow.NodeInput.Previous).
	Interrupted bool
	// WatchdogPause is true when the turn follows a watchdog pause a human answered (a watchdog notice in the inbox).
	WatchdogPause bool
}

// WorkOutput is a work node's output: the turn's result plus who ran it and the session decision. It is a superset of
// rt.TurnResult, so a reader of the turn result reads it unchanged.
type WorkOutput struct {
	rt.TurnResult
	Agent   string           `json:"agent,omitempty"`
	Adapter string           `json:"adapter,omitempty"`
	Model   string           `json:"model,omitempty"`
	Session *SessionDecision `json:"session,omitempty"`
	// StartedAt is when the turn started (Unix milliseconds): a handoff the agent wrote with the handoff tool counts as this
	// turn's only when it is newer.
	StartedAt int64 `json:"startedAt,omitempty"`
}

// Occupancy is the context occupancy the turn's agent reported last: tokens in the context and the model's window (0 when the
// CLI reported none), and the session's running usage totals (passed back when the session is continued).
func (w WorkOutput) Occupancy() (used, size int64, totals json.RawMessage) {
	for _, u := range w.Usage {
		if u.ContextSize > 0 {
			used, size = u.ContextUsed, u.ContextSize
		}
		if len(u.Totals) > 0 {
			totals = u.Totals
		}
	}
	return used, size, totals
}

// Compactions is how many times the turn's CLI compacted the session's context during the turn (UsageChunk.Compactions).
func (w WorkOutput) Compactions() int {
	n := 0
	for _, u := range w.Usage {
		n += u.Compactions
	}
	return n
}

// DecideSession decides whether the next work turn continues the previous turn's session. prev is the previous work turn of
// the run (nil for the first).
func DecideSession(prev *WorkOutput, p SessionPlan) SessionDecision {
	d := SessionDecision{ThresholdPct: ContextHandoffPct, ToModel: p.Model}
	if prev == nil || prev.SessionID == "" {
		d.Action, d.Reason = SessionFresh, "the first work turn of this run: a fresh session with the task"
		return d
	}
	d.PrevSession, d.FromModel = prev.SessionID, prev.Model
	d.ContextUsed, d.ContextSize, _ = prev.Occupancy()
	if d.ContextSize > 0 {
		d.OccupancyPct = math.Round(1000*float64(d.ContextUsed)/float64(d.ContextSize)) / 10
	}
	handoff := func(trigger, reason string) SessionDecision {
		d.Action, d.Trigger, d.Reason = SessionHandoff, trigger, reason+": a fresh session seeded with a handoff artifact, never the transcript"
		return d
	}
	occ := fmt.Sprintf("context occupancy %.1f%% of %d tokens", d.OccupancyPct, d.ContextSize)
	switch {
	case p.Interrupted:
		return handoff(HandoffInterrupted, "the previous attempt of this turn was cut off")
	case p.WatchdogPause:
		return handoff(HandoffWatchdog, "the watchdog stopped the previous turn and a human let the work go on with a different approach")
	case prev.Agent != p.Agent || prev.Adapter != p.Adapter:
		return handoff(HandoffAgentChange, "another agent takes the task over")
	case prev.Model != p.Model && p.Escalated:
		return handoff(HandoffEscalation, fmt.Sprintf("the router escalated the turn from %s to %s", orNone(prev.Model), orNone(p.Model)))
	case prev.Model != p.Model:
		return handoff(HandoffModelChange, fmt.Sprintf("the turn runs on %s instead of %s", orNone(p.Model), orNone(prev.Model)))
	case !p.CanResume:
		d.Action, d.Reason = SessionFresh, "the agent's CLI cannot continue a stored session here: a fresh session with the task and the latest feedback"
		return d
	case prev.Compactions() > 0:
		// the CLI ran out of room during the turn and replaced the transcript with a summary of its own: past the threshold by
		// definition, and the session no longer holds what continuing it would assume (OpenCode reports this over ACP)
		d.Compactions = prev.Compactions()
		return handoff(HandoffContextPressure, "the agent's CLI compacted the session's context during the previous turn (it ran out of room)")
	case d.ContextSize <= 0:
		d.Action, d.Reason = SessionFresh, "the previous turn reported no context occupancy: a fresh session with the task and the latest feedback"
		return d
	case d.ContextUsed*100 >= d.ContextSize*ContextHandoffPct:
		return handoff(HandoffContextPressure, fmt.Sprintf("%s is at or above the %d%% threshold", occ, ContextHandoffPct))
	}
	d.Action, d.Reason = SessionContinue, fmt.Sprintf("%s is below the %d%% threshold: the same session continues", occ, ContextHandoffPct)
	return d
}

func orNone(model string) string {
	if model == "" {
		return "the CLI's default model"
	}
	return model
}

// previousWork is the run's latest completed work turn (the work node's output in the shared state), nil when there is none.
func previousWork(in workflow.NodeInput) *WorkOutput {
	raw, ok := in.Shared["out/work"]
	if !ok {
		return nil
	}
	if v, err := workflow.ReadRef(raw); err == nil {
		raw = v
	}
	var w WorkOutput
	if json.Unmarshal(raw, &w) != nil || w.SessionID == "" {
		return nil
	}
	return &w
}

// watchdogNotice reports whether the inbox carries a watchdog notice (a human answered a watchdog pause of this node).
func watchdogNotice(in workflow.NodeInput) bool {
	for _, m := range in.Inbox {
		var o map[string]any
		if json.Unmarshal(m.Value, &o) == nil {
			if _, ok := o["watchdog"].(string); ok {
				return true
			}
		}
	}
	return false
}

// canResume reports whether the turn's CLI can continue a stored session.
func (d Deps) canResume(cfg rt.Config) bool {
	if d.Runtime == nil {
		return false
	}
	ad, ok := d.Runtime.Adapters[cfg.Adapter]
	if !ok || !ad.Capabilities().SessionResume {
		return false
	}
	for _, a := range cfg.Spec.ExtraArgs {
		if a == "--no-session-persistence" {
			return false
		}
	}
	return true
}

// HandoffTask is the task as a handoff artifact carries it.
type HandoffTask struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Acceptance  []string `json:"acceptance,omitempty"`
	Status      string   `json:"status"`
	Iteration   int      `json:"iteration"`
	Branch      string   `json:"branch,omitempty"`
	Files       []string `json:"files,omitempty"`
}

// HandoffTaskOf is the task part of every handoff artifact (the watchdog's stage-2 artifact uses it too).
func HandoffTaskOf(t tasks.Task) HandoffTask {
	return HandoffTask{ID: t.ID, Title: t.Title, Description: clip(strings.TrimSpace(t.Description), 1200), Acceptance: t.Acceptance,
		Status: string(t.Status), Iteration: t.Iteration, Branch: t.Branch, Files: t.Files}
}

// HandoffArtifact is the artifact Drydock writes when a task's next turn starts a fresh session instead of continuing one.
type HandoffArtifact struct {
	Kind          string      `json:"kind"` // "session_handoff"
	Trigger       string      `json:"trigger"`
	Reason        string      `json:"reason"`
	Node          string      `json:"node"`
	Attempt       int         `json:"attempt"`
	Workflow      string      `json:"workflow"`
	Task          HandoffTask `json:"task"`
	Done          string      `json:"done,omitempty"`     // the previous session's final summary
	DiffStat      string      `json:"diffStat,omitempty"` // the worktree against its base: files, insertions, deletions
	FilesTouched  []string    `json:"filesTouched,omitempty"`
	FailingChecks []string    `json:"failingChecks,omitempty"`
	Remaining     []string    `json:"remaining,omitempty"` // review feedback and guidance the next turn must address
	OpenQuestions []string    `json:"openQuestions,omitempty"`
	FromSession   string      `json:"fromSession,omitempty"`
	FromModel     string      `json:"fromModel,omitempty"`
	ToModel       string      `json:"toModel,omitempty"`
	ContextUsed   int64       `json:"contextUsed,omitempty"`
	ContextSize   int64       `json:"contextSize,omitempty"`
	OccupancyPct  float64     `json:"occupancyPct,omitempty"`
	// The fields of token-control section 3 that P39 did not write (decision P119): the goal, where the acceptance criteria
	// stand by the recorded facts, the safe point and head the work starts from, the task's decisions, the touched files with
	// their content hashes, the failing check's signature, the commands Drydock ran, references to the evidence on disk (never
	// its content) and the next step.
	Goal                  string        `json:"goal,omitempty"`
	AcceptanceStatus      string        `json:"acceptanceStatus,omitempty"`
	WorktreeRef           *WorktreeRef  `json:"worktreeRef,omitempty"`
	Decisions             []string      `json:"decisions,omitempty"`
	Files                 []HandoffFile `json:"files,omitempty"`
	FailingCheckSignature string        `json:"failingCheckSignature,omitempty"`
	CommandsOfNote        []string      `json:"commandsOfNote,omitempty"`
	EvidenceRefs          []string      `json:"evidenceRefs,omitempty"`
	NextStep              string        `json:"nextStep,omitempty"`
	// AgentHandoff is the handoff the previous session wrote itself with the handoff tool, as Drydock checked it: when it
	// passed, the fresh session gets its account of the work; when it failed, the reasons, and the session gets Drydock's own.
	AgentHandoff *AgentHandoffCheck `json:"agentHandoff,omitempty"`
}

// WorktreeRef is where a handoff's work starts from: the safe point recorded before this attempt and the branch head.
type WorktreeRef struct {
	SafePoint string `json:"safePoint,omitempty"`
	Head      string `json:"head,omitempty"`
}

// HandoffFile is a file the work touched, with the first 12 hex digits of the SHA-256 of its content now ("deleted" when it is
// gone), so the next session can tell whether a file changed since.
type HandoffFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// HandoffRecord is one row of handoffs.
type HandoffRecord struct {
	ProjectID, TaskID, RunID, AgentID string
	Reason                            string // watchdog | context_pressure | escalation | ...
	Artifact                          any
	// Event is extra data for the handoff.created event.
	Event map[string]any
}

// SaveHandoff stores a handoff artifact (bounded by MaxHandoffBytes, redacted) in handoffs and publishes handoff.created. The
// watchdog's stage-2 artifact and the session handoffs are written by it.
func SaveHandoff(ctx context.Context, db *storage.DB, bus events.Bus, redact func(string) string, h HandoffRecord) (string, error) {
	b, err := json.Marshal(h.Artifact)
	if err != nil {
		return "", err
	}
	if len(b) > MaxHandoffBytes {
		b = b[:MaxHandoffBytes]
	}
	body := string(b)
	if redact != nil {
		body = redact(body)
	}
	id := ids.New("hnd")
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO handoffs(id,task_id,run_id,agent_id,artifact,reason,created_at) VALUES(?,?,?,?,?,?,?)`,
			id, h.TaskID, h.RunID, h.AgentID, body, h.Reason, storage.Now())
		return err
	}); err != nil {
		return "", err
	}
	if bus != nil {
		data := map[string]any{"handoff": id, "reason": h.Reason}
		for k, v := range h.Event {
			data[k] = v
		}
		raw, _ := json.Marshal(data)
		_, _ = bus.Publish(ctx, events.Event{Type: "handoff.created", ProjectID: h.ProjectID, TaskID: h.TaskID, RunID: h.RunID, AgentID: h.AgentID, Data: raw})
	}
	return id, nil
}

// redact is Deps.Redact, else the scanner's (Deps.Scanner, or the process-wide one).
func (d Deps) redact(s string) string {
	if d.Redact != nil {
		return d.Redact(s)
	}
	sc := d.Scanner
	if sc == nil {
		var err error
		if sc, err = checks.DefaultScanner(); err != nil {
			return s
		}
	}
	return sc.Redact(s)
}

// buildHandoff synthesizes the artifact from what Drydock knows: the task, the previous turn's final text, the worktree's diff
// against its base, and the failing checks and feedback in the inbox.
func (d Deps) buildHandoff(ctx context.Context, in workflow.NodeInput, t tasks.Task, prev *WorkOutput, dec SessionDecision) HandoffArtifact {
	h := HandoffArtifact{Kind: "session_handoff", Trigger: dec.Trigger, Reason: dec.Reason, Node: string(in.NodeID), Attempt: in.Attempt, Workflow: in.Workflow,
		Task: HandoffTaskOf(t), FromSession: dec.PrevSession, FromModel: dec.FromModel, ToModel: dec.ToModel,
		ContextUsed: dec.ContextUsed, ContextSize: dec.ContextSize, OccupancyPct: dec.OccupancyPct}
	if prev != nil {
		h.Done = clip(strings.TrimSpace(prev.Text), 1500)
		h.OpenQuestions = questions(prev.Text, 5)
	}
	h.Goal = t.Title
	if d.Repo != nil && t.Worktree != "" {
		if repo, err := d.Repo(ctx, t.ProjectID); err == nil {
			w := worktreeOf(t)
			if st, err := repo.DiffStat(ctx, w, t.BaseBranch); err == nil {
				h.DiffStat = fmt.Sprintf("%d file(s), +%d -%d", st.Files, st.Insertions, st.Deletions)
			}
			if paths, err := repo.ChangedPaths(ctx, w, t.BaseBranch); err == nil {
				h.Files = fileHashes(t.Worktree, paths, maxHandoffFiles)
				if len(paths) > 40 {
					paths = append(paths[:40], fmt.Sprintf("... and %d more", len(paths)-40))
				}
				h.FilesTouched = paths
			}
			ref := &WorktreeRef{SafePoint: d.safePointOf(ctx, in.NodeInfo)}
			if head, err := repo.Head(ctx, w); err == nil {
				ref.Head = head
			}
			if ref.SafePoint != "" || ref.Head != "" {
				h.WorktreeRef = ref
			}
		}
	}
	for _, m := range in.Inbox {
		var cr workflow.CheckResult
		if json.Unmarshal(m.Value, &cr) == nil && cr.Name != "" && !cr.Passed {
			f := fmt.Sprintf("%s (exit %d)", cr.Name, cr.ExitCode)
			if len(cr.FailingTests) > 0 {
				f += ": " + clip(strings.Join(cr.FailingTests, ", "), 400)
			}
			h.FailingChecks = append(h.FailingChecks, f)
			if h.FailingCheckSignature == "" {
				h.FailingCheckSignature = cr.Name + ":" + watchdog.FailureSignature(cr.Output, cr.FailingTests)
			}
		}
	}
	for _, f := range inboxFeedback(in) {
		if !strings.HasPrefix(f, "The ") || !strings.Contains(f, " check failed") {
			h.Remaining = append(h.Remaining, clip(f, 600))
		}
	}
	h.Decisions = d.taskDecisions(ctx, t.ID)
	if ev, err := d.Tasks.Evidence(ctx, t.ID); err == nil {
		h.CommandsOfNote, h.EvidenceRefs = checkCommandsAndRefs(ev)
	}
	h.AcceptanceStatus = acceptanceStatus(t, len(h.FailingChecks) > 0)
	h.NextStep = nextStep(h)
	return h
}

// questions are the lines of an agent's text that ask something (at most n, each bounded).
func questions(text string, n int) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "-*0123456789.) "))
		if strings.HasSuffix(l, "?") && len(l) > 3 {
			out = append(out, clip(l, 300))
			if len(out) == n {
				break
			}
		}
	}
	return out
}

// HandoffPrompt is what a fresh session seeded with a handoff gets before the turn's own prompt. The task, its acceptance
// criteria and what to fix now are in that prompt, so they are not repeated here. When the previous session's own handoff passed
// Drydock's checks, its account of the work takes the place of its final text.
func HandoffPrompt(id string, h HandoffArtifact) string {
	return handoffIntro(id, h) + quoted("Handoff", handoffBody(h)) + "\n"
}

// handoffIntro is what the fresh session is told before the handoff's data block.
func handoffIntro(id string, h HandoffArtifact) string {
	why := strings.TrimSuffix(h.Reason, ": a fresh session seeded with a handoff artifact, never the transcript")
	return fmt.Sprintf("This is a fresh session on a task another session was working on (%s). Its transcript is not included; "+
		"Drydock wrote this handoff (%s) of where the work stands. The files in the working folder are the current state: read them "+
		"before you change anything. The task and what to do now follow the handoff.\n\n", why, nonEmptyStr(id, "not stored"))
}

// handoffBody is the handoff's data block.
func handoffBody(h HandoffArtifact) string {
	var b strings.Builder
	agent := h.AgentHandoff
	if agent != nil && agent.Accepted && agent.Handoff != nil {
		a := agent.Handoff
		b.WriteString("The previous session's own handoff (" + agent.ID + "; Drydock checked that its files exist and that what it says about the checks matches their recorded results):\n")
		b.WriteString(a.Summary + "\n")
		list := func(title string, items []string) {
			if len(items) > 0 {
				b.WriteString(title + ":\n")
				for _, it := range items {
					b.WriteString("- " + it + "\n")
				}
			}
		}
		list("Done", a.Done)
		list("Remaining", a.Remaining)
		list("Decisions", a.Decisions)
		list("Commands of note", a.Commands)
		list("Open questions", a.Open)
	} else if h.Done != "" {
		b.WriteString("What the previous session said it did:\n" + h.Done + "\n")
	}
	if h.DiffStat != "" {
		b.WriteString("Changes in the working folder so far: " + h.DiffStat)
		if len(h.FilesTouched) > 0 {
			b.WriteString(" (" + strings.Join(h.FilesTouched, ", ") + ")")
		}
		b.WriteString("\n")
	}
	if h.WorktreeRef != nil && h.WorktreeRef.SafePoint != "" {
		b.WriteString("Safe point of this attempt: " + h.WorktreeRef.SafePoint + "\n")
	}
	if len(h.FailingChecks) > 0 {
		b.WriteString("Failing checks: " + strings.Join(h.FailingChecks, "; ") + "\n")
	}
	if len(h.Decisions) > 0 {
		b.WriteString("Decisions recorded for this task: " + strings.Join(h.Decisions, "; ") + "\n")
	}
	if len(h.CommandsOfNote) > 0 {
		b.WriteString("Commands Drydock runs as checks: " + strings.Join(h.CommandsOfNote, "; ") + "\n")
	}
	if h.AcceptanceStatus != "" {
		b.WriteString("Acceptance criteria: " + h.AcceptanceStatus + "\n")
	}
	if (agent == nil || !agent.Accepted) && len(h.OpenQuestions) > 0 {
		b.WriteString("Open questions the previous session left:\n")
		for _, q := range h.OpenQuestions {
			b.WriteString("- " + q + "\n")
		}
	}
	if b.Len() == 0 {
		b.WriteString("The previous session left no summary and the working folder has no changes yet.\n")
	}
	next := h.NextStep
	if agent != nil && agent.Accepted && agent.Handoff != nil && agent.Handoff.NextStep != "" {
		next = agent.Handoff.NextStep
	}
	if next != "" {
		b.WriteString("Next step: " + next + "\n")
	}
	return b.String()
}

// runWorkTurn runs a worker's turn under the session policy: it decides between continuing the previous turn's session and a
// fresh one (seeded with a handoff artifact when another session of the task came before), assembles the turn's package under
// the project's ceiling, records the decision (turn output, context manifest, agent.session event), runs the turn, and adds its
// cost to the task.
func (d Deps) runWorkTurn(ctx context.Context, in workflow.NodeInput, t tasks.Task, cfg rt.Config, pieces []ctxeng.Piece) (workflow.NodeOutput, error) {
	prev := previousWork(in)
	routed, _ := RoutedFrom(ctx)
	dec := DecideSession(prev, SessionPlan{Agent: cfg.AgentID, Adapter: cfg.Adapter, Model: cfg.Spec.Model, CanResume: d.canResume(cfg),
		Escalated: routed.Handoff, Interrupted: in.Previous != nil, WatchdogPause: watchdogNotice(in)})
	started := storage.Now()
	turnPrompt := d.prepareSession(ctx, in, t, &cfg, prev, &dec, pieces)
	out, err := d.Runtime.AgentTask(rt.TaskSpec{Config: cfg, Prompt: func(workflow.NodeInput) string { return turnPrompt }})(ctx, in)
	if dec.Action == SessionContinue && resumeFailed(ctx, out, err) {
		// the stored session is gone (deleted, or kept by another machine): hand the work to a fresh session instead
		failed := dec.PrevSession
		dec.Action, dec.Trigger = SessionHandoff, HandoffResumeFailed
		dec.Reason = "session " + failed + " could not be resumed: a fresh session seeded with a handoff artifact, never the transcript"
		cfg.Spec.ResumeID, cfg.Spec.UsageBaseline = "", nil
		turnPrompt = d.prepareSession(ctx, in, t, &cfg, prev, &dec, pieces)
		out, err = d.Runtime.AgentTask(rt.TaskSpec{Config: cfg, Prompt: func(workflow.NodeInput) string { return turnPrompt }})(ctx, in)
	}
	if err != nil {
		return out, err
	}
	var res rt.TurnResult
	if json.Unmarshal(out.Value, &res) == nil {
		var cost float64
		for _, u := range res.Usage {
			cost += u.CostUSD
		}
		_, _ = d.Tasks.Patch(ctx, t.ID, tasks.Update{AddCost: cost})
		d.recordDoD(ctx, t, res.Text)
		if d.OnTurn != nil && strings.TrimSpace(res.Text) != "" {
			d.OnTurn(ctx, t, cfg.AgentID, "work", res.Text)
		}
		b, _ := json.Marshal(WorkOutput{TurnResult: res, Agent: cfg.AgentID, Adapter: cfg.Adapter, Model: cfg.Spec.Model, Session: &dec, StartedAt: started})
		out.Value = b
	}
	return out, nil
}

// prepareSession applies a decision to the turn: the session to resume, or the handoff artifact written (with the previous
// session's own handoff when it passes Drydock's checks) and put before the prompt. It assembles the package under the project's
// ceiling, records the decision and the package in the manifest and as an agent.session event, and returns the prompt the turn
// sends.
func (d Deps) prepareSession(ctx context.Context, in workflow.NodeInput, t tasks.Task, cfg *rt.Config, prev *WorkOutput, dec *SessionDecision, pieces []ctxeng.Piece) string {
	var head []ctxeng.Piece
	var check *AgentHandoffCheck
	switch dec.Action {
	case SessionContinue:
		_, _, totals := prev.Occupancy()
		cfg.Spec.ResumeID, cfg.Spec.UsageBaseline = prev.SessionID, totals
	case SessionHandoff:
		art := d.buildHandoff(ctx, in, t, prev, *dec)
		check = d.checkAgentHandoff(ctx, t, prev, art)
		art.AgentHandoff = check
		if check != nil && check.Accepted {
			art.NextStep = nonEmptyStr(check.Handoff.NextStep, art.NextStep)
		}
		id := ""
		if d.Tasks != nil && d.Tasks.DB != nil {
			var err error
			if id, err = SaveHandoff(ctx, d.Tasks.DB, d.Bus, d.redact, HandoffRecord{ProjectID: t.ProjectID, TaskID: t.ID, RunID: string(in.RunID),
				AgentID: cfg.AgentID, Reason: dec.Trigger, Artifact: art, Event: map[string]any{"node": string(in.NodeID), "attempt": in.Attempt}}); err != nil {
				id = ""
			}
			d.recordHandoffCheck(ctx, check, id)
		}
		dec.Handoff = id
		hp := quotedPiece(ctxeng.KindHandoff, "handoff:"+nonEmptyStr(id, "unsaved"),
			"handoff artifact ("+dec.Trigger+") put before the prompt: what the previous session did, the diff stat, failing checks, decisions and the next step",
			"the handoff tool (action get) reads it whole", d.redact(handoffIntro(id, art)), "Handoff", d.redact(handoffBody(art)), "\n")
		hp.Priority = 2
		head = append(head, hp)
		if check != nil {
			it := ctxeng.Piece{Source: "agent handoff:" + check.ID, Priority: 2}
			if check.Accepted {
				it.Skip = "the previous session's own handoff passed Drydock's checks: its account of the work is in the handoff above"
			} else {
				it.Skip = "the previous session's own handoff was not used, Drydock's handoff was: " + strings.Join(check.Reasons, "; ")
			}
			head = append(head, it)
		}
	}
	asm := ctxeng.Assemble(d.packageCeiling(ctx, t), append(append(d.instructionPieces(t), head...), pieces...))
	d.saveManifest(ctx, in, t, cfg.AgentID, asm.Text, "work", dec, &asm)
	data := map[string]any{"action": dec.Action, "trigger": dec.Trigger, "reason": dec.Reason, "occupancyPct": dec.OccupancyPct,
		"contextUsed": dec.ContextUsed, "contextSize": dec.ContextSize, "thresholdPct": dec.ThresholdPct, "handoff": dec.Handoff,
		"fromModel": dec.FromModel, "toModel": dec.ToModel, "node": string(in.NodeID), "attempt": in.Attempt,
		"packageTokens": asm.Tokens, "packageCeiling": asm.Ceiling, "leftOut": len(asm.LeftOut())}
	if check != nil {
		data["agentHandoff"] = check.ID
		data["agentHandoffAccepted"] = check.Accepted
		if !check.Accepted {
			data["agentHandoffReasons"] = check.Reasons
		}
	}
	b, _ := json.Marshal(data)
	if d.Bus != nil {
		_, _ = d.Bus.Publish(ctx, events.Event{Type: "agent.session", ProjectID: t.ProjectID, TaskID: t.ID, RunID: string(in.RunID), AgentID: cfg.AgentID, Data: b})
	}
	return asm.Text
}

// resumeFailed reports whether a turn that tried to continue a session failed before the session started.
func resumeFailed(ctx context.Context, out workflow.NodeOutput, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if err != nil {
		return errors.Is(err, rt.ErrNotStarted)
	}
	var res rt.TurnResult
	return json.Unmarshal(out.Value, &res) == nil && !res.Started
}
