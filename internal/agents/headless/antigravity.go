package headless

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Antigravity drives the official `agy` binary the person installed and signed in to (decision D2) in its documented headless
// mode: `agy -p --input-format stream-json --output-format stream-json --mode <mode>`, one process for the session, one user
// message per line on stdin and one `result` per turn (docs/phase0/agent-clis.md section 8: init, step_update, result; exit 3
// with `AGY_ERROR: {...}` on stderr when the agent or the model fails). Print mode cannot ask anyone: a permission it was not
// given is skipped and reported in the result's `denied_actions` (D11), so the session runs in a conservative mode and every
// denied action becomes a permission_denied chunk the person sees.
//
// UNVERIFIED: no agy binary was available. The field names below (step_type, state, text_delta, tool, usage, response,
// denied_actions) are the documentation's words, and the input line is the stream-json user message Claude Code reads, which
// community wrappers of agy send; the --mode value is the CLI's default (it asks, so in print mode it denies). The stand-in
// (fakeagy) is written from the same reading. Interrupt, resume and model selection are unknown and not offered.

// AgyConservativeMode is the --mode value sessions run in. The value set of --mode is unverified (docs/connections.md section
// 15); "default" is the mode that asks before acting, which print mode turns into a refusal.
const AgyConservativeMode = "default"

// AgyEnvPrefixes are the variables an agy process keeps besides the base allowlist when the environment is scrubbed: its own
// settings and a Google API key or Vertex project, which the gate recommends over the account sign-in.
var AgyEnvPrefixes = []string{"AGY_", "ANTIGRAVITY_", "GEMINI_", "GOOGLE_"}

// AntigravityConfig builds the adapter from its manifest (internal/agents/manifests builds it).
type AntigravityConfig struct {
	ID       string
	Binaries []string // executable names, in order
	Exe      string   // overrides the lookup (tests; a custom path)
	// Args builds the argv after the executable from the placeholders (the manifest's HeadlessArgs).
	Args        func(vars map[string]string) []string
	Caps        agents.Capabilities
	InstallHint string
	Detect      func(ctx context.Context) (agents.Detection, error)
	ScrubEnv    bool
	PassEnv     func() []string
}

// AntigravityAdapter implements agents.Adapter for agy.
type AntigravityAdapter struct{ cfg AntigravityConfig }

// NewAntigravity returns the adapter.
func NewAntigravity(cfg AntigravityConfig) *AntigravityAdapter {
	if cfg.ID == "" {
		cfg.ID = "antigravity"
	}
	return &AntigravityAdapter{cfg: cfg}
}

func (a *AntigravityAdapter) ID() string                        { return a.cfg.ID }
func (a *AntigravityAdapter) Capabilities() agents.Capabilities { return a.cfg.Caps }

func (a *AntigravityAdapter) executable() (string, error) {
	if a.cfg.Exe != "" {
		return a.cfg.Exe, nil
	}
	return findProgram(a.cfg.Binaries)
}

// Detect uses the manifest's detector (internal/connections) when it was given one; else it only looks for the program.
func (a *AntigravityAdapter) Detect(ctx context.Context) (agents.Detection, error) {
	if a.cfg.Detect != nil {
		return a.cfg.Detect(ctx)
	}
	return lookDetect(ctx, a.cfg.ID, a.cfg.InstallHint, a.executable)
}

// Args is the argv (after the executable) of a session.
func (a *AntigravityAdapter) Args(spec agents.RunSpec) []string {
	vars := map[string]string{"conservative_mode": AgyConservativeMode, "effort": spec.Effort, "cwd": spec.Cwd}
	if a.cfg.Args != nil {
		return append(a.cfg.Args(vars), spec.ExtraArgs...)
	}
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--mode", AgyConservativeMode}
	if spec.Effort != "" {
		args = append(args, "--effort", spec.Effort)
	}
	return append(args, spec.ExtraArgs...)
}

func (a *AntigravityAdapter) Start(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (agents.Session, error) {
	if err := agents.CheckRunMode(a, spec); err != nil {
		return nil, err
	}
	if spec.ResumeID != "" {
		return nil, fmt.Errorf("%s: resuming a session is not available in agy's headless mode (unverified)", a.cfg.ID)
	}
	exe, err := a.executable()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", a.cfg.ID, err)
	}
	env := childEnv(a.cfg.ScrubEnv, a.cfg.PassEnv, AgyEnvPrefixes, spec.Env)
	p, err := pm.Start(ctx, proc.Spec{Path: exe, Args: a.Args(spec), Dir: spec.Cwd, Env: env, Label: "agent:" + spec.AgentID})
	if err != nil {
		return nil, err
	}
	s := &agySession{id: a.cfg.ID, p: p, stream: newStream(), exited: make(chan struct{})}
	safe.Go("headless.agy.wait", func() { s.ei = <-p.Wait(); close(s.exited) })
	safe.Go("headless.agy.stdout", s.readStdout)
	safe.Go("headless.agy.stderr", func() { _, _ = bufio.NewReader(p.Stderr()).WriteTo(&s.errTail) })
	return s, nil
}

