// Package conformance is the reusable test suite every agents.Adapter must pass against a real install: start, send, stream,
// interrupt, usage, tool-call visibility, the permission path where it can be triggered, and stop. Run(t, adapter, opts) is
// called from each adapter's tests against the real CLI when it is installed and authenticated, and against the real
// stand-in processes (the scripted fake agents, the SDK's example agent) in CI. A step that cannot run is skipped with the
// reason; it is never passed by faking (decision P3).
package conformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/agents/acp"
	"github.com/kjmaguire/drydock/internal/proc"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// Options tailor the suite to one adapter and one environment.
type Options struct {
	// Spec is the base session spec. Cwd defaults to a fresh temp directory containing hello.txt ("hello from file").
	Spec agents.RunSpec
	// PM starts processes; default proc.New().
	PM proc.Manager

	// Prompt is the base turn; it must make the agent answer in text. Default: "Reply with the single word OK."
	Prompt string
	// ExpectText, when set, must appear (case-insensitively) in the base turn's final message.
	ExpectText string
	// ExpectUsage requires at least one reported usage chunk on the base turn. ExpectNoUsage requires none (an agent that
	// reports no usage must not be given invented numbers). With neither, usage is validated only if present.
	ExpectUsage   bool
	ExpectNoUsage bool

	// ToolPrompt makes the agent call at least one tool. Empty means the base turn must do it (set ExpectTools) or the tool
	// step is skipped. ExpectTools makes the absence of tool calls a failure. ExpectToolArgs requires non-empty JSON args.
	ToolPrompt     string
	ExpectTools    bool
	ExpectToolArgs bool

	// LongPrompt keeps the agent busy long enough to interrupt. Empty skips the interrupt step with a reason.
	LongPrompt string

	// PermissionPrompt makes the agent ask for permission. Empty skips the permission step with a reason (not every CLI can be
	// made to ask). AfterPermission, when set, inspects the chunks of that turn.
	PermissionPrompt string
	AfterPermission  func(t *testing.T, chunks []workflow.Chunk)

	// HandshakeOnly runs the suite for an install that cannot answer prompts here (no model credentials): detect, capability
	// consistency and the protocol handshake run for real; every step that needs a model is skipped with SkipReason.
	HandshakeOnly bool
	SkipReason    string

	// PerTurnProcess is for an adapter that starts one process per turn (a prompt-and-exit CLI driven by the custom dialect):
	// Start runs nothing, so PID is 0 until the first turn, a turn's process ending does not end the session (only Stop does),
	// and Interrupt ends the turn's process. Every other step is the same.
	PerTurnProcess bool
	// NoSessionID is for a CLI that prints no session id (a plain-text custom CLI): Session.ID may stay empty.
	NoSessionID bool

	// StepTimeout bounds each wait; default 90s.
	StepTimeout time.Duration
}

func (o *Options) defaults() {
	if o.PM == nil {
		o.PM = proc.New()
	}
	if o.Prompt == "" {
		o.Prompt = "Reply with the single word OK."
	}
	if o.StepTimeout <= 0 {
		o.StepTimeout = 90 * time.Second
	}
	if o.SkipReason == "" {
		o.SkipReason = "no model credentials in this environment"
	}
}

// knownKinds are the chunk kinds adapters may emit; a new kind must be added here deliberately.
var knownKinds = map[string]bool{
	workflow.ChunkText: true, workflow.ChunkThinking: true, workflow.ChunkMessageEnd: true, workflow.ChunkToolCall: true,
	workflow.ChunkToolResult: true, workflow.ChunkUsage: true, workflow.ChunkPermission: true, workflow.ChunkDone: true,
	workflow.ChunkError: true, workflow.ChunkLog: true, workflow.ChunkSession: true, workflow.ChunkRateLimit: true, workflow.ChunkPlan: true,
	workflow.ChunkPermissionDenied: true,
}

type suite struct {
	t    *testing.T
	ad   agents.Adapter
	o    Options
	cwd  string
	base []workflow.Chunk // the base turn, once it has run
}

