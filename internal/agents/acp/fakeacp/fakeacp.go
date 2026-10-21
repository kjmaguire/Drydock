// Package fakeacp is a scripted ACP agent. It is a real separate process (tests re-exec their own binary, as with
// internal/agents/fakeagent) that speaks ACP over stdio on the real SDK's agent-side connection, so it exercises the real
// client code. It exists to drive protocol paths no installed CLI can be made to take without model credentials (usage
// updates, permission requests, resume capabilities, fs/terminal calls, cancellation); it is never presented as a real agent.
package fakeacp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
)

// Script drives the agent. Turn i handles the i-th prompt; Rules (substring of the prompt) win over Turns; Default handles the rest.
type Script struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`

	LoadSession     bool `json:"loadSession,omitempty"`
	Resume          bool `json:"resume,omitempty"`
	Fork            bool `json:"fork,omitempty"`
	List            bool `json:"list,omitempty"`
	Close           bool `json:"close,omitempty"`
	MCPHTTP         bool `json:"mcpHttp,omitempty"`
	MCPSSE          bool `json:"mcpSse,omitempty"`
	Image           bool `json:"image,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
	AuthRequired    bool `json:"authRequired,omitempty"` // session/new fails with "authentication required"

	Modes   []string `json:"modes,omitempty"`
	Models  []string `json:"models,omitempty"`
	Efforts []string `json:"efforts,omitempty"`
	// ReplayOnLoad are agent messages streamed back during session/load, as real agents replay history.
	ReplayOnLoad []string `json:"replayOnLoad,omitempty"`

	Rules   []Rule   `json:"rules,omitempty"`
	Turns   [][]Step `json:"turns,omitempty"`
	Default []Step   `json:"default,omitempty"`

	PromptUsage *PromptUsage `json:"promptUsage,omitempty"` // PromptResponse.usage
	Quota       *Quota       `json:"quota,omitempty"`       // PromptResponse._meta.quota (Gemini)
	StopReason  string       `json:"stopReason,omitempty"`  // default end_turn
}

// Rule selects steps by prompt content.
type Rule struct {
	Match string `json:"match"`
	Steps []Step `json:"steps"`
}

// PromptUsage is PromptResponse.usage.
type PromptUsage struct {
	In, Out, CachedRead, CachedWrite, Thought int
}

// Quota is Gemini's PromptResponse._meta.quota.
type Quota struct {
	In     int          `json:"in"`
	Out    int          `json:"out"`
	Models []QuotaModel `json:"models,omitempty"`
}

// QuotaModel is one model_usage entry.
type QuotaModel struct {
	Model string `json:"model"`
	In    int    `json:"in"`
	Out   int    `json:"out"`
}

// Step is one scripted action; exactly one field group is used.
type Step struct {
	Say          string       `json:"say,omitempty"`
	Think        string       `json:"think,omitempty"`
	Tool         *Tool        `json:"tool,omitempty"`
	Plan         []string     `json:"plan,omitempty"`
	UsageUpdate  *UsageUpdate `json:"usageUpdate,omitempty"`
	Perm         *Perm        `json:"perm,omitempty"`
	FSRead       *FSOp        `json:"fsRead,omitempty"`
	FSWrite      *FSOp        `json:"fsWrite,omitempty"`
	Term         *TermOp      `json:"term,omitempty"`
	SleepMS      int          `json:"sleepMs,omitempty"`
	Block        bool         `json:"block,omitempty"` // wait for session/cancel
	Exit         int          `json:"exit,omitempty"`  // exit the process mid-turn
	ReportConfig bool         `json:"reportConfig,omitempty"`
	// MCPCall connects to an MCP server the client attached in session/new (the way a real ACP agent would) and calls a tool,
	// or lists the tools when Tool is empty. The outcome is reported as an agent message.
	MCPCall     *MCPCall       `json:"mcpCall,omitempty"`
	ErrorPrompt string         `json:"errorPrompt,omitempty"` // fail the prompt request with this message
	Rate        map[string]any `json:"rate,omitempty"`        // _meta["_claude/rateLimit"] on the next usage update
	// Compaction reports a context compaction the way OpenCode 2 does to a client that did not advertise ACP's own
	// compaction capability: a session_info_update whose _meta["opencode/compaction"] carries the status.
	Compaction *Compaction `json:"compaction,omitempty"`
	// Child runs a sub-agent the way OpenCode's task tool does (see Child).
	Child *Child `json:"child,omitempty"`
	// Notify sends an arbitrary notification (an extension the client may not know), written as the agent wrote it.
	Notify *Notify `json:"notify,omitempty"`
	// ReportClientMeta reports the clientCapabilities._meta keys of initialize as an agent message ("clientMeta=k1,k2").
	ReportClientMeta bool `json:"reportClientMeta,omitempty"`
}

