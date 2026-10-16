package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kjmaguire/drydock/internal/ids"
	"github.com/kjmaguire/drydock/internal/safe"
	"github.com/kjmaguire/drydock/internal/security"
	"github.com/kjmaguire/drydock/internal/storage"
)

// Run is the gateway state of one agent run: its token, its MCP server (towards the CLI), its policy and its result handles.
type Run struct {
	g         *Gateway
	cfg       RunConfig
	dir       string
	specs     map[string]*ServerSpec
	tokenHash [32]byte
	server    *sdk.Server
	handler   http.Handler
	closed    atomic.Bool
	// done is closed by EndRun: a call still waiting (a permission prompt waiting for a person) ends with a refusal.
	done      chan struct{}
	closeOnce sync.Once
	// withheld remembers the tools already reported as kept from the model (toolreview.go).
	withheld sync.Map

	hmu     sync.Mutex
	handles map[string]string // handle -> raw file path

	credMu sync.Mutex
	creds  map[string]credInfo

	fetchLim *bucket
	postLim  *bucket
}

// middleware replaces tools/list and tools/call: the run's policy, not a static tool registry, decides what exists.
func (r *Run) middleware(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (res sdk.Result, err error) {
		// the SDK serves each request on a goroutine of its own and does not recover: a panic in a tool call, in the list or in
		// anything under them (a server's answer, an agent's arguments) ends the application unless it is contained here
		defer safe.Recover("mcp "+method, &err)
		switch method {
		case "tools/list":
			// The SDK fills in the cache fields only for results its own handlers build. This result replaces them, and a client that
			// negotiated the 2026-07-28 protocol (Claude Code 2.1.294 does) rejects a tools/list whose cacheScope is empty. The
			// list depends on the run's policy, so it is private and not cacheable.
			return &sdk.ListToolsResult{Cacheable: sdk.Cacheable{TTLMs: 0, CacheScope: "private"}, Tools: r.listTools(ctx)}, nil
		case "tools/call":
			cr, ok := req.(*sdk.CallToolRequest)
			if !ok {
				return nil, fmt.Errorf("unexpected request type %T", req)
			}
			return completeResult(cr, r.callTool(ctx, cr)), nil
		}
		return next(ctx, method, req)
	}
}

// ---- policy ----

// serverUsable reports whether an upstream may be offered at all to this run.
func (r *Run) serverUsable(spec *ServerSpec) bool {
	if r.cfg.DataClass == DataClassTrainable && spec.DataClass == ContentPrivate {
		return false
	}
	return true
}

func (r *Run) workspaceUsable() bool { return r.cfg.DataClass != DataClassTrainable }

// toolAllowed applies, in order: data class, the run policy and the server's own ceiling.
func (r *Run) upstreamToolAllowed(spec *ServerSpec, tool string) bool {
	return r.serverUsable(spec) && r.cfg.Policy.Allows(spec.ID, tool) && spec.toolAllowedBySpec(tool)
}

func (r *Run) workspaceToolAllowed(tool string) bool {
	return r.workspaceUsable() && r.cfg.Policy.Allows(WorkspaceServerID, tool)
}

// anyToolAllowed reports (without contacting any upstream) whether the run has at least one tool besides fetch_range.
func (r *Run) anyToolAllowed() bool {
	if r.workspaceUsable() && len(r.cfg.Policy.Allow[WorkspaceServerID]) > 0 {
		for _, t := range workspaceToolDefs() {
			if r.workspaceToolAllowed(t.Name) {
				return true
			}
		}
	}
	for id, spec := range r.specs {
		if r.serverUsable(spec) && len(r.cfg.Policy.Allow[id]) > 0 {
			return true
		}
	}
	return false
}

// ---- tools/list ----

