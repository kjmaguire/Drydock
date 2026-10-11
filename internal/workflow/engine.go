package workflow

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Config configures an Engine.
type Config struct {
	DB       *storage.DB
	Bus      events.Bus
	DataDir  string        // runs/<run>/<node>/<attempt>/ lives under here
	Owner    string        // lease owner id; default hostname:pid:random
	LeaseTTL time.Duration // default 30s
	// SafePoint, if set, is called before every agent_task attempt to record the worktree's exact state (a hidden git
	// commit, brief section 13). The returned ref is stored on the attempt's node_executions row. Failures are reported
	// as events and never block the attempt.
	SafePoint func(ctx context.Context, n NodeInfo) (string, error)
}

// ObserverFactory returns a StreamObserver for one node execution, or nil for none.
type ObserverFactory func(n NodeInfo) StreamObserver

// StartOptions scopes a run.
type StartOptions struct {
	ProjectID string
	TaskID    string
}

// Engine runs workflow graphs.
type Engine struct {
	cfg       Config
	st        *store
	mu        sync.Mutex
	graphs    map[string]*Graph
	mw        map[NodeKind][]Middleware
	observers []ObserverFactory
	active    map[RunID]*driver
	wg        sync.WaitGroup
	closed    bool
}

// New creates an Engine.
func New(cfg Config) *Engine {
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = 30 * time.Second
	}
	if cfg.Owner == "" {
		host, _ := os.Hostname()
		cfg.Owner = fmt.Sprintf("%s:%d:%s", host, os.Getpid(), ids.New("o"))
	}
	return &Engine{cfg: cfg, st: &store{db: cfg.DB}, graphs: map[string]*Graph{}, mw: map[NodeKind][]Middleware{}, active: map[RunID]*driver{}}
}

// Register makes a graph available by name (needed for Start and Recover).
func (e *Engine) Register(g *Graph) error {
	if err := g.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.graphs[g.Name] = g
	return nil
}

// Use appends middleware for nodes of a kind; the first registered is outermost.
func (e *Engine) Use(kind NodeKind, mw ...Middleware) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mw[kind] = append(e.mw[kind], mw...)
}

// Observe registers a stream observer factory (watchdog, telemetry).
func (e *Engine) Observe(f ObserverFactory) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.observers = append(e.observers, f)
}

// Owner returns this engine's lease owner id.
func (e *Engine) Owner() string { return e.cfg.Owner }

// driver is a run this engine is driving: cancel stops it; done is closed once its goroutine has ended, after the run's
// lease was released.
type driver struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Close cancels active runs' drivers and waits for them. Runs stay RUNNING in the database and are resumed by Recover.
func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	for _, d := range e.active {
		d.cancel()
	}
	e.mu.Unlock()
	e.wg.Wait()
}

func (e *Engine) graph(name string) (*Graph, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	g, ok := e.graphs[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownWorkflow, name)
	}
	return g, nil
}

func (e *Engine) emit(ctx context.Context, typ string, run RunInfo, data any) {
	if e.cfg.Bus == nil {
		return
	}
	raw, _ := json.Marshal(data)
	_, _ = e.cfg.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: run.ProjectID, RunID: string(run.ID), TaskID: run.TaskID, Data: raw})
}

// Start creates a run and begins executing it in the background.
func (e *Engine) Start(ctx context.Context, workflow string, input any, opts StartOptions) (RunID, error) {
	g, err := e.graph(workflow)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	id := RunID(ids.New("run"))
	state := newState(raw, g.TopologyHash())
	for _, n := range g.Entry {
		state.Inboxes[n] = append(state.Inboxes[n], Message{Value: raw})
	}
	cp := Checkpoint{ID: CheckpointID(ids.New("cp")), RunID: id, Workflow: g.Name, Step: 0, Kind: CheckpointEntry, State: state, Created: storage.Now()}
	info := RunInfo{ID: id, Workflow: g.Name, Status: StatusRunning, ProjectID: opts.ProjectID, TaskID: opts.TaskID, Input: raw, Topology: state.Topology}
	if err := e.st.createRun(ctx, info, cp); err != nil {
		return "", err
	}
	ok, err := e.st.acquireLease(ctx, id, e.cfg.Owner, e.cfg.LeaseTTL)
	if err != nil || !ok {
		return id, fmt.Errorf("%w (start)", ErrLeaseHeld)
	}
	e.emit(ctx, "workflow.started", info, map[string]any{"workflow": g.Name})
	e.emit(ctx, "workflow.checkpoint_written", info, map[string]any{"checkpoint": cp.ID, "kind": cp.Kind, "step": 0})
	e.launch(info, g, cp)
	return id, nil
}

// launch starts the driver goroutine; the lease must already be held by this engine.
func (e *Engine) launch(info RunInfo, g *Graph, from Checkpoint) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	dctx, cancel := context.WithCancel(context.Background())
	d := &driver{cancel: cancel, done: make(chan struct{})}
	e.active[info.ID] = d
	e.wg.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.wg.Done()
		defer func() {
			e.mu.Lock()
			delete(e.active, info.ID)
			e.mu.Unlock()
			cancel()
			close(d.done)
		}()
		e.drive(dctx, info, g, from)
	}()
}