// Compaction is OpenCode's compaction marker.
type Compaction struct {
	Status string `json:"status"`          // started | completed | failed
	Child  string `json:"child,omitempty"` // the marker of a child session forwarded on the session's stream
}

// Child is a sub-agent's work, forwarded as OpenCode 2 does (its acp/child.ts): on the notification
// "opencode/session/child_update" when the client sent _meta["opencode/child-session-updates"], else on the session's own
// stream with the child's id in front of each tool call id and _meta["opencode/child-session"] set.
type Child struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	Say   string `json:"say,omitempty"`  // the child's own text
	Tool  *Tool  `json:"tool,omitempty"` // one tool call of the child
	// Root overrides rootSessionId (another session's child).
	Root string `json:"root,omitempty"`
}

// Notify is a raw notification.
type Notify struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// Tool is a tool call: tool_call, then tool_call_update to completed (or failed).
type Tool struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Kind      string         `json:"kind,omitempty"`
	Input     any            `json:"input,omitempty"`
	Late      bool           `json:"late,omitempty"`   // announce without input; the input arrives with the update
	Output    string         `json:"output,omitempty"` // text content of the final update
	RawOutput any            `json:"rawOutput,omitempty"`
	Fail      bool           `json:"fail,omitempty"`
	Pending   bool           `json:"pending,omitempty"` // never complete it
	Locations []string       `json:"locations,omitempty"`
	Meta      map[string]any `json:"meta,omitempty"`
}

// UsageUpdate is a usage_update notification.
type UsageUpdate struct {
	Used int      `json:"used"`
	Size int      `json:"size"`
	Cost *float64 `json:"cost,omitempty"` // cumulative USD
	// TurnCost is this turn's cost: the agent adds it to the session's cumulative cost and reports the total, as OpenCode
	// does (the stored session's cost). With DRYDOCK_FAKE_ACP_STATE_DIR set the total outlives the process, so a process that
	// loads or resumes the session reports a total that includes the earlier processes.
	TurnCost *float64 `json:"turnCost,omitempty"`
}

// Perm is a tool call that needs permission. The outcome is reported back as an agent message.
type Perm struct {
	Tool    Tool         `json:"tool"`
	Options []PermOption `json:"options,omitempty"` // default: allow_once + reject_once
}

// PermOption is one offered option.
type PermOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// FSOp asks the client to read or write a file; the result is reported as an agent message.
type FSOp struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
}

// TermOp asks the client to run a command in a terminal and report its output.
type TermOp struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Cwd     string   `json:"cwd,omitempty"`
}

// LoadScript reads the JSON script named by DRYDOCK_FAKE_ACP_SCRIPT.
func LoadScript() (Script, error) {
	var sc Script
	path := os.Getenv("DRYDOCK_FAKE_ACP_SCRIPT")
	if path == "" {
		return sc, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return sc, err
	}
	return sc, json.Unmarshal(b, &sc)
}