func (r *Run) listTools(ctx context.Context) []*sdk.Tool {
	out := []*sdk.Tool{}
	if r.workspaceUsable() {
		for _, t := range workspaceToolDefs() {
			if r.workspaceToolAllowed(t.Name) {
				wt := &sdk.Tool{Name: t.Name, Description: t.Description, InputSchema: t.Schema}
				if t.ReadOnly {
					wt.Annotations = &sdk.ToolAnnotations{ReadOnlyHint: true}
				}
				out = append(out, wt)
			}
		}
	}
	for _, id := range sortedKeys(r.specs) {
		spec := r.specs[id]
		if !r.serverUsable(spec) || len(r.cfg.Policy.Allow[id]) == 0 {
			continue
		}
		u, err := r.upstreamFor(ctx, spec)
		if err != nil {
			r.g.opts.Logger.Debug("mcp: tools/list skips upstream", "server", id, "err", err)
			continue
		}
		tools := u.cachedTools()
		fromSpec := false
		if tools == nil && len(spec.Tools) > 0 {
			tools = declaredTools(spec) // lazy start: do not contact the upstream just to list
			fromSpec = true             // these are the reviewed tools themselves: nothing to compare them with yet
		}
		if tools == nil {
			tools, err = u.listTools(ctx)
			if err != nil {
				r.g.opts.Logger.Warn("mcp: tools/list: upstream unavailable", "server", id, "err", err)
				continue
			}
		}
		for _, t := range tools {
			if !r.upstreamToolAllowed(spec, t.Name) {
				continue
			}
			if why := r.reviewed(spec, t.Name, t.Description, t.InputSchema, !fromSpec); why != "" {
				r.withhold(ctx, id, t.Name, why)
				continue
			}
			ro, _ := toolHints(t)
			et := &sdk.Tool{Name: exposed(id, t.Name), Description: t.Description, InputSchema: t.InputSchema, Title: t.Title}
			if et.InputSchema == nil {
				et.InputSchema = map[string]any{"type": "object", "additionalProperties": true}
			}
			// Hints come from our classification, never from the upstream: a server must not be able to make a CLI skip
			// its own permission prompt by claiming readOnlyHint.
			if spec.isKnownReadOnly(t.Name, ro && spec.Trust != TrustPrivateData) {
				et.Annotations = &sdk.ToolAnnotations{ReadOnlyHint: true}
			}
			out = append(out, et)
		}
	}
	if len(out) > 0 {
		out = append(out, fetchRangeTool())
	}
	if r.permissionPromptAllowed() {
		out = append(out, permissionPromptTool())
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func declaredTools(spec *ServerSpec) []*sdk.Tool {
	out := make([]*sdk.Tool, 0, len(spec.Tools))
	for _, d := range spec.Tools {
		var schema any = map[string]any{"type": "object", "additionalProperties": true}
		if d.InputSchema != nil {
			schema = d.InputSchema
		}
		out = append(out, &sdk.Tool{Name: d.Name, Description: d.Description, InputSchema: schema})
	}
	return out
}

func toolHints(t *sdk.Tool) (readOnly, destructive bool) {
	if t == nil || t.Annotations == nil {
		return false, false
	}
	a := t.Annotations
	return a.ReadOnlyHint, !a.ReadOnlyHint && a.DestructiveHint != nil && *a.DestructiveHint
}

func fetchRangeTool() *sdk.Tool {
	return &sdk.Tool{
		Name:        ToolFetchRange,
		Description: "Read a further byte range of a truncated tool result. Use the handle and next offset printed in the truncation footer. Handles are valid only for this run.",
		InputSchema: map[string]any{
			"type": "object", "required": []any{"handle"},
			"properties": map[string]any{
				"handle": map[string]any{"type": "string"},
				"offset": map[string]any{"type": "integer", "minimum": 0},
				"length": map[string]any{"type": "integer", "minimum": 1, "maximum": 65536, "default": 8192},
			},
		},
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}
}

// ---- tools/call ----

type callRec struct {
	id             string
	server, tool   string
	argsHash       string
	bytesIn        int
	decision       string
	reason         string
	cache          string
	rawBytes       int
	returnedBytes  int
	truncated      bool
	redactions     []string
	upstreamStatus string
	suspicious     []string
	egressChecked  bool
	handle         string
	tokens         int
	savedTrunc     int
	savedCache     int
	hit            bool
	cacheKey       string
	critical       bool
}

type target struct {
	server     string
	tool       string
	spec       *ServerSpec // nil for the Workspace server
	sideEffect bool
}

func (r *Run) resolve(name string) (target, bool) {
	if id, tool, ok := strings.Cut(name, ToolSeparator); ok {
		if spec := r.specs[id]; spec != nil && tool != "" {
			return target{server: id, tool: tool, spec: spec}, true
		}
		return target{}, false
	}
	for _, t := range workspaceToolDefs() {
		if t.Name == name {
			return target{server: WorkspaceServerID, tool: name}, true
		}
	}
	return target{}, false
}

func textResult(text string, isErr bool) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}, IsError: isErr}
}

