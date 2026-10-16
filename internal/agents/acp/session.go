package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Session is one ACP agent process with one ACP session. It implements agents.Session.
//
// Turns: Send starts `session/prompt` in the background; while a turn is running further messages queue and are sent when
// it ends. Interrupt sends `session/cancel` and keeps the process and session alive; the agent answers the pending prompt
// with stopReason "cancelled", which becomes the turn's done chunk.
type Session struct {
	adapter *Adapter
	spec    agents.RunSpec
	pm      proc.Manager
	proc    proc.Proc
	conn    *acp.ClientSideConnection
	guard   *guard
	terms   *terminals

	out    chan workflow.Chunk
	emitMu sync.RWMutex
	closed bool
	stopCh chan struct{}
	stop   sync.Once

	ctx    context.Context // session lifetime; cancelled by Stop
	cancel context.CancelFunc
	wg     sync.WaitGroup // running prompt goroutines

	exited chan struct{}
	ei     proc.ExitInfo
	errBuf tail

	mu         sync.Mutex
	info       AgentInfo
	sinfo      SessionInfo
	sid        acp.SessionId
	model      string
	introSent  bool // spec.SystemAppend has been put in front of a prompt (ACP has no system prompt channel)
	dead       bool // the connection ended; no new turns
	turnActive bool
	cancelReq  bool
	queue      []string
	replaying  bool
	msg        strings.Builder
	msgID      string
	tools      map[acp.ToolCallId]*toolState
	toolOrder  []acp.ToolCallId
	usage      usageTracker
	usageSeen  bool
	costSeen   bool
	turns      int
	cancelled  bool
	perms      map[string]context.CancelFunc
	permSeq    int
	// compactions counts the context compactions the agent reported for this session (ext.go); childUpdates counts the
	// sub-agent updates that arrived on the child-session extension notification.
	compactions  int
	childUpdates int
}

var _ agents.Session = (*Session)(nil)

type tail struct {
	mu sync.Mutex
	b  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 8192 {
		t.b = t.b[len(t.b)-8192:]
	}
	return len(p), nil
}
func (t *tail) String() string { t.mu.Lock(); defer t.mu.Unlock(); return string(t.b) }

func newSession(a *Adapter, pm proc.Manager, p proc.Proc, g *guard, spec agents.RunSpec) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{adapter: a, spec: spec, pm: pm, proc: p, guard: g, out: make(chan workflow.Chunk, 256), stopCh: make(chan struct{}),
		ctx: ctx, cancel: cancel, exited: make(chan struct{}), tools: map[acp.ToolCallId]*toolState{}, perms: map[string]context.CancelFunc{}}
	s.terms = newTerminals(s)
	if spec.ResumeID != "" {
		// the session's cumulative cost so far, so the first turn of this process records only its own share (usage.go)
		s.usage.setBaseline(spec.UsageBaseline)
	}
	var stdout io.Reader = p.Stdout()
	if declaresChildUpdates(a.cfg.ClientMeta) {
		stdout = newExtRenamer(stdout)
	}
	s.conn = acp.NewClientSideConnection(&client{s: s}, p.Stdin(), stdout)
	if a.cfg.Logger != nil {
		s.conn.SetLogger(a.cfg.Logger)
	} else {
		s.conn.SetLogger(discardLogger())
	}
	safe.Go("acp.newSession", func() { s.ei = <-p.Wait(); close(s.exited) })
	safe.Go("acp.newSession#2", func() { _, _ = bufio.NewReader(p.Stderr()).WriteTo(&s.errBuf) })
	safe.Go("acp.newSession#3", func() { s.finish() })
	return s
}

// finish closes Output once the connection has ended and every running turn has delivered its last chunk.
func (s *Session) finish() {
	<-s.conn.Done()
	s.mu.Lock()
	s.dead = true
	s.mu.Unlock()
	s.wg.Wait()
	s.emitMu.Lock()
	s.closed = true
	close(s.out)
	s.emitMu.Unlock()
}