// drive executes supersteps until the run completes, fails, is cancelled, or parks at a request port.
func (e *Engine) drive(ctx context.Context, info RunInfo, g *Graph, from Checkpoint) {
	hbCtx, stopHB := context.WithCancel(context.Background())
	defer stopHB()
	go e.heartbeat(hbCtx, info.ID)
	defer e.st.releaseLease(context.Background(), info.ID, e.cfg.Owner)

	st := from.State.clone()
	parent := from.ID
	if st.Status == StatusWaitingHuman || st.Status == StatusRecovering {
		st.Status = StatusRunning
	}
	_ = e.st.setStatus(ctx, info.ID, StatusRunning, "")

	for {
		if ctx.Err() != nil {
			e.onStopped(info)
			return
		}
		ready := e.ready(g, st)
		if len(ready) == 0 {
			if len(st.Pending) > 0 {
				st.Status = StatusWaitingHuman
				_ = e.st.setStatus(context.Background(), info.ID, StatusWaitingHuman, "")
				e.emit(context.Background(), "workflow.paused", info, map[string]any{"reason": "waiting_human", "pending": len(st.Pending)})
				return
			}
			e.finish(ctx, info, g, &st, &parent)
			return
		}
		step := st.Step + 1
		e.emit(ctx, "workflow.step_started", info, map[string]any{"step": step, "nodes": ready})

		type result struct {
			node NodeID
			out  NodeOutput
			err  error
		}
		results := make([]result, len(ready))
		var wg sync.WaitGroup
		for i, nid := range ready {
			wg.Add(1)
			go func(i int, nid NodeID) {
				defer wg.Done()
				inbox := st.Inboxes[nid]
				out, err := e.execNode(ctx, info, g, g.Nodes[nid], step, inbox, st)
				results[i] = result{nid, out, err}
			}(i, nid)
		}
		wg.Wait()

		if ctx.Err() != nil {
			e.onStopped(info)
			return
		}
		var failed error
		for _, r := range results {
			if r.err != nil {
				failed = fmt.Errorf("node %s: %w", r.node, r.err)
				break
			}
		}
		if failed != nil {
			e.fail(ctx, info, failed.Error())
			return
		}

		// Apply: consume inboxes, record outputs, route.
		var reqs []approvalInsert
		for _, r := range results {
			consumed := st.Inboxes[r.node]
			delete(st.Inboxes, r.node)
			delete(st.Barriers, r.node)
			if r.out.Request != nil {
				rid := RequestID(ids.New("req"))
				spec := *r.out.Request
				pr := PendingRequest{ID: rid, NodeID: r.node, Step: step, Spec: spec}
				if spec.Retry {
					pr.Inbox = append([]Message(nil), consumed...)
					if len(pr.Inbox) == 0 {
						pr.Inbox = []Message{{From: r.node}}
					}
				}
				st.Pending[rid] = pr
				st.Waiting[r.node] = rid
				reqs = append(reqs, approvalInsert{id: rid, nodeID: r.node, spec: spec, projectID: info.ProjectID, taskID: info.TaskID})
				continue
			}
			if len(r.out.Value) > 0 {
				st.Shared["out/"+string(r.node)] = e.externalize(info.ID, r.node, step, r.out.Value)
			}
			if err := route(g, &st, r.node, r.out); err != nil {
				e.fail(ctx, info, err.Error())
				return
			}
		}
		st.Step = step
		st.LastNodes = append([]NodeID(nil), ready...)
		cp := Checkpoint{ID: CheckpointID(ids.New("cp")), RunID: info.ID, Workflow: g.Name, Step: step, Kind: CheckpointStep, Parent: parent, State: st, Created: storage.Now()}
		if err := e.st.commit(ctx, stepCommit{cp: cp, status: StatusRunning, newRequests: reqs, owner: e.cfg.Owner, ttl: e.cfg.LeaseTTL}); err != nil {
			if errors.Is(err, ErrLeaseHeld) {
				e.onStopped(info) // fenced out: another owner took over; do not touch the run
				return
			}
			e.fail(ctx, info, "commit step: "+err.Error())
			return
		}
		parent = cp.ID
		e.emit(ctx, "workflow.step_completed", info, map[string]any{"step": step})
		e.emit(ctx, "workflow.checkpoint_written", info, map[string]any{"checkpoint": cp.ID, "kind": cp.Kind, "step": step})
		for _, r := range reqs {
			e.emit(ctx, "workflow.request_raised", info, map[string]any{"request": r.id, "node": r.nodeID, "kind": r.spec.Kind, "title": r.spec.Title})
			e.emit(ctx, "approval.requested", info, map[string]any{"request": r.id, "kind": r.spec.Kind, "title": r.spec.Title})
		}
	}
}

func (e *Engine) onStopped(info RunInfo) {
	// The driver was cancelled (Cancel or Close). Cancel marks the run itself; Close leaves it RUNNING for Recover.
}

func (e *Engine) heartbeat(ctx context.Context, run RunID) {
	t := time.NewTicker(e.cfg.LeaseTTL / 3)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if ok, err := e.st.renewLease(ctx, run, e.cfg.Owner, e.cfg.LeaseTTL); err == nil && !ok {
				return
			}
		}
	}
}

// ready returns nodes that can run in the next superstep, in declaration order.
func (e *Engine) ready(g *Graph, st RunState) []NodeID {
	var out []NodeID
	for _, id := range g.order {
		if len(st.Inboxes[id]) == 0 {
			continue
		}
		n := g.Nodes[id]
		if len(n.FanIn) > 0 {
			have := map[NodeID]bool{}
			for _, s := range st.Barriers[id] {
				have[s] = true
			}
			all := true
			for _, s := range n.FanIn {
				if !have[s] {
					all = false
					break
				}
			}
			if !all {
				continue
			}
		}
		out = append(out, id)
	}
	return out
}