func (r *Run) callTool(ctx context.Context, req *sdk.CallToolRequest) *sdk.CallToolResult {
	g := r.g
	start := g.now()
	c := &callRec{id: ids.New("c"), tool: req.Params.Name, decision: DecisionAllowed, cache: CacheBypass, upstreamStatus: "n/a", server: "gateway"}
	var args json.RawMessage
	if req.Params != nil {
		args = req.Params.Arguments
	}
	c.bytesIn = len(args)
	canon, cerr := canonicalJSON(args)
	if cerr == nil {
		c.argsHash = sha256Hex(canon)
		// From here on the call is made with the bytes that were hashed (and shown to a person): the agent's own bytes may
		// carry a duplicate key or another spelling that a server reads differently from the canonical text (audit 2026-10-16, H8).
		args = canon
	}
	var res *sdk.CallToolResult
	defer func() { r.audit(ctx, c, start, res) }()

	if r.closed.Load() {
		res = r.deny(ctx, c, "the run has ended")
		return res
	}
	if cerr != nil {
		c.upstreamStatus = "n/a"
		res = textResult("invalid arguments: "+cerr.Error(), true)
		return res
	}
	if c.tool == ToolPermissionPrompt {
		c.server = WorkspaceServerID
		if !r.permissionPromptAllowed() {
			res = r.deny(ctx, c, "tool is not in the run's allowlist")
			return res
		}
		res = r.permissionPrompt(ctx, c, args)
		return res
	}
	if c.tool == ToolFetchRange {
		if !r.anyToolAllowed() {
			res = r.deny(ctx, c, "no tools are allowed for this run")
			return res
		}
		res = r.fetchRange(ctx, c, canon)
		return res
	}
	t, ok := r.resolve(c.tool)
	c.server = t.server
	if !ok {
		c.server = "gateway"
		res = r.deny(ctx, c, "unknown tool")
		return res
	}
	c.tool = t.tool
	if t.spec != nil {
		if !r.upstreamToolAllowed(t.spec, t.tool) {
			res = r.deny(ctx, c, "tool is not in the run's allowlist")
			return res
		}
		ro, destr := false, false
		var live *sdk.Tool
		if u, err := r.upstreamFor(ctx, t.spec); err == nil {
			for _, ut := range u.cachedTools() {
				if ut.Name == t.tool {
					ro, destr = toolHints(ut)
					live = ut
				}
			}
		}
		// the tool must still be what a person reviewed, whatever the model was told about it
		desc, schema, known := "", any(nil), false
		if live != nil {
			desc, schema, known = live.Description, live.InputSchema, true
		}
		if why := r.reviewed(t.spec, t.tool, desc, schema, known); why != "" {
			r.withhold(ctx, t.server, t.tool, why)
			res = r.deny(ctx, c, "tool was withheld: "+why)
			return res
		}
		t.sideEffect = t.spec.isSideEffect(t.tool, ro && t.spec.Trust != TrustPrivateData, destr)
	} else if !r.workspaceToolAllowed(t.tool) {
		res = r.deny(ctx, c, "tool is not in the run's allowlist")
		return res
	}
	if t.sideEffect || r.cfg.Policy.NeedsApproval(exposed(t.server, t.tool)) {
		if dres := r.approval(ctx, c, t, canon); dres != nil {
			res = dres
			return res
		}
	}
	if t.spec == nil {
		res = r.callWorkspace(ctx, c, t, args)
	} else {
		res = r.callUpstream(ctx, c, t, args, canon)
	}
	return res
}

func (r *Run) deny(ctx context.Context, c *callRec, reason string) *sdk.CallToolResult {
	c.decision, c.reason = DecisionDenied, reason
	r.g.publish(ctx, EventDenied, r.cfg.Info, c.id, map[string]any{"server": c.server, "tool": c.tool, "reason": reason})
	return textResult(fmt.Sprintf("denied: %q is not available to this run (%s)", c.tool, reason), true)
}