func (s *Session) emit(c workflow.Chunk) {
	s.emitMu.RLock()
	defer s.emitMu.RUnlock()
	if s.closed {
		return
	}
	select {
	case s.out <- c:
	case <-s.stopCh:
	}
}

func (s *Session) emitKind(kind string, v any) { s.emit(workflow.NewChunk(kind, v)) }

// ID is the ACP session id.
func (s *Session) ID() string { s.mu.Lock(); defer s.mu.Unlock(); return string(s.sid) }

// Output implements agents.Session.
func (s *Session) Output() <-chan workflow.Chunk { return s.out }

// Wait implements agents.Session.
func (s *Session) Wait() <-chan proc.ExitInfo {
	ch := make(chan proc.ExitInfo, 1)
	safe.Go("acp.Session.Wait", func() { <-s.exited; ch <- s.ei })
	return ch
}

// Stderr implements agents.Session.
func (s *Session) Stderr() string { return s.errBuf.String() }

// PID implements agents.Session.
func (s *Session) PID() int { return s.proc.PID() }

// Info returns what the agent reported in `initialize`.
func (s *Session) Info() AgentInfo { s.mu.Lock(); defer s.mu.Unlock(); return s.info }

// SessionInfo returns the model/mode/effort choices the agent exposed in session/new.
func (s *Session) SessionInfo() SessionInfo { s.mu.Lock(); defer s.mu.Unlock(); return s.sinfo }

// Compactions is how many context compactions the agent reported for this session so far.
func (s *Session) Compactions() int { s.mu.Lock(); defer s.mu.Unlock(); return s.compactions }

// ChildUpdates is how many sub-agent updates arrived on the child-session extension notification so far.
func (s *Session) ChildUpdates() int { s.mu.Lock(); defer s.mu.Unlock(); return s.childUpdates }

// Capabilities merges the adapter's declared capabilities with this session's observations.
func (s *Session) Capabilities() agents.Capabilities {
	return Capabilities(s.adapter.cfg.Declared, s.observedNow())
}

func (s *Session) observedNow() Observed {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Observed{Info: s.info, Session: s.sinfo, Usage: s.usageSeen, Cost: s.costSeen, Turns: s.turns, Cancelled: s.cancelled}
}

// openSession creates (or resumes/loads) the ACP session and applies the requested model, effort and mode where the agent
// exposes them. A requested setting the agent does not expose is reported as a non-fatal error chunk, never silently dropped.
func (s *Session) openSession(ctx context.Context) error {
	servers, warns := s.mcpServers()
	for _, w := range warns {
		s.emitKind(workflow.ChunkError, workflow.ErrorChunk{Message: w})
	}
	cwd := s.guard.root
	var (
		modes   *acp.SessionModeState
		options []acp.SessionConfigOption
		sid     acp.SessionId
	)
	switch {
	case s.spec.ResumeID != "":
		sid = acp.SessionId(s.spec.ResumeID)
		switch {
		case s.info.Resume:
			r, err := s.conn.ResumeSession(ctx, acp.ResumeSessionRequest{SessionId: sid, Cwd: cwd, McpServers: servers})
			if err != nil {
				return fmt.Errorf("session/resume: %w", err)
			}
			modes, options = r.Modes, r.ConfigOptions
		case s.info.LoadSession:
			s.mu.Lock()
			s.replaying, s.sid = true, sid
			s.mu.Unlock()
			r, err := s.conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: sid, Cwd: cwd, McpServers: servers})
			s.mu.Lock()
			s.replaying = false
			s.mu.Unlock()
			if err != nil {
				return fmt.Errorf("session/load: %w", err)
			}
			modes, options = r.Modes, r.ConfigOptions
		default:
			return errors.New("this agent did not advertise loadSession or session/resume, so a session cannot be resumed")
		}
	default:
		r, err := s.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: servers})
		if err != nil {
			return fmt.Errorf("session/new: %w", err)
		}
		sid, modes, options = r.SessionId, r.Modes, r.ConfigOptions
	}
	s.mu.Lock()
	s.sid = sid
	s.sinfo = readSessionInfo(modes, options)
	si := s.sinfo
	s.mu.Unlock()
	return s.applyRequested(ctx, si)
}

