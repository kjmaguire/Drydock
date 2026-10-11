package connections

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Step statuses of a Test connection report.
const (
	StepPass    = "pass"
	StepFail    = "fail"
	StepSkipped = "skipped"
)

// DefaultPrompt is the one tiny prompt of the short form.
const DefaultPrompt = "Reply with the single word OK."

// TestOptions control Test connection. Nothing that costs tokens runs unless Prompt is set and a model is configured.
type TestOptions struct {
	Prompt     bool          // opt in to one tiny prompt (spends tokens)
	PromptText string        // default DefaultPrompt
	Model      string        // the model to use for the prompt; without one the prompt step is skipped
	Cancel     bool          // also run a second, longer prompt and interrupt it (T4); needs Prompt
	Timeout    time.Duration // whole test; default 3 minutes
	Cwd        string        // default: a fresh empty temp directory that is removed afterwards
	Env        []string      // extra KEY=VALUE for the session (for example a provider key the caller owns)
}

// Step is one line of a Test connection report: pass, fail, or skipped with the reason.
type Step struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Cell is one capability matrix cell with its provenance (docs/connections.md section 1).
type Cell struct {
	Value      any    `json:"value"`
	Provenance string `json:"provenance"` // observed | declared | unknown
}

// Report is the outcome of Test connection (a short form of the conformance suite).
type Report struct {
	Adapter      string                `json:"adapter"`
	CLIVersion   string                `json:"cliVersion,omitempty"`
	Bridge       string                `json:"bridge,omitempty"`
	Platform     string                `json:"platform"`
	Transport    string                `json:"transport"`
	Form         string                `json:"form"` // short
	Steps        []Step                `json:"steps"`
	Capabilities agents.Capabilities   `json:"capabilities"`
	Matrix       map[string]Cell       `json:"matrix"`
	AgentMatrix  []string              `json:"agentMatrix,omitempty"` // raw ACP initialize facts, sorted
	Degraded     []string              `json:"degraded,omitempty"`    // plain language
	Summary      string                `json:"summary"`
	TokensUsed   int64                 `json:"tokensUsed"` // measured from the agent's own report; 0 when none was reported
	Usage        []workflow.UsageChunk `json:"usage,omitempty"`
	OK           bool                  `json:"ok"`
	Elapsed      time.Duration         `json:"elapsed"`
}

// FailureReason is the first failed step's reason, for RecordTest.
func (r Report) FailureReason() string {
	for _, s := range r.Steps {
		if s.Status == StepFail {
			return s.ID + ": " + s.Reason
		}
	}
	return ""
}

// Describe tells the user, before anything runs, what the test will do and what it can cost.
func Describe(o TestOptions) string {
	var b strings.Builder
	b.WriteString("Test connection will: check the install, version, prerequisites and sign-in; start the agent in an empty temporary folder; do the protocol handshake and read what the agent says it can do; then stop it and its child processes.")
	switch {
	case o.Prompt && o.Model != "":
		fmt.Fprintf(&b, " It will also send one tiny prompt to model %q, which spends a small number of tokens that Drydock measures from the agent's own report.", o.Model)
		if o.Cancel {
			b.WriteString(" A second, longer prompt is started and cancelled to prove interrupt, which spends a few more.")
		}
	case o.Prompt:
		b.WriteString(" No prompt will be sent because no model is configured.")
	default:
		b.WriteString(" No prompt is sent, so no tokens are spent.")
	}
	return b.String()
}

