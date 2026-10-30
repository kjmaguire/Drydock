package acp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/workflow"
)

// client is the callback side the agent calls: session/update notifications, permission requests, fs and terminal methods.
type client struct{ s *Session }

var _ acp.Client = (*client)(nil)

// maxToolOutput caps one tool_result's text.
const maxToolOutput = 64 << 10

// toolState is what has been learned about one tool call across tool_call and tool_call_update messages.
type toolState struct {
	id        string
	title     string
	kind      string
	status    string
	input     any
	locations []string
	output    string
	rawOutput any
	metaName  string

	emittedCall   bool
	emittedResult bool

	// approvedSum is the callSum of the call as it was when Drydock answered its permission request with an allow (decision
	// P117); changed is set once a later tool_call or tool_call_update made it another call.
	approvedSum string
	changed     bool

	// asked is set once the call's permission request was handled, announced once its own tool_call was. The SDK runs an
	// agent's requests beside its notification queue, so a request can be handled before the tool_call the agent sent ahead
	// of it (seen with the SDK's example agent); onToolCall then treats that tool_call as the announcement it was.
	asked, announced bool
}

// callSum is the SHA-256 of what a tool call does, as far as Drydock was asked about it: its kind, its input and, for a kind
// that acts on the paths it names (read, edit, delete, move), those paths. A search's locations are left out: an agent may
// report what a search found as its locations when it ends, which does not change the call that was allowed.
func (t *toolState) callSum() string {
	doc := map[string]any{"kind": t.kind, "input": t.args()}
	switch t.kind {
	case "read", "edit", "delete", "move":
		doc["locations"] = t.locations
	}
	b, _ := json.Marshal(doc)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// changedSinceAllowed reports, once, that a call allowed by its permission request is now another call. Caller holds s.mu.
func (t *toolState) changedSinceAllowed() bool {
	if t.approvedSum == "" || t.changed {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(t.callSum()), []byte(t.approvedSum)) == 1 {
		return false
	}
	t.changed = true
	return true
}

func (t *toolState) haveInput() bool {
	if t.input == nil {
		return false
	}
	b, err := json.Marshal(t.input)
	if err != nil {
		return false
	}
	switch string(bytes.TrimSpace(b)) {
	case "null", "{}", `""`, "[]":
		return false
	}
	return true
}

// args renders the tool input. When the agent supplied no rawInput the kind and locations stand in, so repeated calls still
// compare equal and a human still sees what was touched.
func (t *toolState) args() json.RawMessage {
	if t.haveInput() {
		b, _ := json.Marshal(t.input)
		return b
	}
	m := map[string]any{}
	if t.kind != "" && t.kind != string(acp.ToolKindOther) {
		m["kind"] = t.kind
	}
	if len(t.locations) > 0 {
		m["locations"] = t.locations
	}
	if len(m) == 0 {
		return nil
	}
	b, _ := json.Marshal(m)
	return b
}

func (t *toolState) toolName() string {
	switch {
	case t.metaName != "":
		return t.metaName
	case t.title != "":
		return t.title
	case t.kind != "":
		return t.kind
	}
	return "tool"
}

// metaToolName reads the raw tool name some agents put in `_meta` (the Claude bridge uses _meta.claudeCode.toolName).
func metaToolName(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	if cc, ok := meta["claudeCode"].(map[string]any); ok {
		if n, ok := cc["toolName"].(string); ok && n != "" {
			return n
		}
	}
	for _, k := range []string{"toolName", "tool_name"} {
		if n, ok := meta[k].(string); ok && n != "" {
			return n
		}
	}
	return ""
}

func textOf(b acp.ContentBlock) (string, bool) {
	if b.Text != nil {
		return b.Text.Text, true
	}
	return "", false
}

func isTerminal(status string) bool { return status == "completed" || status == "failed" }

// SessionUpdate maps session/update notifications onto normalized chunks.
func (c *client) SessionUpdate(_ context.Context, n acp.SessionNotification) (err error) {
	defer safe.Recover("acp.SessionUpdate", &err)
	s := c.s
	u := n.Update
	s.mu.Lock()
	replay := s.replaying
	s.mu.Unlock()
	if replay {
		return nil // history replayed by session/load is not new output
	}
	switch {
	case u.AgentMessageChunk != nil:
		if t, ok := textOf(u.AgentMessageChunk.Content); ok && t != "" {
			id := ""
			if u.AgentMessageChunk.MessageId != nil {
				id = *u.AgentMessageChunk.MessageId
			}
			s.mu.Lock()
			if id != "" && s.msgID != "" && id != s.msgID {
				s.mu.Unlock()
				s.flushMessage()
				s.mu.Lock()
			}
			if id != "" {
				s.msgID = id
			}
			s.msg.WriteString(t)
			s.mu.Unlock()
			s.emitKind(workflow.ChunkText, workflow.TextDelta{Text: t})
		}
	case u.AgentThoughtChunk != nil:
		if t, ok := textOf(u.AgentThoughtChunk.Content); ok && t != "" {
			s.emitKind(workflow.ChunkThinking, workflow.TextDelta{Text: t})
		}
	case u.ToolCall != nil:
		s.flushMessage() // a tool call ends the assistant message before it
		s.onToolCall(u.ToolCall)
	case u.ToolCallUpdate != nil:
		s.onToolCallUpdate(u.ToolCallUpdate)
	case u.SessionInfoUpdate != nil:
		s.onSessionInfo(u.SessionInfoUpdate)
	case u.Plan != nil:
		s.emitKind(workflow.ChunkPlan, planChunk(u.Plan.Entries))
	case u.PlanUpdate != nil:
		if it := u.PlanUpdate.Plan.Items; it != nil {
			s.emitKind(workflow.ChunkPlan, planChunk(it.Entries))
		}
	case u.UsageUpdate != nil:
		s.mu.Lock()
		s.usage.onUpdate(u.UsageUpdate)
		s.usageSeen = true
		s.mu.Unlock()
	case u.CurrentModeUpdate != nil:
		s.mu.Lock()
		s.sinfo.CurrentMode = string(u.CurrentModeUpdate.CurrentModeId)
		s.mu.Unlock()
	}
	return nil
}

// onToolCall records a tool_call (the session's own, or a sub-agent's from ext.go).
func (s *Session) onToolCall(tc *acp.SessionUpdateToolCall) {
	st := s.toolFor(tc.ToolCallId)
	s.mu.Lock()
	late := st.asked && !st.announced // the tool_call the agent sent before its permission request, handled after it
	st.announced = true
	if late {
		// what the request said stands and the tool_call fills in only what it left out, as if it had been handled first;
		// what was allowed is then the call as it would have been, so this is no change to it
		st.title = firstNonEmpty(st.title, tc.Title)
		st.kind = firstNonEmpty(st.kind, string(tc.Kind))
		st.metaName = firstNonEmpty(st.metaName, metaToolName(tc.Meta))
		if !st.haveInput() && tc.RawInput != nil {
			st.input = tc.RawInput
		}
		if len(st.locations) == 0 {
			st.locations = locPaths(tc.Locations, nil)
		}
	} else {
		st.title = firstNonEmpty(tc.Title, st.title)
		st.kind = firstNonEmpty(string(tc.Kind), st.kind)
		st.metaName = firstNonEmpty(metaToolName(tc.Meta), st.metaName)
		if tc.RawInput != nil {
			st.input = tc.RawInput
		}
		st.locations = locPaths(tc.Locations, st.locations)
	}
	st.status = firstNonEmpty(string(tc.Status), st.status)
	st.absorbOutput(tc.Content, tc.RawOutput)
	if late && st.approvedSum != "" {
		st.approvedSum = st.callSum()
	}
	changed := !late && st.changedSinceAllowed()
	s.mu.Unlock()
	if changed {
		s.callChanged(st)
	}
	s.progressTool(st)
}

// onToolCallUpdate records a tool_call_update (the session's own, or a sub-agent's from ext.go).
func (s *Session) onToolCallUpdate(tu *acp.SessionToolCallUpdate) {
	st := s.toolFor(tu.ToolCallId)
	s.mu.Lock()
	if tu.Title != nil {
		st.title = *tu.Title
	}
	if tu.Kind != nil {
		st.kind = string(*tu.Kind)
	}
	if tu.Status != nil {
		st.status = string(*tu.Status)
	}
	st.metaName = firstNonEmpty(metaToolName(tu.Meta), st.metaName)
	if tu.RawInput != nil {
		st.input = tu.RawInput
	}
	st.locations = locPaths(tu.Locations, st.locations)
	st.absorbOutput(tu.Content, tu.RawOutput)
	changed := st.changedSinceAllowed()
	s.mu.Unlock()
	if changed {
		s.callChanged(st)
	}
	s.progressTool(st)
}

// callChanged handles a call the agent changed after Drydock allowed it (decision P117): an ACP agent runs an allowed call by
// itself, so Drydock cannot refuse the changed one; it compares what the agent then reports with what was allowed, and on the
// first difference says so (a permission_denied and an error chunk the person sees, and the session's OnCallChanged) and
// cancels the turn, so the agent does nothing more on an approval that no longer stands.
func (s *Session) callChanged(st *toolState) {
	s.mu.Lock()
	q := agents.ToolAsk{ToolCallID: st.id, Tool: st.toolName(), Title: st.title, Kind: st.kind, Args: st.args(), Locations: append([]string(nil), st.locations...)}
	allowed, now := st.approvedSum, st.callSum()
	s.mu.Unlock()
	const why = "the agent changed this call after Drydock allowed it, so the approval does not cover it: Drydock stopped the turn"
	s.emitKind(workflow.ChunkPermissionDenied, workflow.PermissionDenial{ToolName: q.Tool, ToolUseID: q.ToolCallID, Input: q.Args, Reason: why})
	s.emitKind(workflow.ChunkError, workflow.ErrorChunk{Message: q.Tool + ": " + why})
	if f := s.spec.OnCallChanged; f != nil {
		f(q, allowed, now)
	}
	safe.Go("acp.Session.callChanged", func() {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		defer cancel()
		_ = s.Interrupt(ctx)
	})
}

func planChunk(es []acp.PlanEntry) workflow.PlanChunk {
	pc := workflow.PlanChunk{Entries: []workflow.PlanEntry{}}
	for _, e := range es {
		pc.Entries = append(pc.Entries, workflow.PlanEntry{Content: e.Content, Status: string(e.Status), Priority: string(e.Priority)})
	}
	return pc
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func locPaths(ls []acp.ToolCallLocation, prev []string) []string {
	if len(ls) == 0 {
		return prev
	}
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Path)
	}
	return out
}