// Run serves ACP on in/out until the peer disconnects. It returns the process exit code.
func Run(args []string, in io.Reader, out io.Writer) int {
	for _, a := range args {
		if a == "--version" {
			fmt.Fprintln(out, "fakeacp 0.0.1")
			return 0
		}
	}
	sc, err := LoadScript()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakeacp: bad script:", err)
		return 2
	}
	lw := &lockedWriter{w: out}
	ag := &agent{script: sc, sessions: map[string]*sess{}, raw: lw}
	asc := acp.NewAgentSideConnection(ag, lw, in)
	asc.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ag.conn = asc
	select {
	case <-asc.Done():
	case code := <-ag.exit():
		return code
	}
	return 0
}

type sess struct {
	id      string
	cost    float64 // cumulative session cost (UsageUpdate.TurnCost)
	cwd     string
	mcp     []string
	servers []acp.McpServer
	model   string
	mode    string
	effort  string
	resumed string
	cancel  context.CancelFunc
	turn    int
}

type agent struct {
	conn       *acp.AgentSideConnection
	raw        *lockedWriter // the connection's own writer, for notifications the SDK refuses to send (Notify, Child)
	script     Script
	mu         sync.Mutex
	sessions   map[string]*sess
	seq        int
	exitCh     chan int
	once       sync.Once
	clientMeta map[string]any
}

// lockedWriter makes each Write (the SDK writes one whole message per call) atomic, so raw notifications never interleave.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// notify writes a JSON-RPC notification as it is, whatever its method name.
func (a *agent) notify(method string, params any) {
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return
	}
	_, _ = a.raw.Write(append(b, '\n'))
}

// sessionCostPath is the file that keeps a session's cumulative cost under DRYDOCK_FAKE_ACP_STATE_DIR ("" without it).
func sessionCostPath(id string) string {
	dir := os.Getenv("DRYDOCK_FAKE_ACP_STATE_DIR")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, id+".cost")
}

func loadSessionCost(id string) float64 {
	p := sessionCostPath(id)
	if p == "" {
		return 0
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	return v
}

func saveSessionCost(id string, v float64) {
	if p := sessionCostPath(id); p != "" {
		_ = os.WriteFile(p, []byte(strconv.FormatFloat(v, 'g', -1, 64)), 0o644)
	}
}

func (a *agent) exit() chan int {
	a.once.Do(func() { a.exitCh = make(chan int, 1) })
	return a.exitCh
}

func (a *agent) Initialize(_ context.Context, p acp.InitializeRequest) (acp.InitializeResponse, error) {
	a.mu.Lock()
	a.clientMeta = p.ClientCapabilities.Meta
	a.mu.Unlock()
	sc := a.script
	caps := acp.AgentCapabilities{
		LoadSession:        sc.LoadSession,
		McpCapabilities:    acp.McpCapabilities{Http: sc.MCPHTTP, Sse: sc.MCPSSE},
		PromptCapabilities: acp.PromptCapabilities{Image: sc.Image, EmbeddedContext: sc.EmbeddedContext},
	}
	if sc.Resume {
		caps.SessionCapabilities.Resume = &acp.SessionResumeCapabilities{}
	}
	if sc.Fork {
		caps.SessionCapabilities.Fork = &acp.SessionForkCapabilities{}
	}
	if sc.List {
		caps.SessionCapabilities.List = &acp.SessionListCapabilities{}
	}
	if sc.Close {
		caps.SessionCapabilities.Close = &acp.SessionCloseCapabilities{}
	}
	name, ver := sc.Name, sc.Version
	if name == "" {
		name = "fakeacp"
	}
	if ver == "" {
		ver = "0.0.1"
	}
	return acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber, AgentCapabilities: caps,
		AgentInfo: &acp.Implementation{Name: name, Version: ver}, AuthMethods: []acp.AuthMethod{}}, nil
}

