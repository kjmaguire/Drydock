// Package mcp is Drydock's MCP Gateway (docs/mcp.md): one local MCP endpoint per agent run, a Workspace server
// (retrieval tools over the app's own knowledge), a pool of upstream servers (hosted HTTP and stdio servers started under the
// process manager) and the per-call pipeline allowlist -> egress check -> cache -> shaping -> secret scan and
// untrusted marking -> audit.
//
// The gateway is built on github.com/modelcontextprotocol/go-sdk both as server (towards the CLI) and as client
// (towards upstreams). It deliberately does not use the SDK's CommandTransport for upstreams: that kills only the direct
// child on Windows, so stdio upstreams are started through proc.Manager (Job Objects).
package mcp

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Names fixed by docs/mcp.md.
const (
	// ServerName is the name under which the gateway is registered in every CLI (docs/mcp.md section 10).
	ServerName = "drydock"
	// WorkspaceServerID is the pseudo server id the Workspace tools are allowlisted under. Workspace tools are exposed
	// unprefixed (read_range, task, ...); upstream tools are exposed as <server_id>__<tool>.
	WorkspaceServerID = "workspace"
	// ToolFetchRange is the gateway-native continuation tool (docs/mcp.md section 5).
	ToolFetchRange = "fetch_range"
	// ToolSeparator joins a server id and an upstream tool name.
	ToolSeparator = "__"
)

// Event types published on the event bus (docs/mcp.md section 7). The cache hit event is spelled mcp.cache_hit (the task
// brief); docs/mcp.md writes mcp.cache.hit. See the package report for the contradiction.
const (
	EventCall            = "mcp.call"
	EventDenied          = "mcp.denied"
	EventAuthFailed      = "mcp.auth_failed"
	EventSecretRedacted  = "mcp.secret_redacted"
	EventEgressBlocked   = "mcp.egress_blocked"
	EventServerStarted   = "mcp.server.started"
	EventServerStopped   = "mcp.server.stopped"
	EventServerFailed    = "mcp.server.failed"
	EventCacheHit        = "mcp.cache_hit"
	EventCacheEvict      = "mcp.cache_evict"
	EventCriticalFailure = "mcp.upstream_critical"
)

// Cache states recorded in mcp.call.
const (
	CacheHit    = "hit"
	CacheMiss   = "miss"
	CacheBypass = "bypass"
)

// Decisions recorded in mcp.call.
const (
	DecisionAllowed         = "allowed"
	DecisionDenied          = "denied"
	DecisionApprovalPending = "approval_pending"
)

// Data classes (security.md section 9).
const (
	DataClassPrivate   = "private"
	DataClassTrainable = "trainable"
	// ContentPublicDocs / ContentPrivate classify what a server returns.
	ContentPublicDocs = "public_docs"
	ContentPrivate    = "private"
)

// Errors returned by the gateway packages.
var (
	// ErrNotAvailable is returned by providers whose backing subsystem does not exist yet. The Workspace server turns it
	// into an honest "not available yet" tool error.
	ErrNotAvailable = errors.New("not available yet")
	// ErrUpstreamUnavailable is returned (and surfaced as a structured tool error) when an upstream is down or its
	// circuit breaker is open.
	ErrUpstreamUnavailable = errors.New("upstream_unavailable")
	// ErrDenied is returned for a call blocked by the run policy.
	ErrDenied = errors.New("denied by tool allowlist")
	// ErrNotFound is returned by providers for an unknown task, handoff, skill, ...
	ErrNotFound = errors.New("not found")
)

// RepoMapGatedMessage is the exact error returned by repo_map while no RepoMapper is injected (decision D5).
const RepoMapGatedMessage = "not available: repo map is gated (decision D5): the tree-sitter repo map is built only after the Windows CI job proves CGO"

// Finding is one secret match. Offsets are byte offsets into the scanned text.
type Finding struct {
	Rule  string
	Start int
	End   int
}

// Scanner detects secrets. Any implementation can be injected (the production one wraps gitleaks in internal/security);
// the gateway never imports that package. Scan must be safe for concurrent use. Matches are replaced by
// [REDACTED:<rule>] by the gateway, and only the rule id is ever logged.
type Scanner interface {
	Scan(text string) []Finding
}

// NoopScanner finds nothing. Use only when secret scanning is explicitly disabled.
type NoopScanner struct{}

// Scan implements Scanner.
func (NoopScanner) Scan(string) []Finding { return nil }

// BasicScanner is a small self-contained pattern scanner for well-known token formats. It is a floor, not a
// replacement for gitleaks: it exists so that the gateway is never run without any scanner.
type BasicScanner struct{}