// approval parks a write-class call until the Approver decides. It returns nil when the call may proceed.
func (r *Run) approval(ctx context.Context, c *callRec, t target, canon []byte) *sdk.CallToolResult {
	ap := r.g.opts.Approver
	if ap == nil {
		c.decision, c.reason = DecisionApprovalPending, "approval required and no approver is configured"
		r.g.publish(ctx, EventDenied, r.cfg.Info, c.id, map[string]any{"server": c.server, "tool": c.tool, "reason": c.reason})
		return textResult(fmt.Sprintf("%q needs a human approval and none can be requested right now; the call was not made", c.tool), true)
	}
	c.decision = DecisionApprovalPending
	ok, err := ap.Approve(ctx, ApprovalRequest{Run: r.cfg.Info, CallID: c.id, Server: t.server, Tool: t.tool, ArgsHash: c.argsHash, Args: canon})
	if err != nil || !ok {
		c.decision = DecisionDenied
		c.reason = "approval rejected"
		if err != nil {
			c.reason = "approval failed: " + err.Error()
		}
		r.g.publish(ctx, EventDenied, r.cfg.Info, c.id, map[string]any{"server": c.server, "tool": c.tool, "reason": c.reason})
		if errors.Is(err, security.ErrUnreviewable) {
			return textResult(fmt.Sprintf("%q was not approved: its arguments are too large for a person to read, so nobody can approve them. Call it again with smaller arguments; the call was not made", c.tool), true)
		}
		return textResult(fmt.Sprintf("%q was not approved; the call was not made", c.tool), true)
	}
	c.decision = DecisionAllowed
	c.reason = "approved"
	return nil
}

// ---- upstream path ----