// absorbOutput records the latest tool output: content blocks (text, diffs, terminals) win over rawOutput.
func (t *toolState) absorbOutput(content []acp.ToolCallContent, raw any) {
	if raw != nil {
		t.rawOutput = raw
	}
	var parts []string
	for _, c := range content {
		switch {
		case c.Content != nil:
			if txt, ok := textOf(c.Content.Content); ok && txt != "" {
				parts = append(parts, txt)
			}
		case c.Diff != nil:
			parts = append(parts, fmt.Sprintf("diff %s (%d bytes new)", c.Diff.Path, len(c.Diff.NewText)))
		case c.Terminal != nil:
			parts = append(parts, "terminal "+c.Terminal.TerminalId)
		}
	}
	if len(parts) > 0 {
		t.output = strings.Join(parts, "\n")
	}
}

func (t *toolState) outputText() string {
	out := t.output
	if out == "" && t.rawOutput != nil {
		switch v := t.rawOutput.(type) {
		case string:
			out = v
		default:
			b, _ := json.Marshal(v)
			out = string(b)
		}
	}
	if len(out) > maxToolOutput {
		out = out[:maxToolOutput] + "\n[truncated]"
	}
	return out
}

func (s *Session) toolFor(id acp.ToolCallId) *toolState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.tools[id]
	if !ok {
		st = &toolState{id: string(id)}
		s.tools[id] = st
		s.toolOrder = append(s.toolOrder, id)
	}
	return st
}

