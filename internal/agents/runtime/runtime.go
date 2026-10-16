// Package runtime is the Agent Runtime: it owns agent sessions (start, send, interrupt, stop), records agent runs,
// publishes agent.* events, records token usage, and turns a session into a workflow node handler.
package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/redact"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Runtime supervises agent sessions.
type Runtime struct {
	PM       proc.Manager
	Bus      events.Bus
	DB       *storage.DB
	Adapters map[string]agents.Adapter
	// NudgeWait is how long a turn waits for a watchdog nudge after an interrupt (default 15s).
	NudgeWait time.Duration
	// MCP, if set, is called by Spawn before the agent process starts to give the session its MCP gateway endpoint (docs/mcp.md).
	// It may set cfg.Spec.MCPConfigPath and extend cfg.Spec.AllowedTools. Every session goes through Spawn (workflow turns,
	// room replies, the planner), so this one hook covers all of them. A hook error does not stop the turn: the session runs
	// without MCP and the reason is published as agent.mcp. Nil means no MCP (tests of other packages).
	MCP func(ctx context.Context, cfg *Config, runID string) (MCPAttachment, error)
	// Instructions, if set, returns the standing instructions the human wrote for an agent. Spawn adds them to the session's
	// system prompt (cfg.Spec.SystemAppend), so every path that starts an agent (workflow turns, room replies, templates)
	// carries the agent's purpose without each one having to remember it.
	Instructions func(ctx context.Context, agentID, role string) (text string, practicesVersion int)
	// RunMode, if set, says where an agent's CLI runs (native, or wsl with the distro; decision P41) for a session whose spec
	// does not say: every path that starts an agent gets the agent's own setting without each one reading it. why is set when
	// Drydock chose the place itself (decision P58: the CLI is not installed on Windows and the agent follows its project).
	RunMode func(ctx context.Context, agentID string) (mode, distro, why string)
	// Redact, if set, removes secrets from the text an agent streams before it is published as agent.output (the scanner's
	// Redact). Streamed text arrives a few characters at a time, so it is held to the end of each word: a key the model prints
	// is whole in one event, never split across two that are each clean (redact.Stream). Other event data is redacted by the
	// event bus (events.SQLBus.SetRedactor). Nil: text is published as it arrives.
	Redact func(string) string

	wmu     sync.Mutex
	running map[*Managed]struct{} // sessions whose end is not recorded yet (Wait)
}

// MCPAttachment is what the MCP hook reports for one session. It never carries the run's bearer token.
type MCPAttachment struct {
	// Attached is true when cfg.Spec.MCPConfigPath was set.
	Attached bool
	// Reason says why MCP was not attached (empty when it was).
	Reason string
	// Unverified is true when the human attached the gateway to a CLI whose MCP mechanism no real run has proven yet.
	Unverified bool
	// Transport is the transport of the single gateway entry ("stdio-shim" or "http").
	Transport string
	// Tools are the exposed tool names the run may call.
	Tools []string
	// Release ends the gateway run and removes the config file. Spawn calls it exactly once, when the session ends (Stop,
	// process exit, crash) or when the process could not be started.
	Release func()
}

// Config describes one agent session to start.
type Config struct {
	AgentID   string
	ProjectID string
	TaskID    string
	Adapter   string
	Spec      agents.RunSpec
	Provider  string // model provider label for usage rows ("anthropic", "openrouter", a host id)
	Tier      string // premium | cheap | free
}

// Managed is a running agent session with bookkeeping.
type Managed struct {
	rt      *Runtime
	cfg     Config
	RunID   string
	Sess    agents.Session
	mu      sync.Mutex
	delta   strings.Builder
	deltaK  string
	timer   *time.Timer
	stream  *redact.Stream // text not yet at the end of a word, when the runtime redacts
	usage   []workflow.UsageChunk
	stopped atomic.Bool

	releaseMCP func()
	mcpOnce    sync.Once
	recorded   chan struct{} // closed when watchExit has recorded the end (the run row and agent.stopped)
}

