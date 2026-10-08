// Package watchdog is Drydock's deterministic circuit breaker (brief section 7). It detects agents that spend
// without progress and stops them cheaply. It makes no LLM calls and runs in microseconds per event.
//
// It sits in two places: as a workflow.StreamObserver on every agent_task node (it sees tool calls and text as they
// stream, so it can trip mid-turn) and as middleware (it enforces the trip policy, and sees check results at loop
// back edges via OnCheckResult).
package watchdog

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/workflow"
)

// Signal names recorded in watchdog_trips.
const (
	SignalToolSpin    = "tool_spin"
	SignalErrorEcho   = "error_echo"
	SignalToolCap     = "tool_call_cap"
	SignalTimeCap     = "time_cap"
	SignalErrorStreak = "tool_error_streak"
	SignalStagnation  = "stagnation" // soft: warning only
)

// Config holds thresholds. Defaults are the brief's; templates override (Iterate Until Verified raises spin
// window and tool-call cap because repeated test runs are expected there).
type Config struct {
	SpinWindow          int           // rolling window of tool-call keys (default 8)
	SpinThreshold       int           // same key this many times trips (default 3)
	SpinThresholdWarned int           // threshold after a stagnation warning (default 2)
	MaxToolCalls        int           // per turn (default 60)
	MaxTurnDuration     time.Duration // per turn (default 20m)
	MaxToolErrors       int           // consecutive tool errors (default 5)
	StagnationMinLen    int           // characters (default 200)
	StagnationSim       float64       // bigram Jaccard (default 0.85)
	StagnationCount     int           // consecutive scores at/above StagnationSim (default 3)
}

// Defaults returns the brief's default thresholds.
func Defaults() Config {
	return Config{SpinWindow: 8, SpinThreshold: 3, SpinThresholdWarned: 2, MaxToolCalls: 60, MaxTurnDuration: 20 * time.Minute,
		MaxToolErrors: 5, StagnationMinLen: 200, StagnationSim: 0.85, StagnationCount: 3}
}

// IterateUntilVerified returns the thresholds for the Iterate Until Verified template: repeated test runs expected.
func IterateUntilVerified() Config {
	c := Defaults()
	c.SpinWindow = 12
	c.MaxToolCalls = 120
	return c
}

func (c Config) withDefaults() Config {
	d := Defaults()
	if c.SpinWindow <= 0 {
		c.SpinWindow = d.SpinWindow
	}
	if c.SpinThreshold <= 0 {
		c.SpinThreshold = d.SpinThreshold
	}
	if c.SpinThresholdWarned <= 0 {
		c.SpinThresholdWarned = d.SpinThresholdWarned
	}
	if c.MaxToolCalls <= 0 {
		c.MaxToolCalls = d.MaxToolCalls
	}
	if c.MaxTurnDuration <= 0 {
		c.MaxTurnDuration = d.MaxTurnDuration
	}
	if c.MaxToolErrors <= 0 {
		c.MaxToolErrors = d.MaxToolErrors
	}
	if c.StagnationMinLen <= 0 {
		c.StagnationMinLen = d.StagnationMinLen
	}
	if c.StagnationSim <= 0 {
		c.StagnationSim = d.StagnationSim
	}
	if c.StagnationCount <= 0 {
		c.StagnationCount = d.StagnationCount
	}
	return c
}

// VerdictKind is the outcome of one observation.
type VerdictKind int

const (
	None VerdictKind = iota
	Warn
	Trip
)

// Verdict is the result of observing an event.
type Verdict struct {
	Kind     VerdictKind
	Signal   string
	Stage    int // 1 or 2; set by the policy layer for Trip
	Evidence string
}

// Turn is the per-turn detector state. It is safe for concurrent use.
type Turn struct {
	cfg  Config
	mu   sync.Mutex
	now  func() time.Time
	t0   time.Time
	keys []string // rolling window of ToolKeys
	// pending tool calls by id so a result can be matched to its call even if the adapter omitted name/args
	calls      map[string]workflow.ToolCall
	toolCalls  int
	errStreak  int
	buf        strings.Builder // current assistant message text
	lastMsg    string
	simStreak  int
	warned     bool
	toolVisibl bool // whether any tool call has been observed (for reduced-detection reporting)
}

// NewTurn starts detector state for one turn.
func NewTurn(cfg Config) *Turn {
	return &Turn{cfg: cfg.withDefaults(), now: time.Now, t0: time.Now(), calls: map[string]workflow.ToolCall{}}
}

// SetClock replaces the clock (tests).
func (t *Turn) SetClock(now func() time.Time) { t.now = now; t.t0 = now() }

// ToolCallsSeen reports how many tool calls this turn has seen.
func (t *Turn) ToolCallsSeen() int { t.mu.Lock(); defer t.mu.Unlock(); return t.toolCalls }

func (t *Turn) threshold() int {
	if t.warned {
		return t.cfg.SpinThresholdWarned
	}
	return t.cfg.SpinThreshold
}

