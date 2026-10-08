package room

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/events"
)

// Presence is what the UI shows for an agent (brief section 11). It is derived from events, never stored as chat.
type Presence string

const (
	PresenceWorking        Presence = "Working"
	PresenceThinking       Presence = "Thinking"
	PresenceIdle           Presence = "Idle"
	PresenceWaiting        Presence = "Waiting"
	PresenceCompleted      Presence = "Completed"
	PresenceNeedsAttention Presence = "Needs Attention"
	PresenceFailed         Presence = "Failed"
	PresencePaused         Presence = "Paused"
)

// PresenceState is one agent's current presence with its activity text.
type PresenceState struct {
	AgentID   string    `json:"agentId"`
	ProjectID string    `json:"projectId,omitempty"`
	TaskID    string    `json:"taskId,omitempty"`
	RunID     string    `json:"runId,omitempty"`
	Presence  Presence  `json:"presence"`
	Label     string    `json:"label,omitempty"`    // "starting" and "stopped" have no presence of their own (room-protocol s4)
	Activity  string    `json:"activity,omitempty"` // short text, for example "tool: Read"
	Since     time.Time `json:"since"`              // when Presence last changed
	Updated   time.Time `json:"updated"`
	Seq       uint64    `json:"seq"` // bus seq of the last event applied
}

func (p PresenceState) sameView(q PresenceState) bool {
	return p.Presence == q.Presence && p.Label == q.Label && p.Activity == q.Activity
}

// sticky states are left only by an explicit lifecycle event, so late output from a dying process cannot revive them.
func (p PresenceState) sticky() bool {
	switch p.Presence {
	case PresenceFailed, PresenceNeedsAttention, PresencePaused:
		return true
	}
	return false
}

// agentStateMap maps the brief section 12 agent states to presence (room-protocol s4).
var agentStateMap = map[string]struct {
	p     Presence
	label string
}{
	"STARTING":        {PresenceIdle, "starting"},
	"IDLE":            {PresenceIdle, ""},
	"WORKING":         {PresenceWorking, ""},
	"WAITING":         {PresenceWaiting, ""},
	"COMPLETED":       {PresenceCompleted, ""},
	"NEEDS_ATTENTION": {PresenceNeedsAttention, ""},
	"FAILED":          {PresenceFailed, ""},
	"PAUSED":          {PresencePaused, ""},
	"STOPPED":         {PresencePaused, "stopped"},
}

type presenceData struct {
	Kind       string          `json:"kind"`
	Data       json.RawMessage `json:"data"`
	State      string          `json:"state"`
	Error      string          `json:"error"`
	StopReason string          `json:"stopReason"`
	Signal     string          `json:"signal"`
	Message    string          `json:"message"`
	Evidence   string          `json:"evidence"`
	AgentID    string          `json:"agentId"`
	AgentID2   string          `json:"agent_id"`
	Agent      string          `json:"agent"`
	Run        string          `json:"agentRun"`
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n]
		for len(s) > 0 && !isRuneStart(s[len(s)-1]) {
			s = s[:len(s)-1]
		}
		s += "..."
	}
	return s
}