func (a *agent) newSessionResponse(id string, s *sess) acp.NewSessionResponse {
	sc := a.script
	r := acp.NewSessionResponse{SessionId: acp.SessionId(id)}
	if len(sc.Modes) > 0 {
		st := &acp.SessionModeState{CurrentModeId: acp.SessionModeId(sc.Modes[0])}
		for _, m := range sc.Modes {
			st.AvailableModes = append(st.AvailableModes, acp.SessionMode{Id: acp.SessionModeId(m), Name: m})
		}
		r.Modes = st
		s.mode = sc.Modes[0]
	}
	add := func(id, name string, cat acp.SessionConfigOptionCategory, vals []string) string {
		if len(vals) == 0 {
			return ""
		}
		var opts acp.SessionConfigSelectOptionsUngrouped
		for _, v := range vals {
			opts = append(opts, acp.SessionConfigSelectOption{Name: v, Value: acp.SessionConfigValueId(v)})
		}
		c := cat
		r.ConfigOptions = append(r.ConfigOptions, acp.SessionConfigOption{Select: &acp.SessionConfigOptionSelect{
			Id: acp.SessionConfigId(id), Name: name, Type: "select", Category: &c, CurrentValue: acp.SessionConfigValueId(vals[0]),
			Options: acp.SessionConfigSelectOptions{Ungrouped: &opts}}})
		return vals[0]
	}
	s.model = add("model", "Model", acp.SessionConfigOptionCategoryModel, sc.Models)
	s.effort = add("effort", "Effort", acp.SessionConfigOptionCategoryThoughtLevel, sc.Efforts)
	return r
}

func mcpNames(ms []acp.McpServer) []string {
	var out []string
	for _, m := range ms {
		switch {
		case m.Stdio != nil:
			out = append(out, "stdio:"+m.Stdio.Name)
		case m.Http != nil:
			out = append(out, "http:"+m.Http.Name)
		case m.Sse != nil:
			out = append(out, "sse:"+m.Sse.Name)
		}
	}
	return out
}

func (a *agent) NewSession(_ context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if a.script.AuthRequired {
		return acp.NewSessionResponse{}, acp.NewAuthRequired(map[string]any{"why": "fakeacp requires authentication"})
	}
	a.mu.Lock()
	a.seq++
	id := fmt.Sprintf("fake-sess-%d", a.seq)
	s := &sess{id: id, cwd: p.Cwd, mcp: mcpNames(p.McpServers), servers: p.McpServers}
	a.sessions[id] = s
	a.mu.Unlock()
	saveSessionCost(id, 0) // a new session starts from nothing, whatever an earlier process kept under the same id
	return a.newSessionResponse(id, s), nil
}

func (a *agent) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	a.mu.Lock()
	s := &sess{id: string(p.SessionId), cost: loadSessionCost(string(p.SessionId)), cwd: p.Cwd, mcp: mcpNames(p.McpServers), resumed: "load:" + string(p.SessionId)}
	a.sessions[string(p.SessionId)] = s
	a.mu.Unlock()
	for _, m := range a.script.ReplayOnLoad {
		_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: p.SessionId, Update: acp.UpdateAgentMessageText(m)})
	}
	r := a.newSessionResponse(string(p.SessionId), s)
	return acp.LoadSessionResponse{Modes: r.Modes, ConfigOptions: r.ConfigOptions}, nil
}

func (a *agent) ResumeSession(_ context.Context, p acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	a.mu.Lock()
	s := &sess{id: string(p.SessionId), cost: loadSessionCost(string(p.SessionId)), cwd: p.Cwd, mcp: mcpNames(p.McpServers), resumed: "resume:" + string(p.SessionId)}
	a.sessions[string(p.SessionId)] = s
	a.mu.Unlock()
	r := a.newSessionResponse(string(p.SessionId), s)
	return acp.ResumeSessionResponse{Modes: r.Modes, ConfigOptions: r.ConfigOptions}, nil
}

func (a *agent) SetSessionMode(_ context.Context, p acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.sessions[string(p.SessionId)]; s != nil {
		s.mode = string(p.ModeId)
	}
	return acp.SetSessionModeResponse{}, nil
}