func (r *Run) callUpstream(ctx context.Context, c *callRec, t target, args json.RawMessage, canon []byte) *sdk.CallToolResult {
	g := r.g
	spec := t.spec
	if rule := spec.egressRule(); rule != nil {
		c.egressChecked = true
		if reason := r.egressViolation(rule, args); reason != "" {
			c.decision, c.reason = DecisionDenied, "egress: "+reason
			g.publish(ctx, EventEgressBlocked, r.cfg.Info, c.id, map[string]any{"server": spec.ID, "tool": t.tool, "reason": reason})
			return textResult("blocked before leaving the machine: "+reason+". Rephrase as a short plain-language question without code, file contents or credentials.", true)
		}
	}
	u, err := r.upstreamFor(ctx, spec)
	if err != nil {
		return r.unavailable(ctx, c, spec, err)
	}
	var (
		raw   string
		isErr bool
	)
	scope := "global"
	if spec.Cache.Scope == "project" || spec.DataClass == ContentPrivate {
		scope = "project:" + r.cfg.Info.ProjectID
	}
	ttl := spec.ttlFor(t.tool)
	// cacheableNow is re-evaluated after the call: the first call to a server learns its tools' read-only hints.
	cacheableNow := func() bool {
		ro := false
		for _, ut := range u.cachedTools() {
			if ut.Name == t.tool {
				ro, _ = toolHints(ut)
			}
		}
		return g.cache.enabled() && ttl > 0 && !t.sideEffect && spec.isKnownReadOnly(t.tool, ro && spec.Trust != TrustPrivateData)
	}
	if g.cache.enabled() && ttl > 0 && !t.sideEffect && u.currentVersion() == "" {
		// The server version is part of the cache key. On the very first call (no version persisted from an earlier
		// process) it is only known after connecting; connecting first keeps the key stable for the whole call.
		if _, err := u.ensure(ctx); err != nil {
			return r.unavailable(ctx, c, spec, err)
		}
	}
	cacheable := cacheableNow()
	var fl *flight
	if cacheable {
		c.cache = CacheMiss
		key := cacheKey(scope, spec.ID, serverIdent(u), t.tool, canon)
		e, evicted, cerr := g.cache.Get(ctx, key)
		if cerr != nil {
			g.opts.Logger.Warn("mcp: cache read failed", "err", cerr)
		}
		if evicted {
			g.publish(ctx, EventCacheEvict, r.cfg.Info, c.id, map[string]any{"server": spec.ID, "tool": t.tool, "reason": "expired"})
		}
		if e != nil {
			raw, isErr = e.Text, e.IsError
			c.cache, c.hit, c.cacheKey = CacheHit, true, key
		} else {
			// Identical calls in flight share one upstream request: followers are served the leader's result.
			var leader bool
			fl, leader = g.joinFlight(key)
			if leader {
				// A previous leader may have stored its result between our lookup and joining: look once more.
				if e2, _, _ := g.cache.Get(ctx, key); e2 != nil {
					raw, isErr = e2.Text, e2.IsError
					c.cache, c.hit, c.cacheKey = CacheHit, true, key
				}
			} else {
				select {
				case <-fl.done:
				case <-ctx.Done():
					return textResult("cancelled", true)
				}
				if fl.ok {
					raw, isErr = fl.raw, fl.isErr
					c.cache, c.hit, c.cacheKey = CacheHit, true, key
				}
				fl = nil
			}
		}
	}
	if fl != nil {
		defer g.leaveFlight(fl) // always releases followers, also on error
	}
	if !c.hit {
		res, err := u.call(ctx, t.tool, args)
		if err != nil {
			return r.unavailable(ctx, c, spec, err)
		}
		raw, isErr = flattenResult(res)
		c.upstreamStatus = "ok"
		if isErr {
			c.upstreamStatus = "error"
		}
		if fl != nil {
			fl.raw, fl.isErr, fl.ok = raw, isErr, true
		}
		if !cacheable && cacheableNow() { // the connection just learned the tools' read-only hints
			c.cache = CacheMiss
			cacheable = true
		}
	}
	return r.finish(ctx, c, spec.ID, t.tool, raw, isErr, spec.inlineBytes(t.tool, g.opts.DefaultInlineBytes), func(redacted string, rules []string) {
		// Store the redacted raw result for later identical calls (docs/mcp.md section 4: never cached when secrets were
		// redacted unless the catalog allows it; never errors; never write-class tools).
		if !cacheable || c.hit || isErr || (len(rules) > 0 && !spec.Cache.AllowRedacted) {
			return
		}
		pk := cacheKey(scope, spec.ID, serverIdent(u), t.tool, canon) // the version may have been learned by this very call
		e := &cacheEntry{Key: pk, Server: spec.ID, Tool: t.tool, Scope: scope, Version: u.currentVersion(), ArgsHash: c.argsHash, Text: redacted}
		if err := g.cache.Put(ctx, e, ttl); err != nil {
			g.opts.Logger.Warn("mcp: cache write failed", "err", err)
		}
	})
}

// flight is one in-progress cacheable upstream call that identical concurrent calls wait for.
type flight struct {
	key   string
	done  chan struct{}
	raw   string
	isErr bool
	ok    bool
}

func (g *Gateway) joinFlight(key string) (*flight, bool) {
	g.fmu.Lock()
	defer g.fmu.Unlock()
	if f := g.flights[key]; f != nil {
		return f, false
	}
	f := &flight{key: key, done: make(chan struct{})}
	g.flights[key] = f
	return f, true
}

func (g *Gateway) leaveFlight(f *flight) {
	g.fmu.Lock()
	delete(g.flights, f.key)
	g.fmu.Unlock()
	close(f.done)
}

func serverIdent(u *upstream) string {
	return sha256Hex([]byte(specPoolHash(u.spec)), []byte(u.credID), []byte(u.currentVersion()))[:32]
}

// unavailable builds the structured upstream_unavailable result and applies the critical/non-critical policy.
func (r *Run) unavailable(ctx context.Context, c *callRec, spec *ServerSpec, err error) *sdk.CallToolResult {
	c.upstreamStatus = "unavailable"
	msg := r.g.scrub(err.Error())
	if len(msg) > 300 {
		msg = truncateUTF8(msg, 300) + "..."
	}
	critical := spec.Critical
	c.critical = critical
	body := map[string]any{"error": "upstream_unavailable", "server": spec.ID, "critical": critical, "message": msg}
	if critical {
		r.g.publish(ctx, EventCriticalFailure, r.cfg.Info, c.id, map[string]any{"server": spec.ID, "tool": c.tool, "message": msg})
		if f := r.g.opts.OnCritical; f != nil {
			f(r.cfg.Info, spec.ID, err)
		}
	}
	b, _ := json.Marshal(body)
	res := textResult(string(b), true)
	res.StructuredContent = body
	return res
}

