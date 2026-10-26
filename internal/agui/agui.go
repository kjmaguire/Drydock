// Package agui is Drydock's typed vocabulary for what an agent does during a run, modelled on the AG-UI protocol 1.0
// (docs.ag-ui.com): a run's lifecycle, its text and reasoning messages, its tool calls with their arguments and results, and
// its plan as an activity (decision P87). The events are not a second record: Project reads the agent.* events the core
// already stored for a task and returns them in this vocabulary, one stream per agent process, so the UI draws a live
// timeline from typed events instead of reading raw chunks. The TypeScript types are generated from these structs
// (cmd/drydock-tsgen, ui/app/api/agui.gen.ts) and a test keeps the two in step.
//
// Mapping (one agent process is a thread; each turn is a run):
//
//	agent.started                             the thread's adapter, model, runMode and wslDistro (no event)
//	agent.waiting {state: working}            RUN_STARTED
//	agent.output text                         TEXT_MESSAGE_START (first delta) + TEXT_MESSAGE_CONTENT
//	agent.output message_end                  TEXT_MESSAGE_END (or START, CONTENT, END for a message that came whole)
//	agent.output thinking                     REASONING_MESSAGE_START (first delta) + REASONING_MESSAGE_CONTENT
//	agent.output tool_call                    TOOL_CALL_START, TOOL_CALL_ARGS, TOOL_CALL_END
//	agent.output tool_result                  TOOL_CALL_RESULT
//	agent.output plan                         ACTIVITY_SNAPSHOT {activityType: PLAN}
//	agent.output permission_denied {reason}   CUSTOM drydock.decided {outcome: refused}: Drydock refused it with nobody to ask
//	agent.output error {fatal}                RUN_ERROR
//	agent.completed                           RUN_FINISHED {outcome: {type: success}}
//	agent.completed {stopReason: error*}      RUN_ERROR with the turn's last error (also: an error, no answer, no end_turn)
//	agent.completed {stopReason: interrupted} RUN_FINISHED {outcome: {type: cancelled}} (and cancelled)
//	agent.failed                              RUN_ERROR (a failure outside a turn is a run of its own that errored)
//	agent.failed {phase: bookkeeping}         left out: Drydock could not record the run's end, the agent did not fail
//	agent.stopped during a turn               RUN_FINISHED {outcome: {type: cancelled}}
//	approval.requested (a permission request) CUSTOM drydock.interrupt: the turn waits for a person (P88)
//	permission.decided after a request        CUSTOM drydock.resume: how the person (or nobody) answered
//	permission.decided without a request      CUSTOM drydock.decided: a rule refused the call, or allowed it for the task
//
// AG-UI 1.0 ends a run with outcome "interrupt" and resumes it as a new run; a Drydock agent's turn does not end while it
// waits, so the wait and its answer are AG-UI CUSTOM events inside the turn, named drydock.interrupt and drydock.resume. A
// decision nobody was asked for (a rule's refusal, Drydock's refusal in a session with nobody to ask, an approval for the
// whole task) is drydock.decided; the ordinary allowed_by_rule decision of every call is left out, it would flood the feed,
// and so are the CLI's own permission_denials, whose failed results (and, when the gate was asked, the resume) already tell it.
//
// Whether a turn failed is workflow.TurnFailed, the one rule the chat (internal/chats) and this projection read a turn by, so
// one turn is never told two ways: the adapter's own error stop reason fails it, and so does an error with no answer and no
// end_turn; warnings that carry a code (adapter drift, a setting the agent does not offer) never do.
//
// A message_end is what the runtime and the chat store as one assistant message, and an adapter may send it after streaming
// the same text as deltas (an ACP agent sends the whole message at its next tool call or at the end of the turn; Claude Code
// sends one per text block). The projection keeps the text streamed in the open run that no message_end has closed yet, and
// a message_end that repeats the front of it takes that text and adds no second message, so a thought or a tool result
// between the deltas and the message_end does not show the sentence twice; a message_end with other words is a message of
// its own, however short, even when an earlier message of the run said the same.
//
// A thread keeps the ordering AG-UI's verifier checks (CheckOrder): a run opens with RUN_STARTED and ends once, with
// RUN_FINISHED or RUN_ERROR, and nothing comes between its end and the next RUN_STARTED. A failure right after a run already
// failed (the fatal error chunk, then the process's agent.failed) adds its words to that RUN_ERROR instead of sending another;
// once the agent has moved on from that turn (agent.completed, agent.waiting, agent.stopped) a failure is its own. An answer
// to a permission request that comes after its turn ended is left out.
//
// The published AG-UI 1.0 client (@ag-ui/client 1.0.3; the 1.0 schema is the same from 1.0.0 to 1.0.3) strips every
// top-level key its schema does not declare and keeps "metadata" on every event, so what Drydock adds to an event (the
// stored event's seq, the agent's stop reason, whether a tool result is an error, the failed run's thread and run) lives
// under metadata.drydock. Those fields are Drydock's own: the reference reducer ignores them, and a consumer other than
// Drydock's UI loses nothing AG-UI promises without them. RUN_STARTED announces ProtocolVersion.
//
// Usage (AG-UI 1.0 carries TokenUsage on RUN_FINISHED.usage and RUN_ERROR.usage) is not projected yet; session, log and
// rate limit chunks have no place in the vocabulary and are left out. The text is the redacted text the bus stored;
// arguments and results are cut to MaxText (clipArgs keeps arguments parseable JSON, a result keeps its head and its tail).
package agui

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Event types, named as in AG-UI 1.0.
const (
	TypeRunStarted              = "RUN_STARTED"
	TypeRunFinished             = "RUN_FINISHED"
	TypeRunError                = "RUN_ERROR"
	TypeTextMessageStart        = "TEXT_MESSAGE_START"
	TypeTextMessageContent      = "TEXT_MESSAGE_CONTENT"
	TypeTextMessageEnd          = "TEXT_MESSAGE_END"
	TypeReasoningMessageStart   = "REASONING_MESSAGE_START"
	TypeReasoningMessageContent = "REASONING_MESSAGE_CONTENT"
	TypeReasoningMessageEnd     = "REASONING_MESSAGE_END"
	TypeToolCallStart           = "TOOL_CALL_START"
	TypeToolCallArgs            = "TOOL_CALL_ARGS"
	TypeToolCallEnd             = "TOOL_CALL_END"
	TypeToolCallResult          = "TOOL_CALL_RESULT"
	TypeActivitySnapshot        = "ACTIVITY_SNAPSHOT"
	TypeCustom                  = "CUSTOM"
)