func (a *agent) SetSessionConfigOption(_ context.Context, p acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p.ValueId == nil {
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams("value id required")
	}
	if s := a.sessions[string(p.ValueId.SessionId)]; s != nil {
		switch string(p.ValueId.ConfigId) {
		case "model":
			s.model = string(p.ValueId.Value)
		case "effort":
			s.effort = string(p.ValueId.Value)
		}
	}
	return acp.SetSessionConfigOptionResponse{}, nil
}

func (a *agent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}
func (a *agent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, acp.NewMethodNotFound(acp.AgentMethodLogout)
}
func (a *agent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}
func (a *agent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{Sessions: []acp.SessionInfo{}}, nil
}

func (a *agent) Cancel(_ context.Context, p acp.CancelNotification) error {
	a.mu.Lock()
	s := a.sessions[string(p.SessionId)]
	var c context.CancelFunc
	if s != nil {
		c = s.cancel
	}
	a.mu.Unlock()
	if c != nil {
		c()
	}
	return nil
}

func promptText(p acp.PromptRequest) string {
	var sb strings.Builder
	for _, b := range p.Prompt {
		if b.Text != nil {
			sb.WriteString(b.Text.Text)
		}
	}
	return sb.String()
}

func (a *agent) steps(s *sess, prompt string) []Step {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range a.script.Rules {
		if strings.Contains(prompt, r.Match) {
			return r.Steps
		}
	}
	i := s.turn
	s.turn++
	if i < len(a.script.Turns) {
		return a.script.Turns[i]
	}
	if len(a.script.Default) > 0 {
		return a.script.Default
	}
	return []Step{{Say: "done"}}
}