type handshaker interface {
	Handshake(ctx context.Context, pm proc.Manager, spec agents.RunSpec) (acp.Handshake, error)
}

// Run executes the suite. It skips (with the reason) when the adapter's CLI is not installed or not signed in.
func Run(t *testing.T, ad agents.Adapter, o Options) {
	t.Helper()
	o.defaults()
	s := &suite{t: t, ad: ad, o: o}
	cwd := o.Spec.Cwd
	if cwd == "" {
		cwd = t.TempDir()
		if err := os.WriteFile(filepath.Join(cwd, "hello.txt"), []byte("hello from file\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s.cwd = cwd
	s.o.Spec.Cwd = cwd
	if s.o.Spec.AgentID == "" {
		s.o.Spec.AgentID = "conformance-" + ad.ID()
	}

	// Detection decides whether there is anything real to test.
	dctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	d, err := ad.Detect(dctx)
	cancel()
	if err != nil {
		t.Fatalf("Detect returned an error (a missing CLI is a status, not an error): %v", err)
	}
	if d.Adapter != "" && d.Adapter != ad.ID() {
		t.Errorf("Detection.Adapter %q != adapter id %q", d.Adapter, ad.ID())
	}
	t.Logf("%s: installed=%v version=%q status=%s auth=%v path=%s", ad.ID(), d.Installed, d.Version, d.Status, d.Authenticated, d.Path)
	if !d.Installed || d.Status == agents.StatusNotInstalled || d.Status == agents.StatusUnsupportedVersion {
		if d.InstallHint == "" {
			t.Errorf("a missing CLI must come with an install hint")
		}
		t.Skipf("%s is not usable here (status %s): %v", ad.ID(), d.Status, d.Problems)
	}
	if d.Status == agents.StatusUnauthenticated && !o.HandshakeOnly {
		t.Skipf("%s is not signed in here: %v", ad.ID(), d.Problems)
	}

	s.capabilities()
	if o.HandshakeOnly {
		s.handshake()
		for _, n := range []string{"send_stream", "usage", "tool_call_visibility", "interrupt", "permission_request"} {
			t.Run(n, func(t *testing.T) { t.Skipf("%s", o.SkipReason) })
		}
		s.stop()
		return
	}
	s.start()
	s.sendStream()
	s.usage()
	s.toolVisibility()
	s.interrupt()
	s.permission()
	s.stop()
}

// capabilities checks the declared matrix is self-consistent.
func (s *suite) capabilities() {
	s.t.Run("capabilities", func(t *testing.T) {
		c := s.ad.Capabilities()
		if !c.Streaming {
			t.Logf("note: %s does not declare streaming", s.ad.ID())
		}
		if s.o.ToolPrompt != "" || s.o.ExpectTools {
			if !c.ToolCallVisibility {
				t.Errorf("the suite expects tool calls but the adapter declares no ToolCallVisibility")
			}
		}
		if s.o.PermissionPrompt != "" && !c.Interrupt && !c.Headless {
			t.Logf("note: permission test configured for an adapter with no declared interrupt")
		}
		b, _ := json.Marshal(c)
		t.Logf("capabilities: %s", b)
	})
}

// handshake is the protocol-level check for installs that cannot take a prompt.
func (s *suite) handshake() {
	s.t.Run("handshake", func(t *testing.T) {
		h, ok := s.ad.(handshaker)
		if !ok {
			// no separate handshake: start must work
			sess := s.startSession(t, s.o.Spec)
			if sess.PID() <= 0 {
				t.Fatalf("pid %d", sess.PID())
			}
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), s.o.StepTimeout)
		defer cancel()
		hs, err := h.Handshake(ctx, s.o.PM, s.o.Spec)
		if err != nil {
			t.Fatalf("handshake: %v", err)
		}
		if hs.Info.ProtocolVersion < 1 || hs.PID <= 0 {
			t.Fatalf("not a sane initialize result: %+v", hs.Info)
		}
		t.Logf("agent %s %s; session/new ok=%v err=%q; matrix %s", hs.Info.Name, hs.Info.Version, hs.SessionOK, hs.SessionErr, strings.Join(hs.Info.Matrix(), " "))
		if !hs.SessionOK && !strings.Contains(hs.SessionErr, s.ad.ID()) {
			t.Errorf("a failed session/new must carry the adapter id and a clear reason, got %q", hs.SessionErr)
		}
		// the merged capabilities must follow the agent's report, not the name
		caps := s.ad.Capabilities()
		if caps.SessionResume != (hs.Info.LoadSession || hs.Info.Resume) {
			t.Errorf("SessionResume=%v but the agent advertised loadSession=%v resume=%v", caps.SessionResume, hs.Info.LoadSession, hs.Info.Resume)
		}
		if caps.ImageInput != hs.Info.Image {
			t.Errorf("ImageInput=%v but the agent advertised image=%v", caps.ImageInput, hs.Info.Image)
		}
	})
}

// startSession starts a session and registers its cleanup.
func (s *suite) startSession(t *testing.T, spec agents.RunSpec) agents.Session {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sess, err := s.ad.Start(ctx, s.o.PM, spec)
	if err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		sctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = sess.Stop(sctx)
		cancel()
	})
	return sess
}

