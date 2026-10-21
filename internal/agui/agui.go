// Package agui is Drydock's typed vocabulary for what an agent does during a run, modelled on the AG-UI protocol 1.0
// (docs.ag-ui.com): a run's lifecycle, its text and reasoning messages, its tool calls with their arguments and results, and
// its plan as an activity (decision P87). The events are not a second record: Project reads the agent.* events the core
// already stored for a task and returns them in this vocabulary, one stream per agent process, so the UI draws a live
// timeline from typed events instead of reading raw chunks. The TypeScript types are generated from these structs
// (cmd/drydock-tsgen, ui/app/api/agui.gen.ts) and a test keeps the two in step.
//
// Mapping (one agent process is a thread; each turn is a run):
//
//	agent.waiting {state: working}            RUN_STARTED
//	agent.output text                         TEXT_MESSAGE_START (first delta) + TEXT_MESSAGE_CONTENT
//	agent.output message_end                  TEXT_MESSAGE_END (or START, CONTENT, END for a message that came whole)
//	agent.output thinking                     REASONING_MESSAGE_START (first delta) + REASONING_MESSAGE_CONTENT
//	agent.output tool_call                    TOOL_CALL_START, TOOL_CALL_ARGS, TOOL_CALL_END
//	agent.output tool_result                  TOOL_CALL_RESULT
//	agent.output plan                         ACTIVITY_SNAPSHOT {activityType: PLAN}
//	agent.output error {fatal}                RUN_ERROR
//	agent.completed                           RUN_FINISHED {outcome: success}
//	agent.failed                              RUN_ERROR
//	agent.stopped during a turn               RUN_FINISHED {outcome: stopped}
//	approval.requested (a permission request) CUSTOM drydock.interrupt: the turn waits for a person (P88)
//	permission.decided after a request        CUSTOM drydock.resume: how the person (or nobody) answered
//
// AG-UI 1.0 ends a run with outcome "interrupt" and resumes it as a new run; a Drydock agent's turn does not end while it
// waits, so the wait and its answer are AG-UI CUSTOM events inside the turn, named drydock.interrupt and drydock.resume.
//
// Everything else (usage, session, log, rate limits) has no place in the vocabulary and is left out. The text is the
// redacted text the bus stored; arguments and results are cut to MaxText.
package agui