func (a *agent) Prompt(_ context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	a.mu.Lock()
	s := a.sessions[string(p.SessionId)]
	a.mu.Unlock()
	if s == nil {
		return acp.PromptResponse{}, acp.NewInvalidParams("unknown session")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	s.cancel = cancel
	a.mu.Unlock()
	defer cancel()
	steps := a.steps(s, promptText(p))
	sid := p.SessionId
	send := func(u acp.SessionUpdate) {
		_ = a.conn.SessionUpdate(context.Background(), acp.SessionNotification{SessionId: sid, Update: u})
	}
	say := func(t string) { send(acp.UpdateAgentMessageText(t)) }
	var rate map[string]any
	for _, st := range steps {
		if ctx.Err() != nil {
			return a.finish(acp.StopReasonCancelled, false), nil
		}
		switch {
		case st.Say != "":
			for _, w := range strings.SplitAfter(st.Say, " ") {
				say(w)
			}
		case st.Think != "":
			send(acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock(st.Think), SessionUpdate: "agent_thought_chunk"}})
		case st.Tool != nil:
			a.runTool(send, st.Tool)
		case len(st.Plan) > 0:
			var es []acp.PlanEntry
			for _, e := range st.Plan {
				es = append(es, acp.PlanEntry{Content: e, Priority: acp.PlanEntryPriorityMedium, Status: acp.PlanEntryStatusPending})
			}
			send(acp.SessionUpdate{Plan: &acp.SessionUpdatePlan{Entries: es, SessionUpdate: "plan"}})
		case st.Rate != nil:
			rate = st.Rate
		case st.UsageUpdate != nil:
			u := &acp.SessionUsageUpdate{SessionUpdate: "usage_update", Used: st.UsageUpdate.Used, Size: st.UsageUpdate.Size}
			if st.UsageUpdate.Cost != nil {
				u.Cost = &acp.Cost{Amount: *st.UsageUpdate.Cost, Currency: "USD"}
			}
			if st.UsageUpdate.TurnCost != nil {
				a.mu.Lock()
				s.cost += *st.UsageUpdate.TurnCost
				total := s.cost
				a.mu.Unlock()
				saveSessionCost(s.id, total)
				u.Cost = &acp.Cost{Amount: total, Currency: "USD"}
			}
			if rate != nil {
				u.Meta = map[string]any{"_claude/rateLimit": rate}
				rate = nil
			}
			send(acp.SessionUpdate{UsageUpdate: u})
		case st.Perm != nil:
			a.runPerm(ctx, sid, send, say, st.Perm)
		case st.FSRead != nil:
			r, err := a.conn.ReadTextFile(ctx, acp.ReadTextFileRequest{SessionId: sid, Path: st.FSRead.Path})
			if err != nil {
				say("fs.read ERR " + err.Error())
			} else {
				say("fs.read OK " + r.Content)
			}
		case st.FSWrite != nil:
			if _, err := a.conn.WriteTextFile(ctx, acp.WriteTextFileRequest{SessionId: sid, Path: st.FSWrite.Path, Content: st.FSWrite.Content}); err != nil {
				say("fs.write ERR " + err.Error())
			} else {
				say("fs.write OK")
			}
		case st.Term != nil:
			a.runTerm(ctx, sid, say, st.Term)
		case st.SleepMS > 0:
			select {
			case <-time.After(time.Duration(st.SleepMS) * time.Millisecond):
			case <-ctx.Done():
			}
		case st.Block:
			<-ctx.Done()
		case st.Exit != 0:
			a.exit() <- st.Exit
			<-ctx.Done()
		case st.MCPCall != nil:
			a.mu.Lock()
			servers := s.servers
			a.mu.Unlock()
			say(callMCP(ctx, servers, st.MCPCall))
		case st.ReportConfig:
			a.mu.Lock()
			say(fmt.Sprintf("config model=%s mode=%s effort=%s mcp=%s cwd=%s resumed=%s", s.model, s.mode, s.effort, strings.Join(s.mcp, ","), s.cwd, s.resumed))
			a.mu.Unlock()
		case st.ErrorPrompt != "":
			return acp.PromptResponse{}, acp.NewInternalError(map[string]any{"error": st.ErrorPrompt})
		case st.Compaction != nil:
			meta := map[string]any{"opencode/compaction": map[string]any{"status": st.Compaction.Status, "messageId": "msg_compaction", "reason": "auto"}}
			if st.Compaction.Child != "" {
				meta["opencode/child-session"] = map[string]any{"id": st.Compaction.Child, "parentID": string(sid), "depth": 1}
			}
			send(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{SessionUpdate: "session_info_update", Meta: meta}})
		case st.Child != nil:
			a.runChild(sid, send, st.Child)
		case st.Notify != nil:
			a.notify(st.Notify.Method, st.Notify.Params)
		case st.ReportClientMeta:
			a.mu.Lock()
			keys := make([]string, 0, len(a.clientMeta))
			for k := range a.clientMeta {
				keys = append(keys, k)
			}
			a.mu.Unlock()
			sort.Strings(keys)
			say("clientMeta=" + strings.Join(keys, ","))
		}
	}
	if ctx.Err() != nil {
		return a.finish(acp.StopReasonCancelled, false), nil
	}
	return a.finish(acp.StopReason(a.stop()), true), nil
}

func (a *agent) stop() string {
	if a.script.StopReason != "" {
		return a.script.StopReason
	}
	return string(acp.StopReasonEndTurn)
}

// finish builds the prompt response; usage is attached only to turns that were not cancelled.
func (a *agent) finish(stop acp.StopReason, withUsage bool) acp.PromptResponse {
	r := acp.PromptResponse{StopReason: stop}
	if !withUsage {
		return r
	}
	if u := a.script.PromptUsage; u != nil {
		pu := &acp.Usage{InputTokens: u.In, OutputTokens: u.Out, TotalTokens: u.In + u.Out}
		if u.CachedRead > 0 {
			pu.CachedReadTokens = acp.Ptr(u.CachedRead)
		}
		if u.CachedWrite > 0 {
			pu.CachedWriteTokens = acp.Ptr(u.CachedWrite)
		}
		if u.Thought > 0 {
			pu.ThoughtTokens = acp.Ptr(u.Thought)
		}
		r.Usage = pu
	}
	if q := a.script.Quota; q != nil {
		var mu []any
		for _, m := range q.Models {
			mu = append(mu, map[string]any{"model": m.Model, "token_count": map[string]any{"input_tokens": m.In, "output_tokens": m.Out}})
		}
		if mu == nil {
			mu = []any{}
		}
		r.Meta = map[string]any{"quota": map[string]any{"token_count": map[string]any{"input_tokens": q.In, "output_tokens": q.Out}, "model_usage": mu}}
	}
	return r
}