// route delivers a finished node's output to its out-edges (pure over state).
func route(g *Graph, st *RunState, from NodeID, out NodeOutput) error {
	if out.ResetLoops {
		st.LoopCounts = map[string]int{}
	}
	if out.Halt {
		return nil
	}
	for _, ed := range g.outEdges(from) {
		if ed.When != "" && ed.When != out.Route {
			continue
		}
		if ed.Cond != nil && !ed.Cond(out) {
			continue
		}
		target := ed.To
		iter := 0
		if ed.Kind == EdgeLoop {
			key := EdgeKey(ed.From, ed.To)
			if st.LoopCounts[key] >= ed.MaxIter {
				if ed.OnExhausted == "" {
					return fmt.Errorf("%w: %s", ErrLoopExhausted, key)
				}
				target = ed.OnExhausted
			} else {
				st.LoopCounts[key]++
				iter = st.LoopCounts[key]
			}
		}
		st.Inboxes[target] = append(st.Inboxes[target], Message{From: from, Value: out.Value, Iteration: iter})
		if len(g.Nodes[target].FanIn) > 0 {
			st.Barriers[target] = appendUnique(st.Barriers[target], from)
		}
	}
	return nil
}

func appendUnique(s []NodeID, v NodeID) []NodeID {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func (e *Engine) finish(ctx context.Context, info RunInfo, g *Graph, st *RunState, parent *CheckpointID) {
	// The result is the outputs of the nodes of the final superstep: nothing was routed onward from them. (A node with a
	// loop edge is not a graph sink, so sinks alone would lose the result of looping workflows.)
	res := map[string]json.RawMessage{}
	for _, n := range st.LastNodes {
		if v, ok := st.Shared["out/"+string(n)]; ok {
			res[string(n)] = v
		}
	}
	raw, _ := json.Marshal(res)
	st.Status = StatusCompleted
	st.Result = raw
	cp := Checkpoint{ID: CheckpointID(ids.New("cp")), RunID: info.ID, Workflow: g.Name, Step: st.Step, Kind: CheckpointStep, Parent: *parent, State: *st, Created: storage.Now()}
	if err := e.st.commit(ctx, stepCommit{cp: cp, status: StatusCompleted, result: raw, owner: e.cfg.Owner, ttl: e.cfg.LeaseTTL}); err != nil {
		if !errors.Is(err, ErrLeaseHeld) {
			e.fail(ctx, info, "commit completion: "+err.Error())
		}
		return
	}
	*parent = cp.ID
	e.emit(context.Background(), "workflow.completed", info, map[string]any{"result": json.RawMessage(raw)})
}

func (e *Engine) fail(ctx context.Context, info RunInfo, reason string) {
	_ = e.st.setStatus(context.Background(), info.ID, StatusFailed, reason)
	e.emit(context.Background(), "workflow.failed", info, map[string]any{"reason": reason})
}

// externalize moves large values to disk and returns a small {"$ref": path} object.
func (e *Engine) externalize(run RunID, node NodeID, step int, v json.RawMessage) json.RawMessage {
	const limit = 64 * 1024
	if len(v) <= limit || e.cfg.DataDir == "" {
		return v
	}
	dir := filepath.Join(e.cfg.DataDir, "runs", string(run), string(node), fmt.Sprintf("step-%d", step))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return v
	}
	p := filepath.Join(dir, "output.json")
	if err := os.WriteFile(p, v, 0o644); err != nil {
		return v
	}
	b, _ := json.Marshal(map[string]any{"$ref": p, "bytes": len(v)})
	return b
}

// ReadRef resolves a {"$ref": path} value; other values are returned unchanged.
func ReadRef(v json.RawMessage) (json.RawMessage, error) {
	var probe struct {
		Ref string `json:"$ref"`
	}
	if len(v) > 0 && v[0] == '{' && json.Unmarshal(v, &probe) == nil && probe.Ref != "" {
		return os.ReadFile(probe.Ref)
	}
	return v, nil
}

// --- node execution ---

