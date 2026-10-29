package headless

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// The generic runner of a person's own CLI (the `custom` dialect, decision P115). Its manifest
// (<app data>/adapters/custom/<id>.yaml) meets the headless contract: one process per turn that takes the prompt (on stdin, or
// in its argv as {{prompt}}), prints its answer in a declared format and exits with a code that says how the turn went. The
// format is JSON lines, each event found by its type field and read by field paths ($.a.b), or plain text. A session id the CLI
// prints (the session event) is passed to the next turn's {{session_id}}, which is how prompt-and-exit CLIs continue a
// conversation. Interrupting a turn ends its process (there is nothing else to ask a prompt-and-exit program); the session
// stays and takes the next message.

// Dialect formats.
const (
	FormatJSONL = "jsonl"
	FormatText  = "text"
)

// Prompt routes.
const (
	PromptStdin = "stdin"
	PromptArg   = "arg"
)

// Dialect is the declared output format (the manifest's transports.headless.dialect).
type Dialect struct {
	Format    string
	TypeField string
	// Map names, per event (assistant_text, tool_call, tool_result, usage, result, error, session), the type value it is
	// found by ("type") and where each of its fields is ("text": "$.text").
	Map map[string]map[string]string
}

// DialectFields are the fields each event may read; the first one listed is required.
var DialectFields = map[string][]string{
	"assistant_text": {"text"},
	"tool_call":      {"name", "id", "args"},
	"tool_result":    {"id", "name", "ok", "output"},
	"usage":          {"input", "output", "cached", "cost"},
	"result":         {"status", "error"},
	"error":          {"message"},
	"session":        {"id", "model"},
}

// PathRe is a field path: $ and one or more .name steps.
var PathRe = regexp.MustCompile(`^\$(\.[A-Za-z_][A-Za-z0-9_-]{0,63}){1,8}$`)