// ProtocolVersion is the AG-UI protocol version every RUN_STARTED announces (the schema's optional protocolVersion).
const ProtocolVersion = "1.0"

// Custom event names.
const (
	NameInterrupt = "drydock.interrupt"
	NameResume    = "drydock.resume"
	NameDecided   = "drydock.decided"
)

// Run outcomes (RUN_FINISHED's outcome type), as in AG-UI 1.0. A run that failed ends with RUN_ERROR instead; AG-UI's third
// outcome, "interrupt", is not used (a Drydock turn waits for a person inside the run: drydock.interrupt).
const (
	OutcomeSuccess   = "success"
	OutcomeCancelled = "cancelled" // stopped before it completed, without failing: interrupted, or its program ended
)

// DecidedRefused is the outcome of a drydock.decided for a tool use Drydock refused with nobody to ask (an ACP session without
// a permission asker); the gate's own outcomes (denied, approved_for_task) are the words of its permission.decided event.
const DecidedRefused = "refused"

// ActivityPlan is the activity type of an agent's own plan.
const ActivityPlan = "PLAN"

// MaxText bounds a tool call's arguments and result in the vocabulary (the stored event keeps what the bus kept).
const MaxText = 8 << 10

// Cut markers: what a cut text ends with (CutMarker) or carries where its middle was (CutMiddleMarker), so the UI can tell a
// cut from the text and strip it.
const (
	CutMarker       = "[cut by Drydock: longer than the activity view keeps]"
	CutMiddleMarker = "[cut by Drydock: the middle of this output is not kept]"
)

// Event is any event of the vocabulary.
type Event interface{ EventType() string }

// Base is what every event carries: its type, the time of the stored Drydock event it was read from (Unix ms) and, under
// metadata.drydock, what Drydock adds to AG-UI's shape.
type Base struct {
	Type      string   `json:"type"`
	Timestamp int64    `json:"timestamp"`
	Metadata  Metadata `json:"metadata"`
}

// Metadata is AG-UI's free-form metadata on an event; Drydock keeps its own fields under one key.
type Metadata struct {
	Drydock DrydockMeta `json:"drydock"`
}

