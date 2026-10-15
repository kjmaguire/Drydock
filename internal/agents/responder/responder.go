// Package responder is how agents take part in the Engineering Room: when a human posts, the room's turn-taking rules
// (room.PlanTurns) decide which agents may answer, and each answer is a real agent turn through the Agent Runtime, in a
// read-only permission mode, over a bounded context package selected from the room (room.ContextFor), posted back as a
// message authored by the agent. docs/room-protocol.md s6 and s11 are the contract.
//
// Rules this package enforces in code:
//   - only a human message starts a conversation; tool, system and agent messages never wake an agent on their own;
//   - no loop: the room's debate bound (rounds, passes) ends a chain, a hard cap bounds it again, and a turn happens at
//     most once per (triggering message, agent) even if an event is delivered twice;
//   - agent text is untrusted data: it is stored as an untrusted CHAT, redacted by the room's secret scan on the way in,
//     and has no path to an approval (there is no call from here into the workflow engine or the approvals service);
//   - an agent sees only the messages the Context Engine selected, listed in a stored manifest, never the room's history.
package responder

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
	rt "github.com/kjmaguire/drydock/internal/agents/runtime"
	ctxeng "github.com/kjmaguire/drydock/internal/context"
	"github.com/kjmaguire/drydock/internal/events"
	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/room"
	"github.com/kjmaguire/drydock/internal/storage"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Agent is the part of a team member the responder needs.
type Agent struct {
	ID, ProjectID, Name, Adapter, Role, Model, Effort, ProviderID string
}

// Deps are the services the responder uses. Everything that needs the application (agent rows, project paths, the
// concurrency gate) is a function, so this package never imports the core.
type Deps struct {
	Room    *room.Service
	Runtime *rt.Runtime
	Context *ctxeng.Engine // optional: stores each turn's context manifest
	DB      *storage.DB
	Bus     events.Bus

	LookupAgent func(ctx context.Context, id string) (Agent, error)
	// Cwd is the directory the agent reads: the task worktree in a task room, otherwise the project root.
	Cwd func(ctx context.Context, r room.Room) (string, error)
	// Gate takes the concurrency slots a turn needs (the project's parallel-agent limit and the provider's max_concurrent)
	// and returns the release function. It blocks until a slot is free or ctx ends. Nil means no limit.
	Gate func(ctx context.Context, projectID string, a Agent) (release func(), err error)
	// Budget refuses a turn while a budget that applies to it (the project's, the agent's, the task's in a task room) is at its
	// hard limit, with the sentence to show; nil means no budgets. A refused turn does not start and the refusal is posted.
	Budget func(ctx context.Context, projectID, taskID string, a Agent) error
	// Enabled reports whether agents answer in this project (default true).
	Enabled func(ctx context.Context, projectID string) bool
	// OnCost receives the cost of a turn in a task room so it counts toward the task (cost per accepted task).
	OnCost func(ctx context.Context, taskID string, usd float64)
	// Brief returns the project's brief (the human's description of the project), shown to the agent before the conversation.
	// Optional.
	Brief func(ctx context.Context, projectID string) string
	// Spec lets the application adjust the run spec (MCP configuration, test environment). Optional.
	Spec func(a Agent, base agents.RunSpec) agents.RunSpec
	// Route chooses the model of a reply turn and runs it (decision P63): the application asks the model router, which
	// chooses among the agent's own models only (never another CLI or login), records the decision, and calls attempt once,
	// or again on the next model of a fallback chain when the model was unavailable before the turn did any work. It returns
	// the error of the last attempt. Nil: one attempt on the agent's configured model.
	Route func(ctx context.Context, t ReplyTurn, attempt Attempt) error
	Logf  func(format string, args ...any)
}

// ReplyTurn is a reply turn as the model router sees it.
type ReplyTurn struct {
	TurnID        string // the room_turns row; the routing decisions of the turn carry it as their run id
	RoomID        string
	RoomKind      string
	ProjectID     string
	TaskID        string // the task of a task channel
	Agent         Agent
	ContextTokens int // the selected room context, estimated
}

