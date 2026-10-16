package acp

import (
	"context"
	"encoding/json"
)

// Option kinds of session/request_permission (ACP).
const (
	KindAllowOnce    = "allow_once"
	KindAllowAlways  = "allow_always"
	KindRejectOnce   = "reject_once"
	KindRejectAlways = "reject_always"
)

// PermissionOption is one choice the agent offers.
type PermissionOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// PermissionRequest is an agent's request for approval of a tool call. It is delivered both to the PermissionHandler and,
// as the payload of a permission_request chunk, to whoever consumes the session output.
type PermissionRequest struct {
	ID         string             `json:"id"` // unique per request within the session
	SessionID  string             `json:"sessionId"`
	ToolCallID string             `json:"toolCallId"`
	Tool       string             `json:"tool"`  // normalized tool name (as in the tool_call chunk)
	Title      string             `json:"title"` // the agent's human-readable description
	Kind       string             `json:"kind,omitempty"`
	Args       json.RawMessage    `json:"args,omitempty"`
	Locations  []string           `json:"locations,omitempty"`
	Options    []PermissionOption `json:"options"`
}

// PermissionHandler decides a permission request and returns the chosen option id. Returning "" (or an id the agent did
// not offer) cancels the request; an unknown id is never mapped to an approval. The context is cancelled when the turn is
// interrupted or the session stops, in which case the answer is discarded and the agent is told the request was cancelled.
// The handler may block (for example while a human decides).
type PermissionHandler func(ctx context.Context, req PermissionRequest) (optionID string)

// RejectOnce is the default PermissionHandler: it selects a reject_once option, then reject_always, and otherwise
// cancels. It never approves anything.
func RejectOnce(_ context.Context, req PermissionRequest) string {
	for _, want := range []string{KindRejectOnce, KindRejectAlways} {
		for _, o := range req.Options {
			if o.Kind == want {
				return o.ID
			}
		}
	}
	return ""
}

// AllowOnce selects an allow_once option (then allow_always is NOT used). It exists for tests and for callers that have
// already decided policy elsewhere; it is never the default.
func AllowOnce(_ context.Context, req PermissionRequest) string {
	for _, o := range req.Options {
		if o.Kind == KindAllowOnce {
			return o.ID
		}
	}
	return ""
}