var (
	errNoPersist = errors.New("result could not be stored")
	handleRe     = regexp.MustCompile(`^res_c_[0-9a-f]{12}$`)
)

// finish runs the tail of the pipeline on a raw result text: scan and redact (afterScan sees the redacted text, which is
// what the cache stores), persist, shape, mark.
func (r *Run) finish(ctx context.Context, c *callRec, source, tool, raw string, isErr bool, budget int, afterScan func(redacted string, rules []string)) *sdk.CallToolResult {
	g := r.g
	redacted, rules := redact(raw, g.opts.Scanner.Scan(raw))
	c.redactions = rules
	for _, rule := range rules {
		g.publish(ctx, EventSecretRedacted, r.cfg.Info, c.id, map[string]any{"server": source, "tool": tool, "rule": rule})
	}
	c.rawBytes = len(redacted)
	if afterScan != nil {
		afterScan(redacted, rules)
	}

	c.handle = "res_" + c.id
	persisted := r.persist(c, source, tool, redacted) == nil

	body := redacted
	truncated := len(body) > budget
	if truncated {
		body = truncateUTF8(body, budget)
	}
	c.truncated = truncated
	c.suspicious = FlagInjection(body)
	text := wrapUntrusted(source, c.id, c.handle, body, c.suspicious)
	if truncated {
		if persisted {
			next := len(body)
			text += fmt.Sprintf("\n[truncated: returned %d of %d bytes; call fetch_range{handle:%q, offset:%d, length:%d}]", len(body), len(redacted), c.handle, next, minInt(8192, g.opts.FetchRangeMax))
		} else {
			text += fmt.Sprintf("\n[truncated: returned %d of %d bytes; the full result could not be stored, so it cannot be continued]", len(body), len(redacted))
			c.handle = ""
		}
	}
	c.returnedBytes = len(text)
	chars := utf8.RuneCountInString(text)
	c.tokens = EstimateTokens(chars)
	if truncated {
		c.savedTrunc = maxInt(0, EstimateTokens(utf8.RuneCountInString(redacted))-EstimateTokens(utf8.RuneCountInString(body)))
	}
	if c.hit {
		c.savedCache = EstimateTokens(utf8.RuneCountInString(redacted))
		g.publish(ctx, EventCacheHit, r.cfg.Info, c.id, map[string]any{"server": source, "tool": tool, "key": c.cacheKey, "saved_bytes": len(redacted)})
	}
	res := textResult(text, isErr)
	res.Meta = sdk.Meta{"drydock": map[string]any{"call_id": c.id, "handle": c.handle, "truncated": truncated, "cache": c.cache, "bytes_total": len(redacted)}}
	return res
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// persist writes the redacted raw result and its metadata under the run dir and registers the handle.
func (r *Run) persist(c *callRec, source, tool, text string) error {
	dir := filepath.Join(r.dir, "mcp")
	p := filepath.Join(dir, c.id+".raw")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		r.g.opts.Logger.Warn("mcp: persist raw failed", "err", err)
		return errNoPersist
	}
	meta, _ := json.Marshal(map[string]any{
		"call_id": c.id, "server": source, "tool": tool, "args_hash": c.argsHash, "bytes": len(text),
		"sha256": sha256Hex([]byte(text)), "redactions": c.redactions, "cache": c.cache,
	})
	_ = os.WriteFile(filepath.Join(dir, c.id+".meta.json"), meta, 0o600)
	r.hmu.Lock()
	r.handles[c.handle] = p
	r.hmu.Unlock()
	return nil
}

