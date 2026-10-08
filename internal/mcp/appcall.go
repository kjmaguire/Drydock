package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// EventAppCall is published for every call the app itself makes to an upstream (as opposed to an agent run's call, which is
// mcp.call). Like mcp.call it carries hashes and sizes, never arguments or results.
const EventAppCall = "mcp.app_call"

// AppUpstream is the gateway's client side for calls made by the app itself, not by an agent run: the integrations package
// uses it to sync Linear, Notion, Sentry and Aikido (docs/integrations.md section 1, "App sync"). It reuses the same pooled
// upstream as agent runs (same transports, credential resolution, circuit breaker, stdio servers under the process manager,
// redirect refusal), but there is no run, so there is no agent allowlist: the caller (the integrations package) owns the
// list of tools it may call and gates every write behind an approval.
type AppUpstream struct {
	g    *Gateway
	spec ServerSpec
	u    *upstream
}

// AppResult is a tool result as the app sees it. Text is the concatenated text content; Structured is the structuredContent
// when the server sent one. The content is untrusted data and has not been scanned: the caller scans it.
type AppResult struct {
	Text       string
	Structured json.RawMessage
	IsError    bool
}

// AppUpstream returns a handle to the pooled upstream for spec, resolving its credential through Options.Credentials. A
// changed credential yields a different pooled upstream (the pool key carries a hash of the credential, never the secret).
func (g *Gateway) AppUpstream(ctx context.Context, spec ServerSpec) (*AppUpstream, error) {
	spec.applyDefaults()
	if err := spec.Validate(false); err != nil {
		return nil, fmt.Errorf("mcp: server %q: %w", spec.ID, err)
	}
	ci := credInfo{id: "anon"}
	if spec.Transport == TransportHTTP && spec.Auth.Kind != AuthNone {
		if g.opts.Credentials == nil {
			return nil, &unavailableError{spec.ID, errors.New("no credential source configured")}
		}
		tok, err := g.opts.Credentials.Credential(ctx, spec.ID, spec.Auth.Keyring)
		if (err != nil || tok == "") && !spec.Auth.Optional {
			if err == nil {
				err = errors.New("credential is empty")
			}
			return nil, &unavailableError{spec.ID, fmt.Errorf("credentials for %s are not available: %w", spec.ID, err)}
		}
		if tok != "" {
			sum := sha256.Sum256([]byte(tok))
			ci = credInfo{token: tok, id: hex.EncodeToString(sum[:8])}
		}
	}
	key := spec.ID + "|" + specPoolHash(spec) + "|" + ci.id
	g.mu.Lock()
	defer g.mu.Unlock()
	u := g.pool[key]
	if u == nil {
		u = &upstream{g: g, spec: spec, key: key, credID: ci.id, token: ci.token}
		u.version = g.loadVersion(key)
		u.touch()
		g.pool[key] = u
	}
	return &AppUpstream{g: g, spec: spec, u: u}, nil
}

// Tools lists the tool names the server offers right now (a fresh tools/list, which also proves the server is reachable and
// the credential is accepted).
func (a *AppUpstream) Tools(ctx context.Context) ([]string, error) {
	sess, err := a.u.ensure(ctx)
	if err != nil {
		return nil, err
	}
	a.u.touch()
	all, err := listAll(ctx, sess)
	if err != nil {
		a.u.failedCall(ctx, err)
		return nil, &unavailableError{a.spec.ID, err}
	}
	a.u.noteSuccess()
	a.u.mu.Lock()
	a.u.tools = all
	a.u.mu.Unlock()
	names := make([]string, 0, len(all))
	for _, t := range all {
		names = append(names, t.Name)
	}
	return names, nil
}

// ToolInfo is one tool a server offers, as discovery reports it. ReadOnlyHint is what the server CLAIMS: Drydock never relies on
// it for a private server (the human decides which tools are read-only), it is only shown as a suggestion.
type ToolInfo struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema,omitempty"`
	ReadOnlyHint bool           `json:"readOnlyHint"`
}

// ToolList is Tools with the descriptions, input schemas and the servers' read-only claims. It makes a fresh tools/list.
func (a *AppUpstream) ToolList(ctx context.Context) ([]ToolInfo, error) {
	if _, err := a.Tools(ctx); err != nil {
		return nil, err
	}
	a.u.mu.Lock()
	all := a.u.tools
	a.u.mu.Unlock()
	out := make([]ToolInfo, 0, len(all))
	for _, t := range all {
		ti := ToolInfo{Name: t.Name, Description: t.Description}
		if m, ok := t.InputSchema.(map[string]any); ok && m["type"] == "object" {
			ti.InputSchema = m
		}
		ti.ReadOnlyHint, _ = toolHints(t)
		out = append(out, ti)
	}
	return out, nil
}

// Call calls one tool. A returned error means the server could not be reached (errors.Is(err, ErrUpstreamUnavailable)); a
// tool-level failure comes back as a result with IsError.
func (a *AppUpstream) Call(ctx context.Context, tool string, args any) (*AppResult, error) {
	start := a.g.now()
	var raw json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("mcp: arguments are not valid JSON: %w", err)
		}
		raw = b
	}
	canon, _ := canonicalJSON(raw)
	res, err := a.u.call(ctx, tool, raw)
	status, size := "ok", 0
	var out *AppResult
	if err != nil {
		status = "unavailable"
	} else {
		text, isErr := flattenResult(res)
		out = &AppResult{Text: text, IsError: isErr}
		if res.StructuredContent != nil {
			if b, merr := json.Marshal(res.StructuredContent); merr == nil {
				out.Structured = b
			}
		}
		size = len(text) + len(out.Structured)
		if isErr {
			status = "error"
		}
	}
	a.g.publish(ctx, EventAppCall, RunInfo{}, "", map[string]any{
		"server": a.spec.ID, "tool": tool, "args_hash": sha256Hex(canon), "status": status, "bytes": size,
		"duration_ms": ms(a.g.now().Sub(start)),
	})
	return out, err
}