func (e *Engine) execNode(ctx context.Context, info RunInfo, g *Graph, n *Node, step int, inbox []Message, st RunState) (NodeOutput, error) {
	// Reuse a completed execution of this step (partial-superstep guarantee).
	rows, err := e.st.execRows(ctx, info.ID, n.ID, step)
	if err != nil {
		return NodeOutput{}, err
	}
	attempt := 1
	interrupted := 0 // attempts of this step whose driver stopped mid-node: the app crashed (started) or was closed (abandoned)
	var prev *Previous
	for _, r := range rows {
		if r.Status == "done" {
			var out NodeOutput
			if err := json.Unmarshal(r.Output, &out); err != nil {
				return NodeOutput{}, fmt.Errorf("decode recorded output: %w", err)
			}
			e.emit(ctx, "workflow.node_reused", info, map[string]any{"node": n.ID, "step": step, "attempt": r.Attempt})
			return out, nil
		}
		if r.Attempt >= attempt {
			attempt = r.Attempt + 1
		}
		if r.Status == "started" {
			_ = e.st.execAbandon(ctx, info.ID, n.ID, step, r.Attempt)
		}
		if r.Status == "started" || r.Status == "abandoned" {
			interrupted++
		}
		prev = &Previous{Attempt: r.Attempt, PartialRef: r.PartialRef, Error: r.Error}
	}
	if n.Kind == KindAgentTask {
		if out, gated, err := e.interruptedTurnGate(ctx, info, n, step, interrupted); gated {
			return out, err
		}
	}
	maxAttempts := n.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	var lastErr error
	for tries := 0; tries < maxAttempts; tries++ {
		if ctx.Err() != nil {
			return NodeOutput{}, ctx.Err()
		}
		partial := ""
		if e.cfg.DataDir != "" {
			partial = filepath.Join(e.cfg.DataDir, "runs", string(info.ID), string(n.ID), fmt.Sprintf("%d-%d", step, attempt), "stream.jsonl")
		}
		if err := e.st.execStart(ctx, info.ID, n.ID, step, attempt, partial); err != nil {
			return NodeOutput{}, err
		}
		if e.cfg.SafePoint != nil && n.Kind == KindAgentTask {
			ni := NodeInfo{RunID: info.ID, ProjectID: info.ProjectID, TaskID: info.TaskID, Workflow: g.Name, NodeID: n.ID, Kind: n.Kind, Step: step, Attempt: attempt}
			if ref, serr := e.cfg.SafePoint(ctx, ni); serr == nil && ref != "" {
				_ = e.st.setSafePoint(ctx, info.ID, n.ID, step, attempt, ref)
				e.emit(ctx, "git.safe_point_created", info, map[string]any{"node": n.ID, "step": step, "attempt": attempt, "ref": ref})
			} else if serr != nil {
				e.emit(ctx, "git.safe_point_failed", info, map[string]any{"node": n.ID, "error": serr.Error()})
			}
		}
		out, err := e.invoke(ctx, info, g, n, step, attempt, inbox, st, prev, partial)
		if err == nil {
			raw, _ := json.Marshal(out)
			if err := e.st.execFinish(ctx, info.ID, n.ID, step, attempt, "done", raw, ""); err != nil {
				return NodeOutput{}, err
			}
			return out, nil
		}
		lastErr = err
		status := "failed"
		if ctx.Err() != nil {
			status = "abandoned" // the driver was stopped mid-node (the app closing, a cancel): cut off, not failed
		}
		_ = e.st.execFinish(context.Background(), info.ID, n.ID, step, attempt, status, nil, err.Error())
		var trip ErrWatchdogTrip
		if ctx.Err() != nil || errors.As(err, &trip) {
			return NodeOutput{}, err
		}
		var gate GateError
		if errors.As(err, &gate) {
			return e.humanGate(ctx, info, n, step, gate)
		}
		prev = &Previous{Attempt: attempt, PartialRef: partial, Error: err.Error()}
		attempt++
	}
	return NodeOutput{}, lastErr
}

// AutoRerunsAfterInterruption is how many times an agent turn cut off by a stop (the app closed or crashed, its driver lost)
// runs again on its own. Cut off once more, the run waits for a human (InterruptedGate): a turn that keeps being cut off
// would otherwise be paid for again at every start.
const AutoRerunsAfterInterruption = 1

// InterruptedGate is the "gate" of the approval request an agent turn raises after AutoRerunsAfterInterruption re-runs.
const InterruptedGate = "interrupted"

// interruptedTurnGate decides, before an agent turn, whether it may start (gated false). A human's rejection of this node's
// earlier interrupted-turn request ends the run here; a turn cut off more than AutoRerunsAfterInterruption times raises an
// approval request instead of running (Retry: the node runs again once the human has answered, and reads the answer here).
func (e *Engine) interruptedTurnGate(ctx context.Context, info RunInfo, n *Node, step, interrupted int) (NodeOutput, bool, error) {
	var status string
	err := e.st.db.Reader().QueryRowContext(ctx, `SELECT status FROM approvals WHERE run_id=? AND node_id=? AND status IN ('APPROVED','REJECTED')
		AND CAST(payload AS TEXT) LIKE '%"gate":"interrupted"%' ORDER BY created_at DESC, rowid DESC LIMIT 1`, string(info.ID), string(n.ID)).Scan(&status)
	if err == nil && status == "REJECTED" {
		return NodeOutput{}, true, fmt.Errorf("the %s turn was cut off by stops and the human chose not to run it again", n.ID)
	}
	if interrupted <= AutoRerunsAfterInterruption {
		return NodeOutput{}, false, nil
	}
	payload, _ := json.Marshal(map[string]any{"gate": InterruptedGate, "node": n.ID, "step": step, "interruptions": interrupted})
	e.emit(ctx, "workflow.turn_interrupted", info, map[string]any{"node": n.ID, "step": step, "interruptions": interrupted})
	return NodeOutput{Request: &RequestSpec{Kind: "approval", Retry: true, Payload: payload,
		Title: fmt.Sprintf("The %s turn was cut off %d times because Drydock stopped while it ran. Approve to run it again (it is paid for again) or reject to stop the run.", n.ID, interrupted)}}, true, nil
}

// GateError is an error a node handler returns when the node cannot go on until a person decides, for example because the
// repository's git configuration changed under the task (git.ConfigChangedError). Instead of failing the attempt, the engine
// raises an approval request carrying Gate's payload (plus "gate"); the node runs again once the person has answered. A
// rejection, the latest decision on that gate for the node, ends the run. Whatever must change for the node to pass after an
// approval (trusting the git configuration) is done by whoever records the answer (core.Respond), before the node runs again.
type GateError interface {
	error
	Gate() (gate, title string, payload map[string]any)
}