var basicRules = []struct {
	id string
	re *regexp.Regexp
}{
	{"aws-access-key-id", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}\b`)},
	{"github-fine-grained-pat", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}\b`)},
	{"private-key", regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----[\s\S]*?(?:-----END (?:[A-Z]+ )?PRIVATE KEY-----|$)`)},
	{"slack-token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`)},
	{"anthropic-api-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`)},
	{"openai-api-key", regexp.MustCompile(`\bsk-[A-Za-z0-9]{32,}\b`)},
	{"bearer-token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{24,}`)},
}

// Scan implements Scanner.
func (BasicScanner) Scan(text string) []Finding {
	var out []Finding
	for _, r := range basicRules {
		for _, m := range r.re.FindAllStringIndex(text, -1) {
			out = append(out, Finding{Rule: r.id, Start: m[0], End: m[1]})
		}
	}
	return out
}

// redact replaces findings (merging overlaps) and returns the redacted text with the distinct rule ids that fired.
func redact(text string, fs []Finding) (string, []string) {
	if len(fs) == 0 {
		return text, nil
	}
	sortFindings(fs)
	// every literal a finding covers is redacted wherever it occurs: a scanner that reports one offset per secret (gitleaks
	// through secretadapt locates a repeated secret at its first occurrence) would otherwise leave a second copy in the text
	type lit struct{ val, rule string }
	var lits []lit
	for _, f := range fs {
		if f.Start >= 0 && f.End > f.Start && f.End <= len(text) && f.End-f.Start >= 8 {
			lits = append(lits, lit{text[f.Start:f.End], f.Rule})
		}
	}
	var b strings.Builder
	var rules []string
	seen := map[string]bool{}
	pos := 0
	for _, f := range fs {
		if f.Start < pos || f.End <= f.Start || f.End > len(text) {
			if f.Start < pos && f.End > pos && f.End <= len(text) { // overlap: extend the previous redaction
				pos = f.End
			}
			continue
		}
		b.WriteString(text[pos:f.Start])
		b.WriteString("[REDACTED:" + f.Rule + "]")
		pos = f.End
		if !seen[f.Rule] {
			seen[f.Rule] = true
			rules = append(rules, f.Rule)
		}
	}
	b.WriteString(text[pos:])
	out := b.String()
	sort.SliceStable(lits, func(i, j int) bool { return len(lits[i].val) > len(lits[j].val) })
	for _, l := range lits {
		out = strings.ReplaceAll(out, l.val, "[REDACTED:"+l.rule+"]")
	}
	return out, rules
}

func sortFindings(fs []Finding) {
	for i := 1; i < len(fs); i++ {
		for j := i; j > 0 && (fs[j].Start < fs[j-1].Start || (fs[j].Start == fs[j-1].Start && fs[j].End > fs[j-1].End)); j-- {
			fs[j], fs[j-1] = fs[j-1], fs[j]
		}
	}
}

// RunInfo identifies the run a provider call belongs to.
type RunInfo struct {
	RunID     string
	ProjectID string
	TaskID    string
	AgentID   string
	Role      string
	Worktree  string
}

// RunPolicy is the per-run tool policy built by the Context Engine (docs/mcp.md section 3). Deny by default.
type RunPolicy struct {
	// Allow maps a server id (WorkspaceServerID for the Workspace tools) to tool globs matched against the bare tool name
	// ("search_*", "pull_request_read", "*").
	Allow map[string][]string
	// Approve lists exposed tool names (globs; "gh__create_pull_request", "room") that must be approved before they run.
	// Catalog side_effects_tools are always approval-gated in addition.
	Approve []string
}

// Allows reports whether the policy lists the tool of the server.
func (p RunPolicy) Allows(server, tool string) bool {
	for _, g := range p.Allow[server] {
		if globMatch(g, tool) {
			return true
		}
	}
	return false
}

// NeedsApproval reports whether the exposed tool name matches an Approve glob.
func (p RunPolicy) NeedsApproval(exposed string) bool {
	for _, g := range p.Approve {
		if globMatch(g, exposed) {
			return true
		}
	}
	return false
}

// globMatch is a minimal glob where '*' matches any run of characters (including none) and everything else is literal.
func globMatch(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(s, parts[i])
		if idx < 0 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// exposed returns the name the CLI sees.
func exposed(server, tool string) string {
	if server == WorkspaceServerID {
		return tool
	}
	return server + ToolSeparator + tool
}

// EstimateTokens is the chars/4 estimator of docs/mcp.md section 7 (an assumption, never mixed with reported usage).
func EstimateTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

// Approver decides whether an approval-gated call may run. The production implementation parks an approvals row bound to
// the hash of the exact call (brief section 12) and returns after a human decided; agents cannot answer approvals.
type Approver interface {
	Approve(ctx context.Context, req ApprovalRequest) (bool, error)
}

// ApprovalRequest describes the exact call awaiting approval.
type ApprovalRequest struct {
	Run      RunInfo
	CallID   string
	Server   string
	Tool     string
	ArgsHash string // sha256 of the canonical JSON arguments
	Args     []byte // canonical JSON arguments (for display)
}

// CredentialSource resolves upstream credentials held by Drydock (agents never see them).
type CredentialSource interface {
	// Credential returns the secret for the keyring reference of a catalog entry.
	Credential(ctx context.Context, serverID, keyringRef string) (string, error)
}

// CredentialFunc adapts a function to CredentialSource.
type CredentialFunc func(ctx context.Context, serverID, keyringRef string) (string, error)

// Credential implements CredentialSource.
func (f CredentialFunc) Credential(ctx context.Context, serverID, keyringRef string) (string, error) {
	return f(ctx, serverID, keyringRef)
}

// Duration formatting helper for events.
func ms(d time.Duration) int64 { return d.Milliseconds() }