type agySession struct {
	id string
	p  proc.Proc
	stream
	exited chan struct{}
	ei     proc.ExitInfo
	wmu    sync.Mutex
	mu     sync.Mutex
	sid    string
	turn   *agyTurn // the turn in progress (nil between turns)
	drift  drift
}

// agyTurn is what one turn has said so far.
type agyTurn struct {
	text  strings.Builder
	usage workflow.UsageChunk
	tools map[string]workflow.ToolCall
	n     int // tool calls without an id get one of their own
}

func (s *agySession) ID() string                    { s.mu.Lock(); defer s.mu.Unlock(); return s.sid }
func (s *agySession) Output() <-chan workflow.Chunk { return s.out }
func (s *agySession) Stderr() string                { return s.errTail.String() }
func (s *agySession) PID() int                      { return s.p.PID() }

func (s *agySession) Wait() <-chan proc.ExitInfo {
	ch := make(chan proc.ExitInfo, 1)
	safe.Go("headless.agy.Wait", func() { <-s.exited; ch <- s.ei })
	return ch
}

func (s *agySession) Send(ctx context.Context, msg string) error {
	s.mu.Lock()
	if s.turn == nil {
		s.turn = &agyTurn{tools: map[string]workflow.ToolCall{}}
	}
	s.mu.Unlock()
	b, err := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": msg}})
	if err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err = s.p.Stdin().Write(append(b, '\n'))
	return err
}

// Interrupt is not available: agy's print mode documents no way to cancel a turn and keep the session (Ctrl+C ends the
// process with a non-zero code), so Drydock does not claim one.
func (s *agySession) Interrupt(ctx context.Context) error {
	return fmt.Errorf("%s: interrupting a turn is not available in agy's headless mode; stop the session instead", s.id)
}

func (s *agySession) Stop(ctx context.Context) error {
	defer s.markStopped()
	_ = s.p.Stdin().Close()
	select {
	case <-s.exited:
		return nil
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
	}
	return s.p.Kill(true)
}

func (s *agySession) readStdout() {
	defer close(s.out)
	sc := bufio.NewScanner(s.p.Stdout())
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	for sc.Scan() {
		for _, c := range s.parse(sc.Bytes()) {
			if !s.emit(c) {
				return
			}
		}
	}
	// the output ended: the process exited. A turn it did not finish ends here, with what agy said on stderr.
	select {
	case <-s.exited:
	case <-time.After(2 * time.Second):
	}
	s.mu.Lock()
	open := s.turn != nil
	s.turn = nil
	s.mu.Unlock()
	if open {
		msg := "agy ended before the turn finished"
		if e := agyError(s.errTail.String()); e != "" {
			msg += ": " + e
		} else if s.ei.Code != 0 {
			msg += fmt.Sprintf(" (exit %d)", s.ei.Code)
		}
		s.emit(chunk(workflow.ChunkError, workflow.ErrorChunk{Message: msg, Fatal: true}))
		s.emit(chunk(workflow.ChunkDone, workflow.DoneChunk{StopReason: "error"}))
	}
}

// agyError is the message of the `AGY_ERROR: {...}` line agy prints on stderr when it fails (exit 3).
func agyError(stderr string) string {
	for _, l := range strings.Split(stderr, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), "AGY_ERROR:"); ok {
			rest = strings.TrimSpace(rest)
			var e struct {
				Message string `json:"message"`
				Code    any    `json:"code"`
			}
			if json.Unmarshal([]byte(rest), &e) == nil && e.Message != "" {
				if e.Code != nil {
					return fmt.Sprintf("%s (%v)", e.Message, e.Code)
				}
				return e.Message
			}
			return firstLine(rest)
		}
	}
	return ""
}

// agyEvent is one line of agy's stream-json output, as the documentation names its fields.
type agyEvent struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
	Init           *struct {
		Cwd            string   `json:"cwd"`
		Tools          []string `json:"tools"`
		PermissionMode string   `json:"permission_mode"`
		Model          string   `json:"model"`
	} `json:"init"`
	// step_update
	StepID    string    `json:"step_id"`
	StepType  string    `json:"step_type"`
	State     string    `json:"state"`
	TextDelta string    `json:"text_delta"`
	Tool      *agyTool  `json:"tool"`
	Usage     *agyUsage `json:"usage"`
	// result
	Status        string            `json:"status"`
	Response      *string           `json:"response"`
	Error         string            `json:"error"`
	DeniedActions []agyDeniedAction `json:"denied_actions"`
}

type agyTool struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
	Output string          `json:"output"`
}

type agyUsage struct {
	Input  int64 `json:"input_tokens"`
	Output int64 `json:"output_tokens"`
	Cached int64 `json:"cached_tokens"`
}

type agyDeniedAction struct {
	Tool   string          `json:"tool"`
	Args   json.RawMessage `json:"args"`
	Reason string          `json:"reason"`
}