// TestConnection runs the short form of the conformance suite against the real install of adapterID on the user's button
// press. Missing installs and logins are reported as skipped with a reason, never as a pass.
func (s *Service) TestConnection(ctx context.Context, adapterID string, o TestOptions) (Report, error) {
	m, ok := s.Registry.Set.Get(adapterID)
	if !ok {
		return Report{}, fmt.Errorf("no manifest for adapter %q", adapterID)
	}
	if o.Timeout <= 0 {
		o.Timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	began := time.Now()
	t := &tester{svc: s, m: m, o: o}
	rep := t.run(ctx)
	rep.Elapsed = time.Since(began)
	return rep, nil
}

type tester struct {
	svc *Service
	m   *manifests.Manifest
	o   TestOptions
	rep Report

	obs     *acp.Observed // set after an ACP handshake
	sessErr string
}

func (t *tester) step(id, name, status, reason, detail string) {
	t.rep.Steps = append(t.rep.Steps, Step{ID: id, Name: name, Status: status, Reason: reason, Detail: detail})
}

func (t *tester) skip(id, name, reason string) { t.step(id, name, StepSkipped, reason, "") }

func (t *tester) run(ctx context.Context) Report {
	m, s := t.m, t.svc
	t.rep = Report{Adapter: m.Meta.ID, Form: "short", Transport: m.Transports.Primary, Matrix: map[string]Cell{}}

	// T1: binary, version, prerequisites, auth probe
	conn := Check(ctx, m, s.options())
	t.rep.CLIVersion, t.rep.Platform = conn.Version, conn.Platform
	if a := m.Transports.ACP; a != nil && m.Transports.Primary == manifests.TransportACP {
		if pkg := npxPackage(a.Argv); pkg != "" {
			t.rep.Bridge = pkg
		}
	}
	detail := fmt.Sprintf("status %s", conn.Status)
	if conn.Version != "" {
		detail += ", version " + conn.Version
	}
	switch conn.Status {
	case agents.StatusNotInstalled, agents.StatusUnsupportedVersion:
		t.step("T1", "Install, version and prerequisites", StepFail, firstOr(conn.Problems, string(conn.Status)), detail)
		t.skipRest("T1 did not pass (" + string(conn.Status) + ")")
		return t.finish(conn)
	case agents.StatusUnauthenticated:
		t.step("T1", "Install, version and prerequisites", StepFail, firstOr(conn.Problems, "not signed in"), detail)
	default:
		t.step("T1", "Install, version and prerequisites", StepPass, "", detail+authText(conn))
	}
	for _, g := range conn.Gates {
		if !g.Acked {
			t.skip("T2", "Start and handshake", "the terms notice "+g.ID+" has not been acknowledged; Drydock does not start this CLI before that")
			t.skipRest("gate " + g.ID + " is not acknowledged")
			return t.finish(conn)
		}
	}

	ad, err := s.Registry.Build(m.Meta.ID)
	if err != nil {
		t.step("T2", "Start and handshake", StepFail, err.Error(), "")
		t.skipRest("no adapter could be built")
		return t.finish(conn)
	}
	cwd := t.o.Cwd
	if cwd == "" {
		d, err := os.MkdirTemp("", "drydock-test-")
		if err != nil {
			t.step("T2", "Start and handshake", StepFail, err.Error(), "")
			t.skipRest("no temporary directory")
			return t.finish(conn)
		}
		defer os.RemoveAll(d)
		cwd = d
	}
	spec := agents.RunSpec{AgentID: "test-" + m.Meta.ID, Cwd: cwd, Model: t.o.Model, Env: t.o.Env}
	pm := s.options().PM
	if pm == nil {
		pm = proc.New()
	}

	// T2: spawn and the transport handshake
	var sess agents.Session
	switch a := ad.(type) {
	case *acp.Adapter:
		h, err := a.Handshake(ctx, pm, spec)
		if err != nil {
			t.step("T2", "Start and handshake (ACP initialize)", StepFail, err.Error(), "")
			t.skipRest("the agent did not complete the handshake")
			return t.finish(conn)
		}
		t.rep.AgentMatrix = h.Info.Matrix()
		o, _ := a.Observed()
		t.obs = &o
		t.step("T2", "Start and handshake (ACP initialize)", StepPass, "",
			fmt.Sprintf("%s %s, protocol %d, initialize took %s", orStr(h.Info.Name, "agent"), h.Info.Version, h.Info.ProtocolVersion, h.InitElapsed.Round(time.Millisecond)))
		if h.SessionOK {
			t.step("T2b", "Create a session (session/new)", StepPass, "", sessionDetail(h.Session))
		} else {
			t.sessErr = h.SessionErr
			st := StepFail
			t.step("T2b", "Create a session (session/new)", st, plainSessionErr(h.SessionErr), "")
		}
	default:
		ss, err := ad.Start(ctx, pm, spec)
		if err != nil {
			status, reason := StepFail, err.Error()
			if errors.Is(err, manifests.ErrNotImplemented) {
				reason = "sessions cannot be started yet: " + err.Error()
			}
			t.step("T2", "Start and handshake", status, reason, "")
			t.skipRest("the agent did not start")
			return t.finish(conn)
		}
		sess = ss
		defer func() { _ = ss.Stop(context.Background()) }()
		time.Sleep(1500 * time.Millisecond)
		select {
		case ei := <-ss.Wait():
			t.step("T2", "Start and handshake", StepFail, fmt.Sprintf("the process exited with code %d right after starting: %s", ei.Code, strings.TrimSpace(ss.Stderr())), "")
			t.skipRest("the agent exited")
			return t.finish(conn)
		default:
		}
		t.step("T2", "Start and handshake", StepPass, "", "the process started and stayed alive; this transport announces its init event with the first prompt, so the handshake itself is confirmed by T3")
	}

	// T3: one tiny prompt, opt-in
	switch {
	case !t.o.Prompt:
		t.skip("T3", "One tiny prompt", "not requested: a prompt spends tokens")
	case t.o.Model == "":
		t.skip("T3", "One tiny prompt", "no model is configured for this agent")
	case conn.Status == agents.StatusUnauthenticated:
		t.skip("T3", "One tiny prompt", "not signed in")
	case t.sessErr != "":
		t.skip("T3", "One tiny prompt", "no session could be created: "+plainSessionErr(t.sessErr))
	default:
		sess = t.prompt(ctx, ad, pm, spec, sess)
	}

	// T4: cancel
	switch {
	case !t.o.Cancel:
		t.skip("T4", "Interrupt a running turn", "not requested: it needs a second, longer prompt")
	case t.rep.stepStatus("T3") != StepPass:
		t.skip("T4", "Interrupt a running turn", "T3 did not pass")
	default:
		t.cancel(ctx, sess)
	}

	// T5: stop
	t.stop(sess)
	t.skip("T6", "Gateway reachability", "checked on the agent's first real run instead: its Inspector and the MCP page show whether Drydock's workspace tools were attached")
	t.skip("T7", "Provider path", "no provider is configured for this agent")
	return t.finish(conn)
}

func (r Report) stepStatus(id string) string {
	for _, s := range r.Steps {
		if s.ID == id {
			return s.Status
		}
	}
	return ""
}

func (t *tester) skipRest(reason string) {
	have := map[string]bool{}
	for _, s := range t.rep.Steps {
		have[s.ID] = true
	}
	for _, st := range []struct{ id, name string }{{"T2", "Start and handshake"}, {"T3", "One tiny prompt"}, {"T4", "Interrupt a running turn"}, {"T5", "Stop and process cleanup"}, {"T6", "Gateway reachability"}, {"T7", "Provider path"}} {
		if !have[st.id] {
			t.skip(st.id, st.name, reason)
		}
	}
}

// prompt starts a session with the configured model (reusing sess when one is already running) and sends the tiny prompt.
func (t *tester) prompt(ctx context.Context, ad agents.Adapter, pm proc.Manager, spec agents.RunSpec, sess agents.Session) agents.Session {
	if sess == nil {
		ss, err := ad.Start(ctx, pm, spec)
		if err != nil {
			t.step("T3", "One tiny prompt", StepFail, err.Error(), "")
			return nil
		}
		sess = ss
	}
	text := t.o.PromptText
	if text == "" {
		text = DefaultPrompt
	}
	if err := sess.Send(ctx, text); err != nil {
		t.step("T3", "One tiny prompt", StepFail, "send: "+err.Error(), "")
		return sess
	}
	var (
		sawText, sawDone, sawDelta bool
		toolCalls, usageChunks     int
		answer                     strings.Builder
		stop                       string
		errMsg                     string
	)
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
loop:
	for {
		select {
		case c, ok := <-sess.Output():
			if !ok {
				errMsg = "the agent's output ended before the turn finished: " + strings.TrimSpace(sess.Stderr())
				break loop
			}
			switch c.Kind {
			case workflow.ChunkText:
				sawDelta = true
			case workflow.ChunkMessageEnd:
				var d workflow.TextDelta
				if c.Decode(&d) == nil {
					sawText = true
					answer.WriteString(d.Text)
				}
			case workflow.ChunkToolCall:
				toolCalls++
			case workflow.ChunkUsage:
				var u workflow.UsageChunk
				if c.Decode(&u) == nil {
					usageChunks++
					t.rep.Usage = append(t.rep.Usage, u)
					t.rep.TokensUsed += u.InputTokens + u.OutputTokens + u.CachedTokens + u.CacheWriteTokens
				}
			case workflow.ChunkError:
				var e workflow.ErrorChunk
				if c.Decode(&e) == nil && errMsg == "" && (e.Fatal || strings.HasPrefix(e.Message, "prompt failed")) {
					errMsg = e.Message
				}
			case workflow.ChunkDone:
				var d workflow.DoneChunk
				_ = c.Decode(&d)
				stop, sawDone = d.StopReason, true
				break loop
			}
		case <-deadline.C:
			errMsg = "no answer within 2 minutes"
			break loop
		case <-ctx.Done():
			errMsg = ctx.Err().Error()
			break loop
		}
	}
	switch {
	case errMsg != "":
		t.step("T3", "One tiny prompt", StepFail, errMsg, "")
	case !sawDone || !sawText:
		t.step("T3", "One tiny prompt", StepFail, "the turn ended without any assistant text", "stop reason "+stop)
	default:
		t.step("T3", "One tiny prompt", StepPass, "", fmt.Sprintf("answer %q, stop reason %s; streaming deltas: %s; usage reported: %s; tool-call events: %d",
			trim(answer.String(), 60), stop, yesNo(sawDelta), yesNo(usageChunks > 0), toolCalls))
	}
	t.rep.observePrompt(sawDelta, usageChunks > 0, t.rep.Usage)
	return sess
}

// observePrompt records what the prompt showed about usage and streaming.
func (r *Report) observePrompt(streamed, usage bool, us []workflow.UsageChunk) {
	cost := false
	for _, u := range us {
		cost = cost || u.CostUSD > 0
	}
	r.Matrix["streaming"] = Cell{streamed, "observed"}
	r.Matrix["token_usage"] = Cell{usage, "observed"}
	r.Matrix["cost_reporting"] = Cell{cost, "observed"}
}

func (t *tester) cancel(ctx context.Context, sess agents.Session) {
	if sess == nil {
		t.skip("T4", "Interrupt a running turn", "no session")
		return
	}
	if err := sess.Send(ctx, "Count from 1 to 500, one number per line, with no other text."); err != nil {
		t.step("T4", "Interrupt a running turn", StepFail, "send: "+err.Error(), "")
		return
	}
	// wait for output to start, then interrupt
	started := false
	timer := time.NewTimer(90 * time.Second)
	defer timer.Stop()
	for !started {
		select {
		case c, ok := <-sess.Output():
			if !ok {
				t.step("T4", "Interrupt a running turn", StepFail, "the output ended before the turn started", "")
				return
			}
			started = c.Kind == workflow.ChunkText || c.Kind == workflow.ChunkThinking || c.Kind == workflow.ChunkToolCall
		case <-timer.C:
			t.step("T4", "Interrupt a running turn", StepFail, "the agent produced no output to interrupt", "")
			return
		}
	}
	ictx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sess.Interrupt(ictx); err != nil {
		t.step("T4", "Interrupt a running turn", StepFail, "interrupt: "+err.Error(), "")
		return
	}
	for {
		select {
		case c, ok := <-sess.Output():
			if !ok {
				t.step("T4", "Interrupt a running turn", StepFail, "the process ended instead of keeping the session alive", "")
				return
			}
			if c.Kind == workflow.ChunkDone {
				var d workflow.DoneChunk
				_ = c.Decode(&d)
				t.step("T4", "Interrupt a running turn", StepPass, "", "the turn ended with stop reason "+d.StopReason+" and the session stayed alive")
				t.rep.Matrix["interrupt"] = Cell{true, "observed"}
				return
			}
		case <-time.After(30 * time.Second):
			t.step("T4", "Interrupt a running turn", StepFail, "the turn did not end after the interrupt", "")
			return
		}
	}
}

func (t *tester) stop(sess agents.Session) {
	if sess == nil {
		t.skip("T5", "Stop and process cleanup", "no session was left running (the handshake already stopped its process)")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = sess.Stop(ctx)
	select {
	case ei := <-sess.Wait():
		detail := fmt.Sprintf("the process exited (code %d)", ei.Code)
		t.step("T5", "Stop and process cleanup", StepPass, "", detail+"; killing grandchildren is asserted only by the Windows tests (decision P1)")
	case <-time.After(5 * time.Second):
		t.step("T5", "Stop and process cleanup", StepFail, "the process was still running 5 seconds after Stop", "")
	}
}

func (t *tester) finish(conn Connection) Report {
	r := &t.rep
	declared := manifests.DeclaredCapabilities(t.m)
	r.Capabilities = declared
	var o acp.Observed
	if t.obs != nil {
		o = *t.obs
		r.Capabilities = acp.Capabilities(declared, o)
	}
	fillMatrix(r, t.m, t.obs)
	r.Degraded = degradedNotes(t.m, r, t.obs, t.sessErr)
	r.OK = true
	for _, s := range r.Steps {
		if s.Status == StepFail {
			r.OK = false
		}
	}
	if r.OK {
		hasPass := false
		for _, s := range r.Steps {
			hasPass = hasPass || s.Status == StepPass
		}
		r.OK = hasPass
	}
	r.Summary = summary(r, conn)
	return *r
}

func firstOr(ss []string, def string) string {
	if len(ss) > 0 {
		return ss[0]
	}
	return def
}

func orStr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func authText(c Connection) string {
	switch {
	case c.Authenticated != nil && *c.Authenticated:
		return "; signed in" + suffix(c.AuthDetail)
	case c.Authenticated != nil:
		return "; not signed in"
	case c.AuthDetail != "":
		return "; sign-in unknown (" + c.AuthDetail + ")"
	}
	return ""
}

func suffix(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

func sessionDetail(si acp.SessionInfo) string {
	var parts []string
	if len(si.Models) > 0 {
		parts = append(parts, fmt.Sprintf("%d models to choose from", len(si.Models)))
	}
	if len(si.Efforts) > 0 {
		parts = append(parts, "effort levels "+strings.Join(si.Efforts, ", "))
	}
	if len(si.Modes) > 0 {
		parts = append(parts, "modes "+strings.Join(si.Modes, ", "))
	}
	if len(parts) == 0 {
		return "the agent exposes no model, effort or mode choices"
	}
	return strings.Join(parts, "; ")
}

// plainSessionErr rewrites a session/new failure for a person.
func plainSessionErr(e string) string {
	if strings.Contains(e, "requires authentication") {
		return "the agent needs you to sign in or add an API key before it can start a session: " + e
	}
	return e
}

func npxPackage(argv []string) string {
	if len(argv) == 0 || !pinnedBridgeCmd.MatchString(argv[0]) {
		return ""
	}
	for _, a := range argv[1:] {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}