// Routed is the model one attempt of a reply turn runs on. An empty Model is the agent's configured model.
type Routed struct {
	Model      string
	Tier       string
	DecisionID string
}

// Attempt runs the reply turn once on r; emit sees the turn's chunks (tool calls, errors, rate limits) as they arrive.
type Attempt func(ctx context.Context, r Routed, emit func(workflow.Chunk)) error

// Options tunes the responder. The zero value is valid.
type Options struct {
	MaxToolCalls  int           // per turn; the turn is cancelled beyond it (default 40)
	TurnTimeout   time.Duration // per turn (default 5 minutes)
	HardTurnCap   int           // agent turns one human message may cause, over the room's own bound (default 12)
	MaxConcurrent int           // agent turns running at once across rooms (default 4)
	MaxReplyBytes int           // longer replies are cut with a marker (default 24 KiB)
	DMRecent      int           // earlier messages added to a DM turn (default 6); other rooms get the thread only
	Context       room.ContextOptions
}

func (o *Options) defaults() {
	if o.MaxToolCalls <= 0 {
		o.MaxToolCalls = 40
	}
	if o.TurnTimeout <= 0 {
		o.TurnTimeout = 5 * time.Minute
	}
	if o.HardTurnCap <= 0 {
		o.HardTurnCap = 12
	}
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = 4
	}
	if o.MaxReplyBytes <= 0 {
		o.MaxReplyBytes = 24 << 10
	}
	if o.DMRecent <= 0 {
		o.DMRecent = 6
	}
}

// Turn statuses.
const (
	StatusRunning     = "running"
	StatusDone        = "done"
	StatusPassed      = "passed"
	StatusEmpty       = "empty"
	StatusFailed      = "failed"
	StatusInterrupted = "interrupted"
)