// progressTool emits the tool_call chunk once there is something to show (input, or a status past "pending"), and the
// tool_result chunk when the call completes or fails. Each is emitted at most once per call.
func (s *Session) progressTool(st *toolState) {
	s.mu.Lock()
	callNow := !st.emittedCall && (st.haveInput() || (st.status != "" && st.status != "pending"))
	s.mu.Unlock()
	if callNow {
		s.emitCall(st)
	}
	s.mu.Lock()
	resultNow := st.emittedCall && !st.emittedResult && isTerminal(st.status)
	var res workflow.ToolResult
	if resultNow {
		st.emittedResult = true
		res = workflow.ToolResult{ID: st.id, Name: st.toolName(), Args: st.args(), Output: st.outputText(), IsError: st.status == "failed"}
	}
	s.mu.Unlock()
	if resultNow {
		s.emitKind(workflow.ChunkToolResult, res)
	}
}

func (s *Session) emitCall(st *toolState) {
	s.mu.Lock()
	if st.emittedCall {
		s.mu.Unlock()
		return
	}
	st.emittedCall = true
	tc := workflow.ToolCall{ID: st.id, Name: st.toolName(), Args: st.args()}
	s.mu.Unlock()
	s.flushMessage()
	s.emitKind(workflow.ChunkToolCall, tc)
}