func (s *suite) start() {
	s.t.Run("start", func(t *testing.T) {
		sess := s.startSession(t, s.o.Spec)
		if sess.PID() <= 0 && !s.o.PerTurnProcess {
			t.Fatalf("PID %d", sess.PID())
		}
		select {
		case ei := <-sess.Wait():
			t.Fatalf("the session ended right after Start: %+v stderr=%q", ei, sess.Stderr())
		case <-time.After(300 * time.Millisecond):
		}
		// an interrupt with nothing running must not end the session
		ictx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sess.Interrupt(ictx)
		select {
		case ei := <-sess.Wait():
			t.Fatalf("Interrupt on an idle session ended the process: %+v", ei)
		case <-time.After(300 * time.Millisecond):
		}
	})
}

// turn sends a prompt and returns the chunks up to and including the turn's done chunk.
func (s *suite) turn(t *testing.T, sess agents.Session, prompt string) []workflow.Chunk {
	t.Helper()
	if err := sess.Send(context.Background(), prompt); err != nil {
		t.Fatalf("Send: %v", err)
	}
	return s.readUntilDone(t, sess)
}

func (s *suite) readUntilDone(t *testing.T, sess agents.Session) []workflow.Chunk {
	t.Helper()
	var out []workflow.Chunk
	timer := time.NewTimer(s.o.StepTimeout)
	defer timer.Stop()
	for {
		select {
		case c, ok := <-sess.Output():
			if !ok {
				t.Fatalf("Output closed before the turn's done chunk (chunks %v, stderr %q)", kindsOf(out), sess.Stderr())
			}
			out = append(out, c)
			if c.Kind == workflow.ChunkDone {
				return out
			}
		case <-timer.C:
			t.Fatalf("no done chunk within %s (chunks %v)", s.o.StepTimeout, kindsOf(out))
		}
	}
}

func kindsOf(cs []workflow.Chunk) []string {
	var k []string
	for _, c := range cs {
		k = append(k, c.Kind)
	}
	return k
}

func (s *suite) sendStream() {
	s.t.Run("send_stream", func(t *testing.T) {
		sess := s.startSession(t, s.o.Spec)
		chunks := s.turn(t, sess, s.o.Prompt)
		s.base = chunks
		validateStream(t, chunks)

		var sawText bool
		var final string
		doneN := 0
		for _, c := range chunks {
			switch c.Kind {
			case workflow.ChunkText, workflow.ChunkMessageEnd:
				sawText = true
			}
			if c.Kind == workflow.ChunkMessageEnd {
				var d workflow.TextDelta
				_ = c.Decode(&d)
				final = d.Text
			}
			if c.Kind == workflow.ChunkDone {
				doneN++
			}
		}
		if !sawText {
			t.Errorf("the turn produced no assistant text (chunks %v)", kindsOf(chunks))
		}
		if doneN != 1 {
			t.Errorf("want exactly one done chunk per turn, got %d", doneN)
		}
		if s.o.ExpectText != "" && !strings.Contains(strings.ToLower(final), strings.ToLower(s.o.ExpectText)) {
			t.Errorf("final message %q does not contain %q", final, s.o.ExpectText)
		}
		if sess.ID() == "" && !s.o.NoSessionID {
			t.Errorf("Session.ID() is empty after a turn")
		}
		// nothing meaningful may trail the done chunk
		select {
		case c, ok := <-sess.Output():
			if ok && (c.Kind == workflow.ChunkText || c.Kind == workflow.ChunkToolCall || c.Kind == workflow.ChunkDone || c.Kind == workflow.ChunkMessageEnd) {
				t.Errorf("a %s chunk arrived after the turn's done", c.Kind)
			}
		case <-time.After(300 * time.Millisecond):
		}
		// a second turn works on the same session
		again := s.turn(t, sess, s.o.Prompt)
		if len(ofKind(again, workflow.ChunkDone)) != 1 {
			t.Errorf("second turn: %v", kindsOf(again))
		}
	})
}

