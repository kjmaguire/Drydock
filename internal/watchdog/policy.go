package watchdog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// TripRecord is one recorded breaker event.
type TripRecord struct {
	TaskID   string
	RunID    string
	NodeID   string
	Attempt  int
	AgentID  string
	Signal   string
	Stage    int
	Evidence string
}

// Store persists trips. Count returns hard trips since the last human reset for the task.
type Store interface {
	Count(ctx context.Context, taskID string) (int, error)
	Record(ctx context.Context, t TripRecord) error
	Reset(ctx context.Context, taskID string) error
}

// SQLStore stores trips in watchdog_trips. A row with signal 'reset' marks a human resuming with guidance.
type SQLStore struct{ DB *storage.DB }

func (s SQLStore) Count(ctx context.Context, taskID string) (int, error) {
	var n int
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM watchdog_trips WHERE task_id=? AND signal <> 'reset' AND signal <> 'stagnation'
		AND id > COALESCE((SELECT MAX(id) FROM watchdog_trips WHERE task_id=? AND signal='reset'),0)`, taskID, taskID).Scan(&n)
	return n, err
}

func (s SQLStore) Record(ctx context.Context, t TripRecord) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO watchdog_trips(task_id,run_id,node_id,attempt,agent_id,signal,stage,evidence,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			t.TaskID, t.RunID, t.NodeID, t.Attempt, t.AgentID, t.Signal, t.Stage, t.Evidence, storage.Now())
		return err
	})
}

func (s SQLStore) Reset(ctx context.Context, taskID string) error {
	return s.Record(ctx, TripRecord{TaskID: taskID, Signal: "reset", Stage: 0, Evidence: "{}"})
}

// Steer is the team lead's decision on a stage-1 trip, as the watchdog applies it: escalate pauses the node for the human
// with the lead's message; any other action lets the work go on, with the lead's message as the notice when it has one.
type Steer struct {
	Action  string
	Message string
}

// SignalLeadEscalation names a pause the team lead asked for (Watchdog.Steer); it is not a watchdog trip and is never counted.
const SignalLeadEscalation = "lead_escalation"

// Hooks connect the watchdog to the rest of the app without importing it.
type Hooks struct {
	// Notify publishes watchdog.warning / watchdog.tripped / watchdog.paused, posts the SYSTEM room message and sends the
	// desktop notification (stage 2). It must not block for long.
	Notify func(ctx context.Context, typ string, t TripRecord, message string)
	// Handoff writes a handoff artifact for a stage-2 pause and returns its id (may be empty).
	Handoff func(ctx context.Context, in workflow.NodeInfo, t TripRecord) (string, error)
	// Lead reports whether the task's team lead will supervise a stage-1 trip on this node (a lead exists, the node is not
	// the lead's own turn, the per-run bound is not used up). Nil or false: the stage-1 nudge works exactly as without a lead.
	Lead func(ctx context.Context, n workflow.NodeInfo) bool
	// Supervise runs the team lead's supervision turn for a stage-1 trip. ok=false: the lead did not decide (the generic
	// nudge is used).
	Supervise func(ctx context.Context, n workflow.NodeInfo, t TripRecord) (Steer, bool)
	// Stop is called when a human rejects a watchdog pause: the task stops (the core cancels it) and the run ends.
	Stop func(ctx context.Context, n workflow.NodeInfo, by string)
}

// Watchdog wires detectors to the workflow engine.
type Watchdog struct {
	cfg   func(workflow.NodeInfo) Config
	store Store
	hooks Hooks
	mu    sync.Mutex
	execs map[string]*execState // by NodeInfo.Key
	echo  map[string]*EchoDetector
	nudge map[string]string // task -> nudge text to prepend to the agent's next turn
}

// New creates a Watchdog. cfg may be nil (defaults).
func New(store Store, hooks Hooks, cfg func(workflow.NodeInfo) Config) *Watchdog {
	if cfg == nil {
		cfg = func(workflow.NodeInfo) Config { return Defaults() }
	}
	return &Watchdog{cfg: cfg, store: store, hooks: hooks, execs: map[string]*execState{}, echo: map[string]*EchoDetector{}, nudge: map[string]string{}}
}

// ForNode is the thresholds a node runs under: the Iterate Until Verified profile (a wider spin window and a higher tool-call
// cap, because repeated test runs are expected) for every turn of an iterate-until-verified run and for the work and
// test-writing turns of the other task workflows; the defaults for the rest (spec, design, review, planner turns).
func ForNode(n workflow.NodeInfo) Config {
	if strings.HasPrefix(n.Workflow, "iterate-until-verified") || strings.HasPrefix(n.Workflow, "task-pipeline") {
		return IterateUntilVerified()
	}
	switch n.Meta["agent"] {
	case "work", "tests":
		return IterateUntilVerified()
	}
	return Defaults()
}

type leadTrip struct {
	rec TripRecord
	v   Verdict
}

type execState struct {
	info      workflow.NodeInfo
	turn      *Turn
	cfgv      Config
	slot      *workflow.TurnSlot
	cancel    context.CancelCauseFunc
	mu        sync.Mutex
	enforcing bool
	pause     *TripRecord // set when a stage-2 pause must end the node
	pauseNote string
	lead      *leadTrip // set when a stage-1 trip goes to the team lead
	tripCount int
	timer     *time.Timer
}

func scope(n workflow.NodeInfo) string {
	if n.TaskID != "" {
		return n.TaskID
	}
	return string(n.RunID)
}

func (w *Watchdog) state(n workflow.NodeInfo) *execState {
	w.mu.Lock()
	defer w.mu.Unlock()
	st, ok := w.execs[n.Key]
	if !ok {
		c := w.cfg(n)
		st = &execState{info: n, cfgv: c.withDefaults(), turn: NewTurn(c)}
		w.execs[n.Key] = st
	}
	return st
}

func (w *Watchdog) drop(n workflow.NodeInfo) {
	w.mu.Lock()
	delete(w.execs, n.Key)
	w.mu.Unlock()
}

// Reset is called when a human resumes a paused task with guidance (and when the task moves on successfully): the trip count
// starts again, so the next hard trip is a stage-1 nudge, not a pause. A reset row is written only when there is a count to
// reset.
func (w *Watchdog) Reset(ctx context.Context, taskID string) error {
	w.mu.Lock()
	delete(w.nudge, taskID)
	if d, ok := w.echo[taskID]; ok {
		d.Reset()
	}
	w.mu.Unlock()
	if n, err := w.store.Count(ctx, taskID); err == nil && n == 0 {
		return nil
	}
	return w.store.Reset(ctx, taskID)
}

// Steer delivers a message to a task's running agent turn from outside it (the team lead acting on a stalled task). With
// escalate, the running turn is stopped and the node parks on a human question carrying the message. Without, the turn is
// interrupted and the same session continues with the message. With no turn running, a nudge is queued for the task's next
// turn (an escalation then has nothing to park). It reports whether a running turn was found.
func (w *Watchdog) Steer(ctx context.Context, taskID, msg string, escalate bool) bool {
	w.mu.Lock()
	var keys []string
	for k, s := range w.execs {
		if scope(s.info) == taskID && s.info.Kind == workflow.KindAgentTask {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var st *execState
	if len(keys) > 0 {
		st = w.execs[keys[0]]
	}
	if st == nil {
		if !escalate {
			w.nudge[taskID] = msg
		}
		w.mu.Unlock()
		return false
	}
	w.mu.Unlock()
	st.mu.Lock()
	if st.enforcing || st.pause != nil || st.cancel == nil {
		st.mu.Unlock()
		return true // something is already stopping this turn
	}
	n := st.info
	if escalate {
		v := Verdict{Kind: Trip, Signal: SignalLeadEscalation, Stage: 2, Evidence: msg}
		st.pause = &TripRecord{TaskID: scope(n), RunID: string(n.RunID), NodeID: string(n.NodeID), Attempt: n.Attempt, AgentID: n.Meta["agent"], Signal: SignalLeadEscalation, Stage: 2, Evidence: Evidence(v, nil)}
		st.enforcing = true
		cancel := st.cancel
		st.mu.Unlock()
		cancel(workflow.ErrWatchdogTrip{Signal: SignalLeadEscalation, Stage: 2, Evidence: msg})
		return true
	}
	st.enforcing = true
	slot := st.slot
	st.mu.Unlock()
	if tc := slot.Get(); tc != nil {
		_ = tc.CancelTurn()
		_ = tc.Send(ctx, msg)
	} else {
		w.mu.Lock()
		w.nudge[taskID] = msg
		w.mu.Unlock()
	}
	st.mu.Lock()
	st.turn = NewTurn(st.cfgv)
	st.enforcing = false
	st.mu.Unlock()
	return true
}

// Observer returns the per-execution stream observer (agent_task nodes only).
func (w *Watchdog) Observer() workflow.ObserverFactory {
	return func(n workflow.NodeInfo) workflow.StreamObserver {
		if n.Kind != workflow.KindAgentTask {
			return nil
		}
		return &observer{w: w, st: w.state(n)}
	}
}

type observer struct {
	w  *Watchdog
	st *execState
}

func (o *observer) OnStart(context.Context, workflow.NodeInfo) {}
func (o *observer) OnEnd(ctx context.Context, n workflow.NodeInfo, out workflow.NodeOutput) {
}
func (o *observer) OnError(context.Context, workflow.NodeInfo, error) {}
func (o *observer) OnChunk(ctx context.Context, n workflow.NodeInfo, c workflow.Chunk) {
	o.st.mu.Lock()
	if o.st.enforcing || o.st.pause != nil || o.st.lead != nil {
		o.st.mu.Unlock()
		return // the cancel/nudge we triggered is in flight; its own chunks are not new evidence
	}
	turn := o.st.turn
	o.st.mu.Unlock()
	v := turn.Observe(c)
	switch v.Kind {
	case Warn:
		t := TripRecord{TaskID: scope(n), RunID: string(n.RunID), NodeID: string(n.NodeID), Attempt: n.Attempt, Signal: v.Signal, Evidence: Evidence(v, nil)}
		_ = o.w.store.Record(context.Background(), t)
		o.w.notify(context.Background(), "watchdog.warning", t, "Watchdog: "+v.Evidence)
	case Trip:
		go o.w.trip(o.st, v)
	}
}

func (w *Watchdog) notify(ctx context.Context, typ string, t TripRecord, msg string) {
	if w.hooks.Notify != nil {
		w.hooks.Notify(ctx, typ, t, msg)
	}
}

// trip applies the fixed two-stage policy to a hard verdict from a running turn.
func (w *Watchdog) trip(st *execState, v Verdict) {
	st.mu.Lock()
	if st.enforcing || st.pause != nil || st.lead != nil {
		st.mu.Unlock()
		return
	}
	st.enforcing = true
	st.mu.Unlock()
	ctx := context.Background()

	n := st.info
	count, _ := w.store.Count(ctx, scope(n))
	v.Stage = 1
	if count >= 1 {
		v.Stage = 2
	}
	t := TripRecord{TaskID: scope(n), RunID: string(n.RunID), NodeID: string(n.NodeID), Attempt: n.Attempt, AgentID: n.Meta["agent"], Signal: v.Signal, Stage: v.Stage, Evidence: Evidence(v, nil)}
	_ = w.store.Record(ctx, t)

	tc := st.slot.Get()
	if v.Stage == 1 && w.hooks.Lead != nil && w.hooks.Supervise != nil && w.hooks.Lead(ctx, n) {
		// the team lead decides what happens next: the turn stops (nothing more is spent meanwhile) and the middleware runs
		// the lead's supervision turn, then restarts the work with the lead's message, or pauses for the human
		w.notify(ctx, "watchdog.tripped", t, fmt.Sprintf("Watchdog tripped (%s): %s Stopping the turn and asking the team lead.", v.Signal, v.Evidence))
		st.mu.Lock()
		st.lead = &leadTrip{rec: t, v: v}
		cancel := st.cancel
		st.mu.Unlock()
		if cancel != nil {
			cancel(workflow.ErrWatchdogTrip{Signal: v.Signal, Stage: 1, Evidence: v.Evidence})
		}
		return
	}
	if v.Stage == 1 {
		w.notify(ctx, "watchdog.tripped", t, fmt.Sprintf("Watchdog tripped (%s): %s Cancelling the turn and asking the agent to change approach.", v.Signal, v.Evidence))
		if tc != nil {
			_ = tc.CancelTurn()
			nudge := nudgeText(v)
			if err := tc.Send(ctx, nudge); err != nil {
				// cannot continue the session: stop the node so the engine escalates
				st.mu.Lock()
				st.pause = &t
				st.pauseNote = "nudge could not be delivered: " + err.Error()
				st.mu.Unlock()
				if st.cancel != nil {
					st.cancel(workflow.ErrWatchdogTrip{Signal: v.Signal, Stage: 1, Evidence: v.Evidence})
				}
				return
			}
		} else if st.cancel != nil {
			st.cancel(workflow.ErrWatchdogTrip{Signal: v.Signal, Stage: 1, Evidence: v.Evidence})
		}
		st.mu.Lock()
		st.turn = NewTurn(st.cfgv) // a nudge starts a fresh turn
		st.tripCount++
		st.enforcing = false
		st.mu.Unlock()
		w.armTimer(st)
		return
	}

	// stage 2: hand off and pause
	if tc != nil {
		_ = tc.CancelTurn()
	}
	handoff := ""
	if w.hooks.Handoff != nil {
		handoff, _ = w.hooks.Handoff(ctx, n, t)
	}
	t.Evidence = Evidence(v, map[string]any{"handoff": handoff})
	w.notify(ctx, "watchdog.paused", t, fmt.Sprintf("Watchdog paused this task (%s, second trip): %s", v.Signal, v.Evidence))
	st.mu.Lock()
	st.pause = &t
	st.mu.Unlock()
	if st.cancel != nil {
		st.cancel(workflow.ErrWatchdogTrip{Signal: v.Signal, Stage: 2, Evidence: v.Evidence})
	}
}

func nudgeText(v Verdict) string {
	return fmt.Sprintf("Watchdog notice: %s Stop, state what is blocking you, and propose a different approach before using any tool.", v.Evidence)
}

func (w *Watchdog) armTimer(st *execState) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.timer != nil {
		st.timer.Stop()
	}
	st.timer = time.AfterFunc(st.cfgv.MaxTurnDuration, func() {
		w.trip(st, Verdict{Kind: Trip, Signal: SignalTimeCap, Evidence: fmt.Sprintf("turn exceeded %s wall clock", st.cfgv.MaxTurnDuration)})
	})
}

// PauseNote tells the human how to answer a watchdog pause.
const PauseNote = `Answer (optionally with data.text: guidance for the agent) to let the agent try again; the guidance reaches its next turn and the trip count starts again. Reject to stop: the task is CANCELLED and the run ends.`

// pauseRequest is the question a pause parks the node on. The node runs again after the answer, with the answer in its inbox
// (Retry + DeliverAnswer): guidance reaches the agent's next turn, a rejection stops the task.
func pauseRequest(n workflow.NodeInfo, pause TripRecord) *workflow.RequestSpec {
	var ev map[string]any
	_ = json.Unmarshal([]byte(pause.Evidence), &ev)
	if ev == nil {
		ev = map[string]any{}
	}
	ev["note"] = PauseNote
	payload, _ := json.Marshal(ev)
	title := fmt.Sprintf("Watchdog paused %s: %s", n.NodeID, pause.Signal)
	if pause.Signal == SignalLeadEscalation {
		title = fmt.Sprintf("The team lead asks for a human decision on %s", n.NodeID)
	}
	return &workflow.RequestSpec{Kind: "question", Title: title, Payload: payload, Retry: true, DeliverAnswer: true}
}

// answer is a human's answer delivered back to a paused node (workflow.RequestSpec.DeliverAnswer).
type answer struct {
	Decision string          `json:"decision"`
	By       string          `json:"by"`
	Request  string          `json:"request"`
	Data     json.RawMessage `json:"data"`
}

// takeAnswer finds a delivered answer in the inbox and returns it with the rest of the inbox.
func takeAnswer(inbox []workflow.Message) (answer, []workflow.Message, bool) {
	for i, m := range inbox {
		var a answer
		if json.Unmarshal(m.Value, &a) == nil && a.Decision != "" && a.Request != "" {
			rest := append(append([]workflow.Message(nil), inbox[:i]...), inbox[i+1:]...)
			return a, rest, true
		}
	}
	return answer{}, inbox, false
}

// answerText renders the human's answer data as readable text (a string, or the text/guidance/feedback/message field, or
// "key: value" lines); never raw JSON.
func answerText(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == "{}" || s == `""` {
		return ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return trunc(str, 4000)
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return trunc(s, 4000)
	}
	for _, k := range []string{"text", "guidance", "feedback", "message", "answer"} {
		if v, ok := obj[k].(string); ok && strings.TrimSpace(v) != "" {
			return trunc(v, 4000)
		}
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		if k != "action" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		if v, ok := obj[k].(string); ok {
			if strings.TrimSpace(v) != "" {
				lines = append(lines, k+": "+strings.TrimSpace(v))
			}
			continue
		}
		b, _ := json.Marshal(obj[k])
		lines = append(lines, k+": "+string(b))
	}
	return trunc(strings.Join(lines, "\n"), 4000)
}

func guidanceNotice(a answer) string {
	if g := answerText(a.Data); g != "" {
		return "A human looked at why the watchdog stopped your previous turn and answered:\n" + g + "\nFollow it, and take a different approach from the one that was stopped."
	}
	return "A human looked at why the watchdog stopped your previous turn and asked you to continue. Take a different approach from the one that was stopped."
}

func noticeMsg(text string) workflow.Message {
	b, _ := json.Marshal(map[string]string{"watchdog": text})
	return workflow.Message{From: "watchdog", Value: b}
}

// handleAnswer applies a human's answer to an earlier pause of this node: a rejection stops the task (Halt), anything else
// resets the trip count and puts the human's guidance in front of the agent. It reports whether the node must stop.
func (w *Watchdog) handleAnswer(ctx context.Context, in *workflow.NodeInput) (workflow.NodeOutput, bool) {
	a, rest, ok := takeAnswer(in.Inbox)
	if !ok {
		return workflow.NodeOutput{}, false
	}
	in.Inbox = rest
	if a.Decision == "rejected" {
		if w.hooks.Stop != nil {
			w.hooks.Stop(ctx, in.NodeInfo, a.By)
		}
		b, _ := json.Marshal(map[string]any{"stopped": true, "by": a.By})
		return workflow.NodeOutput{Value: b, Halt: true}, true
	}
	_ = w.Reset(ctx, scope(in.NodeInfo))
	if in.Kind == workflow.KindAgentTask {
		in.Inbox = append(in.Inbox, noticeMsg(guidanceNotice(a)))
	} else {
		// a paused check: the guidance waits for the agent's next turn
		w.mu.Lock()
		w.nudge[scope(in.NodeInfo)] = guidanceNotice(a)
		w.mu.Unlock()
	}
	return workflow.NodeOutput{}, false
}

// Middleware wraps agent_task handlers: it owns the node context cancel, the wall-clock timer, pending nudges from
// error echo, and converts a stage-2 pause into a human request so the node parks in WAITING_HUMAN. After a stage-1 trip
// that goes to the team lead it runs the lead's supervision and restarts the turn with the lead's message (or pauses).
func (w *Watchdog) Middleware() workflow.Middleware {
	return func(next workflow.NodeHandler) workflow.NodeHandler {
		return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			st := w.state(in.NodeInfo)
			defer w.drop(in.NodeInfo)
			if out, stop := w.handleAnswer(ctx, &in); stop {
				return out, nil
			}

			// a nudge queued by the error-echo detector at the previous check is delivered with this turn's input
			w.mu.Lock()
			if nd, ok := w.nudge[scope(in.NodeInfo)]; ok {
				delete(w.nudge, scope(in.NodeInfo))
				in.Inbox = append(in.Inbox, noticeMsg(nd))
			}
			w.mu.Unlock()

			for {
				cctx, cancel := context.WithCancelCause(ctx)
				st.mu.Lock()
				st.slot, st.cancel = in.Turn, cancel
				st.mu.Unlock()
				w.armTimer(st)

				out, err := next(cctx, in)

				st.mu.Lock()
				if st.timer != nil {
					st.timer.Stop()
				}
				pause, note, lead := st.pause, st.pauseNote, st.lead
				st.lead = nil
				st.mu.Unlock()
				cancel(nil)
				if pause != nil {
					return workflow.NodeOutput{Request: pauseRequest(in.NodeInfo, *pause)}, noteErr(note)
				}
				if lead == nil || ctx.Err() != nil {
					return out, err
				}
				steer, ok := w.hooks.Supervise(ctx, in.NodeInfo, lead.rec)
				if ok && steer.Action == "escalate" {
					p := lead.rec
					p.Stage = 2
					p.Evidence = Evidence(Verdict{Signal: lead.v.Signal, Stage: 2, Evidence: lead.v.Evidence}, map[string]any{"lead": steer.Message})
					w.notify(ctx, "watchdog.paused", p, "The team lead escalated this task to you: "+steer.Message)
					return workflow.NodeOutput{Request: pauseRequest(in.NodeInfo, p)}, nil
				}
				notice := nudgeText(lead.v)
				if ok && strings.TrimSpace(steer.Message) != "" {
					notice = fmt.Sprintf("Message from the team lead (the watchdog stopped your previous turn: %s):\n%s", lead.v.Evidence, strings.TrimSpace(steer.Message))
				}
				in.Inbox = append(in.Inbox, noticeMsg(notice))
				in.Turn = &workflow.TurnSlot{}
				st.mu.Lock()
				st.turn = NewTurn(st.cfgv)
				st.tripCount++
				st.enforcing = false
				st.mu.Unlock()
			}
		}
	}
}

func noteErr(string) error { return nil }

// CheckMiddleware wraps check nodes: it feeds results to the error-echo detector. A stage-1 echo queues a nudge for the
// agent's next turn (the team lead's message instead, when a lead supervises the task); a stage-2 echo parks the check node
// for a human.
func (w *Watchdog) CheckMiddleware(diffHash func(ctx context.Context, n workflow.NodeInfo) string) workflow.Middleware {
	return func(next workflow.NodeHandler) workflow.NodeHandler {
		return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			if out, stop := w.handleAnswer(ctx, &in); stop {
				return out, nil
			}
			out, err := next(ctx, in)
			if err != nil || len(out.Value) == 0 {
				return out, err
			}
			var res workflow.CheckResult
			if json.Unmarshal(out.Value, &res) != nil || res.Name == "" {
				return out, nil
			}
			dh := res.DiffHash
			if diffHash != nil {
				dh = diffHash(ctx, in.NodeInfo)
			}
			sc := scope(in.NodeInfo)
			w.mu.Lock()
			d, ok := w.echo[sc]
			if !ok {
				d = &EchoDetector{}
				w.echo[sc] = d
			}
			w.mu.Unlock()
			v := d.OnCheckResult(res, dh)
			if v.Kind != Trip {
				return out, nil
			}
			count, _ := w.store.Count(ctx, sc)
			v.Stage = 1
			if count >= 1 {
				v.Stage = 2
			}
			t := TripRecord{TaskID: sc, RunID: string(in.RunID), NodeID: string(in.NodeID), Attempt: in.Attempt, Signal: v.Signal, Stage: v.Stage, Evidence: Evidence(v, nil)}
			_ = w.store.Record(ctx, t)
			if v.Stage == 1 {
				nudge := nudgeText(v)
				if w.hooks.Lead != nil && w.hooks.Supervise != nil && w.hooks.Lead(ctx, in.NodeInfo) {
					w.notify(ctx, "watchdog.tripped", t, "Watchdog tripped (error echo): "+v.Evidence+" Asking the team lead.")
					if steer, ok := w.hooks.Supervise(ctx, in.NodeInfo, t); ok {
						if steer.Action == "escalate" {
							p := t
							p.Stage = 2
							p.Evidence = Evidence(Verdict{Signal: v.Signal, Stage: 2, Evidence: v.Evidence}, map[string]any{"lead": steer.Message})
							w.notify(ctx, "watchdog.paused", p, "The team lead escalated this task to you: "+steer.Message)
							return workflow.NodeOutput{Request: pauseRequest(in.NodeInfo, p)}, nil
						}
						if strings.TrimSpace(steer.Message) != "" {
							nudge = fmt.Sprintf("Message from the team lead (%s):\n%s", v.Evidence, strings.TrimSpace(steer.Message))
						}
					}
				} else {
					w.notify(ctx, "watchdog.tripped", t, "Watchdog tripped (error echo): "+v.Evidence+" The next turn will start with a notice to change approach.")
				}
				w.mu.Lock()
				w.nudge[sc] = nudge
				w.mu.Unlock()
				return out, nil
			}
			handoff := ""
			if w.hooks.Handoff != nil {
				handoff, _ = w.hooks.Handoff(ctx, in.NodeInfo, t)
			}
			t.Evidence = Evidence(v, map[string]any{"handoff": handoff})
			w.notify(ctx, "watchdog.paused", t, "Watchdog paused this task (error echo, second trip): "+v.Evidence)
			p := pauseRequest(in.NodeInfo, t)
			p.Title = "Watchdog paused: " + v.Signal
			return workflow.NodeOutput{Request: p}, nil
		}
	}
}