// releaseMCPRun ends the session's gateway run (idempotent).
func (m *Managed) releaseMCPRun() {
	m.mcpOnce.Do(func() {
		if m.releaseMCP != nil {
			m.releaseMCP()
		}
	})
}

// Spawn starts the agent process and records the run.
func (r *Runtime) Spawn(ctx context.Context, cfg Config) (*Managed, error) {
	ad, ok := r.Adapters[cfg.Adapter]
	if !ok {
		return nil, fmt.Errorf("runtime: unknown adapter %q", cfg.Adapter)
	}
	if cfg.Spec.RunMode == "" && r.RunMode != nil && cfg.AgentID != "" {
		cfg.Spec.RunMode, cfg.Spec.WSLDistro, cfg.Spec.RunModeWhy = r.RunMode(ctx, cfg.AgentID)
	}
	if err := agents.CheckRunMode(ad, cfg.Spec); err != nil {
		r.publish(ctx, "agent.failed", cfg, "", map[string]any{"error": err.Error(), "phase": "start"})
		return nil, err
	}
	runID := ids.New("arun")
	if cfg.Spec.RunModeWhy != "" {
		// Drydock chose where the CLI runs (decision P58): the run's events say so before anything starts
		r.publish(ctx, "agent.run_mode_auto", cfg, runID, map[string]any{"runMode": cfg.Spec.RunMode, "wslDistro": cfg.Spec.WSLDistro, "why": cfg.Spec.RunModeWhy})
	}
	if r.Instructions != nil && cfg.AgentID != "" {
		if s, pv := r.Instructions(ctx, cfg.AgentID, cfg.Spec.Role); strings.TrimSpace(s) != "" {
			cfg.Spec.SystemAppend = strings.TrimSpace(cfg.Spec.SystemAppend + "\n\n" + strings.TrimSpace(s))
			cfg.Spec.PracticesVersion = pv
		}
	}
	var att MCPAttachment
	if r.MCP != nil && cfg.Spec.MCPConfigPath == "" {
		var merr error
		if att, merr = r.MCP(ctx, &cfg, runID); merr != nil {
			att = MCPAttachment{Reason: "attaching the gateway failed: " + merr.Error()}
			cfg.Spec.MCPConfigPath = ""
		}
		info := map[string]any{"attached": att.Attached, "reason": att.Reason, "transport": att.Transport, "tools": att.Tools, "unverified": att.Unverified}
		if att.Tools == nil {
			info["tools"] = []string{}
		}
		r.publish(ctx, "agent.mcp", cfg, runID, info)
	}
	sess, err := ad.Start(ctx, r.PM, cfg.Spec)
	if err != nil {
		if att.Release != nil {
			att.Release()
		}
		r.publish(ctx, "agent.failed", cfg, "", map[string]any{"error": err.Error(), "phase": "start"})
		return nil, err
	}
	m := &Managed{rt: r, cfg: cfg, RunID: runID, Sess: sess, releaseMCP: att.Release, recorded: make(chan struct{})}
	if r.DB != nil {
		_ = r.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO agent_runs(id,agent_id,task_id,session_id,pid,cwd,status,started_at) VALUES(?,?,?,?,?,?,?,?)`,
				m.RunID, cfg.AgentID, cfg.TaskID, "", sess.PID(), cfg.Spec.Cwd, "WORKING", storage.Now())
			return err
		})
	}
	started := map[string]any{"pid": sess.PID(), "cwd": cfg.Spec.Cwd, "adapter": cfg.Adapter, "model": cfg.Spec.Model, "effort": cfg.Spec.Effort, "instructions": cfg.Spec.SystemAppend != "", "practices": cfg.Spec.PracticesVersion}
	if cfg.Spec.IsWSL() {
		started["runMode"], started["wslDistro"] = cfg.Spec.RunMode, cfg.Spec.WSLDistro
		if cfg.Spec.RunModeWhy != "" {
			started["runModeWhy"] = cfg.Spec.RunModeWhy
		}
	}
	r.publish(ctx, "agent.started", cfg, m.RunID, started)
	r.wmu.Lock()
	if r.running == nil {
		r.running = map[*Managed]struct{}{}
	}
	r.running[m] = struct{}{}
	r.wmu.Unlock()
	safe.Go("runtime.Runtime.Spawn", func() { m.watchExit() })
	return m, nil
}

// Wait blocks until every session started so far has ended and its end is recorded (the agent_runs row and agent.stopped),
// or ctx ends. It stops nothing: stop the sessions first. Close the database only after it returns: the exit watcher writes
// after the process is gone, so closing earlier can lose that write, and on Windows the open file keeps the data folder from
// being removed.
func (r *Runtime) Wait(ctx context.Context) error {
	r.wmu.Lock()
	pending := make([]chan struct{}, 0, len(r.running))
	for m := range r.running {
		pending = append(pending, m.recorded)
	}
	r.wmu.Unlock()
	for _, ch := range pending {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (r *Runtime) publish(ctx context.Context, typ string, cfg Config, runID string, data map[string]any) {
	if r.Bus == nil {
		return
	}
	if runID != "" {
		data["agentRun"] = runID
	}
	b, _ := json.Marshal(data)
	_, _ = r.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: cfg.ProjectID, TaskID: cfg.TaskID, AgentID: cfg.AgentID, Data: b})
}

// CloseStale ends the agent runs a crash left open (ended_at 0) when the app starts, before anything is spawned: they are
// marked FAILED with the reason and announced (agent.failed), so the agent's presence and the "is running right now" check
// do not stay stuck. It returns how many runs it closed.
func (r *Runtime) CloseStale(ctx context.Context) (int, error) {
	if r.DB == nil {
		return 0, nil
	}
	type stale struct{ id, agent, task, project string }
	var runs []stale
	rows, err := r.DB.Reader().QueryContext(ctx, `SELECT ar.id, ar.agent_id, ar.task_id, COALESCE(a.project_id,'') FROM agent_runs ar LEFT JOIN agents a ON a.id=ar.agent_id WHERE ar.ended_at=0`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var s stale
		if err := rows.Scan(&s.id, &s.agent, &s.task, &s.project); err != nil {
			rows.Close()
			return 0, err
		}
		runs = append(runs, s)
	}
	rows.Close()
	const reason = "the app stopped while this agent was running"
	for _, s := range runs {
		if err := r.DB.WriteTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE agent_runs SET status='FAILED', ended_at=?, error=? WHERE id=? AND ended_at=0`, storage.Now(), reason, s.id)
			return err
		}); err != nil {
			return 0, err
		}
		r.publish(ctx, "agent.failed", Config{AgentID: s.agent, ProjectID: s.project, TaskID: s.task}, s.id, map[string]any{"error": reason, "phase": "app_stopped"})
	}
	return len(runs), nil
}