// ValidateDialect lists what is wrong with a dialect (nil when nothing is).
func ValidateDialect(d Dialect) []string {
	var p []string
	add := func(f string, a ...any) { p = append(p, fmt.Sprintf(f, a...)) }
	switch d.Format {
	case FormatText:
		if d.TypeField != "" || len(d.Map) > 0 {
			add("dialect: a text dialect has no type_field or map (every line it prints is the answer)")
		}
		return p
	case FormatJSONL:
	default:
		add("dialect.format must be jsonl or text, got %q", d.Format)
		return p
	}
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`).MatchString(d.TypeField) {
		add("dialect.type_field %q must be the name of a top-level field", d.TypeField)
	}
	if len(d.Map) == 0 {
		add("dialect.map must map at least one event")
	}
	if _, ok := d.Map["assistant_text"]; !ok && len(d.Map) > 0 {
		add("dialect.map needs assistant_text: the turn's answer has to be read from somewhere")
	}
	types := map[string]string{}
	for ev, fields := range d.Map {
		allowed, ok := DialectFields[ev]
		if !ok {
			add("dialect.map: unknown event %q", ev)
			continue
		}
		tv := fields["type"]
		if strings.TrimSpace(tv) == "" {
			add("dialect.map.%s.type: the value of %s that marks this event is required", ev, d.TypeField)
		} else if other, dup := types[tv]; dup {
			add("dialect.map: %s and %s both use type %q", other, ev, tv)
		} else {
			types[tv] = ev
		}
		if _, ok := fields[allowed[0]]; !ok {
			add("dialect.map.%s.%s is required", ev, allowed[0])
		}
		for k, v := range fields {
			if k == "type" {
				continue
			}
			known := false
			for _, a := range allowed {
				known = known || a == k
			}
			if !known {
				add("dialect.map.%s: unknown field %q (allowed: %s)", ev, k, strings.Join(allowed, ", "))
			} else if !PathRe.MatchString(v) {
				add("dialect.map.%s.%s: %q is not a field path ($.name or $.a.b)", ev, k, v)
			}
		}
	}
	return p
}

// CustomConfig builds the runner from a person's manifest.
type CustomConfig struct {
	ID       string
	Binaries []string
	Exe      string // overrides the lookup (tests)
	// Args builds the argv after the executable from the placeholders cwd, model, effort, session_id and prompt (the
	// manifest's HeadlessArgs: a group whose placeholder has no value is left out).
	Args        func(vars map[string]string) []string
	Prompt      string // stdin or arg
	Dialect     Dialect
	ExitCodes   map[int]string // code -> success | failure; a code not listed is a failure unless it is 0
	Caps        agents.Capabilities
	InstallHint string
	Detect      func(ctx context.Context) (agents.Detection, error)
	ScrubEnv    bool
	PassEnv     func() []string
	// TurnTimeout ends a turn whose process runs longer (default 30 minutes).
	TurnTimeout time.Duration
}

// CustomAdapter implements agents.Adapter for a person's own CLI.
type CustomAdapter struct{ cfg CustomConfig }

// NewCustom returns the runner for a validated dialect.
func NewCustom(cfg CustomConfig) (*CustomAdapter, error) {
	if p := ValidateDialect(cfg.Dialect); len(p) > 0 {
		return nil, fmt.Errorf("%s: %s", cfg.ID, strings.Join(p, "; "))
	}
	if cfg.Prompt != PromptStdin && cfg.Prompt != PromptArg {
		return nil, fmt.Errorf("%s: the prompt goes on stdin or in the argv as {{prompt}}, got %q", cfg.ID, cfg.Prompt)
	}
	if cfg.TurnTimeout <= 0 {
		cfg.TurnTimeout = 30 * time.Minute
	}
	return &CustomAdapter{cfg: cfg}, nil
}

func (a *CustomAdapter) ID() string                        { return a.cfg.ID }
func (a *CustomAdapter) Capabilities() agents.Capabilities { return a.cfg.Caps }

func (a *CustomAdapter) executable() (string, error) {
	if a.cfg.Exe != "" {
		return a.cfg.Exe, nil
	}
	return findProgram(a.cfg.Binaries)
}

func (a *CustomAdapter) Detect(ctx context.Context) (agents.Detection, error) {
	if a.cfg.Detect != nil {
		return a.cfg.Detect(ctx)
	}
	return lookDetect(ctx, a.cfg.ID, a.cfg.InstallHint, a.executable)
}

// Start checks the program exists and returns the session; nothing runs until the first message.
func (a *CustomAdapter) Start(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (agents.Session, error) {
	if err := agents.CheckRunMode(a, spec); err != nil {
		return nil, err
	}
	exe, err := a.executable()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", a.cfg.ID, err)
	}
	s := &customSession{a: a, pm: pm, exe: exe, spec: spec, sid: spec.ResumeID, stream: newStream(), ended: make(chan struct{})}
	return s, nil
}

type customSession struct {
	a    *CustomAdapter
	pm   proc.Manager
	exe  string
	spec agents.RunSpec
	stream

	mu          sync.Mutex
	sid         string
	cur         proc.Proc // the turn's process (nil between turns)
	lastPID     int
	interrupted bool
	stopping    bool
	last        proc.ExitInfo
	turns       sync.WaitGroup
	ended       chan struct{} // closed when Stop has ended the session
	endOnce     sync.Once
	drift       drift
}

func (s *customSession) ID() string                    { s.mu.Lock(); defer s.mu.Unlock(); return s.sid }
func (s *customSession) Output() <-chan workflow.Chunk { return s.out }
func (s *customSession) Stderr() string                { return s.errTail.String() }

// PID is the running turn's process, else the last one's (0 before the first turn).
func (s *customSession) PID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur != nil {
		return s.cur.PID()
	}
	return s.lastPID
}

// Wait answers when the session ends (Stop), with the last turn's exit: a turn's process ending is not the session ending.
func (s *customSession) Wait() <-chan proc.ExitInfo {
	ch := make(chan proc.ExitInfo, 1)
	safe.Go("headless.custom.Wait", func() {
		<-s.ended
		s.mu.Lock()
		ei := s.last
		s.mu.Unlock()
		ch <- ei
	})
	return ch
}

func (s *customSession) Send(ctx context.Context, msg string) error {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return errors.New(s.a.cfg.ID + ": the session was stopped")
	}
	if s.cur != nil {
		s.mu.Unlock()
		return errTurnRunning
	}
	vars := map[string]string{"cwd": s.spec.Cwd, "model": s.spec.Model, "effort": s.spec.Effort, "session_id": s.sid}
	if s.a.cfg.Prompt == PromptArg {
		vars["prompt"] = msg
	}
	args := append(s.a.cfg.Args(vars), s.spec.ExtraArgs...)
	env := childEnv(s.a.cfg.ScrubEnv, s.a.cfg.PassEnv, nil, s.spec.Env)
	p, err := s.pm.Start(context.WithoutCancel(ctx), proc.Spec{Path: s.exe, Args: args, Dir: s.spec.Cwd, Env: env, Label: "agent:" + s.spec.AgentID})
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("%s: %w", s.a.cfg.ID, err)
	}
	s.cur, s.lastPID, s.interrupted = p, p.PID(), false
	s.turns.Add(1)
	s.mu.Unlock()
	if s.a.cfg.Prompt == PromptStdin {
		_, _ = p.Stdin().Write([]byte(msg))
	}
	_ = p.Stdin().Close()
	safe.Go("headless.custom.turn", func() { s.runTurn(p) })
	return nil
}

// turnState is what a turn has printed so far.
type turnState struct {
	text   strings.Builder
	usage  workflow.UsageChunk
	result *struct{ status, err string }
	calls  map[string]workflow.ToolCall
	n      int
}

func (s *customSession) runTurn(p proc.Proc) {
	defer s.turns.Done()
	st := &turnState{calls: map[string]workflow.ToolCall{}}
	errDone := make(chan struct{})
	safe.Go("headless.custom.stderr", func() { _, _ = bufio.NewReader(p.Stderr()).WriteTo(&s.errTail); close(errDone) })
	timer := time.AfterFunc(s.a.cfg.TurnTimeout, func() { _ = p.Kill(true) })
	sc := bufio.NewScanner(p.Stdout())
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		for _, c := range s.parse(st, sc.Text()) {
			if !s.emit(c) {
				_ = p.Kill(true)
				break
			}
		}
	}
	ei := <-p.Wait()
	timer.Stop()
	select {
	case <-errDone:
	case <-time.After(2 * time.Second):
	}
	s.mu.Lock()
	s.cur, s.last = nil, ei
	interrupted, stopping := s.interrupted, s.stopping
	s.mu.Unlock()
	if stopping {
		return
	}
	var out []workflow.Chunk
	if t := st.text.String(); t != "" {
		out = append(out, chunk(workflow.ChunkMessageEnd, workflow.TextDelta{Text: t}))
	}
	if st.usage.InputTokens+st.usage.OutputTokens+st.usage.CachedTokens > 0 || st.usage.CostUSD > 0 {
		st.usage.Source = "reported"
		out = append(out, chunk(workflow.ChunkUsage, st.usage))
	}
	stop := "success"
	switch {
	case interrupted:
		stop = "cancelled"
	case s.failed(ei, st):
		stop = "error"
		msg := fmt.Sprintf("%s ended the turn with exit code %d", s.a.cfg.ID, ei.Code)
		if st.result != nil && st.result.err != "" {
			msg = st.result.err
		} else if l := firstLine(s.errTail.String()); l != "" {
			msg += ": " + l
		}
		out = append(out, chunk(workflow.ChunkError, workflow.ErrorChunk{Message: msg}))
	case st.result != nil && st.result.status != "":
		stop = st.result.status
	}
	out = append(out, chunk(workflow.ChunkDone, workflow.DoneChunk{StopReason: stop}))
	for _, c := range out {
		if !s.emit(c) {
			return
		}
	}
}

// failed says whether the turn failed: an exit code the manifest calls failure (any code it does not list but 0), or a result
// event that carries an error.
func (s *customSession) failed(ei proc.ExitInfo, st *turnState) bool {
	if st.result != nil && st.result.err != "" {
		return true
	}
	if v, ok := s.a.cfg.ExitCodes[ei.Code]; ok {
		return v != "success"
	}
	return ei.Code != 0 || ei.Err != nil
}

func (s *customSession) parse(st *turnState, line string) []workflow.Chunk {
	d := s.a.cfg.Dialect
	if d.Format == FormatText {
		st.text.WriteString(line + "\n")
		return []workflow.Chunk{chunk(workflow.ChunkText, workflow.TextDelta{Text: line + "\n"})}
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal([]byte(line), &obj) != nil {
		if c, ok := s.drift.once("(unreadable line)"); ok {
			return []workflow.Chunk{c}
		}
		return nil
	}
	typ := fmt.Sprint(obj[d.TypeField])
	for ev, fields := range d.Map {
		if fields["type"] != typ {
			continue
		}
		get := func(f string) (any, bool) {
			p, ok := fields[f]
			if !ok {
				return nil, false
			}
			return lookup(obj, p)
		}
		str := func(f string) string {
			v, ok := get(f)
			if !ok || v == nil {
				return ""
			}
			if s, ok := v.(string); ok {
				return s
			}
			b, _ := json.Marshal(v)
			return string(b)
		}
		num := func(f string) int64 {
			v, _ := get(f)
			switch x := v.(type) {
			case float64:
				return int64(x)
			case string:
				n, _ := strconv.ParseInt(x, 10, 64)
				return n
			}
			return 0
		}
		switch ev {
		case "assistant_text":
			t := str("text")
			if t == "" {
				return nil
			}
			st.text.WriteString(t)
			return []workflow.Chunk{chunk(workflow.ChunkText, workflow.TextDelta{Text: t})}
		case "tool_call":
			name := str("name")
			if name == "" {
				return nil
			}
			id := str("id")
			if id == "" {
				st.n++
				id = fmt.Sprintf("%s-call-%d", s.a.cfg.ID, st.n)
			}
			var args json.RawMessage
			if v, ok := get("args"); ok && v != nil {
				args, _ = json.Marshal(v)
			}
			call := workflow.ToolCall{ID: id, Name: name, Args: args}
			st.calls[id] = call
			return []workflow.Chunk{chunk(workflow.ChunkToolCall, call)}
		case "tool_result":
			id := str("id")
			call, ok := st.calls[id]
			if !ok {
				if call.Name = str("name"); call.Name == "" {
					return nil // a result of no call Drydock saw and with no name: nothing an observer can join
				}
				id = ""
			}
			isErr := false
			if v, ok := get("ok"); ok {
				b, isBool := v.(bool)
				isErr = isBool && !b
			}
			return []workflow.Chunk{chunk(workflow.ChunkToolResult, workflow.ToolResult{ID: id, Name: call.Name, Args: call.Args, Output: str("output"), IsError: isErr})}
		case "usage":
			st.usage.InputTokens += num("input")
			st.usage.OutputTokens += num("output")
			st.usage.CachedTokens += num("cached")
			if v, ok := get("cost"); ok {
				if f, ok := v.(float64); ok {
					st.usage.CostUSD += f
				}
			}
			return nil
		case "result":
			st.result = &struct{ status, err string }{status: str("status"), err: str("error")}
			return nil
		case "error":
			if m := str("message"); m != "" {
				return []workflow.Chunk{chunk(workflow.ChunkError, workflow.ErrorChunk{Message: m})}
			}
			return nil
		case "session":
			id := str("id")
			if id == "" {
				return nil
			}
			s.mu.Lock()
			s.sid = id
			s.mu.Unlock()
			return []workflow.Chunk{chunk(workflow.ChunkSession, workflow.SessionChunk{SessionID: id, Model: str("model")})}
		}
	}
	if c, ok := s.drift.once(typ); ok {
		return []workflow.Chunk{c}
	}
	return nil
}

// lookup reads a field path ($.a.b) in a JSON object.
func lookup(obj map[string]any, path string) (any, bool) {
	var cur any = obj
	for _, step := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[step]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// Interrupt ends the running turn's process; the turn ends with stop reason "cancelled" and the session takes the next
// message. With no turn running it does nothing.
func (s *customSession) Interrupt(ctx context.Context) error {
	s.mu.Lock()
	p := s.cur
	if p != nil {
		s.interrupted = true
	}
	s.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.Kill(true)
}

// Stop ends the running turn, if any, and the session.
func (s *customSession) Stop(ctx context.Context) error {
	s.mu.Lock()
	first := !s.stopping
	s.stopping = true
	p := s.cur
	s.mu.Unlock()
	var err error
	if p != nil {
		err = p.Kill(true)
	}
	s.markStopped()
	if first {
		// the output closes once the turn's reader is done with it (it gives up emitting now that the session is stopped)
		safe.Go("headless.custom.Stop", func() {
			s.turns.Wait()
			s.endOnce.Do(func() {
				close(s.out)
				close(s.ended)
			})
		})
	}
	select {
	case <-s.ended:
	case <-time.After(10 * time.Second):
	case <-ctx.Done():
	}
	return err
}