// TurnInfo describes one agent turn of a chain.
type TurnInfo struct {
	TurnID    string `json:"turnId"`
	AgentID   string `json:"agentId"`
	TriggerID string `json:"triggerId"`
	Reason    string `json:"reason"`
	Round     int    `json:"round"`
	Status    string `json:"status"`
	MessageID string `json:"messageId,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Outcome is what Handle did for one human message.
type Outcome struct {
	Trigger string     `json:"trigger"`
	Turns   []TurnInfo `json:"turns"`
	Bounded bool       `json:"bounded"`           // the room's debate bound ended the conversation
	Stopped string     `json:"stopped,omitempty"` // why the chain ended early (preempted, hard cap, disabled, ...)
}

// Responder runs agent replies. Create one with New, start it with Start, or call Handle directly.
type Responder struct {
	d   Deps
	o   Options
	sem chan struct{}

	mu      sync.Mutex
	rooms   map[string]*roomState
	queues  map[string][]string
	running map[string]bool
	wg      sync.WaitGroup
}

type roomState struct {
	run sync.Mutex   // one chain at a time per room, in arrival order
	gen atomic.Int64 // bumped by every new human message: a running chain stops before its next turn
}

// New returns a Responder.
func New(d Deps, o Options) *Responder {
	o.defaults()
	return &Responder{d: d, o: o, sem: make(chan struct{}, o.MaxConcurrent), rooms: map[string]*roomState{}, queues: map[string][]string{}, running: map[string]bool{}}
}

func (r *Responder) logf(format string, a ...any) {
	if r.d.Logf != nil {
		r.d.Logf(format, a...)
	}
}

func (r *Responder) state(roomID string) *roomState {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.rooms[roomID]
	if s == nil {
		s = &roomState{}
		r.rooms[roomID] = s
	}
	return s
}

// Start subscribes to room.message_posted and answers human messages until ctx ends. Turns left "running" by a crash are
// marked interrupted first (they are never re-run automatically: a re-run is a new, billed turn the human did not ask for).
func (r *Responder) Start(ctx context.Context) (stop func(), err error) {
	if err := r.markInterrupted(ctx); err != nil {
		return nil, err
	}
	ch, cancel := r.d.Bus.Subscribe(events.Filter{Types: []string{"room.message_posted"}})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				var d struct {
					Room    string `json:"room"`
					Message string `json:"message"`
					Author  string `json:"author"`
				}
				if json.Unmarshal(e.Data, &d) != nil || !strings.HasPrefix(d.Author, "human:") {
					continue // agents, tools and the system never start a conversation
				}
				r.enqueue(ctx, d.Room, d.Message)
			}
		}
	}()
	return func() {
		cancel()
		<-done
		r.wg.Wait()
	}, nil
}

// enqueue adds a human message to its room's FIFO and makes sure one worker drains it.
func (r *Responder) enqueue(ctx context.Context, roomID, messageID string) {
	r.state(roomID).gen.Add(1) // a human message preempts a running chain
	r.mu.Lock()
	r.queues[roomID] = append(r.queues[roomID], messageID)
	if r.running[roomID] {
		r.mu.Unlock()
		return
	}
	r.running[roomID] = true
	r.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for {
			r.mu.Lock()
			q := r.queues[roomID]
			if len(q) == 0 {
				r.running[roomID] = false
				r.mu.Unlock()
				return
			}
			id := q[0]
			r.queues[roomID] = q[1:]
			r.mu.Unlock()
			if ctx.Err() != nil {
				continue
			}
			if _, err := r.handle(ctx, id); err != nil && !errors.Is(err, context.Canceled) {
				r.logf("room responder: %v", err)
			}
		}
	}()
}

func (r *Responder) markInterrupted(ctx context.Context) error {
	type cut struct{ room, trigger, agent string }
	var cuts []cut
	rows, err := r.d.DB.Reader().QueryContext(ctx, `SELECT room_id, trigger_id, agent_id FROM room_turns WHERE status=?`, StatusRunning)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c cut
		if err := rows.Scan(&c.room, &c.trigger, &c.agent); err != nil {
			rows.Close()
			return err
		}
		cuts = append(cuts, c)
	}
	rows.Close()
	if err := r.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE room_turns SET status=?, error='the app stopped while this turn was running', ended_at=? WHERE status=?`,
			StatusInterrupted, storage.Now(), StatusRunning)
		return err
	}); err != nil {
		return err
	}
	// the human's message would otherwise just go unanswered: say what happened, under it (no re-run: that is a new, billed turn)
	for _, c := range cuts {
		who := c.agent
		if a, err := r.d.LookupAgent(ctx, c.agent); err == nil && a.Name != "" {
			who = a.Name
		}
		if _, err := r.d.Room.Post(ctx, room.PostParams{RoomID: c.room, Type: room.TypeSystem, Author: room.System, ReplyToID: c.trigger,
			Body:    fmt.Sprintf("%s was answering when Drydock stopped, so there is no reply. Mention them again to ask again.", who),
			Payload: map[string]any{"code": "agent_turn_interrupted", "agent_id": c.agent}}); err != nil {
			r.logf("post interrupted turn: %v", err)
		}
	}
	return nil
}

// Handle answers one human message synchronously and returns what happened. It is what the event loop calls, and what a
// test or a "reply now" action calls directly.
func (r *Responder) Handle(ctx context.Context, messageID string) (Outcome, error) {
	m, err := r.d.Room.GetMessage(ctx, messageID)
	if err != nil {
		return Outcome{}, err
	}
	r.state(m.RoomID).gen.Add(1)
	return r.handle(ctx, messageID)
}