func (m *Managed) watchExit() {
	defer func() { // last: the end is written and announced
		m.rt.wmu.Lock()
		delete(m.rt.running, m)
		m.rt.wmu.Unlock()
		close(m.recorded)
	}()
	ei := <-m.Sess.Wait()
	defer m.releaseMCPRun() // the process is gone (clean exit, crash or kill): its gateway run and token die with it
	status := "STOPPED"
	var errMsg string
	if !m.stopped.Load() && (ei.Err != nil || ei.Code != 0) {
		status = "FAILED"
		errMsg = strings.TrimSpace(m.Sess.Stderr())
		if errMsg == "" && ei.Err != nil {
			errMsg = ei.Err.Error()
		}
		if m.rt.Redact != nil { // stderr is whatever the CLI printed on its way out: a stack trace, an environment dump
			errMsg = redact.Lines(errMsg, m.rt.Redact)
		}
		m.rt.publish(context.Background(), "agent.failed", m.cfg, m.RunID, map[string]any{"exitCode": ei.Code, "error": errMsg, "phase": "run"})
	}
	if m.rt.DB != nil {
		if err := m.rt.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(context.Background(), `UPDATE agent_runs SET status=?, ended_at=?, exit_code=?, error=? WHERE id=?`, status, storage.Now(), ei.Code, errMsg, m.RunID)
			return err
		}); err != nil {
			m.rt.publish(context.Background(), "agent.failed", m.cfg, m.RunID, map[string]any{"error": "record run end: " + err.Error(), "phase": "bookkeeping"})
		}
	}
	m.rt.publish(context.Background(), "agent.stopped", m.cfg, m.RunID, map[string]any{"exitCode": ei.Code, "killed": ei.Killed})
}