// flattenResult turns an upstream result into the text the agent will see; non-text content is described, never inlined.
func flattenResult(res *sdk.CallToolResult) (string, bool) {
	if res == nil {
		return "", false
	}
	var parts []string
	for _, c := range res.Content {
		switch x := c.(type) {
		case *sdk.TextContent:
			parts = append(parts, x.Text)
		case *sdk.ImageContent:
			parts = append(parts, fmt.Sprintf("[image %s omitted: %d bytes]", x.MIMEType, len(x.Data)))
		case *sdk.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio %s omitted: %d bytes]", x.MIMEType, len(x.Data)))
		case *sdk.ResourceLink:
			parts = append(parts, fmt.Sprintf("[resource link: %s %s]", x.URI, x.Name))
		case *sdk.EmbeddedResource:
			if x.Resource != nil && x.Resource.Text != "" {
				parts = append(parts, x.Resource.Text)
			} else if x.Resource != nil {
				parts = append(parts, fmt.Sprintf("[embedded resource %s omitted: %d bytes]", x.Resource.URI, len(x.Resource.Blob)))
			}
		default:
			parts = append(parts, "[unsupported content omitted]")
		}
	}
	if len(parts) == 0 && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			parts = append(parts, string(b))
		}
	}
	return strings.Join(parts, "\n\n"), res.IsError
}

// ---- egress ----

func (r *Run) egressViolation(rule *EgressSpec, args json.RawMessage) string {
	var strs []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			strs = append(strs, x)
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for k, e := range x {
				strs = append(strs, k)
				walk(e)
			}
		}
	}
	if len(args) > 0 {
		var v any
		if err := json.Unmarshal(args, &v); err == nil {
			walk(v)
		}
	}
	total := 0
	maxLines := rule.MaxArgLines
	if maxLines == 0 {
		maxLines = 8
	}
	for _, s := range strs {
		total += len(s)
		if rule.DenyCodeFences && (strings.Contains(s, "```") || strings.Contains(s, "~~~")) {
			return "the arguments contain a fenced code block"
		}
		if rule.DenyCodeFences && strings.Count(s, "\n")+1 > maxLines {
			return fmt.Sprintf("an argument spans more than %d lines", maxLines)
		}
	}
	if rule.MaxArgBytes > 0 && total > rule.MaxArgBytes {
		return fmt.Sprintf("the arguments are %d bytes, over the %d byte limit for this server", total, rule.MaxArgBytes)
	}
	if rule.ScanSecrets {
		joined := strings.Join(strs, "\n")
		if fs := r.g.opts.Scanner.Scan(joined); len(fs) > 0 {
			return "the arguments contain a secret (" + fs[0].Rule + ")"
		}
	}
	return ""
}

// ---- audit ----

func (r *Run) audit(ctx context.Context, c *callRec, start time.Time, res *sdk.CallToolResult) {
	g := r.g
	d := g.now().Sub(start)
	data := map[string]any{
		"server": c.server, "tool": c.tool, "args_hash": c.argsHash, "decision": c.decision, "reason": c.reason,
		"cache": c.cache, "raw_bytes": c.rawBytes, "returned_bytes": c.returnedBytes, "bytes_in": c.bytesIn, "bytes_out": c.returnedBytes,
		"truncated": c.truncated, "redactions": len(c.redactions), "upstream_status": c.upstreamStatus, "duration_ms": ms(d),
		"tokens_est_returned": c.tokens, "tokens_est_saved_truncation": c.savedTrunc, "tokens_est_saved_cache": c.savedCache,
		"egress_checked": c.egressChecked, "handle": c.handle, "suspicious": len(c.suspicious) > 0, "flags": c.suspicious,
		"is_error": res != nil && res.IsError, "token_source": "estimated",
	}
	g.publish(ctx, EventCall, r.cfg.Info, c.id, data)
	if g.opts.DB != nil && c.tokens > 0 {
		now := storage.Now()
		err := g.opts.DB.WriteTx(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
			_, e := tx.Exec(`INSERT OR REPLACE INTO mcp_usage(call_id, run_id, server_id, tool, tokens_est_returned, tokens_est_saved_truncation, tokens_est_saved_cache, source, created_at)
VALUES (?,?,?,?,?,?,?,'estimated',?)`, c.id, r.cfg.Info.RunID, c.server, c.tool, c.tokens, c.savedTrunc, c.savedCache, now)
			return e
		})
		if err != nil {
			g.opts.Logger.Warn("mcp: usage row failed", "err", err)
		}
	}
}