func (r *Responder) handle(ctx context.Context, messageID string) (Outcome, error) {
	trig, err := r.d.Room.GetMessage(ctx, messageID)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Trigger: trig.ID}
	if trig.Author.Kind != room.AuthorHuman {
		out.Stopped = "only a human message starts a conversation"
		return out, nil
	}
	rs := r.state(trig.RoomID)
	rs.run.Lock()
	defer rs.run.Unlock()
	myGen := rs.gen.Load()
	rm, err := r.d.Room.GetRoom(ctx, trig.RoomID)
	if err != nil {
		return out, err
	}
	if rm.Archived {
		out.Stopped = "the room is archived"
		return out, nil
	}
	if r.d.Enabled != nil && !r.d.Enabled(ctx, rm.ProjectID) {
		out.Stopped = "agent replies are switched off for this project"
		return out, nil
	}
	queue := []room.Message{trig}
	turns := 0
	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]
		if rs.gen.Load() != myGen && m.ID != trig.ID {
			out.Stopped = "a newer human message took over the room"
			break
		}
		if ctx.Err() != nil {
			out.Stopped = "cancelled"
			break
		}
		dec, err := r.d.Room.PlanTurns(ctx, m.ID)
		if err != nil {
			return out, err
		}
		if dec.Bounded {
			if _, _, err := r.d.Room.ApplyDebateBound(ctx, rm.ID, m.ThreadRootID); err != nil {
				r.logf("debate bound: %v", err)
			}
			out.Bounded = true
			continue
		}
		speakers := r.speakers(ctx, rm, m, dec)
		for _, sp := range speakers {
			if turns >= r.o.HardTurnCap {
				out.Stopped = fmt.Sprintf("hard cap of %d agent turns reached", r.o.HardTurnCap)
				queue = nil
				break
			}
			if rs.gen.Load() != myGen {
				out.Stopped = "a newer human message took over the room"
				queue = nil
				break
			}
			info, reply := r.turn(ctx, rm, m, sp, dec)
			if info.Status == "" {
				continue // already handled (duplicate delivery)
			}
			turns++
			out.Turns = append(out.Turns, info)
			if reply != nil {
				queue = append(queue, *reply)
			}
		}
	}
	return out, nil
}

// speakers applies the room's decision, with two policies of this package: a DM always has its one agent answer, and in
// an auto room an agent's message wakes only the members it explicitly mentions (the owner and role fallbacks are for
// human messages; letting them chain agent to agent would spend turns nobody asked for).
func (r *Responder) speakers(ctx context.Context, rm room.Room, m room.Message, dec room.TurnDecision) []room.Speaker {
	sp := dec.Speakers
	if m.Author.Kind == room.AuthorAgent && rm.Mode == room.ModeAuto {
		var keep []room.Speaker
		for _, s := range sp {
			if s.Reason == room.ReasonMention {
				keep = append(keep, s)
			}
		}
		return keep
	}
	if len(sp) == 0 && m.Author.Kind == room.AuthorHuman && rm.Kind == room.KindDM {
		members, err := r.d.Room.Members(ctx, rm.ID)
		if err == nil {
			for _, mem := range members {
				if mem.Author.Kind == room.AuthorAgent && (mem.State == room.StateActive || mem.State == "") {
					return []room.Speaker{{Member: mem.Author.Key(), Reason: "dm"}}
				}
			}
		}
	}
	return sp
}

// beginTurn inserts the turn row. false means this (trigger, agent) already had a turn.
func (r *Responder) beginTurn(ctx context.Context, id string, rm room.Room, trig room.Message, agentID, reason string, round int) (bool, error) {
	var ok bool
	err := r.d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO room_turns(id,room_id,project_id,trigger_id,agent_id,round,reason,mode,status,started_at)
			VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(trigger_id, agent_id) DO NOTHING`,
			id, rm.ID, rm.ProjectID, trig.ID, agentID, round, reason, string(rm.Mode), StatusRunning, storage.Now())
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		ok = n == 1
		return nil
	})
	return ok, err
}

type turnEnd struct {
	status, messageID, runID, errMsg      string
	ctxMsgs, ctxTokens, inTokens, outToks int
	cost                                  float64
}

func (r *Responder) endTurn(id string, e turnEnd) {
	_ = r.d.DB.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE room_turns SET status=?, message_id=?, agent_run_id=?, error=?, context_msgs=?, context_tokens=?,
			input_tokens=?, output_tokens=?, cost_usd=?, ended_at=? WHERE id=?`,
			e.status, e.messageID, e.runID, e.errMsg, e.ctxMsgs, e.ctxTokens, e.inTokens, e.outToks, e.cost, storage.Now(), id)
		return err
	})
}