// Derive is the pure presence reducer: given an agent's previous state (zero for a new agent) and one agent.* or
// watchdog.* event it returns the next state and whether the event applied. Unknown events do not apply. It is
// deterministic, so replaying the persisted events rebuilds presence exactly.
func Derive(prev PresenceState, e events.Event) (PresenceState, bool) {
	var d presenceData
	_ = json.Unmarshal(e.Data, &d)
	id := e.AgentID
	if id == "" {
		for _, c := range []string{d.AgentID, d.AgentID2, d.Agent} {
			if c != "" {
				id = c
				break
			}
		}
	}
	if id == "" {
		return prev, false
	}
	next := prev
	next.AgentID = id
	set := func(p Presence, label, activity string) {
		next.Presence, next.Label, next.Activity = p, label, activity
	}
	switch e.Type {
	case "agent.starting":
		set(PresenceIdle, "starting", "starting")
	case "agent.started":
		set(PresenceIdle, "", "started")
	case "agent.idle", "agent.resumed":
		set(PresenceIdle, "", "")
	case "agent.stopped":
		if prev.Presence == PresenceFailed { // agent.failed is followed by agent.stopped when the process exits
			next.Activity = firstNonEmpty(prev.Activity, "exited")
		} else {
			set(PresencePaused, "stopped", "stopped")
		}
	case "agent.failed":
		set(PresenceFailed, "", clip(d.Error, 160))
	case "agent.paused":
		set(PresencePaused, "", firstNonEmpty(clip(d.Message, 160), "paused"))
	case "agent.needs_attention":
		set(PresenceNeedsAttention, "", firstNonEmpty(clip(d.Message, 160), "needs attention"))
	case "agent.completed":
		set(PresenceCompleted, "", firstNonEmpty(clip(d.StopReason, 80), "turn finished"))
	case "agent.waiting":
		if strings.EqualFold(d.State, "working") { // the runtime announces a turn start this way
			set(PresenceThinking, "", "turn started")
		} else {
			set(PresenceWaiting, "", firstNonEmpty(clip(d.Message, 160), "waiting"))
		}
	case "agent.state_changed", "agent.state":
		m, ok := agentStateMap[strings.ToUpper(d.State)]
		if !ok {
			return prev, false
		}
		set(m.p, m.label, strings.ToLower(d.State))
	case "agent.output":
		if prev.sticky() || (prev.Presence == PresencePaused && prev.Label == "stopped") {
			return prev, false
		}
		switch d.Kind {
		case "text":
			set(PresenceWorking, "", "writing")
		case "thinking":
			set(PresenceThinking, "", "thinking")
		case "tool_call":
			var tc struct{ Name string }
			_ = json.Unmarshal(d.Data, &tc)
			set(PresenceWorking, "", "tool: "+firstNonEmpty(tc.Name, "call"))
		case "tool_result":
			var tr struct{ Name string }
			_ = json.Unmarshal(d.Data, &tr)
			set(PresenceThinking, "", "after tool: "+firstNonEmpty(tr.Name, "call"))
		case "permission_request":
			set(PresenceWaiting, "", "awaiting permission")
		case "error":
			var er struct{ Message string }
			_ = json.Unmarshal(d.Data, &er)
			if prev.Presence == "" {
				return prev, false
			}
			next.Activity = "error: " + clip(er.Message, 120)
		default:
			return prev, false // usage, session, done, log, message_end: no presence meaning
		}
	case "watchdog.paused":
		set(PresenceNeedsAttention, "watchdog", clip(firstNonEmpty(d.Message, d.Signal, "watchdog paused the task"), 160))
	case "watchdog.tripped", "watchdog.warning":
		if prev.Presence == "" || prev.sticky() {
			return prev, false
		}
		next.Activity = clip("watchdog "+strings.TrimPrefix(e.Type, "watchdog.")+": "+firstNonEmpty(d.Signal, d.Message), 160)
	default:
		return prev, false
	}
	if e.ProjectID != "" {
		next.ProjectID = e.ProjectID
	}
	if e.TaskID != "" {
		next.TaskID = e.TaskID
	}
	if d.Run != "" {
		next.RunID = d.Run
	}
	if next.Presence != prev.Presence || next.Since.IsZero() {
		next.Since = e.Time
	}
	next.Updated, next.Seq = e.Time, e.Seq
	return next, true
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// PresenceFilter selects the events Derive understands.
var PresenceFilter = events.Filter{Types: []string{"agent.*", "watchdog.*"}}

// TrackerOptions tunes a Tracker.
type TrackerOptions struct {
	// Alive, if set, is asked once after the rebuild: an agent it reports as not running is shown stopped, because a
	// restart kills the processes and no stop event was written (room-protocol s13: presence follows live state).
	Alive func(agentID string) bool
	// EmitEvery enables room.presence_changed events, at most one per agent per interval (design: one second). 0 disables.
	EmitEvery time.Duration
}

// Tracker keeps current presence for the UI by folding agent.* and watchdog.* events. It is replay-safe: Start rebuilds
// from the persisted events, then follows the live stream, and drops any event it has already applied.
type Tracker struct {
	bus  events.Bus
	opt  TrackerOptions
	mu   sync.RWMutex
	st   map[string]PresenceState
	last uint64
	dirt map[string]bool
	subs map[chan PresenceState]struct{}
}

// NewTracker returns an idle Tracker; call Start.
func NewTracker(bus events.Bus, opt TrackerOptions) *Tracker {
	return &Tracker{bus: bus, opt: opt, st: map[string]PresenceState{}, dirt: map[string]bool{}, subs: map[chan PresenceState]struct{}{}}
}

// Start rebuilds presence from the event log and then follows live events until ctx ends. It returns once the rebuild is
// done, so Get is immediately accurate.
func (t *Tracker) Start(ctx context.Context) error {
	ch, cancel, err := t.catchUp(ctx)
	if err != nil {
		return err
	}
	t.mu.Lock()
	if t.opt.Alive != nil {
		for id, s := range t.st {
			if s.Presence == PresenceFailed || (s.Presence == PresencePaused && s.Label == "stopped") {
				continue
			}
			if !t.opt.Alive(id) {
				s.Presence, s.Label, s.Activity, s.Since = PresencePaused, "stopped", "not running", s.Updated
				t.st[id] = s
			}
		}
	}
	t.dirt = map[string]bool{} // the rebuild itself is not news
	t.mu.Unlock()
	go t.follow(ctx, ch, cancel)
	if t.opt.EmitEvery > 0 {
		go t.emitLoop(ctx)
	}
	return nil
}

// catchUp subscribes first and then replays, so no event falls in the gap; duplicates are dropped by seq.
func (t *Tracker) catchUp(ctx context.Context) (<-chan events.Event, func(), error) {
	ch, cancel := t.bus.Subscribe(PresenceFilter)
	for {
		t.mu.RLock()
		after := t.last
		t.mu.RUnlock()
		evs, err := t.bus.Replay(ctx, after, PresenceFilter, 1000)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		for _, e := range evs {
			t.apply(e)
		}
		if len(evs) < 1000 {
			return ch, cancel, nil
		}
	}
}

func (t *Tracker) follow(ctx context.Context, ch <-chan events.Event, cancel func()) {
	for {
		select {
		case <-ctx.Done():
			cancel()
			return
		case e, ok := <-ch:
			if !ok { // fell behind: the bus closed the channel, so replay from the last applied seq and resubscribe
				cancel()
				for ctx.Err() == nil {
					var err error
					if ch, cancel, err = t.catchUp(ctx); err == nil {
						break
					}
					select {
					case <-ctx.Done():
					case <-time.After(200 * time.Millisecond):
					}
				}
				if ctx.Err() != nil {
					return
				}
				continue
			}
			t.apply(e)
		}
	}
}

func (t *Tracker) apply(e events.Event) {
	t.mu.Lock()
	if e.Seq != 0 && e.Seq <= t.last {
		t.mu.Unlock()
		return
	}
	if e.Seq > t.last {
		t.last = e.Seq
	}
	id := e.AgentID
	if id == "" {
		var d presenceData
		_ = json.Unmarshal(e.Data, &d)
		id = firstNonEmpty(d.AgentID, d.AgentID2, d.Agent)
	}
	prev := t.st[id]
	next, ok := Derive(prev, e)
	if !ok || id == "" {
		t.mu.Unlock()
		return
	}
	t.st[id] = next
	changed := !prev.sameView(next)
	if changed {
		t.dirt[id] = true
	}
	var subs []chan PresenceState
	if changed {
		for c := range t.subs {
			subs = append(subs, c)
		}
	}
	t.mu.Unlock()
	for _, c := range subs {
		select {
		case c <- next:
		default: // a slow watcher misses an intermediate state; Get always has the latest
		}
	}
}

// Get returns one agent's presence.
func (t *Tracker) Get(agentID string) (PresenceState, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	s, ok := t.st[agentID]
	return s, ok
}

// Snapshot returns every known agent's presence ordered by agent id.
func (t *Tracker) Snapshot() []PresenceState {
	t.mu.RLock()
	out := make([]PresenceState, 0, len(t.st))
	for _, s := range t.st {
		out = append(out, s)
	}
	t.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out
}

// LastSeq is the highest bus seq applied.
func (t *Tracker) LastSeq() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.last
}

// Watch streams presence changes. The channel is buffered and drops under backpressure.
func (t *Tracker) Watch() (<-chan PresenceState, func()) {
	c := make(chan PresenceState, 64)
	t.mu.Lock()
	t.subs[c] = struct{}{}
	t.mu.Unlock()
	return c, func() {
		t.mu.Lock()
		if _, ok := t.subs[c]; ok {
			delete(t.subs, c)
			close(c)
		}
		t.mu.Unlock()
	}
}

func (t *Tracker) emitLoop(ctx context.Context) {
	tick := time.NewTicker(t.opt.EmitEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		t.mu.Lock()
		var out []PresenceState
		for id := range t.dirt {
			out = append(out, t.st[id])
		}
		t.dirt = map[string]bool{}
		t.mu.Unlock()
		sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
		for _, s := range out {
			_, _ = events.Emit(ctx, t.bus, "room.presence_changed", events.Event{ProjectID: s.ProjectID, TaskID: s.TaskID},
				map[string]any{"member": s.AgentID, "presence": s.Presence, "label": s.Label, "activity": s.Activity})
		}
	}
}