// Observe feeds one stream chunk and returns a verdict.
func (t *Turn) Observe(c workflow.Chunk) Verdict {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.now().Sub(t.t0) > t.cfg.MaxTurnDuration {
		return Verdict{Kind: Trip, Signal: SignalTimeCap, Evidence: fmt.Sprintf("turn exceeded %s wall clock", t.cfg.MaxTurnDuration)}
	}
	switch c.Kind {
	case workflow.ChunkText:
		var d workflow.TextDelta
		if c.Decode(&d) == nil {
			t.buf.WriteString(d.Text)
		}
	case workflow.ChunkMessageEnd:
		var d workflow.TextDelta
		if c.Decode(&d) == nil {
			t.buf.Reset()
			return t.message(d.Text)
		}
	case workflow.ChunkToolCall:
		var tc workflow.ToolCall
		if c.Decode(&tc) != nil {
			return Verdict{}
		}
		v := t.flushMessage()
		t.toolVisibl = true
		t.toolCalls++
		if tc.ID != "" {
			t.calls[tc.ID] = tc
		}
		if t.toolCalls > t.cfg.MaxToolCalls {
			return Verdict{Kind: Trip, Signal: SignalToolCap, Evidence: fmt.Sprintf("%d tool calls in one turn (cap %d)", t.toolCalls, t.cfg.MaxToolCalls)}
		}
		return v
	case workflow.ChunkToolResult:
		var tr workflow.ToolResult
		if c.Decode(&tr) != nil {
			return Verdict{}
		}
		if prev, ok := t.calls[tr.ID]; ok && tr.ID != "" {
			if tr.Name == "" {
				tr.Name = prev.Name
			}
			if len(tr.Args) == 0 {
				tr.Args = prev.Args
			}
			delete(t.calls, tr.ID)
		}
		t.toolVisibl = true
		if tr.IsError {
			t.errStreak++
		} else {
			t.errStreak = 0
		}
		if t.errStreak >= t.cfg.MaxToolErrors {
			return Verdict{Kind: Trip, Signal: SignalErrorStreak, Evidence: fmt.Sprintf("%d consecutive tool errors; last: %s %s", t.errStreak, tr.Name, trunc(tr.Output, 200))}
		}
		key := ToolKey(tr.Name, tr.Args, tr.Output)
		t.keys = append(t.keys, key)
		if len(t.keys) > t.cfg.SpinWindow {
			t.keys = t.keys[len(t.keys)-t.cfg.SpinWindow:]
		}
		n := 0
		for _, k := range t.keys {
			if k == key {
				n++
			}
		}
		if n >= t.threshold() {
			return Verdict{Kind: Trip, Signal: SignalToolSpin, Evidence: fmt.Sprintf("tool %s called %d times with identical arguments and identical results (args %s)", tr.Name, n, trunc(CanonicalJSON(tr.Args), 160))}
		}
	case workflow.ChunkDone:
		return t.flushMessage()
	}
	return Verdict{}
}

func (t *Turn) flushMessage() Verdict {
	if t.buf.Len() == 0 {
		return Verdict{}
	}
	s := t.buf.String()
	t.buf.Reset()
	return t.message(s)
}

// message handles a complete assistant message for the soft stagnation signal.
func (t *Turn) message(s string) Verdict {
	if len([]rune(s)) < t.cfg.StagnationMinLen {
		return Verdict{}
	}
	prev := t.lastMsg
	t.lastMsg = s
	if prev == "" {
		return Verdict{}
	}
	if Jaccard(prev, s) >= t.cfg.StagnationSim {
		t.simStreak++
	} else {
		t.simStreak = 0
	}
	if t.simStreak >= t.cfg.StagnationCount && !t.warned {
		t.warned = true
		return Verdict{Kind: Warn, Signal: SignalStagnation, Evidence: fmt.Sprintf("%d consecutive near-identical assistant messages (similarity >= %.2f); lowering tool-spin threshold to %d", t.simStreak, t.cfg.StagnationSim, t.cfg.SpinThresholdWarned)}
	}
	return Verdict{}
}

// ToolCallVisibility reports whether tool calls were visible this turn; when a CLI exposes none, detection is reduced
// to error echo, hard time cap and stagnation, and the Agent Inspector says so.
func (t *Turn) ToolCallVisibility() bool { t.mu.Lock(); defer t.mu.Unlock(); return t.toolVisibl }

func trunc(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// EchoDetector implements the error-echo signal across loop iterations of one task.
type EchoDetector struct {
	mu       sync.Mutex
	lastSig  string
	lastDiff string
	have     bool
}

// OnCheckResult is called at every check node and loop back edge. A passing check resets the detector. A failure whose
// signature equals the previous failure's while the worktree diff hash is unchanged means the agent believed it fixed
// something and changed nothing.
func (e *EchoDetector) OnCheckResult(res workflow.CheckResult, diffHash string) Verdict {
	e.mu.Lock()
	defer e.mu.Unlock()
	if res.Passed {
		e.have = false
		return Verdict{}
	}
	sig := FailureSignature(res.Output, res.FailingTests)
	if diffHash == "" {
		diffHash = res.DiffHash
	}
	if e.have && sig == e.lastSig && diffHash == e.lastDiff {
		return Verdict{Kind: Trip, Signal: SignalErrorEcho, Evidence: fmt.Sprintf("check %q failed with the same signature (%s) and the worktree diff is unchanged since the previous failure", res.Name, sig)}
	}
	e.lastSig, e.lastDiff, e.have = sig, diffHash, true
	return Verdict{}
}

// Reset clears the detector (human resumed with guidance).
func (e *EchoDetector) Reset() { e.mu.Lock(); e.have = false; e.mu.Unlock() }

// Evidence renders a trip as JSON for the trips table and the handoff artifact.
func Evidence(v Verdict, extra map[string]any) string {
	m := map[string]any{"signal": v.Signal, "stage": v.Stage, "evidence": v.Evidence}
	for k, x := range extra {
		m[k] = x
	}
	b, _ := json.Marshal(m)
	return string(b)
}