// DrydockMeta is what Drydock adds to an event beyond AG-UI's own fields: the stored event it was read from (Seq, on every
// event), the agent's own word for a run's end (StopReason, on RUN_FINISHED: end_turn, interrupted, ...; absent when the
// agent's program ended during the turn), whether a tool result is an error (IsError, on TOOL_CALL_RESULT) and the run a
// RUN_ERROR ended (ThreadID and RunID; AG-UI's RUN_ERROR names no run, it belongs to the run that preceded it).
type DrydockMeta struct {
	Seq        uint64 `json:"seq"`
	StopReason string `json:"stopReason,omitempty"`
	IsError    bool   `json:"isError,omitempty"`
	ThreadID   string `json:"threadId,omitempty"`
	RunID      string `json:"runId,omitempty"`
}

func (b Base) EventType() string { return b.Type }

type RunStarted struct {
	Base
	ThreadID        string `json:"threadId"`
	RunID           string `json:"runId"`
	ProtocolVersion string `json:"protocolVersion"` // always ProtocolVersion
}

// RunOutcome is why a run that did not fail ended: AG-UI 1.0's RunFinishedOutcome, an object discriminated on type.
type RunOutcome struct {
	Type string `json:"type"` // OutcomeSuccess or OutcomeCancelled
}

// RunFinished ends a run that did not fail. The agent's own word for the end is metadata.drydock.stopReason.
type RunFinished struct {
	Base
	ThreadID string     `json:"threadId"`
	RunID    string     `json:"runId"`
	Outcome  RunOutcome `json:"outcome"`
}