// turn runs one granted turn end to end and returns the posted reply (nil when nothing was posted as a reply).
func (r *Responder) turn(ctx context.Context, rm room.Room, trig room.Message, sp room.Speaker, dec room.TurnDecision) (TurnInfo, *room.Message) {
	info := TurnInfo{TurnID: ids.New("turn"), AgentID: sp.Member, TriggerID: trig.ID, Reason: string(sp.Reason), Round: dec.Round}
	started, err := r.beginTurn(ctx, info.TurnID, rm, trig, sp.Member, string(sp.Reason), dec.Round)
	if err != nil {
		info.Status, info.Error = StatusFailed, err.Error()
		return info, nil
	}
	if !started {
		return TurnInfo{}, nil
	}
	who := sp.Member // the agent's name once it is known; never the raw id when the name can be found
	fail := func(reason string, e error) (TurnInfo, *room.Message) {
		msg := reason
		if e != nil {
			msg += ": " + e.Error()
		}
		info.Status, info.Error = StatusFailed, msg
		r.endTurn(info.TurnID, turnEnd{status: StatusFailed, errMsg: msg})
		if len(msg) > 600 {
			msg = msg[:600]
		}
		if _, perr := r.d.Room.Post(context.Background(), room.PostParams{RoomID: rm.ID, Type: room.TypeError, Author: room.System, ReplyToID: trig.ID,
			Body:    fmt.Sprintf("%s could not answer: %s", who, msg),
			Payload: map[string]any{"code": "agent_turn_failed", "class": "agent", "recoverable": true, "turn_id": info.TurnID, "agent_id": sp.Member}}); perr != nil {
			r.logf("post turn failure: %v", perr)
		}
		return info, nil
	}
	agent, err := r.d.LookupAgent(ctx, sp.Member)
	if err != nil {
		return fail("unknown agent", err)
	}
	if agent.Name != "" {
		who = agent.Name
	}
	if r.d.Budget != nil {
		if err := r.d.Budget(ctx, rm.ProjectID, rm.TaskID, agent); err != nil {
			return fail("budget", err)
		}
	}
	sel, err := r.d.Room.ContextFor(ctx, rm.ID, room.Agent(agent.ID), trig.ID, r.o.Context)
	if err != nil {
		return fail("context", err)
	}
	extra := r.dmRecent(ctx, rm, trig, sel)
	members, _ := r.d.Room.Members(ctx, rm.ID)
	moderator := rm.Mode == room.ModeModerator && rm.ModeParams.Moderator == agent.ID
	brief := ""
	if r.d.Brief != nil {
		brief = r.d.Brief(ctx, rm.ProjectID)
	}
	prompt := BuildPrompt(PromptInput{Agent: agent, Room: rm, Members: members, Selection: sel, Extra: extra, Moderator: moderator, Trigger: trig, Brief: brief})
	r.saveManifest(ctx, info.TurnID, rm, agent, sel, extra, prompt)

	// admission: the global cap, then the project/provider gate (a queued agent shows Waiting)
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return fail("cancelled while waiting for a turn slot", ctx.Err())
	}
	if r.d.Gate != nil {
		r.publish(ctx, "agent.waiting", rm, agent.ID, map[string]any{"message": "waiting for a free agent slot"})
		release, err := r.d.Gate(ctx, rm.ProjectID, agent)
		if err != nil {
			return fail("no agent slot", err)
		}
		defer release()
	}
	cwd, err := r.d.Cwd(ctx, rm)
	if err != nil {
		return fail("working directory", err)
	}
	spec := agents.RunSpec{AgentID: agent.ID, Role: agent.Role, Cwd: cwd, Model: agent.Model, Effort: agent.Effort,
		PermissionMode: "plan", ReadOnly: true} // no tool-wide Read/Grep/Glob allow rule: one would let the reply read any file on
	// the machine (measured with claude 2.1.296); without it Claude reads inside its working folder and refuses the rest
	if r.d.Spec != nil {
		spec = r.d.Spec(agent, spec)
		spec.PermissionMode, spec.ReadOnly = "plan", true // the application may add environment and MCP, never widen what a reply may do
	}
	base := rt.Config{AgentID: agent.ID, ProjectID: rm.ProjectID, TaskID: rm.TaskID, Adapter: agent.Adapter, Spec: spec, Provider: providerLabel(agent), Tier: "premium"}
	tctx, cancel := context.WithTimeoutCause(ctx, r.o.TurnTimeout, errors.New("the turn timed out"))
	defer cancel()
	// one attempt runs the turn on one model; a fallback hop (Route) is another attempt on a fresh session, and the last
	// attempt's result is the reply
	var (
		res     rt.TurnResult
		runID   string
		ran     Routed
		spawned bool
	)
	attempt := func(actx context.Context, rr Routed, emit func(workflow.Chunk)) error {
		cfg := base
		if rr.Model != "" {
			cfg.Spec.Model = rr.Model
		}
		if rr.Tier != "" {
			cfg.Tier = rr.Tier
		}
		ran, res, runID = rr, rt.TurnResult{}, ""
		m, err := r.d.Runtime.Spawn(actx, cfg)
		if err != nil {
			spawned = false
			return err
		}
		spawned = true
		defer func() { _ = m.Stop(context.Background()) }()
		runID = m.RunID
		var calls int
		actx2, cancelCalls := context.WithCancelCause(actx)
		defer cancelCalls(nil)
		res, err = m.RunTurn(actx2, prompt, nil, func(c workflow.Chunk) {
			if c.Kind == workflow.ChunkToolCall {
				calls++
				if calls > r.o.MaxToolCalls {
					cancelCalls(fmt.Errorf("the turn used more than %d tool calls", r.o.MaxToolCalls))
				}
			}
			if emit != nil {
				emit(c)
			}
		})
		return err
	}
	if r.d.Route != nil {
		err = r.d.Route(tctx, ReplyTurn{TurnID: info.TurnID, RoomID: rm.ID, RoomKind: string(rm.Kind), ProjectID: rm.ProjectID, TaskID: rm.TaskID, Agent: agent,
			ContextTokens: sel.Tokens}, attempt)
	} else {
		err = attempt(tctx, Routed{}, nil)
	}
	if err != nil && !spawned {
		return fail("could not start the agent", err)
	}
	if err != nil {
		return fail("the turn failed", err)
	}
	model := agent.Model
	if ran.Model != "" {
		model = ran.Model
	}
	var in, outTok int
	var cost float64
	for _, u := range res.Usage {
		in += int(u.InputTokens)
		outTok += int(u.OutputTokens)
		cost += u.CostUSD
	}
	end := turnEnd{runID: runID, ctxMsgs: len(sel.Messages) + len(extra), ctxTokens: sel.Tokens, inTokens: in, outToks: outTok, cost: cost}
	if cost > 0 && rm.TaskID != "" && r.d.OnCost != nil {
		r.d.OnCost(ctx, rm.TaskID, cost)
	}
	text := strings.TrimSpace(strings.ToValidUTF8(res.Text, "�"))
	payload := map[string]any{"turn_id": info.TurnID, "in_reply_to": trig.ID, "model": model, "read_only": true, "round": dec.Round,
		"reason": string(sp.Reason), "context_messages": end.ctxMsgs, "context_tokens": end.ctxTokens,
		"input_tokens": in, "output_tokens": outTok, "cost_usd": cost, "tool_calls": res.ToolCalls}
	if ran.DecisionID != "" {
		payload["decision_id"] = ran.DecisionID // "Why this model?" for this reply
	}
	switch {
	case text == "":
		info.Status = StatusEmpty
		end.status = StatusEmpty
		r.endTurn(info.TurnID, end)
		_, _ = r.d.Room.Post(context.Background(), room.PostParams{RoomID: rm.ID, Type: room.TypeError, Author: room.System, ReplyToID: trig.ID,
			Body:    fmt.Sprintf("%s ended its turn without a reply.", agent.Name),
			Payload: map[string]any{"code": "empty_reply", "class": "agent", "recoverable": true, "turn_id": info.TurnID, "agent_id": agent.ID}})
		return info, nil
	case strings.EqualFold(text, "PASS"):
		payload["pass"] = true
		text = "(passed: nothing to add)"
		info.Status, end.status = StatusPassed, StatusPassed
	default:
		if len(text) > r.o.MaxReplyBytes {
			cut := r.o.MaxReplyBytes
			for cut > 0 && text[cut]&0xC0 == 0x80 {
				cut--
			}
			text = text[:cut] + "\n[reply truncated by Drydock]"
		}
		info.Status, end.status = StatusDone, StatusDone
	}
	posted, err := r.d.Room.Post(context.Background(), room.PostParams{RoomID: rm.ID, Type: room.TypeChat, Author: room.Agent(agent.ID), Body: text,
		ReplyToID: trig.ID, Payload: payload, RunID: runID, IdempotencyKey: "turn:" + info.TurnID})
	if err != nil {
		return fail("could not post the reply", err)
	}
	info.MessageID, end.messageID = posted.Message.ID, posted.Message.ID
	r.endTurn(info.TurnID, end)
	reply := posted.Message
	return info, &reply
}