func (a *agent) runTool(send func(acp.SessionUpdate), t *Tool) {
	kind := acp.ToolKind(t.Kind)
	if kind == "" {
		kind = acp.ToolKindOther
	}
	var locs []acp.ToolCallLocation
	for _, l := range t.Locations {
		locs = append(locs, acp.ToolCallLocation{Path: l})
	}
	start := acp.SessionUpdateToolCall{SessionUpdate: "tool_call", ToolCallId: acp.ToolCallId(t.ID), Title: t.Title, Kind: kind,
		Status: acp.ToolCallStatusPending, Locations: locs, Meta: t.Meta}
	if !t.Late {
		start.RawInput = t.Input
	}
	send(acp.SessionUpdate{ToolCall: &start})
	if t.Pending {
		return
	}
	upd := acp.SessionToolCallUpdate{SessionUpdate: "tool_call_update", ToolCallId: acp.ToolCallId(t.ID)}
	st := acp.ToolCallStatusCompleted
	if t.Fail {
		st = acp.ToolCallStatusFailed
	}
	upd.Status = &st
	if t.Late {
		upd.RawInput = t.Input
	}
	if t.Output != "" {
		upd.Content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(t.Output))}
	}
	upd.RawOutput = t.RawOutput
	send(acp.SessionUpdate{ToolCallUpdate: &upd})
}

// runChild forwards a sub-agent's text and one tool call as OpenCode 2 does (its acp/child.ts and translate.ts route).
func (a *agent) runChild(sid acp.SessionId, send func(acp.SessionUpdate), c *Child) {
	a.mu.Lock()
	opted, _ := a.clientMeta["opencode/child-session-updates"].(bool)
	a.mu.Unlock()
	child := map[string]any{"id": c.ID, "parentID": string(sid), "depth": 1}
	if c.Title != "" {
		child["title"] = c.Title
	}
	meta := map[string]any{"opencode/child-session": child}
	var updates []acp.SessionUpdate
	if c.Say != "" {
		updates = append(updates, acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{SessionUpdate: "agent_message_chunk",
			Content: acp.TextBlock(c.Say), Meta: meta}})
	}
	if t := c.Tool; t != nil {
		id := acp.ToolCallId(c.ID + ":" + t.ID)
		title := t.Title
		if c.Title != "" {
			title = c.Title + ": " + title
		}
		kind := acp.ToolKind(t.Kind)
		if kind == "" {
			kind = acp.ToolKindOther
		}
		start := acp.SessionUpdateToolCall{SessionUpdate: "tool_call", ToolCallId: id, Title: title, Kind: kind, Status: acp.ToolCallStatusPending,
			RawInput: t.Input, Meta: meta}
		st := acp.ToolCallStatusCompleted
		if t.Fail {
			st = acp.ToolCallStatusFailed
		}
		upd := acp.SessionToolCallUpdate{SessionUpdate: "tool_call_update", ToolCallId: id, Status: &st, Meta: meta}
		if t.Output != "" {
			upd.Content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(t.Output))}
		}
		updates = append(updates, acp.SessionUpdate{ToolCall: &start}, acp.SessionUpdate{ToolCallUpdate: &upd})
	}
	if !opted {
		for _, u := range updates {
			send(u)
		}
		return
	}
	root := string(sid)
	if c.Root != "" {
		root = c.Root
	}
	msg := func(kv map[string]any) map[string]any {
		m := map[string]any{"rootSessionId": root, "childSessionId": c.ID, "parentSessionId": string(sid), "depth": 1}
		if c.Title != "" {
			m["title"] = c.Title
		}
		for k, v := range kv {
			m[k] = v
		}
		return m
	}
	const method = "opencode/session/child_update" // sent as OpenCode sends it: no "_" in front
	a.notify(method, msg(map[string]any{"type": "status", "status": "running"}))
	for _, u := range updates {
		a.notify(method, msg(map[string]any{"type": "update", "update": u}))
	}
	a.notify(method, msg(map[string]any{"type": "status", "status": "completed"}))
}