// validateStream checks structural invariants every adapter must keep.
func validateStream(t *testing.T, chunks []workflow.Chunk) {
	t.Helper()
	calls := map[string]workflow.ToolCall{}
	usageAt, doneAt := -1, -1
	for i, c := range chunks {
		if !knownKinds[c.Kind] {
			t.Errorf("chunk %d: unknown kind %q (add it to the suite deliberately)", i, c.Kind)
			continue
		}
		if len(c.Data) > 0 && !json.Valid(c.Data) {
			t.Errorf("chunk %d (%s): payload is not valid JSON", i, c.Kind)
		}
		switch c.Kind {
		case workflow.ChunkToolCall:
			var tc workflow.ToolCall
			if c.Decode(&tc) != nil || tc.Name == "" {
				t.Errorf("chunk %d: tool_call without a name: %s", i, c.Data)
			}
			if tc.ID != "" {
				calls[tc.ID] = tc
			}
		case workflow.ChunkToolResult:
			var tr workflow.ToolResult
			if c.Decode(&tr) != nil || tr.Name == "" {
				t.Errorf("chunk %d: tool_result without a name (observers need no join): %s", i, c.Data)
			}
			if call, ok := calls[tr.ID]; tr.ID != "" && !ok {
				t.Errorf("chunk %d: tool_result %s has no preceding tool_call", i, tr.ID)
			} else if ok && call.Name != tr.Name {
				t.Errorf("chunk %d: tool_result name %q != call name %q", i, tr.Name, call.Name)
			}
		case workflow.ChunkUsage:
			if usageAt < 0 {
				usageAt = i
			}
			var u workflow.UsageChunk
			if c.Decode(&u) != nil {
				t.Errorf("chunk %d: bad usage payload", i)
				continue
			}
			if u.Source != "reported" && u.Source != "estimated" {
				t.Errorf("chunk %d: usage source %q must be reported or estimated", i, u.Source)
			}
			if u.Source == "reported" && u.InputTokens == 0 && u.OutputTokens == 0 && u.CachedTokens == 0 && u.CostUSD == 0 && u.ContextUsed == 0 {
				t.Errorf("chunk %d: an all-zero usage chunk labelled reported is invented data", i)
			}
		case workflow.ChunkDone:
			doneAt = i
			var d workflow.DoneChunk
			if c.Decode(&d) != nil {
				t.Errorf("chunk %d: bad done payload", i)
			}
		case workflow.ChunkError:
			var e workflow.ErrorChunk
			if c.Decode(&e) != nil || e.Message == "" {
				t.Errorf("chunk %d: error chunk without a message", i)
			}
		case workflow.ChunkPermission:
			var m map[string]any
			if json.Unmarshal(c.Data, &m) != nil || len(m) == 0 {
				t.Errorf("chunk %d: permission_request payload must be a JSON object", i)
			}
		}
	}
	if doneAt >= 0 && usageAt > doneAt {
		t.Errorf("usage (chunk %d) must precede done (chunk %d)", usageAt, doneAt)
	}
}