func providerLabel(a Agent) string {
	if a.ProviderID != "" {
		return a.ProviderID
	}
	return a.Adapter
}

func (r *Responder) publish(ctx context.Context, typ string, rm room.Room, agentID string, data map[string]any) {
	if r.d.Bus == nil {
		return
	}
	b, _ := json.Marshal(data)
	_, _ = r.d.Bus.Publish(ctx, events.Event{Type: typ, ProjectID: rm.ProjectID, TaskID: rm.TaskID, AgentID: agentID, Data: b})
}

// dmRecent adds the last few messages before the trigger to a DM turn. A DM is one long conversation, but the room's
// context selection is per thread, so without this an agent in a DM would forget everything between questions. The window
// is small and fixed, the messages go through the same untrusted wrapping, and the manifest lists them.
func (r *Responder) dmRecent(ctx context.Context, rm room.Room, trig room.Message, sel room.ContextSelection) []room.Message {
	if rm.Kind != room.KindDM || r.o.DMRecent <= 0 {
		return nil
	}
	have := map[string]bool{}
	for _, m := range sel.Messages {
		have[m.ID] = true
	}
	after := trig.Seq - int64(r.o.DMRecent*4)
	if after < 0 {
		after = 0
	}
	ms, err := r.d.Room.Messages(ctx, rm.ID, after, r.o.DMRecent*4+4)
	if err != nil {
		return nil
	}
	var out []room.Message
	for _, m := range ms {
		if m.Seq >= trig.Seq || have[m.ID] {
			continue
		}
		switch m.Type {
		case room.TypeChat, room.TypeQuestion, room.TypeDecision, room.TypeResult:
			out = append(out, m)
		}
	}
	if len(out) > r.o.DMRecent {
		out = out[len(out)-r.o.DMRecent:]
	}
	return out
}

