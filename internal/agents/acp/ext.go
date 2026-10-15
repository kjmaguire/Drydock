package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/coder/acp-go-sdk"
)

// Agent extensions Drydock reads. ACP lets an agent add notifications and `_meta` keys of its own; the ones here are
// OpenCode's, read from its source (packages/cli/src/acp: capabilities.ts, child.ts, compaction.ts, translate.ts). They are
// matched by key and method name, never by provider name (decision D10), and an agent that sends none of them is unaffected.
//
// Context compaction. OpenCode reports a compaction of the session's context in one of two forms, chosen by the client's
// capabilities: ACP's own (unstable) `compaction_update` when the client advertises `clientCapabilities.session.compaction`,
// otherwise a `session_info_update` whose `_meta["opencode/compaction"]` is {status: started|completed|failed, messageId,
// reason: auto|manual, error?}. The pinned SDK (acp-go-sdk 0.13.5) can do neither half of the first form: ClientCapabilities
// has no `session` field to advertise it with, and it has no `compaction_update` variant (it decodes one as a
// session_info_update and drops its status). So Drydock does not advertise it and reads the second form, which carries the
// whole lifecycle. A completed compaction is counted into the turn's usage chunk (UsageChunk.Compactions), where the session
// policy sees it (internal/pipeline/session.go, decision P39). Unknown session updates are still ignored, as before.
//
// Sub-agents. OpenCode's task tool runs a child session. Without the client's opt-in it forwards the child's updates on the
// session's own stream (tool call ids prefixed with the child's id, `_meta["opencode/child-session"]` set), so a sub-agent's
// text would join the parent's message. A client that sends `clientCapabilities._meta["opencode/child-session-updates"]:
// true` gets them on a notification of their own instead (ChildUpdateMethod). Only a manifest declares it
// (transports.acp.client_meta; only opencode.yaml does). Drydock maps a child's tool calls into the turn's tool events, so
// the watchdog's loop detection and the activity feed see them, and drops its text, thoughts, plans and usage, which belong
// to the child.

const (
	// ChildSessionUpdatesMeta is the clientCapabilities._meta key that asks OpenCode for ChildUpdateMethod.
	ChildSessionUpdatesMeta = "opencode/child-session-updates"
	// ChildUpdateMethod carries a child session's update: {rootSessionId, childSessionId, parentSessionId, depth, title?,
	// type: "update", update: SessionUpdate} or {..., type: "status", status, error?}. OpenCode sends it with the ACP
	// TypeScript SDK's notify, which does not add the "_" ACP reserves for extension methods; extRenamer adds it.
	ChildUpdateMethod = "opencode/session/child_update"
	// compactionMeta is the session_info_update _meta key of OpenCode's compaction marker.
	compactionMeta = "opencode/compaction"
	// childSessionMeta marks an update OpenCode forwarded from a child session onto the session's own stream.
	childSessionMeta = "opencode/child-session"
)

// declaresChildUpdates reports whether the client asks for child-session updates in its initialize _meta.
func declaresChildUpdates(meta map[string]any) bool {
	v, _ := meta[ChildSessionUpdatesMeta].(bool)
	return v
}

var _ acp.ExtensionMethodHandler = (*client)(nil)

// HandleExtensionMethod serves the agent's extension methods ("_"-prefixed). Only the child-session update is read; any other
// is method-not-found, which the SDK drops silently for a notification, as it did before the client handled extensions.
func (c *client) HandleExtensionMethod(_ context.Context, method string, params json.RawMessage) (any, error) {
	if method == "_"+ChildUpdateMethod {
		c.s.onChildUpdate(params)
		return nil, nil
	}
	return nil, acp.NewMethodNotFound(method)
}

// childUpdate is the part of a ChildUpdateMethod notification Drydock reads.
type childUpdate struct {
	RootSessionID  string          `json:"rootSessionId"`
	ChildSessionID string          `json:"childSessionId"`
	Type           string          `json:"type"`
	Update         json.RawMessage `json:"update,omitempty"`
}