// readSessionInfo extracts the selectable models, efforts and modes.
func readSessionInfo(modes *acp.SessionModeState, options []acp.SessionConfigOption) SessionInfo {
	var si SessionInfo
	if modes != nil {
		si.CurrentMode = string(modes.CurrentModeId)
		for _, m := range modes.AvailableModes {
			si.Modes = append(si.Modes, string(m.Id))
		}
	}
	for _, o := range options {
		if o.Select == nil || o.Select.Category == nil {
			continue
		}
		vals := selectValues(o.Select.Options)
		switch *o.Select.Category {
		case acp.SessionConfigOptionCategoryModel:
			si.ModelOption, si.Models, si.CurrentModel = string(o.Select.Id), vals, string(o.Select.CurrentValue)
		case acp.SessionConfigOptionCategoryThoughtLevel:
			si.EffortOption, si.Efforts = string(o.Select.Id), vals
		case acp.SessionConfigOptionCategoryMode:
			si.ModeOption = string(o.Select.Id)
			if len(si.Modes) == 0 {
				si.Modes = vals
				si.CurrentMode = string(o.Select.CurrentValue)
			}
		}
	}
	return si
}

func selectValues(o acp.SessionConfigSelectOptions) []string {
	var out []string
	if o.Ungrouped != nil {
		for _, v := range *o.Ungrouped {
			out = append(out, string(v.Value))
		}
	}
	if o.Grouped != nil {
		for _, g := range *o.Grouped {
			for _, v := range g.Options {
				out = append(out, string(v.Value))
			}
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Session) warn(msg string) {
	s.emitKind(workflow.ChunkError, workflow.ErrorChunk{Message: msg})
}

// applyRequested sets model, effort and permission mode when the agent exposes them. Values are validated against the
// agent's own lists; the request is never forwarded blindly. A requested model that an agent with a model choice does not
// offer, or that it refuses, ends the start: running on the agent's default instead could put a cheap-tier turn on an
// expensive model (or the reverse) without anyone deciding it. An agent with no model choice at all runs its own model, with
// a warning.
func (s *Session) applyRequested(ctx context.Context, si SessionInfo) error {
	if m := s.spec.Model; m != "" && si.ModelOption != "" {
		if len(si.Models) > 0 && !contains(si.Models, m) {
			return fmt.Errorf("model %q was requested but this agent offers %s; choose one of them for the agent", m, strings.Join(si.Models, ", "))
		}
		if _, err := s.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: s.sid, ConfigId: acp.SessionConfigId(si.ModelOption), Value: acp.SessionConfigValueId(m)}}); err != nil {
			return fmt.Errorf("setting the model to %q failed: %w", m, err)
		}
	}
	setOption := func(what, option, value string, allowed []string) {
		if value == "" {
			return
		}
		if option == "" || (len(allowed) > 0 && !contains(allowed, value)) {
			s.warn(fmt.Sprintf("%s %q was requested but this agent does not offer it (offered: %s); the agent's default is in use", what, value, strings.Join(allowed, ", ")))
			return
		}
		_, err := s.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: s.sid, ConfigId: acp.SessionConfigId(option), Value: acp.SessionConfigValueId(value)}})
		if err != nil {
			s.warn(fmt.Sprintf("setting %s to %q failed: %v", what, value, err))
		}
	}
	if s.spec.Model != "" && si.ModelOption == "" {
		s.warn(fmt.Sprintf("model %q was requested but this agent has no model choice; its own model is in use", s.spec.Model))
	}
	s.mu.Lock()
	s.model = si.CurrentModel
	if s.spec.Model != "" && si.ModelOption != "" && contains(si.Models, s.spec.Model) {
		s.model = s.spec.Model
	}
	s.mu.Unlock()
	setOption("effort", si.EffortOption, s.spec.Effort, si.Efforts)
	if m := s.spec.PermissionMode; m != "" {
		switch {
		case si.ModeOption != "":
			setOption("permission mode", si.ModeOption, m, si.Modes)
		case contains(si.Modes, m):
			if _, err := s.conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: s.sid, ModeId: acp.SessionModeId(m)}); err != nil {
				s.warn(fmt.Sprintf("setting permission mode %q failed: %v", m, err))
			}
		default:
			s.warn(fmt.Sprintf("permission mode %q was requested but this agent offers: %s", m, strings.Join(si.Modes, ", ")))
		}
	}
	return nil
}