import (
	"encoding/json"
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

// Custom event names.
const (
	NameInterrupt = "drydock.interrupt"
	NameResume    = "drydock.resume"
)

// Run outcomes (RUN_FINISHED).
const (
	OutcomeSuccess = "success"
	OutcomeStopped = "stopped" // the agent's program ended during the turn
)

// ActivityPlan is the activity type of an agent's own plan.
const ActivityPlan = "PLAN"

// MaxText bounds a tool call's arguments and result in the vocabulary (the stored event keeps what the bus kept).
const MaxText = 8 << 10

// Event is any event of the vocabulary.
type Event interface{ EventType() string }

// Base is what every event carries: its type, the stored Drydock event it was read from and that event's time (Unix ms).
type Base struct {
	Type      string `json:"type"`
	Seq       uint64 `json:"seq"`
	Timestamp int64  `json:"timestamp"`
}

func (b Base) EventType() string { return b.Type }

type RunStarted struct {
	Base
	ThreadID string `json:"threadId"`
	RunID    string `json:"runId"`
}

type RunFinished struct {
	Base
	ThreadID   string `json:"threadId"`
	RunID      string `json:"runId"`
	Outcome    string `json:"outcome"`
	StopReason string `json:"stopReason,omitempty"`
}

type RunError struct {
	Base
	ThreadID string `json:"threadId,omitempty"`
	RunID    string `json:"runId,omitempty"`
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
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

type ToolCallResult struct {
	Base
	MessageID  string `json:"messageId"`
	ToolCallID string `json:"toolCallId"`
	Role       string `json:"role"`
	Content    string `json:"content"`
	IsError    bool   `json:"isError,omitempty"`
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

// InterruptValue is what a turn waits for: a person's answer to a permission request (an approval id).
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

// ResumeValue is how a wait ended: the gate's outcome (approved, approved_with_edits, rejected, timed_out, cancelled, denied).
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

// Thread is one agent process's events: a valid stream on its own.
type Thread struct {
	ThreadID string  `json:"threadId"` // the agent run
	AgentID  string  `json:"agentId,omitempty"`
	Events   []Event `json:"events"`
}

// Activity is a task's threads in the order they started. Truncated says the oldest stored events were left out.
type Activity struct {
	Threads   []Thread `json:"threads"`
	Truncated bool     `json:"truncated,omitempty"`
}

// Project turns a task's stored agent.* events (oldest first) into the vocabulary, one thread per agent run. Consecutive
// text, reasoning and argument deltas of one message are joined into one event, so a reload carries a turn's text in a few
// events instead of one per 50 ms. It is pure: the same events give the same activity.
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
			AgentRun   string          `json:"agentRun"`
			Kind       string          `json:"kind"`
			Data       json.RawMessage `json:"data"`
			State      string          `json:"state"`
			StopReason string          `json:"stopReason"`
			Error      string          `json:"error"`
		}
		if json.Unmarshal(e.Data, &d) != nil {
			continue
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
		p.base = Base{Seq: e.Seq, Timestamp: e.Time.UnixMilli()}
		switch e.Type {
		case "agent.waiting":
			if strings.EqualFold(d.State, "working") {
				p.startRun()
			}
		case "agent.output":
			p.output(d.Kind, d.Data)
		case "agent.completed":
			p.finish(OutcomeSuccess, d.StopReason)
		case "agent.failed":
			p.closeOpen()
			p.emit(&RunError{Base: p.b(TypeRunError), ThreadID: p.thread, RunID: p.run, Message: clip(d.Error, 500)})
			p.run = ""
		case "agent.stopped":
			if p.run != "" {
				p.finish(OutcomeStopped, "")
			}
		case "approval.requested":
			if strings.HasPrefix(d.Title, permissionTitlePrefix) && d.Request != "" {
				p.ensureRun()
				p.waiting = append(p.waiting, d.Request)
				p.emit(&Interrupt{Base: p.b(TypeCustom), Name: NameInterrupt, Value: InterruptValue{ID: d.Request, Title: clip(strings.TrimPrefix(d.Title, permissionTitlePrefix), 300), ExpiresAt: d.ExpiresAt}})
			}
		case "permission.decided":
			if d.Approval != "" && p.isWaiting(d.Approval) {
				p.emit(&Resume{Base: p.b(TypeCustom), Name: NameResume, Value: ResumeValue{ID: d.Approval, Outcome: d.Outcome}})
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
	open      map[string]bool
	started   map[string]bool
	n         int
	base      Base
	pending   []Event
	waiting   []string // interrupts announced in this thread, so a decision is resumed only where it was asked
}

// permissionTitlePrefix is security.PermissionTitlePrefix (not imported: the vocabulary does not depend on the gate).
const permissionTitlePrefix = "Permission request: "

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
		p.finish(OutcomeSuccess, "")
	}
	p.turn++
	p.run = p.thread + ":" + strconv.Itoa(p.turn)
	p.emit(&RunStarted{Base: p.b(TypeRunStarted), ThreadID: p.thread, RunID: p.run})
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
	p.emit(&RunFinished{Base: p.b(TypeRunFinished), ThreadID: p.thread, RunID: p.run, Outcome: outcome, StopReason: stopReason})
	p.run = ""
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
	case workflow.ChunkMessageEnd:
		var t workflow.TextDelta
		_ = json.Unmarshal(raw, &t)
		p.ensureRun()
		p.closeReasoning()
		if p.text == "" { // a message that came whole, with no deltas before it
			if t.Text == "" {
				return
			}
			p.text = p.id("m")
			p.emit(&TextMessageStart{Base: p.b(TypeTextMessageStart), MessageID: p.text, Role: "assistant"})
			p.emit(&TextMessageContent{Base: p.b(TypeTextMessageContent), MessageID: p.text, Delta: t.Text})
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
			p.emit(&ReasoningMessageStart{Base: p.b(TypeReasoningMessageStart), MessageID: p.reasoning})
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
		p.emit(&ToolCallResult{Base: p.b(TypeToolCallResult), MessageID: p.id("t"), ToolCallID: id, Role: "tool", Content: clip(r.Output, MaxText), IsError: r.IsError})
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
	case workflow.ChunkError:
		var er workflow.ErrorChunk
		if json.Unmarshal(raw, &er) != nil || !er.Fatal {
			return
		}
		p.closeOpen()
		p.emit(&RunError{Base: p.b(TypeRunError), ThreadID: p.thread, RunID: p.run, Message: clip(er.Message, 500), Code: er.Code})
		p.run = ""
	}
}

// startCall announces a tool call once (an ACP agent repeats a call as it updates it) and returns its id. A call with no id
// gets one from the stored event.
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
	p.started[id], p.open[id] = true, true
	p.emit(&ToolCallStart{Base: p.b(TypeToolCallStart), ToolCallID: id, ToolCallName: clip(name, 200)})
	if len(args) > 0 && string(args) != "null" {
		p.emit(&ToolCallArgs{Base: p.b(TypeToolCallArgs), ToolCallID: id, Delta: clip(string(args), MaxText)})
	}
	p.emit(&ToolCallEnd{Base: p.b(TypeToolCallEnd), ToolCallID: id})
	return id
}

// compact joins consecutive deltas of one message (or one call's arguments) into the first of them.
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
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n[cut by Drydock: longer than the activity view keeps]"
}
