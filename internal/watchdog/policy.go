package watchdog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

// Hooks connect the watchdog to the rest of the app without importing it.
type Hooks struct {
	// Notify publishes watchdog.warning / watchdog.tripped / watchdog.paused, posts the SYSTEM room message and sends the
	// desktop notification (stage 2). It must not block for long.
	Notify func(ctx context.Context, typ string, t TripRecord, message string)
	// Handoff writes a handoff artifact for a stage-2 pause and returns its id (may be empty).
	Handoff func(ctx context.Context, in workflow.NodeInfo, t TripRecord) (string, error)
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

// Reset is called when a human resumes a paused task with guidance: it allows one more nudge.
func (w *Watchdog) Reset(ctx context.Context, taskID string) error {
	w.mu.Lock()
	delete(w.nudge, taskID)
	if d, ok := w.echo[taskID]; ok {
		d.Reset()
	}
	w.mu.Unlock()
	return w.store.Reset(ctx, taskID)
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
	if o.st.enforcing || o.st.pause != nil {
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
	if st.enforcing || st.pause != nil {
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

// Middleware wraps agent_task handlers: it owns the node context cancel, the wall-clock timer, pending nudges from
// error echo, and converts a stage-2 pause into a human request so the node parks in WAITING_HUMAN.
func (w *Watchdog) Middleware() workflow.Middleware {
	return func(next workflow.NodeHandler) workflow.NodeHandler {
		return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
			st := w.state(in.NodeInfo)
			defer w.drop(in.NodeInfo)
			cctx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			st.mu.Lock()
			st.slot, st.cancel = in.Turn, cancel
			st.mu.Unlock()
			w.armTimer(st)
			defer func() {
				st.mu.Lock()
				if st.timer != nil {
					st.timer.Stop()
				}
				st.mu.Unlock()
			}()

			// a nudge queued by the error-echo detector at the previous check is delivered with this turn's input
			w.mu.Lock()
			if nd, ok := w.nudge[scope(in.NodeInfo)]; ok {
				delete(w.nudge, scope(in.NodeInfo))
				b, _ := json.Marshal(map[string]string{"watchdog": nd})
				in.Inbox = append(in.Inbox, workflow.Message{From: "watchdog", Value: b})
			}
			w.mu.Unlock()

			out, err := next(cctx, in)

			st.mu.Lock()
			pause := st.pause
			note := st.pauseNote
			st.mu.Unlock()
			if pause != nil {
				return workflow.NodeOutput{Request: &workflow.RequestSpec{
					Kind:    "question",
					Title:   fmt.Sprintf("Watchdog paused %s: %s", in.NodeID, pause.Signal),
					Payload: json.RawMessage(pause.Evidence),
				}}, noteErr(note)
			}
			return out, err
		}
	}
}

func noteErr(string) error { return nil }

// CheckMiddleware wraps check nodes: it feeds results to the error-echo detector. A stage-1 echo queues a nudge for the
// agent's next turn; a stage-2 echo parks the check node for a human.
func (w *Watchdog) CheckMiddleware(diffHash func(ctx context.Context, n workflow.NodeInfo) string) workflow.Middleware {
	return func(next workflow.NodeHandler) workflow.NodeHandler {
		return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
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
				w.notify(ctx, "watchdog.tripped", t, "Watchdog tripped (error echo): "+v.Evidence+" The next turn will start with a notice to change approach.")
				w.mu.Lock()
				w.nudge[sc] = nudgeText(v)
				w.mu.Unlock()
				return out, nil
			}
			handoff := ""
			if w.hooks.Handoff != nil {
				handoff, _ = w.hooks.Handoff(ctx, in.NodeInfo, t)
			}
			t.Evidence = Evidence(v, map[string]any{"handoff": handoff})
			w.notify(ctx, "watchdog.paused", t, "Watchdog paused this task (error echo, second trip): "+v.Evidence)
			return workflow.NodeOutput{Request: &workflow.RequestSpec{Kind: "question", Title: "Watchdog paused: " + v.Signal, Payload: json.RawMessage(t.Evidence)}}, nil
		}
	}
}