func (e *Engine) humanGate(ctx context.Context, info RunInfo, n *Node, step int, ge GateError) (NodeOutput, error) {
	gate, title, payload := ge.Gate()
	var status string
	err := e.st.db.Reader().QueryRowContext(ctx, `SELECT status FROM approvals WHERE run_id=? AND node_id=? AND status IN ('APPROVED','REJECTED')
		AND CAST(payload AS TEXT) LIKE ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, string(info.ID), string(n.ID), `%"gate":"`+gate+`"%`).Scan(&status)
	if err == nil && status == "REJECTED" {
		return NodeOutput{}, fmt.Errorf("%s: a person rejected going on: %w", n.ID, ge)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["gate"], payload["node"], payload["step"] = gate, n.ID, step
	raw, _ := json.Marshal(payload)
	e.emit(ctx, "workflow.gated", info, map[string]any{"node": n.ID, "step": step, "gate": gate})
	return NodeOutput{Request: &RequestSpec{Kind: "approval", Retry: true, Payload: raw, Title: title}}, nil
}

func (e *Engine) invoke(ctx context.Context, info RunInfo, g *Graph, n *Node, step, attempt int, inbox []Message, st RunState, prev *Previous, partial string) (NodeOutput, error) {
	ni := NodeInfo{RunID: info.ID, ProjectID: info.ProjectID, TaskID: info.TaskID, Workflow: g.Name, NodeID: n.ID, Kind: n.Kind, Step: step, Attempt: attempt,
		Key: fmt.Sprintf("%s/%s/%d/%d", info.ID, n.ID, step, attempt), Meta: n.Meta}

	// durable chunk file
	var cw *chunkWriter
	if partial != "" {
		var err error
		cw, err = newChunkWriter(partial)
		if err != nil {
			return NodeOutput{}, err
		}
		defer cw.Close()
	}

	e.mu.Lock()
	mws := append([]Middleware(nil), e.mw[n.Kind]...)
	facs := append([]ObserverFactory(nil), e.observers...)
	e.mu.Unlock()
	var obs []StreamObserver
	for _, f := range facs {
		if o := f(ni); o != nil {
			obs = append(obs, o)
		}
	}

	inboxDeref := make([]Message, len(inbox))
	for i, m := range inbox {
		inboxDeref[i] = m
		if v, err := ReadRef(m.Value); err == nil {
			inboxDeref[i].Value = v
		}
	}
	sharedSnap := make(map[string]json.RawMessage, len(st.Shared))
	for k, v := range st.Shared {
		sharedSnap[k] = v
	}
	var emitMu sync.Mutex
	in := NodeInput{NodeInfo: ni, Input: st.Input, Inbox: inboxDeref, Shared: sharedSnap, Previous: prev, Turn: &TurnSlot{}}
	in.Emit = func(c Chunk) {
		emitMu.Lock()
		defer emitMu.Unlock()
		if cw != nil {
			cw.Write(c)
		}
		for _, o := range obs {
			o.OnChunk(ctx, ni, c)
		}
		e.emit(ctx, "workflow.node_chunk", info, map[string]any{"node": n.ID, "step": step, "attempt": attempt, "chunk": c})
	}

	base := e.baseHandler(n)
	h := base
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}

	e.emit(ctx, "workflow.node_started", info, map[string]any{"node": n.ID, "kind": n.Kind, "step": step, "attempt": attempt})
	for _, o := range obs {
		o.OnStart(ctx, ni)
	}
	out, err := h(ctx, in)
	if cw != nil {
		cw.Sync()
	}
	if err != nil {
		for _, o := range obs {
			o.OnError(ctx, ni, err)
		}
		e.emit(context.Background(), "workflow.node_failed", info, map[string]any{"node": n.ID, "step": step, "attempt": attempt, "error": err.Error()})
		return NodeOutput{}, err
	}
	for _, o := range obs {
		o.OnEnd(ctx, ni, out)
	}
	e.emit(ctx, "workflow.node_completed", info, map[string]any{"node": n.ID, "step": step, "attempt": attempt, "route": out.Route})
	return out, nil
}

func (e *Engine) baseHandler(n *Node) NodeHandler {
	switch {
	case n.Kind == KindSubworkflow:
		return func(ctx context.Context, in NodeInput) (NodeOutput, error) {
			return e.runSub(ctx, n, in)
		}
	case n.Handler != nil:
		return n.Handler
	case n.Kind == KindRequest && n.Request != nil:
		spec := *n.Request
		return func(ctx context.Context, in NodeInput) (NodeOutput, error) {
			s := spec
			if len(s.Payload) == 0 && len(in.Inbox) > 0 {
				s.Payload = in.Inbox[0].Value
			}
			return NodeOutput{Request: &s}, nil
		}
	}
	return func(ctx context.Context, in NodeInput) (NodeOutput, error) {
		return NodeOutput{}, fmt.Errorf("node %s has no handler", n.ID)
	}
}

// runSub executes a subworkflow as its own run and waits for it to complete. A subworkflow that parks at a human
// request is reported as an error: human interaction belongs in the parent graph in this version.
func (e *Engine) runSub(ctx context.Context, n *Node, in NodeInput) (NodeOutput, error) {
	if err := e.Register(n.Sub); err != nil {
		return NodeOutput{}, err
	}
	var input json.RawMessage
	if len(in.Inbox) > 0 {
		input = in.Inbox[0].Value
	}
	var v any
	_ = json.Unmarshal(input, &v)
	id, err := e.Start(ctx, n.Sub.Name, v, StartOptions{})
	if err != nil {
		return NodeOutput{}, err
	}
	info, err := e.Await(ctx, id, func(r RunInfo) bool { return r.Status.Terminal() || r.Status == StatusWaitingHuman })
	if err != nil {
		return NodeOutput{}, err
	}
	if info.Status != StatusCompleted {
		return NodeOutput{}, fmt.Errorf("subworkflow %s ended %s: %s", n.Sub.Name, info.Status, info.FailedReason)
	}
	return NodeOutput{Value: info.Result}, nil
}

// --- chunk file ---

type chunkWriter struct {
	f     *os.File
	w     *bufio.Writer
	mu    sync.Mutex
	dirty bool
	stop  chan struct{}
	done  chan struct{}
}

func newChunkWriter(path string) (*chunkWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	c := &chunkWriter{f: f, w: bufio.NewWriter(f), stop: make(chan struct{}), done: make(chan struct{})}
	go c.flusher()
	return c, nil
}

// flusher makes buffered chunks durable at least every 100ms even if the handler goes quiet, so a crash loses
// only the last ~100ms of output.
func (c *chunkWriter) flusher() {
	defer close(c.done)
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			c.mu.Lock()
			if c.dirty {
				c.w.Flush()
				c.f.Sync()
				c.dirty = false
			}
			c.mu.Unlock()
		}
	}
}

func (c *chunkWriter) Write(ch Chunk) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(ch)
	c.w.Write(b)
	c.w.WriteByte('\n')
	c.dirty = true
}

func (c *chunkWriter) Sync() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.w.Flush()
	c.f.Sync()
	c.dirty = false
}

func (c *chunkWriter) Close() {
	close(c.stop)
	<-c.done
	c.Sync()
	c.f.Close()
}

// --- resume, time travel, replay, cancel, recover, queries ---

// Resume answers a pending request and continues the run.
func (e *Engine) Resume(ctx context.Context, run RunID, resp Response) error {
	info, err := e.st.getRun(ctx, run)
	if err != nil {
		return err
	}
	if info.Status.Terminal() {
		return ErrRunTerminal
	}
	g, err := e.graph(info.Workflow)
	if err != nil {
		return err
	}
	// A driver of this very engine may still be winding down after parking the run; wait for it to end.
	if err := e.awaitDriver(ctx, run, info.Status); err != nil {
		return err
	}
	ok, err := e.st.acquireLease(ctx, run, e.cfg.Owner, e.cfg.LeaseTTL)
	if err != nil {
		return err
	}
	if !ok {
		return ErrLeaseHeld
	}
	release := true
	defer func() {
		if release {
			e.st.releaseLease(context.Background(), run, e.cfg.Owner)
		}
	}()

	cp, err := e.st.getCheckpoint(ctx, info.LatestCP)
	if err != nil {
		return err
	}
	if cp.State.Topology != g.TopologyHash() {
		return ErrTopologyMismatch
	}
	st := cp.State.clone()
	pr, ok := st.Pending[resp.RequestID]
	if !ok {
		return ErrRequestConsumed
	}
	status := "APPROVED"
	switch resp.Decision {
	case "rejected":
		status = "REJECTED"
	case "approved", "answered", "":
	default:
		return fmt.Errorf("workflow: unknown decision %q", resp.Decision)
	}
	decision := resp.Decision
	if decision == "" {
		decision = "approved"
	}

	delete(st.Pending, resp.RequestID)
	delete(st.Waiting, pr.NodeID)
	out := NodeOutput{Route: decision}
	body, _ := json.Marshal(map[string]any{"decision": decision, "data": resp.Data, "by": resp.By, "request": resp.RequestID})
	out.Value = body
	if pr.Spec.Retry {
		// the node is still owed: it runs again with the inbox it had, and reads the human's decision from the approvals row
		st.Inboxes[pr.NodeID] = append([]Message(nil), pr.Inbox...)
		if pr.Spec.DeliverAnswer {
			st.Inboxes[pr.NodeID] = append(st.Inboxes[pr.NodeID], Message{From: pr.NodeID, Value: body})
		}
		if n := g.Nodes[pr.NodeID]; n != nil && len(n.FanIn) > 0 {
			st.Barriers[pr.NodeID] = append([]NodeID(nil), n.FanIn...)
		}
	} else {
		st.Shared["out/"+string(pr.NodeID)] = body
		if err := route(g, &st, pr.NodeID, out); err != nil {
			return err
		}
	}
	st.Status = StatusRunning
	newCP := Checkpoint{ID: CheckpointID(ids.New("cp")), RunID: run, Workflow: g.Name, Step: st.Step, Kind: CheckpointResponseEntry, Parent: cp.ID, State: st, Created: storage.Now()}

	err = e.st.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := fence(ctx, tx, run, e.cfg.Owner, e.cfg.LeaseTTL); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE approvals SET status=?, decision=?, response=?, decided_by=?, resolved_at=?, version=version+1
			WHERE id=? AND status='REQUESTED'`, status, decision, nullBytes(resp.Data), resp.By, storage.Now(), string(resp.RequestID))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrRequestConsumed
		}
		if err := insertCheckpoint(ctx, tx, newCP); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET status='RUNNING', latest_checkpoint_id=?, updated_at=?, version=version+1 WHERE id=?`,
			string(newCP.ID), storage.Now(), string(run))
		return err
	})
	if err != nil {
		return err
	}
	e.emit(ctx, "workflow.request_answered", info, map[string]any{"request": resp.RequestID, "decision": decision})
	e.emit(ctx, "approval."+map[string]string{"APPROVED": "approved", "REJECTED": "rejected"}[status], info, map[string]any{"request": resp.RequestID})
	e.emit(ctx, "workflow.checkpoint_written", info, map[string]any{"checkpoint": newCP.ID, "kind": newCP.Kind, "step": newCP.Step})
	e.emit(ctx, "workflow.resumed", info, map[string]any{})
	release = false // ownership passes to the driver, which releases the lease when it stops
	info.Status = StatusRunning
	e.launch(info, g, newCP)
	return nil
}

// awaitDriver waits until this engine's driver of run, if it has one, has ended. A run parked at a request only has its
// driver winding down (its last status, event and lease release go through the database, which a busy machine makes slow),
// so that wait is generous; any other run's driver may be busy for a long time, so that wait is a moment. ErrLeaseHeld when
// the driver did not end in time.
func (e *Engine) awaitDriver(ctx context.Context, run RunID, status RunStatus) error {
	e.mu.Lock()
	d, local := e.active[run]
	e.mu.Unlock()
	if !local {
		return nil
	}
	bound := 2 * time.Second
	if status == StatusWaitingHuman {
		bound = 30 * time.Second
	}
	t := time.NewTimer(bound)
	defer t.Stop()
	select {
	case <-d.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return ErrLeaseHeld
	}
}

// ResumeFrom starts a new run (a lineage branch) from any earlier checkpoint. History is never modified.
func (e *Engine) ResumeFrom(ctx context.Context, cpID CheckpointID) (RunID, error) {
	src, err := e.st.getCheckpoint(ctx, cpID)
	if err != nil {
		return "", err
	}
	g, err := e.graph(src.Workflow)
	if err != nil {
		return "", err
	}
	if src.State.Topology != g.TopologyHash() {
		return "", ErrTopologyMismatch
	}
	srcRun, err := e.st.getRun(ctx, src.RunID)
	if err != nil {
		return "", err
	}
	id := RunID(ids.New("run"))
	st := src.State.clone()
	st.Status = StatusRunning
	st.Result = nil
	st.FailedReason = ""
	cp := Checkpoint{ID: CheckpointID(ids.New("cp")), RunID: id, Workflow: g.Name, Step: st.Step, Kind: CheckpointEntry, Parent: src.ID, State: st, Created: storage.Now()}
	info := RunInfo{ID: id, Workflow: g.Name, Status: StatusRunning, ProjectID: srcRun.ProjectID, TaskID: srcRun.TaskID, Input: st.Input, Topology: st.Topology, ParentCP: src.ID}
	if err := e.st.createRun(ctx, info, cp); err != nil {
		return "", err
	}
	// pending requests in the copied state need approval rows in the new run
	if len(st.Pending) > 0 {
		var reqs []approvalInsert
		for _, pr := range st.Pending {
			reqs = append(reqs, approvalInsert{id: RequestID(ids.New("req")), nodeID: pr.NodeID, spec: pr.Spec, projectID: info.ProjectID, taskID: info.TaskID})
		}
		// re-key pending requests to the new approval ids
		newState := st
		newState.Pending = map[RequestID]PendingRequest{}
		newState.Waiting = map[NodeID]RequestID{}
		for _, r := range reqs {
			newState.Pending[r.id] = PendingRequest{ID: r.id, NodeID: r.nodeID, Spec: r.spec}
			newState.Waiting[r.nodeID] = r.id
		}
		cp2 := Checkpoint{ID: CheckpointID(ids.New("cp")), RunID: id, Workflow: g.Name, Step: st.Step, Kind: CheckpointEntry, Parent: cp.ID, State: newState, Created: storage.Now()}
		if err := e.st.commit(ctx, stepCommit{cp: cp2, status: StatusRunning, newRequests: reqs}); err != nil {
			return "", err
		}
		cp = cp2
	}
	ok, err := e.st.acquireLease(ctx, id, e.cfg.Owner, e.cfg.LeaseTTL)
	if err != nil || !ok {
		return id, ErrLeaseHeld
	}
	e.emit(ctx, "workflow.started", info, map[string]any{"workflow": g.Name, "branchedFrom": src.ID})
	e.launch(info, g, cp)
	return id, nil
}

// Replay starts a new run from the entry checkpoint of run's lineage using the recorded input.
func (e *Engine) Replay(ctx context.Context, run RunID) (RunID, error) {
	lin, err := e.st.lineage(ctx, run)
	if err != nil {
		return "", err
	}
	info, err := e.st.getRun(ctx, run)
	if err != nil {
		return "", err
	}
	var v any
	_ = json.Unmarshal(lin[0].State.Input, &v)
	return e.Start(ctx, lin[0].Workflow, v, StartOptions{ProjectID: info.ProjectID, TaskID: info.TaskID})
}

// Cancel stops a run.
func (e *Engine) Cancel(ctx context.Context, run RunID) error {
	info, err := e.st.getRun(ctx, run)
	if err != nil {
		return err
	}
	if info.Status.Terminal() {
		return ErrRunTerminal
	}
	e.mu.Lock()
	d, active := e.active[run]
	e.mu.Unlock()
	if active {
		d.cancel()
		// wait briefly for the driver to stop so its lease is released before we mark the run
		select {
		case <-d.done:
		case <-time.After(5 * time.Second):
		}
	}
	err = e.st.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET status='CANCELLED', updated_at=?, version=version+1 WHERE id=?`, storage.Now(), string(run)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE approvals SET status='CANCELLED', resolved_at=? WHERE run_id=? AND status='REQUESTED'`, storage.Now(), string(run))
		return err
	})
	if err != nil {
		return err
	}
	e.emit(ctx, "workflow.cancelled", info, map[string]any{})
	return nil
}

// Recover resumes runs whose driver is gone (expired lease). Call once at startup. It returns how many runs it took over.
func (e *Engine) Recover(ctx context.Context) (int, error) { return e.recover(ctx, true) }

// WatchRecover recovers, every interval (a third of the lease TTL when 0) until ctx ends, the runs whose owner's lease has
// expired. Recover at start cannot take a run whose dead owner's lease is still valid (the app was killed and started again
// within the lease TTL); without this loop such a run would stay RUNNING, with nothing driving it, until the next start.
// Runs this engine drives and runs that have no lease row (one being started right now) are never taken.
func (e *Engine) WatchRecover(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = e.cfg.LeaseTTL / 3
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = e.recover(ctx, false)
		}
	}
}

// recover launches the RUNNING runs nobody drives: with unleased, also those without a lease row (only at start, when no
// run of this process can be between creating its row and taking its lease).
func (e *Engine) recover(ctx context.Context, unleased bool) (int, error) {
	cond := `l.run_id IS NOT NULL AND l.expires_at < ?`
	if unleased {
		cond = `(l.run_id IS NULL OR l.expires_at < ?)`
	}
	rows, err := e.st.db.Reader().QueryContext(ctx, `SELECT r.id FROM workflow_runs r
		LEFT JOIN run_leases l ON l.run_id = r.id
		WHERE r.status IN ('RUNNING','RECOVERING') AND `+cond, storage.Now())
	if err != nil {
		return 0, err
	}
	var runs []RunID
	for rows.Next() {
		var id RunID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		runs = append(runs, id)
	}
	rows.Close()
	n := 0
	for _, id := range runs {
		e.mu.Lock()
		_, local := e.active[id]
		e.mu.Unlock()
		if local {
			continue // this engine drives it already (its lease renewal is late, not lost)
		}
		ok, err := e.st.acquireLease(ctx, id, e.cfg.Owner, e.cfg.LeaseTTL)
		if err != nil || !ok {
			continue
		}
		info, err := e.st.getRun(ctx, id)
		if err != nil {
			e.st.releaseLease(ctx, id, e.cfg.Owner)
			continue
		}
		g, err := e.graph(info.Workflow)
		if err != nil {
			e.st.releaseLease(ctx, id, e.cfg.Owner)
			continue
		}
		cp, err := e.st.getCheckpoint(ctx, info.LatestCP)
		if err != nil || cp.State.Topology != g.TopologyHash() {
			e.st.releaseLease(ctx, id, e.cfg.Owner)
			_ = e.st.setStatus(ctx, id, StatusFailed, "recover: topology mismatch or missing checkpoint")
			continue
		}
		_ = e.st.setStatus(ctx, id, StatusRecovering, "")
		e.emit(ctx, "workflow.recovering", info, map[string]any{"fromCheckpoint": cp.ID})
		e.launch(info, g, cp)
		n++
	}
	return n, nil
}

// Status returns the run row.
func (e *Engine) Status(ctx context.Context, run RunID) (RunInfo, error) {
	return e.st.getRun(ctx, run)
}

// Checkpoints returns the lineage (root to latest) of a run.
func (e *Engine) Checkpoints(ctx context.Context, run RunID) ([]Checkpoint, error) {
	return e.st.lineage(ctx, run)
}

// RunCheckpoints returns only the checkpoints stored under the run itself, oldest first.
func (e *Engine) RunCheckpoints(ctx context.Context, run RunID) ([]Checkpoint, error) {
	return e.st.runCheckpoints(ctx, run)
}

// Executions returns node execution rows for a run.
func (e *Engine) Executions(ctx context.Context, run RunID) ([]ExecRow, error) {
	return e.st.allExecs(ctx, run)
}

// Approvals lists requests, optionally filtered by status ("REQUESTED" for the Needs-you inbox).
func (e *Engine) Approvals(ctx context.Context, status string) ([]ApprovalRow, error) {
	return e.st.listApprovals(ctx, status)
}

// Pending returns the pending requests of a run (from its latest checkpoint).
func (e *Engine) Pending(ctx context.Context, run RunID) ([]PendingRequest, error) {
	cp, err := e.st.latest(ctx, run)
	if err != nil {
		return nil, err
	}
	var out []PendingRequest
	for _, p := range cp.State.Pending {
		out = append(out, p)
	}
	return out, nil
}

// MarkConsumed moves an APPROVED request to CONSUMED once the action it authorized has run.
func (e *Engine) MarkConsumed(ctx context.Context, id RequestID) error {
	return e.st.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE approvals SET status='CONSUMED', version=version+1 WHERE id=? AND status='APPROVED'`, string(id))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrRequestConsumed
		}
		return nil
	})
}

// Await polls until done(run) is true or ctx ends.
func (e *Engine) Await(ctx context.Context, run RunID, done func(RunInfo) bool) (RunInfo, error) {
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		info, err := e.st.getRun(ctx, run)
		if err != nil {
			return info, err
		}
		if done(info) {
			return info, nil
		}
		select {
		case <-ctx.Done():
			return info, ctx.Err()
		case <-t.C:
		}
	}
}