// flushTools emits calls that were held back waiting for input, and results for those already finished, at turn end.
func (s *Session) flushTools() {
	s.mu.Lock()
	order := append([]acp.ToolCallId(nil), s.toolOrder...)
	s.mu.Unlock()
	for _, id := range order {
		s.mu.Lock()
		st := s.tools[id]
		s.mu.Unlock()
		if st == nil {
			continue
		}
		s.emitCall(st)
		s.progressTool(st)
	}
	s.mu.Lock()
	s.tools = map[acp.ToolCallId]*toolState{}
	s.toolOrder = nil
	s.mu.Unlock()
}

// flushMessage emits the accumulated assistant text as one message_end chunk.
func (s *Session) flushMessage() {
	s.mu.Lock()
	txt := s.msg.String()
	s.msg.Reset()
	s.msgID = ""
	s.mu.Unlock()
	if strings.TrimSpace(txt) != "" {
		s.emitKind(workflow.ChunkMessageEnd, workflow.TextDelta{Text: txt})
	}
}

// RequestPermission surfaces the request as a permission_request chunk, asks the PermissionHandler and answers the agent.
// The default handler rejects. A cancelled turn or a stopped session answers "cancelled".
func (c *client) RequestPermission(_ context.Context, p acp.RequestPermissionRequest) (res acp.RequestPermissionResponse, err error) {
	defer safe.Recover("acp.RequestPermission", &err)
	s := c.s
	cancelled := acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{Outcome: "cancelled"}}}

	st := s.toolFor(p.ToolCall.ToolCallId)
	s.mu.Lock()
	if p.ToolCall.Title != nil {
		st.title = *p.ToolCall.Title
	}
	if p.ToolCall.Kind != nil {
		st.kind = string(*p.ToolCall.Kind)
	}
	st.metaName = firstNonEmpty(metaToolName(p.ToolCall.Meta), st.metaName)
	if p.ToolCall.RawInput != nil {
		st.input = p.ToolCall.RawInput
	}
	st.locations = locPaths(p.ToolCall.Locations, st.locations)
	st.asked = true
	s.permSeq++
	req := PermissionRequest{
		ID: fmt.Sprintf("perm_%d", s.permSeq), SessionID: string(p.SessionId), ToolCallID: st.id,
		Tool: st.toolName(), Title: st.title, Kind: st.kind, Args: st.args(), Locations: st.locations,
	}
	s.mu.Unlock()
	for _, o := range p.Options {
		req.Options = append(req.Options, PermissionOption{ID: string(o.OptionId), Name: o.Name, Kind: string(o.Kind)})
	}
	s.emitCall(st) // the call must precede its permission request in the stream
	s.emitKind(workflow.ChunkPermission, req)

	handler := s.adapter.cfg.Permission
	if ask := s.spec.AskPermission; ask != nil {
		handler = askHandler(ask) // the session's own asker (a worker turn, a chat) decides over the adapter's default
	}
	nobody := handler == nil // no asker and no adapter handler: Drydock itself refuses, and says so below
	if nobody {
		handler = RejectOnce
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	s.perms[req.ID] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.perms, req.ID)
		s.mu.Unlock()
	}()

	type answer struct{ id string }
	ch := make(chan answer, 1)
	safe.Go("acp.client.RequestPermission", func() { ch <- answer{handler(ctx, req)} })
	var chosen string
	select {
	case a := <-ch:
		chosen = a.id
	case <-ctx.Done():
		return cancelled, nil
	}
	if ctx.Err() != nil {
		return cancelled, nil
	}
	if nobody {
		// the request was answered without anyone being asked: the refusal is on the record, right after the request and before
		// the agent reports the call as failed, so the person sees why the call failed (AG-UI review 2026-10-25)
		s.emitKind(workflow.ChunkPermissionDenied, workflow.PermissionDenial{ToolName: req.Tool, ToolUseID: req.ToolCallID, Input: req.Args,
			Reason: "nobody could be asked in this session, so Drydock refused the request"})
	}
	for _, o := range req.Options {
		if o.ID == chosen && chosen != "" {
			if o.Kind == KindAllowOnce || o.Kind == KindAllowAlways {
				// what was allowed is this call as it is now: what the agent reports of it later is compared with it
				s.mu.Lock()
				st.approvedSum, st.changed = st.callSum(), false
				s.mu.Unlock()
			}
			return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{
				OptionId: acp.PermissionOptionId(chosen), Outcome: "selected"}}}, nil
		}
	}
	return cancelled, nil // "" or an id the agent did not offer is never an approval
}