func ofKind(cs []workflow.Chunk, kind string) []workflow.Chunk {
	var out []workflow.Chunk
	for _, c := range cs {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

func (s *suite) needBase(t *testing.T) []workflow.Chunk {
	t.Helper()
	if s.base == nil {
		t.Skip("the base turn did not run")
	}
	return s.base
}

func (s *suite) usage() {
	s.t.Run("usage", func(t *testing.T) {
		chunks := s.needBase(t)
		us := ofKind(chunks, workflow.ChunkUsage)
		switch {
		case s.o.ExpectUsage:
			if len(us) == 0 {
				t.Fatalf("the agent is expected to report usage but no usage chunk arrived (chunks %v)", kindsOf(chunks))
			}
			var total int64
			for _, c := range us {
				var u workflow.UsageChunk
				_ = c.Decode(&u)
				if u.Source != "reported" {
					t.Errorf("source %q, want reported", u.Source)
				}
				total += u.InputTokens + u.OutputTokens + u.CachedTokens
			}
			if total == 0 {
				t.Errorf("usage chunks carry no tokens")
			}
			t.Logf("usage reported: %d chunk(s), %d tokens", len(us), total)
		case s.o.ExpectNoUsage:
			if len(us) != 0 {
				t.Fatalf("this agent reports no usage, yet %d usage chunk(s) were produced: %s", len(us), us[0].Data)
			}
			if s.ad.Capabilities().TokenUsage {
				t.Errorf("the adapter declares TokenUsage although the agent reported none")
			}
		default:
			t.Logf("usage chunks: %d (not required)", len(us))
		}
	})
}

func (s *suite) toolVisibility() {
	s.t.Run("tool_call_visibility", func(t *testing.T) {
		if !s.o.ExpectTools && s.o.ToolPrompt == "" {
			t.Skip("no tool-triggering prompt configured for this adapter")
		}
		if !s.ad.Capabilities().ToolCallVisibility {
			t.Skip("the adapter does not declare ToolCallVisibility")
		}
		chunks := s.base
		if s.o.ToolPrompt != "" && s.o.ToolPrompt != s.o.Prompt {
			sess := s.startSession(t, s.o.Spec)
			chunks = s.turn(t, sess, s.o.ToolPrompt)
			validateStream(t, chunks)
		} else if chunks == nil {
			t.Skip("the base turn did not run")
		}
		calls := ofKind(chunks, workflow.ChunkToolCall)
		results := ofKind(chunks, workflow.ChunkToolResult)
		if len(calls) == 0 {
			t.Fatalf("no tool_call chunk (chunks %v): the agent's tool use is invisible to the watchdog", kindsOf(chunks))
		}
		seen := map[string]workflow.ToolCall{}
		for _, c := range calls {
			var tc workflow.ToolCall
			_ = c.Decode(&tc)
			if tc.ID == "" || tc.Name == "" {
				t.Errorf("tool_call needs id and name: %s", c.Data)
			}
			if s.o.ExpectToolArgs && (len(tc.Args) == 0 || !json.Valid(tc.Args)) {
				t.Errorf("tool_call %s (%s) carries no JSON args: %q", tc.ID, tc.Name, tc.Args)
			}
			seen[tc.ID] = tc
		}
		if len(results) == 0 {
			t.Errorf("tool calls were made but no tool_result arrived")
		}
		for _, c := range results {
			var tr workflow.ToolResult
			_ = c.Decode(&tr)
			call, ok := seen[tr.ID]
			if !ok {
				t.Errorf("tool_result %q matches no tool_call", tr.ID)
				continue
			}
			if tr.Name != call.Name {
				t.Errorf("tool_result name %q != call name %q", tr.Name, call.Name)
			}
			if s.o.ExpectToolArgs && string(tr.Args) != string(call.Args) {
				t.Errorf("tool_result args %s != call args %s", tr.Args, call.Args)
			}
		}
		t.Logf("%d tool call(s), %d result(s); first: %s", len(calls), len(results), calls[0].Data)
	})
}

func (s *suite) interrupt() {
	s.t.Run("interrupt", func(t *testing.T) {
		if s.o.LongPrompt == "" {
			t.Skip("no long-running prompt configured for this adapter")
		}
		sess := s.startSession(t, s.o.Spec)
		if err := sess.Send(context.Background(), s.o.LongPrompt); err != nil {
			t.Fatalf("Send: %v", err)
		}
		timer := time.NewTimer(s.o.StepTimeout)
		defer timer.Stop()
	wait:
		for {
			select {
			case c, ok := <-sess.Output():
				if !ok {
					t.Fatalf("output closed before the turn produced anything: %q", sess.Stderr())
				}
				if c.Kind == workflow.ChunkText || c.Kind == workflow.ChunkThinking || c.Kind == workflow.ChunkToolCall || c.Kind == workflow.ChunkMessageEnd {
					break wait
				}
			case <-timer.C:
				t.Fatal("the turn produced no output to interrupt")
			}
		}
		ictx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := sess.Interrupt(ictx); err != nil {
			t.Fatalf("Interrupt: %v", err)
		}
		chunks := s.readUntilDone(t, sess)
		var d workflow.DoneChunk
		_ = ofKind(chunks, workflow.ChunkDone)[0].Decode(&d)
		t.Logf("interrupted turn ended with stop reason %q", d.StopReason)
		select {
		case ei := <-sess.Wait():
			t.Fatalf("Interrupt must keep the session alive, but the process exited: %+v stderr=%q", ei, sess.Stderr())
		case <-time.After(300 * time.Millisecond):
		}
		// the same session takes the next message
		after := s.turn(t, sess, s.o.Prompt)
		if len(ofKind(after, workflow.ChunkDone)) != 1 || len(ofKind(after, workflow.ChunkText))+len(ofKind(after, workflow.ChunkMessageEnd)) == 0 {
			t.Errorf("after an interrupt the same session must answer the next message, got %v", kindsOf(after))
		}
	})
}

func (s *suite) permission() {
	s.t.Run("permission_request", func(t *testing.T) {
		if s.o.PermissionPrompt == "" {
			t.Skip("a permission request cannot be triggered for this adapter in this environment (headless Claude answers approvals by mode or --permission-prompt-tool, not by a client request; the real ACP CLIs need a model)")
		}
		chunks := s.base
		if s.o.PermissionPrompt != s.o.Prompt || chunks == nil {
			sess := s.startSession(t, s.o.Spec)
			chunks = s.turn(t, sess, s.o.PermissionPrompt)
			validateStream(t, chunks)
		}
		pr := ofKind(chunks, workflow.ChunkPermission)
		if len(pr) == 0 {
			t.Fatalf("no permission_request chunk (chunks %v)", kindsOf(chunks))
		}
		if len(ofKind(chunks, workflow.ChunkDone)) != 1 {
			t.Errorf("the turn must still end with one done chunk after a permission request")
		}
		if s.o.AfterPermission != nil {
			s.o.AfterPermission(t, chunks)
		}
	})
}

func (s *suite) stop() {
	s.t.Run("stop", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sess, err := s.ad.Start(ctx, s.o.PM, s.o.Spec)
		if err != nil {
			if s.o.HandshakeOnly {
				t.Skipf("a session cannot be created here without credentials: %v", err)
			}
			t.Fatalf("Start: %v", err)
		}
		pid := sess.PID()
		sctx, c2 := context.WithTimeout(context.Background(), 15*time.Second)
		defer c2()
		if err := sess.Stop(sctx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		select {
		case <-sess.Wait():
		case <-time.After(10 * time.Second):
			t.Fatalf("the process is still running 10s after Stop")
		}
		drain := time.After(10 * time.Second)
		for closed := false; !closed; {
			select {
			case _, ok := <-sess.Output():
				closed = !ok
			case <-drain:
				t.Fatal("Output was not closed after Stop")
			}
		}
		if pid <= 0 && s.o.PerTurnProcess {
			// nothing was started: a per-turn session runs its first process with its first message
		} else if gone, known := processGone(pid); known && !gone {
			t.Errorf("pid %d still exists after Stop", pid)
		}
		if err := sess.Stop(sctx); err != nil {
			t.Logf("second Stop returned %v (allowed, must not panic or hang)", err)
		}
	})
}