// mcpServers reads RunSpec.MCPConfigPath (the Claude-style `{"mcpServers":{name:{...}}}` file Drydock writes per run) and
// converts each entry to an ACP server the agent said it can use. Entries needing an unadvertised transport are skipped
// with a warning.
func (s *Session) mcpServers() ([]acp.McpServer, []string) {
	servers := []acp.McpServer{}
	if s.spec.MCPConfigPath == "" {
		return servers, nil
	}
	b, err := os.ReadFile(s.spec.MCPConfigPath)
	if err != nil {
		return servers, []string{"MCP config unreadable, no MCP servers attached: " + err.Error()}
	}
	var cfg struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return servers, []string{"MCP config is not valid JSON, no MCP servers attached: " + err.Error()}
	}
	var warns []string
	for _, name := range sortedKeys(cfg.Servers) {
		e := cfg.Servers[name]
		switch {
		case e.Command != "" && (e.Type == "" || e.Type == "stdio"):
			env := []acp.EnvVariable{}
			for _, k := range sortedKeys(e.Env) {
				env = append(env, acp.EnvVariable{Name: k, Value: e.Env[k]})
			}
			args := e.Args
			if args == nil {
				args = []string{}
			}
			servers = append(servers, acp.McpServer{Stdio: &acp.McpServerStdio{Name: name, Command: e.Command, Args: args, Env: env}})
		case e.URL != "":
			hdrs := []acp.HttpHeader{}
			for _, k := range sortedKeys(e.Headers) {
				hdrs = append(hdrs, acp.HttpHeader{Name: k, Value: e.Headers[k]})
			}
			if e.Type == "sse" {
				if !s.info.MCPSSE {
					warns = append(warns, fmt.Sprintf("MCP server %q needs sse, which this agent did not advertise; skipped", name))
					continue
				}
				servers = append(servers, acp.McpServer{Sse: &acp.McpServerSseInline{Name: name, Url: e.URL, Headers: hdrs, Type: "sse"}})
				continue
			}
			if !s.info.MCPHTTP {
				warns = append(warns, fmt.Sprintf("MCP server %q needs http, which this agent did not advertise; skipped", name))
				continue
			}
			servers = append(servers, acp.McpServer{Http: &acp.McpServerHttpInline{Name: name, Url: e.URL, Headers: hdrs, Type: "http"}})
		default:
			warns = append(warns, fmt.Sprintf("MCP server %q has an unsupported definition; skipped", name))
		}
	}
	return servers, warns
}

// Send implements agents.Session: it starts a turn, or queues the message behind the running one.
func (s *Session) Send(ctx context.Context, msg string) error {
	s.mu.Lock()
	if s.dead || s.sid == "" {
		s.mu.Unlock()
		return errors.New("acp: session is not running")
	}
	// ACP has no system-prompt channel, so the standing instructions go in front of the first prompt of the session
	if !s.introSent && strings.TrimSpace(s.spec.SystemAppend) != "" {
		s.introSent = true
		msg = strings.TrimSpace(s.spec.SystemAppend) + "\n\n---\n\n" + msg
	}
	if s.turnActive {
		s.queue = append(s.queue, msg)
		s.mu.Unlock()
		return nil
	}
	s.turnActive, s.cancelReq = true, false
	s.wg.Add(1)
	s.mu.Unlock()
	safe.Go("acp.Session.Send", func() { s.runTurn(msg) })
	return nil
}