// ReadTextFile serves fs/read_text_file for paths inside the session cwd only.
func (c *client) ReadTextFile(_ context.Context, p acp.ReadTextFileRequest) (res acp.ReadTextFileResponse, err error) {
	defer safe.Recover("acp.ReadTextFile", &err)
	if c.s.adapter.cfg.NoFS {
		return acp.ReadTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsReadTextFile)
	}
	txt, err := c.s.guard.readFile(p.Path, p.Line, p.Limit)
	if err != nil {
		return acp.ReadTextFileResponse{}, reqErr(err)
	}
	return acp.ReadTextFileResponse{Content: txt}, nil
}

// WriteTextFile serves fs/write_text_file for paths inside the session cwd only.
func (c *client) WriteTextFile(_ context.Context, p acp.WriteTextFileRequest) (res acp.WriteTextFileResponse, err error) {
	defer safe.Recover("acp.WriteTextFile", &err)
	if c.s.adapter.cfg.NoFS {
		return acp.WriteTextFileResponse{}, acp.NewMethodNotFound(acp.ClientMethodFsWriteTextFile)
	}
	if err := c.s.guard.writeFile(p.Path, p.Content); err != nil {
		return acp.WriteTextFileResponse{}, reqErr(err)
	}
	return acp.WriteTextFileResponse{}, nil
}

// reqErr turns a guard error into a JSON-RPC error the agent can show its model without us leaking host paths beyond
// what the agent itself sent.
func reqErr(err error) error {
	return &acp.RequestError{Code: -32602, Message: err.Error()}
}

func (c *client) CreateTerminal(ctx context.Context, p acp.CreateTerminalRequest) (res acp.CreateTerminalResponse, err error) {
	defer safe.Recover("acp.CreateTerminal", &err)
	return c.s.terms.create(ctx, p)
}
func (c *client) KillTerminal(_ context.Context, p acp.KillTerminalRequest) (res acp.KillTerminalResponse, err error) {
	defer safe.Recover("acp.KillTerminal", &err)
	return c.s.terms.kill(p)
}
func (c *client) TerminalOutput(_ context.Context, p acp.TerminalOutputRequest) (res acp.TerminalOutputResponse, err error) {
	defer safe.Recover("acp.TerminalOutput", &err)
	return c.s.terms.output(p)
}
func (c *client) ReleaseTerminal(_ context.Context, p acp.ReleaseTerminalRequest) (res acp.ReleaseTerminalResponse, err error) {
	defer safe.Recover("acp.ReleaseTerminal", &err)
	return c.s.terms.release(p)
}
func (c *client) WaitForTerminalExit(ctx context.Context, p acp.WaitForTerminalExitRequest) (res acp.WaitForTerminalExitResponse, err error) {
	defer safe.Recover("acp.WaitForTerminalExit", &err)
	return c.s.terms.wait(ctx, p)
}