func (a *agent) runPerm(ctx context.Context, sid acp.SessionId, send func(acp.SessionUpdate), say func(string), p *Perm) {
	t := p.Tool
	kind := acp.ToolKind(t.Kind)
	if kind == "" {
		kind = acp.ToolKindEdit
	}
	var locs []acp.ToolCallLocation
	for _, l := range t.Locations {
		locs = append(locs, acp.ToolCallLocation{Path: l})
	}
	start := acp.SessionUpdateToolCall{SessionUpdate: "tool_call", ToolCallId: acp.ToolCallId(t.ID), Title: t.Title, Kind: kind,
		Status: acp.ToolCallStatusPending, RawInput: t.Input, Locations: locs}
	send(acp.SessionUpdate{ToolCall: &start})
	opts := []acp.PermissionOption{
		{Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow", OptionId: "allow"},
		{Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject", OptionId: "reject"},
	}
	if len(p.Options) > 0 {
		opts = nil
		for _, o := range p.Options {
			opts = append(opts, acp.PermissionOption{Kind: acp.PermissionOptionKind(o.Kind), Name: o.Name, OptionId: acp.PermissionOptionId(o.ID)})
		}
	}
	resp, err := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{SessionId: sid, Options: opts, ToolCall: acp.ToolCallUpdate{
		ToolCallId: acp.ToolCallId(t.ID), Title: acp.Ptr(t.Title), Kind: acp.Ptr(kind), RawInput: t.Input, Locations: locs}})
	st := acp.ToolCallStatusCompleted
	switch {
	case err != nil:
		say("permission: error " + err.Error())
		st = acp.ToolCallStatusFailed
	case resp.Outcome.Selected != nil:
		id := string(resp.Outcome.Selected.OptionId)
		say("permission: selected " + id)
		for _, o := range opts {
			if string(o.OptionId) == id && strings.HasPrefix(string(o.Kind), "reject") {
				st = acp.ToolCallStatusFailed
			}
		}
	default:
		say("permission: cancelled")
		st = acp.ToolCallStatusFailed
	}
	upd := acp.SessionToolCallUpdate{SessionUpdate: "tool_call_update", ToolCallId: acp.ToolCallId(t.ID), Status: &st}
	send(acp.SessionUpdate{ToolCallUpdate: &upd})
}

func (a *agent) runTerm(ctx context.Context, sid acp.SessionId, say func(string), t *TermOp) {
	req := acp.CreateTerminalRequest{SessionId: sid, Command: t.Command, Args: t.Args}
	if t.Cwd != "" {
		req.Cwd = &t.Cwd
	}
	ct, err := a.conn.CreateTerminal(ctx, req)
	if err != nil {
		say("term.create ERR " + err.Error())
		return
	}
	if _, err := a.conn.WaitForTerminalExit(ctx, acp.WaitForTerminalExitRequest{SessionId: sid, TerminalId: ct.TerminalId}); err != nil {
		say("term.wait ERR " + err.Error())
		return
	}
	o, err := a.conn.TerminalOutput(ctx, acp.TerminalOutputRequest{SessionId: sid, TerminalId: ct.TerminalId})
	if err != nil {
		say("term.output ERR " + err.Error())
		return
	}
	code := -1
	if o.ExitStatus != nil && o.ExitStatus.ExitCode != nil {
		code = *o.ExitStatus.ExitCode
	}
	say(fmt.Sprintf("term OK exit=%d out=%s", code, strings.TrimSpace(o.Output)))
	_, _ = a.conn.ReleaseTerminal(ctx, acp.ReleaseTerminalRequest{SessionId: sid, TerminalId: ct.TerminalId})
}