// parse turns one output line into chunks. It never panics on what it does not know: an unreadable line or an unknown event
// type is a drift warning, once per kind.
func (s *agySession) parse(line []byte) []workflow.Chunk {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 {
		return nil
	}
	var e agyEvent
	if json.Unmarshal(line, &e) != nil || e.Type == "" {
		if c, ok := s.drift.once("(unreadable line)"); ok {
			return []workflow.Chunk{c}
		}
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.turn
	if t == nil && e.Type != "init" {
		t = &agyTurn{tools: map[string]workflow.ToolCall{}} // output before a message was sent: read it as a turn anyway
		s.turn = t
	}
	var out []workflow.Chunk
	switch e.Type {
	case "init":
		s.sid = e.ConversationID
		sc := workflow.SessionChunk{SessionID: e.ConversationID}
		if e.Init != nil {
			sc.Tools, sc.PermissionMode, sc.Model = len(e.Init.Tools), e.Init.PermissionMode, e.Init.Model
		}
		out = append(out, chunk(workflow.ChunkSession, sc))
	case "step_update":
		if e.Usage != nil {
			t.usage.InputTokens += e.Usage.Input
			t.usage.OutputTokens += e.Usage.Output
			t.usage.CachedTokens += e.Usage.Cached
		}
		if e.TextDelta != "" {
			kind := workflow.ChunkText
			if e.StepType == "thinking" {
				kind = workflow.ChunkThinking
			} else {
				t.text.WriteString(e.TextDelta)
			}
			out = append(out, chunk(kind, workflow.TextDelta{Text: e.TextDelta}))
		}
		if e.Tool != nil && e.Tool.Name != "" {
			id := e.Tool.ID
			if id == "" {
				id = e.StepID
			}
			if id == "" {
				t.n++
				id = fmt.Sprintf("agy-tool-%d", t.n)
			}
			call, known := t.tools[id]
			if !known {
				call = workflow.ToolCall{ID: id, Name: e.Tool.Name, Args: rawOrNil(e.Tool.Args)}
				t.tools[id] = call
				out = append(out, chunk(workflow.ChunkToolCall, call))
			}
			if e.State == "done" || e.State == "error" {
				out = append(out, chunk(workflow.ChunkToolResult, workflow.ToolResult{ID: id, Name: call.Name, Args: call.Args, Output: e.Tool.Output, IsError: e.State == "error"}))
				delete(t.tools, id)
			}
		} else if e.TextDelta == "" && e.Usage == nil && !knownQuietStep(e.StepType) {
			if c, ok := s.drift.once("step_update " + e.StepType); ok {
				out = append(out, c)
			}
		}
	case "result":
		text := t.text.String()
		if e.Response != nil && strings.TrimSpace(*e.Response) != "" {
			text = *e.Response
		}
		if text != "" {
			out = append(out, chunk(workflow.ChunkMessageEnd, workflow.TextDelta{Text: text}))
		}
		for _, d := range e.DeniedActions {
			reason := "agy's print mode refused it: nobody could be asked (decision D11)"
			if d.Reason != "" {
				reason = d.Reason
			}
			out = append(out, chunk(workflow.ChunkPermissionDenied, workflow.PermissionDenial{ToolName: d.Tool, Input: rawOrNil(d.Args), Reason: reason}))
		}
		if e.Usage != nil { // the result's totals win over the steps' sum
			t.usage = workflow.UsageChunk{InputTokens: e.Usage.Input, OutputTokens: e.Usage.Output, CachedTokens: e.Usage.Cached}
		}
		if t.usage.InputTokens+t.usage.OutputTokens+t.usage.CachedTokens > 0 {
			t.usage.Source = "reported"
			out = append(out, chunk(workflow.ChunkUsage, t.usage))
		}
		status := strings.ToLower(e.Status)
		if status != "" && status != "success" {
			msg := e.Error
			if msg == "" {
				msg = "agy reported " + e.Status
			}
			out = append(out, chunk(workflow.ChunkError, workflow.ErrorChunk{Message: msg}))
		}
		if status == "" {
			status = "success"
		}
		out = append(out, chunk(workflow.ChunkDone, workflow.DoneChunk{StopReason: status}))
		s.turn = nil
	default:
		if c, ok := s.drift.once(e.Type); ok {
			out = append(out, c)
		}
	}
	return out
}

// knownQuietStep are step types that carry nothing Drydock shows (progress of a step that has no text, tool or usage).
func knownQuietStep(t string) bool {
	switch t {
	case "", "text", "thinking", "tool_call", "planning", "progress":
		return true
	}
	return false
}

// lookDetect is a detection that only looks for the program (used when no manifest detector is wired: tests).
func lookDetect(ctx context.Context, id, hint string, exe func() (string, error)) (agents.Detection, error) {
	d := agents.Detection{Adapter: id, Status: agents.StatusNotInstalled, InstallHint: hint}
	p, err := exe()
	if err != nil {
		d.Problems = append(d.Problems, err.Error())
		return d, nil
	}
	d.Installed, d.Path, d.Status = true, p, agents.StatusReady
	return d, nil
}

var errTurnRunning = errors.New("a turn is still running: wait for it or interrupt it")