// Stop ends the session and kills the process tree.
func (m *Managed) Stop(ctx context.Context) error {
	m.stopped.Store(true)
	m.flushDelta()
	err := m.Sess.Stop(ctx)
	m.releaseMCPRun()
	return err
}

// observe records a chunk: events (text deltas coalesced), usage rows, session id.
func (m *Managed) observe(ctx context.Context, c workflow.Chunk) {
	switch c.Kind {
	case workflow.ChunkText, workflow.ChunkThinking:
		var d workflow.TextDelta
		if c.Decode(&d) != nil {
			return
		}
		m.mu.Lock()
		if m.deltaK != "" && m.deltaK != c.Kind {
			m.flushLocked()
		}
		m.deltaK = c.Kind
		if m.rt.Redact != nil {
			if m.stream == nil {
				m.stream = redact.NewStream(m.rt.Redact)
			}
			m.delta.WriteString(m.stream.Write(d.Text))
		} else {
			m.delta.WriteString(d.Text)
		}
		if m.timer == nil {
			m.timer = time.AfterFunc(50*time.Millisecond, m.flushWhole)
		}
		m.mu.Unlock()
		return
	}
	m.flushDelta()
	switch c.Kind {
	case workflow.ChunkUsage:
		var u workflow.UsageChunk
		if c.Decode(&u) == nil {
			m.usage = append(m.usage, u)
			if err := m.recordUsage(ctx, u); err != nil {
				// the turn went on and its cost is in the result, but the budgets read this table: say so instead of letting the
				// spend go uncounted without a trace (audit 2026-10-16, M4)
				m.rt.publish(ctx, "agent.usage_unrecorded", m.cfg, m.RunID, map[string]any{"error": err.Error(), "model": u.Model, "costUsd": u.CostUSD,
					"inputTokens": u.InputTokens, "outputTokens": u.OutputTokens})
			}
		}
	case workflow.ChunkSession:
		var s workflow.SessionChunk
		if c.Decode(&s) == nil && m.rt.DB != nil {
			_ = m.rt.DB.WriteTx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE agent_runs SET session_id=? WHERE id=?`, s.SessionID, m.RunID)
				return err
			})
		}
	}
	m.rt.publish(ctx, "agent.output", m.cfg, m.RunID, map[string]any{"kind": c.Kind, "data": json.RawMessage(c.Data)})
}

// flushDelta ends the text in progress: whatever is held, including an unfinished word, goes out.
func (m *Managed) flushDelta() {
	m.mu.Lock()
	m.flushLocked()
	m.mu.Unlock()
}

// flushWhole is the 50 ms timer: it publishes the complete words so far and leaves an unfinished word where it is, until it
// is finished by the text that follows or by flushDelta.
func (m *Managed) flushWhole() {
	m.mu.Lock()
	m.publishDeltaLocked()
	m.mu.Unlock()
}

func (m *Managed) flushLocked() {
	if m.stream != nil && m.deltaK != "" {
		m.delta.WriteString(m.stream.Flush())
	}
	m.publishDeltaLocked()
}

func (m *Managed) publishDeltaLocked() {
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	if m.delta.Len() == 0 {
		return
	}
	text, kind := m.delta.String(), m.deltaK
	m.delta.Reset()
	if m.stream == nil || m.stream.Held() == 0 {
		m.deltaK = ""
	}
	data, _ := json.Marshal(workflow.TextDelta{Text: text})
	m.rt.publish(context.Background(), "agent.output", m.cfg, m.RunID, map[string]any{"kind": kind, "data": json.RawMessage(data), "coalesced": true})
}

func (m *Managed) recordUsage(ctx context.Context, u workflow.UsageChunk) error {
	if m.rt.DB == nil {
		return nil
	}
	model := u.Model
	if model == "" {
		model = m.cfg.Spec.Model
	}
	// the turn may have been cancelled already (the usage often arrives with its end): the money was spent all the same
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return m.rt.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO model_usage(ts,agent_run_id,task_id,agent_id,provider,model,tier,input_tokens,output_tokens,cached_tokens,cache_write_tokens,reasoning_tokens,cost_usd,source)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, storage.Now(), m.RunID, m.cfg.TaskID, m.cfg.AgentID, m.cfg.Provider, model, m.cfg.Tier,
			u.InputTokens, u.OutputTokens, u.CachedTokens, u.CacheWriteTokens, u.ReasoningTokens, u.CostUSD, nz(u.Source))
		return err
	})
}

func nz(s string) string {
	if s == "" {
		return "reported"
	}
	return s
}

// TurnResult is what a completed turn produced.
type TurnResult struct {
	Text       string                `json:"text"`
	SessionID  string                `json:"sessionId"`
	StopReason string                `json:"stopReason"`
	Usage      []workflow.UsageChunk `json:"usage,omitempty"`
	ToolCalls  int                   `json:"toolCalls"`
	// Started is false for a turn that ended without its session ever starting: no session announcement, no text, no tool
	// call (Claude Code answers a --resume of a session it does not have with an error result and nothing else).
	Started bool `json:"started"`
}

// Controller implements workflow.TurnController for one managed session (the watchdog steers through it).
type Controller struct {
	m      *Managed
	await  atomic.Bool
	nudges chan string
}

func newController(m *Managed) *Controller { return &Controller{m: m, nudges: make(chan string, 4)} }

// CancelTurn interrupts the current turn; the turn loop then waits for a nudge (or ctx cancel) before finishing.
func (c *Controller) CancelTurn() error {
	c.await.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return c.m.Sess.Interrupt(ctx)
}

// Send queues a message to be delivered as the next turn after an interrupt.
func (c *Controller) Send(ctx context.Context, msg string) error {
	select {
	case c.nudges <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ErrNotStarted marks a turn that failed before its session started: no session announcement, no text and no tool call came
// from the agent. A session that could not be resumed fails this way, so the caller can start a fresh one instead.
var ErrNotStarted = errors.New("the agent session did not start")

// startedKind reports whether a chunk of this kind shows that the session started (as opposed to an error report or usage).
func startedKind(kind string) bool {
	switch kind {
	case workflow.ChunkSession, workflow.ChunkText, workflow.ChunkThinking, workflow.ChunkToolCall, workflow.ChunkMessageEnd:
		return true
	}
	return false
}

// RunTurn sends prompt and consumes the output until the turn ends. emit receives every chunk (may be nil). An error before
// the session started wraps ErrNotStarted.
func (m *Managed) RunTurn(ctx context.Context, prompt string, ctl *Controller, emit func(workflow.Chunk)) (TurnResult, error) {
	var res TurnResult
	started := false
	notStarted := func(err error) error {
		if started {
			return err
		}
		return fmt.Errorf("%w: %w", ErrNotStarted, err)
	}
	wait := m.rt.NudgeWait
	if wait <= 0 {
		wait = 15 * time.Second
	}
	m.rt.publish(ctx, "agent.waiting", m.cfg, m.RunID, map[string]any{"state": "working"})
	if err := m.Sess.Send(ctx, prompt); err != nil {
		return res, notStarted(fmt.Errorf("send: %w", err))
	}
	for {
		select {
		case <-ctx.Done():
			_ = m.Stop(context.Background())
			if cause := context.Cause(ctx); cause != nil {
				return res, cause
			}
			return res, ctx.Err()
		case c, ok := <-m.Sess.Output():
			if !ok {
				m.flushDelta()
				return res, notStarted(fmt.Errorf("agent output ended before the turn finished: %s", strings.TrimSpace(m.Sess.Stderr())))
			}
			started = started || startedKind(c.Kind)
			m.observe(ctx, c)
			if emit != nil {
				emit(c)
			}
			switch c.Kind {
			case workflow.ChunkMessageEnd:
				var d workflow.TextDelta
				if c.Decode(&d) == nil {
					res.Text = d.Text
				}
			case workflow.ChunkToolCall:
				res.ToolCalls++
			case workflow.ChunkError:
				var e workflow.ErrorChunk
				if c.Decode(&e) == nil && e.Fatal {
					return res, notStarted(errors.New(e.Message))
				}
			case workflow.ChunkDone:
				var d workflow.DoneChunk
				_ = c.Decode(&d)
				if ctl != nil && ctl.await.Load() {
					// the watchdog interrupted this turn; it will hand us a nudge to continue the same session
					select {
					case n := <-ctl.nudges:
						ctl.await.Store(false)
						if err := m.Sess.Send(ctx, n); err != nil {
							return res, fmt.Errorf("send nudge: %w", err)
						}
						res.Text = ""
						continue
					case <-ctx.Done():
						continue
					case <-time.After(wait):
						ctl.await.Store(false)
					}
				}
				m.flushDelta()
				res.StopReason = d.StopReason
				res.SessionID = m.Sess.ID()
				res.Usage = m.usage
				res.Started = started
				m.rt.publish(ctx, "agent.completed", m.cfg, m.RunID, map[string]any{"stopReason": d.StopReason, "toolCalls": res.ToolCalls})
				return res, nil
			}
		}
	}
}

// TaskSpec configures an agent_task node.
type TaskSpec struct {
	Config Config
	// Prompt builds the turn prompt from the node input. Default: the first inbox message's text.
	Prompt func(in workflow.NodeInput) string
	// Keep leaves the session running after the node (used by chat); default is a fresh session per attempt, stopped after.
	Keep bool
}

// AgentTask returns a workflow node handler that runs one turn on a fresh session (brief section 5: fresh session per
// independent task attempt). The handler registers a TurnController so middleware (the watchdog) can steer the turn.
func (r *Runtime) AgentTask(spec TaskSpec) workflow.NodeHandler {
	return func(ctx context.Context, in workflow.NodeInput) (workflow.NodeOutput, error) {
		cfg := spec.Config
		cfg.TaskID = in.TaskID
		cfg.ProjectID = in.ProjectID
		m, err := r.Spawn(ctx, cfg)
		if err != nil {
			if cfg.Spec.ResumeID != "" {
				// an adapter that resumes at start (ACP session/load) refuses a session it does not have here
				err = fmt.Errorf("%w: resume session %s: %w", ErrNotStarted, cfg.Spec.ResumeID, err)
			}
			return workflow.NodeOutput{}, err
		}
		if !spec.Keep {
			defer func() { _ = m.Stop(context.Background()) }()
		}
		ctl := newController(m)
		if in.Turn != nil {
			in.Turn.Set(ctl)
		}
		prompt := defaultPrompt(in)
		if spec.Prompt != nil {
			prompt = spec.Prompt(in)
		}
		res, err := m.RunTurn(ctx, prompt, ctl, in.Emit)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		b, _ := json.Marshal(res)
		return workflow.NodeOutput{Value: b}, nil
	}
}

func defaultPrompt(in workflow.NodeInput) string {
	var parts []string
	for _, m := range in.Inbox {
		var s string
		if json.Unmarshal(m.Value, &s) == nil {
			parts = append(parts, s)
			continue
		}
		var o map[string]any
		if json.Unmarshal(m.Value, &o) == nil {
			if w, ok := o["watchdog"].(string); ok {
				parts = append(parts, w)
				continue
			}
			if t, ok := o["text"].(string); ok {
				parts = append(parts, t)
				continue
			}
		}
		parts = append(parts, string(m.Value))
	}
	return strings.Join(parts, "\n\n")
}