// saveManifest stores what the agent was given and what the selection left out, so a human can see exactly why an agent
// said what it said (token-control s2, room-protocol s11).
func (r *Responder) saveManifest(ctx context.Context, turnID string, rm room.Room, a Agent, sel room.ContextSelection, extra []room.Message, prompt string) {
	if r.d.Context == nil {
		return
	}
	var m ctxeng.Manifest
	m.Add("prompt (room reply)", true, len(prompt), "fixed instructions, member list and the selected messages; read-only permission mode", 1)
	for _, e := range sel.Entries {
		src := "room:" + e.MessageID
		if e.Included {
			m.Add(src, true, e.EstTokens*4, fmt.Sprintf("%s (%s from %s, %s)", e.Rule, e.Type, e.Author, e.Trust), 10+e.Priority)
		} else {
			m.Add(src, false, 0, e.Exclusion, 20)
		}
	}
	for _, e := range extra {
		m.Add("room:"+e.ID, true, len(e.Body), "dm_recent (a DM carries a short window of earlier messages)", 15)
	}
	if sel.Digest != "" {
		m.Add("room digest", true, len(sel.Digest), fmt.Sprintf("older thread messages folded (%d)", len(sel.DigestFrom)), 12)
	}
	m.Add("room history", false, 0, "the rest of the room is not sent: only the selected messages above (token rule 1)", 30)
	m.Add("other rooms", false, 0, "an agent receives only the room it is answering in", 30)
	if err := r.d.Context.SaveManifest(ctx, turnID, "room_reply", 1, rm.TaskID, a.ID, m); err != nil {
		r.logf("room reply manifest: %v", err)
	}
}