func (s *Session) runTurn(msg string) {
	defer s.wg.Done()
	for {
		resp, err := s.conn.Prompt(s.ctx, acp.PromptRequest{SessionId: s.sid, Prompt: []acp.ContentBlock{acp.TextBlock(msg)}})
		s.endTurn(resp, err)
		s.mu.Lock()
		if len(s.queue) == 0 || s.dead {
			s.turnActive = false
			s.mu.Unlock()
			return
		}
		msg, s.queue = s.queue[0], s.queue[1:]
		s.cancelReq = false
		s.mu.Unlock()
	}
}

// endTurn closes out a turn: held tool calls, the full assistant message, usage, then done.
func (s *Session) endTurn(resp acp.PromptResponse, err error) {
	s.flushTools()
	s.flushMessage()
	s.mu.Lock()
	model := s.model
	cancelReq := s.cancelReq
	s.mu.Unlock()
	stop := string(resp.StopReason)
	if err != nil {
		stop = "error"
		switch {
		case s.ctx.Err() != nil || cancelReq && isCancel(err):
			stop = string(acp.StopReasonCancelled)
		default:
			select {
			case <-s.conn.Done():
				s.emitKind(workflow.ChunkError, workflow.ErrorChunk{Message: "connection to the agent ended during the turn: " + err.Error(), Fatal: true})
			default:
				s.emitKind(workflow.ChunkError, workflow.ErrorChunk{Message: "prompt failed: " + err.Error()})
			}
		}
	}
	if err == nil {
		s.mu.Lock()
		chunks := s.usage.finishTurn(resp, model)
		if len(chunks) > 0 {
			s.usageSeen = true
			for _, c := range chunks {
				if c.CostUSD > 0 {
					s.costSeen = true
				}
			}
		}
		s.turns++
		if resp.StopReason == acp.StopReasonCancelled {
			s.cancelled = true
		}
		rate := s.usage.rateMeta
		s.usage.rateMeta = nil
		s.mu.Unlock()
		for _, c := range chunks {
			s.emitKind(workflow.ChunkUsage, c)
		}
		if rate != nil {
			if b, merr := json.Marshal(rate); merr == nil {
				s.emit(workflow.Chunk{Kind: workflow.ChunkRateLimit, Data: b})
			}
		}
	}
	s.emitKind(workflow.ChunkDone, workflow.DoneChunk{StopReason: stop})
}

func isCancel(err error) bool {
	var re *acp.RequestError
	return errors.Is(err, context.Canceled) || errors.As(err, &re) && re.Code == -32800
}

// Interrupt implements agents.Session: session/cancel, then answer any pending permission request as cancelled. The
// session stays alive. Messages queued behind the running turn are dropped; the caller re-sends what it still wants.
func (s *Session) Interrupt(ctx context.Context) error {
	s.mu.Lock()
	active := s.turnActive
	if active {
		s.cancelReq = true
	}
	s.queue = nil
	cancels := s.takePerms()
	s.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	if !active {
		return nil
	}
	return s.conn.Cancel(ctx, acp.CancelNotification{SessionId: s.sid})
}

// takePerms detaches the pending-permission cancel funcs. Caller holds s.mu.
func (s *Session) takePerms() []context.CancelFunc {
	var fs []context.CancelFunc
	for id, c := range s.perms {
		fs = append(fs, c)
		delete(s.perms, id)
	}
	return fs
}

// Stop implements agents.Session: cancel the turn, end the process and kill its tree.
func (s *Session) Stop(ctx context.Context) error {
	s.stop.Do(func() {
		s.mu.Lock()
		active := s.turnActive
		s.queue = nil
		cancels := s.takePerms()
		sid := s.sid
		s.mu.Unlock()
		for _, c := range cancels {
			c()
		}
		if active && sid != "" {
			cctx, cancel := context.WithTimeout(ctx, time.Second)
			_ = s.conn.Cancel(cctx, acp.CancelNotification{SessionId: sid})
			cancel()
		}
		close(s.stopCh)
		s.terms.killAll()
		s.cancel()
	})
	_ = s.proc.Stdin().Close()
	var err error
	select {
	case <-s.exited:
	case <-time.After(2 * time.Second):
		err = s.proc.Kill(true)
	case <-ctx.Done():
		err = s.proc.Kill(true)
	}
	s.guard.close()
	return err
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