// RunError ends a run that failed. The run it ended is metadata.drydock.threadId and runId.
type RunError struct {
	Base
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

type TextMessageStart struct {
	Base
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
}

type TextMessageContent struct {
	Base
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

type TextMessageEnd struct {
	Base
	MessageID string `json:"messageId"`
}

type ReasoningMessageStart struct {
	Base
	MessageID string `json:"messageId"`
	Role      string `json:"role"` // always "reasoning"
}

type ReasoningMessageContent struct {
	Base
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

type ReasoningMessageEnd struct {
	Base
	MessageID string `json:"messageId"`
}

type ToolCallStart struct {
	Base
	ToolCallID   string `json:"toolCallId"`
	ToolCallName string `json:"toolCallName"`
}

type ToolCallArgs struct {
	Base
	ToolCallID string `json:"toolCallId"`
	Delta      string `json:"delta"`
}

type ToolCallEnd struct {
	Base
	ToolCallID string `json:"toolCallId"`
}

// ToolCallResult carries what a tool returned. Whether it is an error is metadata.drydock.isError.
type ToolCallResult struct {
	Base
	MessageID  string `json:"messageId"`
	ToolCallID string `json:"toolCallId"`
	Role       string `json:"role"`
	Content    string `json:"content"`
}

// PlanEntry is one step of an agent's plan.
type PlanEntry struct {
	Content  string `json:"content"`
	Status   string `json:"status,omitempty"`   // pending, in_progress, completed
	Priority string `json:"priority,omitempty"` // high, medium, low
}

// PlanActivity is the content of a PLAN activity: the whole current plan.
type PlanActivity struct {
	Entries []PlanEntry `json:"entries"`
}

type ActivitySnapshot struct {
	Base
	MessageID    string       `json:"messageId"`
	ActivityType string       `json:"activityType"`
	Content      PlanActivity `json:"content"`
	Replace      bool         `json:"replace"`
}

// InterruptValue is what a turn waits for: a person's answer to a permission request (an approval id). A decision nobody
// was asked for is no wait: drydock.decided (DecidedValue).
type InterruptValue struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
}

// Interrupt is CUSTOM drydock.interrupt.
type Interrupt struct {
	Base
	Name  string         `json:"name"`
	Value InterruptValue `json:"value"`
}

// ResumeValue is how a wait ended: the gate's outcome (approved, approved_with_edits, approved_for_task, rejected, timed_out,
// cancelled, denied). approved_for_task also answers the calls that follow without asking; each of those is drydock.decided.
type ResumeValue struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
}

// Resume is CUSTOM drydock.resume.
type Resume struct {
	Base
	Name  string      `json:"name"`
	Value ResumeValue `json:"value"`
}

// DecidedValue is a decision about a tool use nobody was asked for: the gate's rule refused it (outcome denied) or allowed
// it for the whole task (approved_for_task), with the rule that did; or Drydock refused it in a session with nobody to ask
// (DecidedRefused), with the call it refused (ToolCallID, when the agent named it) and why.
type DecidedValue struct {
	Tool       string `json:"tool"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
	Rule       string `json:"rule,omitempty"`
}

// Decided is CUSTOM drydock.decided.
type Decided struct {
	Base
	Name  string       `json:"name"`
	Value DecidedValue `json:"value"`
}

// Thread is one agent process's events: a valid stream on its own. Adapter, Model, RunMode and WSLDistro say what the process
// ran as (the CLI, its model, and the WSL distribution when it ran inside one), from its agent.started; a thread read without
// that event has them from the agent's settings (TaskActivity) or not at all. Ended says the process is gone (its
// agent.stopped was read): a thread with no events that ended was stopped before its first turn, not one still starting.
type Thread struct {
	ThreadID  string  `json:"threadId"` // the agent run
	AgentID   string  `json:"agentId,omitempty"`
	Adapter   string  `json:"adapter,omitempty"`
	Model     string  `json:"model,omitempty"`
	RunMode   string  `json:"runMode,omitempty"`
	WSLDistro string  `json:"wslDistro,omitempty"`
	Ended     bool    `json:"ended,omitempty"`
	Events    []Event `json:"events"`
}

// Activity is a task's threads in the order they started. Truncated says the oldest stored events were left out.
type Activity struct {
	Threads   []Thread `json:"threads"`
	Truncated bool     `json:"truncated,omitempty"`
}

// Project turns a task's stored agent.* events (oldest first) into the vocabulary, one thread per agent run. Consecutive
// text and reasoning deltas of one message are joined into one event, so a reload carries a turn's text in a few events
// instead of one per 50 ms. It is pure: the same events give the same activity.
func Project(evs []events.Event) Activity {
	var out Activity
	idx := map[string]int{}
	ps := map[string]*projector{}
	for _, e := range evs {
		var d struct {
			Request    string          `json:"request"`
			Approval   string          `json:"approval"`
			Title      string          `json:"title"`
			ExpiresAt  int64           `json:"expiresAt"`
			Outcome    string          `json:"outcome"`
			Tool       string          `json:"tool"`
			Rule       string          `json:"rule"`
			AgentRun   string          `json:"agentRun"`
			Kind       string          `json:"kind"`
			Data       json.RawMessage `json:"data"`
			State      string          `json:"state"`
			StopReason string          `json:"stopReason"`
			Error      string          `json:"error"`
			Phase      string          `json:"phase"`
			Adapter    string          `json:"adapter"`
			Model      string          `json:"model"`
			RunMode    string          `json:"runMode"`
			WSLDistro  string          `json:"wslDistro"`
		}
		if json.Unmarshal(e.Data, &d) != nil {
			continue
		}
		if e.Type == "agent.failed" && d.Phase == phaseBookkeeping {
			continue // Drydock could not record the run's end (its own database), which is no failure of the agent's turn
		}
		if d.AgentRun == "" && (e.Type == "approval.requested" || e.Type == "permission.decided") {
			// the gate scopes its approvals and decisions by the agent run; they join a thread that agent process opened (a
			// workflow's own request carries the workflow run, which is no thread)
			if _, ok := ps[e.RunID]; !ok {
				continue
			}
			d.AgentRun = e.RunID
		}
		if d.AgentRun == "" {
			continue
		}
		p, ok := ps[d.AgentRun]
		if !ok {
			p = &projector{thread: d.AgentRun}
			ps[d.AgentRun] = p
			idx[d.AgentRun] = len(out.Threads)
			out.Threads = append(out.Threads, Thread{ThreadID: d.AgentRun, AgentID: e.AgentID})
		}
		p.base = Base{Timestamp: e.Time.UnixMilli(), Metadata: Metadata{Drydock: DrydockMeta{Seq: e.Seq}}}
		switch e.Type {
		case "agent.started":
			// what the process runs as; the event itself has no place in the vocabulary
			t := &out.Threads[idx[d.AgentRun]]
			t.Adapter, t.Model, t.RunMode, t.WSLDistro = d.Adapter, d.Model, d.RunMode, d.WSLDistro
		case "agent.waiting":
			if strings.EqualFold(d.State, "working") {
				p.startRun()
			} else {
				p.failed = nil // the agent moved on from a failed turn: a failure after this is its own
			}
		case "agent.output":
			p.output(d.Kind, d.Data)
		case "agent.completed":
			p.complete(d.StopReason)
			p.failed = nil // the agent closed its turn: a failure after this is its own
		case "agent.failed":
			p.fail(d.Error, "")
		case "agent.stopped":
			if p.run != "" {
				p.finish(OutcomeCancelled, "")
			}
			p.failed = nil
			out.Threads[idx[d.AgentRun]].Ended = true
		case "approval.requested":
			if strings.HasPrefix(d.Title, permissionTitlePrefix) && d.Request != "" {
				p.ensureRun()
				p.waiting = append(p.waiting, d.Request)
				p.emit(&Interrupt{Base: p.b(TypeCustom), Name: NameInterrupt, Value: InterruptValue{ID: d.Request, Title: clip(strings.TrimPrefix(d.Title, permissionTitlePrefix), 300), ExpiresAt: d.ExpiresAt}})
			}
		case "permission.decided":
			switch {
			case d.Approval != "":
				if p.run != "" && p.isWaiting(d.Approval) { // an answer after its turn ended has no run to go in
					p.emit(&Resume{Base: p.b(TypeCustom), Name: NameResume, Value: ResumeValue{ID: d.Approval, Outcome: d.Outcome}})
				}
			case d.Outcome == "denied" || d.Outcome == "approved_for_task":
				// nobody was asked: a rule decided. The allowed_by_rule of every ordinary call is left out.
				p.ensureRun()
				p.emit(&Decided{Base: p.b(TypeCustom), Name: NameDecided, Value: DecidedValue{Tool: clip(d.Tool, 200), Outcome: d.Outcome, Rule: clip(d.Rule, 300)}})
			}
		}
		t := &out.Threads[idx[d.AgentRun]]
		t.Events = append(t.Events, p.take()...)
	}
	for i := range out.Threads {
		out.Threads[i].Events = compact(out.Threads[i].Events)
	}
	return out
}

type projector struct {
	thread    string
	turn      int
	run       string
	text      string // the open text message
	reasoning string // the open reasoning message
	streamed  string // the text streamed in the open run that no message_end has taken yet, so one that repeats it adds no second message
	open      map[string]bool
	started   map[string]bool
	n         int
	base      Base
	pending   []Event
	waiting   []string              // interrupts announced in this thread, so a decision is resumed only where it was asked
	errs      []workflow.ErrorChunk // the open run's non-fatal error chunks in order (the last maxErrs), for workflow.TurnFailed
	answered  bool                  // the open run has an assistant message (a message_end with text), as the chat counts an answer
	failed    *RunError             // the RUN_ERROR that ended the last run, while the agent has not moved on from that turn
}

// permissionTitlePrefix is security.PermissionTitlePrefix (not imported: the vocabulary does not depend on the gate).
const permissionTitlePrefix = "Permission request: "

// phaseBookkeeping is the phase of the agent.failed the runtime publishes when it could not record a run's end.
const phaseBookkeeping = "bookkeeping"

// maxStreamed bounds the streamed text a run keeps for matching a message_end; an older head is dropped.
const maxStreamed = 4 << 20

// maxErrs bounds the non-fatal errors a run keeps for workflow.TurnFailed, which reads the last uncoded one.
const maxErrs = 32

func (p *projector) isWaiting(id string) bool {
	for _, w := range p.waiting {
		if w == id {
			return true
		}
	}
	return false
}

func (p *projector) b(typ string) Base { b := p.base; b.Type = typ; return b }

func (p *projector) emit(e Event) { p.pending = append(p.pending, e) }

func (p *projector) take() []Event { out := p.pending; p.pending = nil; return out }

func (p *projector) id(prefix string) string {
	p.n++
	return p.thread + ":" + prefix + strconv.Itoa(p.n)
}

func (p *projector) startRun() {
	if p.run != "" {
		p.complete("") // a turn that never reported its end ends as one with no stop reason would
	}
	p.turn++
	p.run = p.thread + ":" + strconv.Itoa(p.turn)
	p.errs, p.answered, p.failed, p.streamed = nil, false, nil, ""
	p.started, p.open = nil, nil // an ACP agent may use a call id again in a later turn: that call is announced again
	p.emit(&RunStarted{Base: p.b(TypeRunStarted), ThreadID: p.thread, RunID: p.run, ProtocolVersion: ProtocolVersion})
}

// ensureRun opens a run for output that arrived without one (a stream read from its middle, or an adapter that does not
// announce turns).
func (p *projector) ensureRun() {
	if p.run == "" {
		p.startRun()
	}
}

func (p *projector) finish(outcome, stopReason string) {
	p.closeOpen()
	if p.run == "" {
		return
	}
	e := &RunFinished{Base: p.b(TypeRunFinished), ThreadID: p.thread, RunID: p.run, Outcome: RunOutcome{Type: outcome}}
	e.Metadata.Drydock.StopReason = stopReason
	p.emit(e)
	p.run = ""
}

// complete ends the open run as the agent reported it (agent.completed). An interrupted or cancelled turn is the cancelled
// outcome. Whether any other turn failed, and with what reason, is workflow.TurnFailed, read from the stop reason, whether
// the run answered and its non-fatal errors: a run that failed ends with RUN_ERROR and that reason, coded as the stop reason.
func (p *projector) complete(stopReason string) {
	if p.run == "" {
		p.closeOpen()
		return
	}
	if stopReason == "interrupted" || stopReason == "cancelled" {
		p.finish(OutcomeCancelled, stopReason)
		return
	}
	if failed, reason := workflow.TurnFailed(stopReason, p.answered, p.errs); failed {
		p.fail(reason, stopReason)
		return
	}
	p.finish(OutcomeSuccess, stopReason)
}

// fail ends the open run with RUN_ERROR. A failure outside a run (a start that failed, a crash between turns) is a run of its
// own that errored: RUN_STARTED then RUN_ERROR. A failure right after the last run failed, before the agent moved on from it
// (the process's agent.failed after a fatal error chunk), adds its words to that RUN_ERROR: one RUN_ERROR per failed turn,
// and AG-UI allows nothing but RUN_STARTED after RUN_ERROR.
func (p *projector) fail(msg, code string) {
	msg = strings.TrimSpace(msg)
	if p.run == "" && p.failed != nil {
		if msg != "" && !strings.Contains(p.failed.Message, msg) {
			p.failed.Message = clip(p.failed.Message+"; "+clip(msg, 500), 1000)
		}
		return
	}
	if msg == "" {
		msg = "the agent failed"
	}
	p.ensureRun()
	p.closeOpen()
	e := &RunError{Base: p.b(TypeRunError), Message: clip(msg, 500), Code: code}
	e.Metadata.Drydock.ThreadID, e.Metadata.Drydock.RunID = p.thread, p.run
	p.emit(e)
	p.run, p.failed = "", e
}

func (p *projector) closeText() {
	if p.text != "" {
		p.emit(&TextMessageEnd{Base: p.b(TypeTextMessageEnd), MessageID: p.text})
		p.text = ""
	}
}

func (p *projector) closeReasoning() {
	if p.reasoning != "" {
		p.emit(&ReasoningMessageEnd{Base: p.b(TypeReasoningMessageEnd), MessageID: p.reasoning})
		p.reasoning = ""
	}
}

// closeOpen ends what a turn left open: its messages, and its tool calls that never reported a result.
func (p *projector) closeOpen() {
	p.closeText()
	p.closeReasoning()
	p.open = nil
}

func (p *projector) output(kind string, raw json.RawMessage) {
	switch kind {
	case workflow.ChunkText:
		var t workflow.TextDelta
		if json.Unmarshal(raw, &t) != nil || t.Text == "" {
			return
		}
		p.ensureRun()
		p.closeReasoning()
		if p.text == "" {
			p.text = p.id("m")
			p.emit(&TextMessageStart{Base: p.b(TypeTextMessageStart), MessageID: p.text, Role: "assistant"})
		}
		p.emit(&TextMessageContent{Base: p.b(TypeTextMessageContent), MessageID: p.text, Delta: t.Text})
		p.streamed += t.Text
		if len(p.streamed) > maxStreamed {
			p.streamed = p.streamed[len(p.streamed)-maxStreamed:]
		}
	case workflow.ChunkMessageEnd:
		var t workflow.TextDelta
		_ = json.Unmarshal(raw, &t)
		p.ensureRun()
		p.closeReasoning()
		s := strings.TrimSpace(t.Text)
		if p.text == "" {
			if t.Text == "" {
				return
			}
			if p.takeStreamed(s) {
				// the whole of a message whose deltas were streamed and closed already (a thought or a tool result came
				// between): what the chat stores as the message, shown once
				p.answered = true
				return
			}
			// a message that came whole, with no deltas before it
			p.text = p.id("m")
			p.emit(&TextMessageStart{Base: p.b(TypeTextMessageStart), MessageID: p.text, Role: "assistant"})
			p.emit(&TextMessageContent{Base: p.b(TypeTextMessageContent), MessageID: p.text, Delta: t.Text})
		} else if !p.takeStreamed(s) {
			p.streamed = "" // the open message ends with other words than its deltas: none of them is left to match
		}
		if s != "" {
			p.answered = true
		}
		p.closeText()
	case workflow.ChunkThinking:
		var t workflow.TextDelta
		if json.Unmarshal(raw, &t) != nil || t.Text == "" {
			return
		}
		p.ensureRun()
		p.closeText()
		if p.reasoning == "" {
			p.reasoning = p.id("r")
			p.emit(&ReasoningMessageStart{Base: p.b(TypeReasoningMessageStart), MessageID: p.reasoning, Role: "reasoning"})
		}
		p.emit(&ReasoningMessageContent{Base: p.b(TypeReasoningMessageContent), MessageID: p.reasoning, Delta: t.Text})
	case workflow.ChunkToolCall:
		var c workflow.ToolCall
		if json.Unmarshal(raw, &c) != nil {
			return
		}
		p.ensureRun()
		p.closeText()
		p.closeReasoning()
		p.startCall(c.ID, c.Name, c.Args)
	case workflow.ChunkToolResult:
		var r workflow.ToolResult
		if json.Unmarshal(raw, &r) != nil {
			return
		}
		p.ensureRun()
		p.closeText()
		p.closeReasoning()
		id := p.startCall(r.ID, r.Name, r.Args)
		delete(p.open, id)
		e := &ToolCallResult{Base: p.b(TypeToolCallResult), MessageID: p.id("t"), ToolCallID: id, Role: "tool", Content: clipResult(r.Output)}
		e.Metadata.Drydock.IsError = r.IsError
		p.emit(e)
	case workflow.ChunkPlan:
		var pl workflow.PlanChunk
		if json.Unmarshal(raw, &pl) != nil {
			return
		}
		p.ensureRun()
		entries := make([]PlanEntry, 0, len(pl.Entries))
		for _, e := range pl.Entries {
			entries = append(entries, PlanEntry{Content: clip(e.Content, 500), Status: e.Status, Priority: e.Priority})
		}
		p.emit(&ActivitySnapshot{Base: p.b(TypeActivitySnapshot), MessageID: p.thread + ":plan", ActivityType: ActivityPlan, Content: PlanActivity{Entries: entries}, Replace: true})
	case workflow.ChunkPermissionDenied:
		var d workflow.PermissionDenial
		if json.Unmarshal(raw, &d) != nil {
			return
		}
		// Only a refusal Drydock made itself says so here (the reason says why: an ACP session with nobody to ask). The CLI's
		// own permission_denials carry no reason and are left out: the call's failed result already shows the step as failed,
		// and when the gate was asked, the request's drydock.resume already says who refused it; a second row would repeat
		// that, with the wrong words for a refusal a person gave.
		reason := strings.TrimSpace(d.Reason)
		if reason == "" {
			return
		}
		p.ensureRun()
		p.emit(&Decided{Base: p.b(TypeCustom), Name: NameDecided, Value: DecidedValue{Tool: clip(d.ToolName, 200), Outcome: DecidedRefused, Reason: clip(reason, 500), ToolCallID: d.ToolUseID}})
	case workflow.ChunkError:
		var er workflow.ErrorChunk
		if json.Unmarshal(raw, &er) != nil {
			return
		}
		if !er.Fatal {
			// a warning the turn goes on after; workflow.TurnFailed reads the run's errors when the turn ends
			if p.run != "" {
				p.errs = append(p.errs, er)
				if len(p.errs) > maxErrs {
					p.errs = p.errs[len(p.errs)-maxErrs:]
				}
			}
			return
		}
		p.fail(er.Message, er.Code)
	}
}

// takeStreamed takes s, a message_end's text, off the front of the streamed text no message_end has taken yet, and says
// whether it was there. A message_end repeats the deltas of its message, and the deltas of one message may be closed by
// several message_ends (one per text block), so each takes its own part and leaves the rest for the next.
func (p *projector) takeStreamed(s string) bool {
	rest := strings.TrimSpace(p.streamed)
	if s == "" || !strings.HasPrefix(rest, s) {
		return false
	}
	p.streamed = rest[len(s):]
	return true
}

// startCall announces a tool call once in its run (an ACP agent repeats a call as it updates it) and returns its id. A call
// with no id gets one from the stored event; a call with no name (a result for a tool use the parser never saw) is "tool",
// as the ACP adapter names one.
func (p *projector) startCall(id, name string, args json.RawMessage) string {
	if id == "" {
		id = p.id("c")
	}
	if p.started == nil {
		p.started, p.open = map[string]bool{}, map[string]bool{}
	}
	if p.open == nil {
		p.open = map[string]bool{}
	}
	if p.started[id] {
		return id
	}
	if name == "" {
		name = "tool"
	}
	p.started[id], p.open[id] = true, true
	p.emit(&ToolCallStart{Base: p.b(TypeToolCallStart), ToolCallID: id, ToolCallName: clip(name, 200)})
	if len(args) > 0 && string(args) != "null" {
		p.emit(&ToolCallArgs{Base: p.b(TypeToolCallArgs), ToolCallID: id, Delta: clipArgs(string(args))})
	}
	p.emit(&ToolCallEnd{Base: p.b(TypeToolCallEnd), ToolCallID: id})
	return id
}

// compact joins consecutive text deltas of one message, and consecutive reasoning deltas of one message, into the first of
// them.
func compact(in []Event) []Event {
	out := make([]Event, 0, len(in))
	for _, e := range in {
		if len(out) > 0 {
			switch x := e.(type) {
			case *TextMessageContent:
				if prev, ok := out[len(out)-1].(*TextMessageContent); ok && prev.MessageID == x.MessageID {
					prev.Delta += x.Delta
					continue
				}
			case *ReasoningMessageContent:
				if prev, ok := out[len(out)-1].(*ReasoningMessageContent); ok && prev.MessageID == x.MessageID {
					prev.Delta += x.Delta
					continue
				}
			}
		}
		out = append(out, e)
	}
	return out
}

// clip cuts s to at most n bytes on a character boundary, saying so.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:runeFloor(s, n)] + "\n" + CutMarker
}

// clipResult bounds a tool result to MaxText keeping its head and its tail, since the end of a command's output is where a
// test runner puts its summary: the first resultHead bytes and the last resultTail bytes, on character boundaries, around
// CutMiddleMarker on a line of its own.
func clipResult(s string) string {
	if len(s) <= MaxText {
		return s
	}
	head := s[:runeFloor(s, resultHead)]
	tail := s[runeCeil(s, len(s)-resultTail):]
	return head + "\n" + CutMiddleMarker + "\n" + tail
}

// What a cut result keeps of its head and its tail.
const (
	resultHead = 6 << 10
	resultTail = 2 << 10
)

// minCutString is the length a string value of arguments is not cut below: cutting shorter values gains little and loses
// the paths and commands the UI reads.
const minCutString = 256

// clipArgs bounds a tool call's arguments to MaxText as JSON that still parses, so the UI keeps the file path and the shape
// of a Write whose content is long: the longest string values are cut first, each to its prefix (on a character boundary)
// plus " " + CutMarker, until the compact re-encoding fits or no string over minCutString bytes remains; the result has Go's
// key order, and its numbers as the agent wrote them (an offset above 2^53 is not rounded through a float). Arguments that
// are not JSON, or do not fit even so, are cut as text (clip).
func clipArgs(args string) string {
	if len(args) <= MaxText {
		return args
	}
	dec := json.NewDecoder(strings.NewReader(args))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return clip(args, MaxText)
	}
	suffix := " " + CutMarker
	for {
		enc, err := json.Marshal(v)
		if err != nil {
			return clip(args, MaxText)
		}
		if len(enc) <= MaxText {
			return string(enc)
		}
		s, set := longestString(v, func(x any) { v = x })
		body, cut := strings.CutSuffix(s, suffix)
		if len(body) <= minCutString {
			return clip(args, MaxText)
		}
		keep := len(body) - (len(enc) - MaxText)
		if !cut {
			keep -= len(suffix)
		}
		keep = max(keep, minCutString)
		set(body[:runeFloor(body, keep)] + suffix)
	}
}

// longestString finds the longest string value in v (an object, an array or a string itself) and returns it with a function
// that replaces it where it is; set replaces v itself when v is the string. Two values of one length tie in favour of the
// first in key order, so the same arguments are cut the same way on every read (Project is pure).
func longestString(v any, set func(any)) (string, func(any)) {
	switch x := v.(type) {
	case string:
		return x, set
	case map[string]any:
		best, bestSet := "", set
		for _, k := range slices.Sorted(maps.Keys(x)) {
			inner := x[k]
			if s, f := longestString(inner, func(y any) { x[k] = y }); len(s) > len(best) {
				best, bestSet = s, f
			}
		}
		return best, bestSet
	case []any:
		best, bestSet := "", set
		for i, inner := range x {
			i := i
			if s, f := longestString(inner, func(y any) { x[i] = y }); len(s) > len(best) {
				best, bestSet = s, f
			}
		}
		return best, bestSet
	}
	return "", set
}

// runeFloor is the largest cut at or before n that does not split a character of s.
func runeFloor(s string, n int) int {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// runeCeil is the smallest cut at or after n that does not split a character of s.
func runeCeil(s string, n int) int {
	for n < len(s) && !utf8.RuneStart(s[n]) {
		n++
	}
	return n
}