// Turn is a stored turn row (room.turns).
type Turn struct {
	TurnID        string  `json:"turnId"`
	RoomID        string  `json:"roomId"`
	TriggerID     string  `json:"triggerId"`
	AgentID       string  `json:"agentId"`
	Round         int     `json:"round"`
	Reason        string  `json:"reason"`
	Mode          string  `json:"mode"`
	Status        string  `json:"status"`
	MessageID     string  `json:"messageId,omitempty"`
	AgentRunID    string  `json:"agentRunId,omitempty"`
	ContextMsgs   int     `json:"contextMessages"`
	ContextTokens int     `json:"contextTokens"`
	InputTokens   int     `json:"inputTokens"`
	OutputTokens  int     `json:"outputTokens"`
	CostUSD       float64 `json:"costUsd"`
	Error         string  `json:"error,omitempty"`
	Started       int64   `json:"startedAt"`
	Ended         int64   `json:"endedAt"`
	// Model and DecisionID are the turn's last routing decision (the model it ran on, after any fallback hop), empty for a
	// turn from before replies were routed (P63).
	Model      string `json:"model,omitempty"`
	DecisionID string `json:"decisionId,omitempty"`
}

// Turns lists a room's agent turns, newest first.
func (r *Responder) Turns(ctx context.Context, roomID string, limit int) ([]Turn, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.d.DB.Reader().QueryContext(ctx, `SELECT id,room_id,trigger_id,agent_id,round,reason,mode,status,message_id,agent_run_id,context_msgs,context_tokens,
		input_tokens,output_tokens,cost_usd,error,started_at,ended_at,
		COALESCE((SELECT d.model FROM routing_decisions d WHERE d.run_id=room_turns.id ORDER BY d.ts DESC, d.rowid DESC LIMIT 1),''),
		COALESCE((SELECT d.id FROM routing_decisions d WHERE d.run_id=room_turns.id ORDER BY d.ts DESC, d.rowid DESC LIMIT 1),'')
		FROM room_turns WHERE room_id=? ORDER BY started_at DESC, rowid DESC LIMIT ?`, roomID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Turn{}
	for rows.Next() {
		var t Turn
		if err := rows.Scan(&t.TurnID, &t.RoomID, &t.TriggerID, &t.AgentID, &t.Round, &t.Reason, &t.Mode, &t.Status, &t.MessageID, &t.AgentRunID, &t.ContextMsgs, &t.ContextTokens,
			&t.InputTokens, &t.OutputTokens, &t.CostUSD, &t.Error, &t.Started, &t.Ended, &t.Model, &t.DecisionID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