// onChildUpdate maps a sub-agent's tool calls into the running turn's tool events. Updates for another session, outside a
// turn (children outlive a cancelled turn) or during a session/load replay are dropped.
func (s *Session) onChildUpdate(raw json.RawMessage) {
	var cu childUpdate
	if json.Unmarshal(raw, &cu) != nil || cu.Type != "update" || len(cu.Update) == 0 {
		return
	}
	s.mu.Lock()
	ours := !s.replaying && s.turnActive && s.sid != "" && acp.SessionId(cu.RootSessionID) == s.sid
	if ours {
		s.childUpdates++
	}
	s.mu.Unlock()
	if !ours {
		return
	}
	var u acp.SessionUpdate
	if json.Unmarshal(cu.Update, &u) != nil {
		return
	}
	switch {
	case u.ToolCall != nil:
		tc := *u.ToolCall
		tc.ToolCallId = childToolID(cu.ChildSessionID, tc.ToolCallId)
		s.onToolCall(&tc)
	case u.ToolCallUpdate != nil:
		tu := *u.ToolCallUpdate
		tu.ToolCallId = childToolID(cu.ChildSessionID, tu.ToolCallId)
		s.onToolCallUpdate(&tu)
	}
}

// childToolID keeps a child's tool call ids apart from the session's own. OpenCode already prefixes them with the child's id;
// an id without the prefix gets it.
func childToolID(child string, id acp.ToolCallId) acp.ToolCallId {
	if child == "" || strings.HasPrefix(string(id), child+":") {
		return id
	}
	return acp.ToolCallId(child + ":" + string(id))
}

// onSessionInfo reads OpenCode's compaction marker. A child's compaction concerns the child's context and is not counted.
func (s *Session) onSessionInfo(u *acp.SessionSessionInfoUpdate) {
	if u.Meta == nil {
		return
	}
	if _, child := u.Meta[childSessionMeta]; child {
		return
	}
	m, ok := u.Meta[compactionMeta].(map[string]any)
	if !ok {
		return
	}
	if st, _ := m["status"].(string); st == "completed" {
		s.mu.Lock()
		s.compactions++
		s.usage.compactions++
		s.mu.Unlock()
	}
}

// extRenamer sits between the agent's stdout and the SDK when the client asked for child-session updates. The SDK routes a
// notification it does not know to the client's extension handler only when its name starts with "_" and drops it otherwise,
// so the agent's ChildUpdateMethod is renamed to "_" + ChildUpdateMethod; every other line passes unchanged and in order.
type extRenamer struct {
	r       *bufio.Reader
	pending []byte
	err     error
}

func newExtRenamer(r io.Reader) *extRenamer { return &extRenamer{r: bufio.NewReaderSize(r, 64<<10)} }

func (x *extRenamer) Read(p []byte) (int, error) {
	for len(x.pending) == 0 {
		if x.err != nil {
			return 0, x.err
		}
		var line []byte
		line, x.err = x.r.ReadBytes('\n')
		x.pending = renameChildUpdate(line)
	}
	n := copy(p, x.pending)
	x.pending = x.pending[n:]
	return n, nil
}

var bareChildUpdate = []byte(`"` + ChildUpdateMethod + `"`)

// renameChildUpdate returns line with a ChildUpdateMethod method renamed, or line itself.
func renameChildUpdate(line []byte) []byte {
	if !bytes.Contains(line, bareChildUpdate) {
		return line
	}
	var msg map[string]json.RawMessage
	if json.Unmarshal(line, &msg) != nil {
		return line
	}
	var method string
	if json.Unmarshal(msg["method"], &method) != nil || method != ChildUpdateMethod {
		return line
	}
	msg["method"], _ = json.Marshal("_" + ChildUpdateMethod)
	out, err := json.Marshal(msg)
	if err != nil {
		return line
	}
	return append(out, '\n')
}
