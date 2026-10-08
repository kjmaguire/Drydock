package mcp

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed catalog.yaml
var builtinCatalogYAML []byte

// CatalogSchema is the only schema version this build reads.
const CatalogSchema = 1

// Duration is a time.Duration that unmarshals from "24h", "60s" and the bare integer 0.
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: duration must be a scalar", n.Line)
	}
	if n.Value == "0" || n.Value == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q (use 24h, 60s or 0)", n.Line, n.Value)
	}
	if v < 0 {
		return fmt.Errorf("line %d: negative duration %q", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// Transport kinds.
const (
	TransportHTTP  = "http"
	TransportStdio = "stdio"
)

// Auth kinds (docs/mcp.md section 9). The OAuth flows themselves are outside this package: for oauth2_pkce and
// oauth_dcr the gateway attaches whatever bearer token the CredentialSource holds for the entry.
const (
	AuthNone   = "none"
	AuthBearer = "bearer"
	AuthPKCE   = "oauth2_pkce"
	AuthDCR    = "oauth_dcr"
)

// Trust levels (docs/mcp.md section 9).
const (
	TrustPublicDocs     = "public_docs"
	TrustThirdPartyDocs = "third_party_docs"
	TrustPrivateData    = "private_data"
)

// Roles recognised in recommended_roles (docs/mcp.md section 3).
var knownRoles = map[string]bool{
	"pm": true, "architect": true, "ux": true, "frontend": true, "backend": true, "database": true,
	"devops": true, "qa": true, "security": true, "reviewer": true,
}

// ServerSpec is one catalog entry: schema of docs/mcp.md section 9 plus the extensions listed on each field.
type ServerSpec struct {
	ID        string `yaml:"id"`
	Display   string `yaml:"display"`
	Transport string `yaml:"transport"`
	URL       string `yaml:"url,omitempty"`
	// Command is the stdio command (user-installed unless License.Bundled).
	Command *CommandSpec `yaml:"command,omitempty"`
	Auth    AuthSpec     `yaml:"auth"`
	Trust   string       `yaml:"trust"`
	// DataClass is the content class (extension): "public_docs" or "private". Derived from Trust when empty.
	DataClass string `yaml:"data_class,omitempty"`
	// SideEffectsTools are write-class tools (globs on the bare tool name); they are always approval-gated and never cached.
	SideEffectsTools []string `yaml:"side_effects_tools,omitempty"`
	// ReadOnlyTools (extension) names tools known to be read-only: the only tools that may be cached, and the only
	// tools of a private_data server that are not treated as write-class.
	ReadOnlyTools []string `yaml:"read_only_tools,omitempty"`
	// AllowTools (extension) is the server's own ceiling: when non-empty a tool must match one of these globs in
	// addition to the run policy. Custom servers use it as their own allowlist.
	AllowTools []string `yaml:"allow_tools,omitempty"`
	// Tools (extension) declares the tools the gateway lists before the upstream was ever contacted (lazy start,
	// docs/mcp.md section 2 "Readiness"). Once the upstream is connected its real list replaces the declaration.
	Tools   []ToolDecl  `yaml:"tools,omitempty"`
	Cache   CacheSpec   `yaml:"cache"`
	Shape   ShapeSpec   `yaml:"shape"`
	Egress  *EgressSpec `yaml:"egress,omitempty"`
	License LicenseSpec `yaml:"license"`
	Health  HealthSpec  `yaml:"health"`
	// TermsStatus is "verified" or "unverified".
	TermsStatus string `yaml:"terms_status"`
	// Critical (extension): when true an upstream failure pauses the run (OnCritical callback and
	// mcp.upstream_critical event) instead of only returning upstream_unavailable to the agent.
	Critical bool `yaml:"critical,omitempty"`
	// RecommendedRoles (extension) lists roles for which the Context Engine should offer the server.
	RecommendedRoles []string `yaml:"recommended_roles,omitempty"`
	// DefaultEnabled (extension): enabled for a project that did not decide.
	DefaultEnabled bool `yaml:"default_enabled,omitempty"`
	// Template (extension): a user-install template that is never started unless the project enables it explicitly.
	Template bool   `yaml:"template,omitempty"`
	Notes    string `yaml:"notes,omitempty"`
}

// CommandSpec is a stdio server command.
type CommandSpec struct {
	Argv []string `yaml:"argv"`
	// Pin is the exact package pin ("pkg@x.y.z") the argv launches; informational and checked for npx/uvx style argv.
	Pin string `yaml:"pin,omitempty"`
	Cwd string `yaml:"cwd,omitempty"`
	// Env holds non-secret environment entries; SecretEnv maps variable name -> credential reference resolved by the
	// CredentialSource (secrets never appear in argv, which is visible to other processes).
	Env       map[string]string `yaml:"env,omitempty"`
	SecretEnv map[string]string `yaml:"secret_env,omitempty"`
}

// AuthSpec describes how the gateway authenticates to the upstream.
type AuthSpec struct {
	Kind   string `yaml:"kind"`
	Header string `yaml:"header,omitempty"` // default Authorization
	// Scheme (extension) is the value prefix, default "Bearer" ("Sentry-Bearer" for hosted Sentry).
	Scheme  string `yaml:"scheme,omitempty"`
	Keyring string `yaml:"keyring,omitempty"`
	// Optional (extension): the server works without a credential (Context7 with rate limits).
	Optional bool `yaml:"optional,omitempty"`
}

// ToolDecl declares one tool for lazy listing.
type ToolDecl struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description,omitempty"`
	InputSchema map[string]any `yaml:"input_schema,omitempty"`
}

// CacheSpec is the per-server result cache policy.
type CacheSpec struct {
	TTL   Duration `yaml:"ttl"`
	Scope string   `yaml:"scope"` // global | project
	// Tools (extension) overrides the TTL per tool.
	Tools map[string]Duration `yaml:"tools,omitempty"`
	// AllowRedacted caches results in which secrets were redacted (default: never).
	AllowRedacted bool `yaml:"allow_redacted,omitempty"`
}

// ShapeSpec is the per-server result shaping policy.
type ShapeSpec struct {
	MaxInlineBytes int `yaml:"max_inline_bytes"`
	// ToolBytes (extension) overrides max_inline_bytes per tool.
	ToolBytes map[string]int `yaml:"tool_bytes,omitempty"`
	// URLParams are appended to the upstream URL query (Microsoft Learn: maxTokenBudget).
	URLParams map[string]any `yaml:"url_params,omitempty"`
}

// EgressSpec limits what leaves the machine towards third-party docs servers (docs/mcp.md section 6).
type EgressSpec struct {
	MaxArgBytes    int  `yaml:"max_arg_bytes"`
	ScanSecrets    bool `yaml:"scan_secrets"`
	DenyCodeFences bool `yaml:"deny_code_fences"`
	// MaxArgLines (extension) is the "more than N lines" rule; 0 means the default of 8.
	MaxArgLines int `yaml:"max_arg_lines,omitempty"`
}

// LicenseSpec records licensing facts (docs/mcp.md section 12).
type LicenseSpec struct {
	Server      string `yaml:"server"`
	Content     string `yaml:"content,omitempty"`
	Attribution string `yaml:"attribution,omitempty"`
	Bundled     bool   `yaml:"bundled"`
	Notes       string `yaml:"notes,omitempty"`
}

// HealthSpec is the connection probe.
type HealthSpec struct {
	Probe   string   `yaml:"probe,omitempty"`
	Timeout Duration `yaml:"timeout,omitempty"`
}

// Catalog is a parsed catalog.yaml (or user-servers.yaml).
type Catalog struct {
	Schema  int          `yaml:"schema"`
	Servers []ServerSpec `yaml:"servers"`
}

// ProjectConfig is the per-project MCP configuration: which catalog servers are on, overrides, and custom servers.
type ProjectConfig struct {
	// Enabled / Disabled are catalog ids. Disabled wins.
	Enabled  []string `yaml:"enabled,omitempty"`
	Disabled []string `yaml:"disabled,omitempty"`
	// Overrides adjusts catalog entries for this project.
	Overrides map[string]ServerOverride `yaml:"overrides,omitempty"`
	// Custom are project-specific servers (stdio or HTTP) with their own allowlist and cache TTL.
	Custom []ServerSpec `yaml:"custom,omitempty"`
}

// ServerOverride is a per-project adjustment of a catalog entry.
type ServerOverride struct {
	CacheTTL   *Duration `yaml:"cache_ttl,omitempty"`
	AllowTools []string  `yaml:"allow_tools,omitempty"`
	Critical   *bool     `yaml:"critical,omitempty"`
	// SideEffectsTools are added to the catalog's list (a project can only make a server stricter this way).
	SideEffectsTools []string `yaml:"side_effects_tools,omitempty"`
}

var idRe = regexp.MustCompile(`^[a-z][a-z0-9]{0,7}$`)

// LoadBuiltin parses and validates the embedded catalog.yaml.
func LoadBuiltin() (*Catalog, error) {
	c, err := ParseCatalog(builtinCatalogYAML)
	if err != nil {
		return nil, fmt.Errorf("built-in catalog: %w", err)
	}
	return c, nil
}

// ParseCatalog parses (strictly: unknown keys are errors) and validates a catalog document.
func ParseCatalog(b []byte) (*Catalog, error) {
	var c Catalog
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("catalog yaml: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// ParseProjectConfig parses a per-project configuration and validates its custom servers.
func ParseProjectConfig(b []byte) (*ProjectConfig, error) {
	var pc ProjectConfig
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&pc); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("project mcp config: %w", err)
	}
	for i := range pc.Custom {
		pc.Custom[i].applyDefaults()
		if err := pc.Custom[i].Validate(false); err != nil {
			return nil, fmt.Errorf("custom server %q: %w", pc.Custom[i].ID, err)
		}
	}
	return &pc, nil
}

// Get returns the catalog entry with the id.
func (c *Catalog) Get(id string) (ServerSpec, bool) {
	for _, s := range c.Servers {
		if s.ID == id {
			return s, true
		}
	}
	return ServerSpec{}, false
}

// Validate checks every entry and id uniqueness.
func (c *Catalog) Validate() error {
	if c.Schema != CatalogSchema {
		return fmt.Errorf("catalog schema %d is not supported (want %d)", c.Schema, CatalogSchema)
	}
	seen := map[string]bool{}
	var errs []error
	for i := range c.Servers {
		s := &c.Servers[i]
		s.applyDefaults()
		if seen[s.ID] {
			errs = append(errs, fmt.Errorf("duplicate server id %q", s.ID))
		}
		seen[s.ID] = true
		if err := s.Validate(true); err != nil {
			errs = append(errs, fmt.Errorf("server %q: %w", s.ID, err))
		}
	}
	return errors.Join(errs...)
}

// applyDefaults fills derived fields.
func (s *ServerSpec) applyDefaults() {
	if s.DataClass == "" {
		if s.Trust == TrustPrivateData {
			s.DataClass = ContentPrivate
		} else if s.Trust != "" {
			s.DataClass = ContentPublicDocs
		}
	}
	if s.Auth.Kind == "" {
		s.Auth.Kind = AuthNone
	}
	if s.Auth.Header == "" {
		s.Auth.Header = "Authorization"
	}
	if s.Auth.Scheme == "" {
		s.Auth.Scheme = "Bearer"
	}
	if s.Cache.Scope == "" {
		if s.Trust == TrustPrivateData {
			s.Cache.Scope = "project"
		} else {
			s.Cache.Scope = "global"
		}
	}
	if s.TermsStatus == "" {
		s.TermsStatus = "unverified"
	}
}

// Normalize fills the derived defaults and validates the entry as a project's own (not shipped) server. The core uses it to
// check a server the human typed before anything is stored.
func (s *ServerSpec) Normalize() error {
	s.applyDefaults()
	return s.Validate(false)
}

// Validate checks one entry. shipped is true for the embedded catalog, which may not bundle anything D14 forbids.
func (s *ServerSpec) Validate(shipped bool) error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if !idRe.MatchString(s.ID) {
		add("id %q must be a lowercase slug of at most 8 characters (tool names are <id>__<tool>)", s.ID)
	}
	if s.ID == WorkspaceServerID || s.ID == ServerName || s.ID == "gateway" {
		add("id %q is reserved", s.ID)
	}
	if strings.TrimSpace(s.Display) == "" {
		add("display is required")
	}
	switch s.Transport {
	case TransportHTTP:
		if s.Command != nil {
			add("transport http must not have a command")
		}
		if err := validateUpstreamURL(s.URL); err != nil {
			add("%v", err)
		}
	case TransportStdio:
		if s.URL != "" {
			add("transport stdio must not have a url")
		}
		if s.Command == nil || len(s.Command.Argv) == 0 || strings.TrimSpace(s.Command.Argv[0]) == "" {
			add("transport stdio needs command.argv")
		} else {
			for _, a := range s.Command.Argv {
				if looksLikeSecretArg(a) {
					add("command.argv must not carry credentials (%q): use command.secret_env", redactArg(a))
				}
			}
		}
	default:
		add("transport must be http or stdio (got %q)", s.Transport)
	}
	switch s.Auth.Kind {
	case AuthNone:
	case AuthBearer, AuthPKCE, AuthDCR:
		if s.Transport == TransportStdio {
			add("auth %s is for http servers; stdio servers use command.secret_env", s.Auth.Kind)
		}
		if s.Auth.Keyring == "" && !s.Auth.Optional && s.Auth.Kind == AuthBearer {
			add("auth bearer needs a keyring reference (or optional: true)")
		}
	default:
		add("auth.kind must be none, bearer, oauth2_pkce or oauth_dcr (got %q)", s.Auth.Kind)
	}
	switch s.Trust {
	case TrustPublicDocs, TrustThirdPartyDocs, TrustPrivateData:
	default:
		add("trust must be public_docs, third_party_docs or private_data (got %q)", s.Trust)
	}
	switch s.DataClass {
	case ContentPublicDocs:
		if s.Trust == TrustPrivateData {
			add("data_class public_docs contradicts trust private_data")
		}
	case ContentPrivate:
	default:
		add("data_class must be public_docs or private (got %q)", s.DataClass)
	}
	if s.Cache.Scope != "global" && s.Cache.Scope != "project" {
		add("cache.scope must be global or project (got %q)", s.Cache.Scope)
	}
	if s.Trust == TrustPrivateData && s.Cache.Scope != "project" && s.Cache.TTL > 0 {
		add("a private_data server may only be cached with scope project")
	}
	if s.Shape.MaxInlineBytes < 0 {
		add("shape.max_inline_bytes must not be negative")
	}
	for t, n := range s.Shape.ToolBytes {
		if n <= 0 {
			add("shape.tool_bytes[%s] must be positive", t)
		}
	}
	if s.Egress != nil && (s.Egress.MaxArgBytes < 0 || s.Egress.MaxArgLines < 0) {
		add("egress limits must not be negative")
	}
	if s.TermsStatus != "verified" && s.TermsStatus != "unverified" {
		add("terms_status must be verified or unverified (got %q)", s.TermsStatus)
	}
	for _, r := range s.RecommendedRoles {
		if !knownRoles[r] {
			add("unknown recommended role %q", r)
		}
	}
	names := map[string]bool{}
	for _, t := range s.Tools {
		if t.Name == "" || strings.Contains(t.Name, ToolSeparator) {
			add("tool declaration name %q is invalid", t.Name)
		}
		if names[t.Name] {
			add("tool %q is declared twice", t.Name)
		}
		names[t.Name] = true
		if t.InputSchema != nil && t.InputSchema["type"] != "object" {
			add("tool %q input_schema must have type object", t.Name)
		}
	}
	if strings.HasPrefix(s.License.Content, "CC-BY") && strings.TrimSpace(s.License.Attribution) == "" {
		add("license.attribution is required for %s content", s.License.Content)
	}
	if s.License.Bundled {
		if shipped && s.Transport == TransportStdio {
			add("the shipped catalog may not bundle a stdio server (D14)")
		}
		l := strings.ToUpper(s.License.Server)
		if strings.HasPrefix(l, "FSL") || strings.HasPrefix(l, "AGPL") || s.ID == "aikido" || s.ID == "sentryx" {
			add("license %s must not be bundled (D14)", s.License.Server)
		}
	}
	return errors.Join(errs...)
}

func validateUpstreamURL(raw string) error {
	if raw == "" {
		return errors.New("url is required for transport http")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %v", err)
	}
	if u.User != nil {
		return errors.New("url must not carry credentials")
	}
	if u.Host == "" {
		return errors.New("url has no host")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return fmt.Errorf("plain http is allowed only for loopback hosts (brief section 16), got %q", u.Hostname())
		}
	default:
		return fmt.Errorf("url scheme must be https (or http on loopback), got %q", u.Scheme)
	}
	if u.Fragment != "" {
		return errors.New("url must not have a fragment")
	}
	return nil
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

var secretArgRe = regexp.MustCompile(`(?i)(token|key|secret|password)=\S{8,}`)

func looksLikeSecretArg(a string) bool { return secretArgRe.MatchString(a) }
func redactArg(a string) string {
	if i := strings.Index(a, "="); i > 0 {
		return a[:i+1] + "..."
	}
	return "..."
}

// Resolve returns the servers enabled for a project: default-enabled catalog entries plus pc.Enabled, minus pc.Disabled,
// with overrides applied, plus the project's custom servers. Templates are included only when named in pc.Enabled.
// The result is sorted by id.
func (c *Catalog) Resolve(pc *ProjectConfig) ([]ServerSpec, error) {
	if pc == nil {
		pc = &ProjectConfig{}
	}
	disabled := map[string]bool{}
	for _, id := range pc.Disabled {
		disabled[id] = true
	}
	explicit := map[string]bool{}
	for _, id := range pc.Enabled {
		if _, ok := c.Get(id); !ok {
			return nil, fmt.Errorf("project enables unknown catalog server %q", id)
		}
		explicit[id] = true
	}
	for id := range pc.Overrides {
		if _, ok := c.Get(id); !ok {
			return nil, fmt.Errorf("project overrides unknown catalog server %q", id)
		}
	}
	var out []ServerSpec
	for _, s := range c.Servers {
		on := explicit[s.ID] || (s.DefaultEnabled && !s.Template)
		if !on || disabled[s.ID] {
			continue
		}
		if ov, ok := pc.Overrides[s.ID]; ok {
			s = ov.apply(s)
			if err := s.Validate(false); err != nil {
				return nil, fmt.Errorf("override of %q: %w", s.ID, err)
			}
		}
		out = append(out, s)
	}
	ids := map[string]bool{}
	for _, s := range c.Servers {
		ids[s.ID] = true
	}
	for _, cs := range pc.Custom {
		cs.applyDefaults()
		if ids[cs.ID] {
			return nil, fmt.Errorf("custom server id %q collides with a catalog id", cs.ID)
		}
		ids[cs.ID] = true
		if err := cs.Validate(false); err != nil {
			return nil, fmt.Errorf("custom server %q: %w", cs.ID, err)
		}
		if disabled[cs.ID] {
			continue
		}
		out = append(out, cs)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (o ServerOverride) apply(s ServerSpec) ServerSpec {
	if o.CacheTTL != nil {
		s.Cache.TTL = *o.CacheTTL
	}
	if o.AllowTools != nil {
		s.AllowTools = append([]string(nil), o.AllowTools...)
	}
	if o.Critical != nil {
		s.Critical = *o.Critical
	}
	if len(o.SideEffectsTools) > 0 {
		s.SideEffectsTools = append(append([]string(nil), s.SideEffectsTools...), o.SideEffectsTools...)
	}
	return s
}

// ---- policy helpers on a spec ----

func matchAny(globs []string, name string) bool {
	for _, g := range globs {
		if globMatch(g, name) {
			return true
		}
	}
	return false
}

// toolAllowedBySpec applies the server's own ceiling.
func (s *ServerSpec) toolAllowedBySpec(tool string) bool {
	return len(s.AllowTools) == 0 || matchAny(s.AllowTools, tool)
}

// isSideEffect classifies a tool as write-class. readOnlyHint / destructiveHint come from the upstream's annotations.
// Unknown tools of a private_data server are treated as write-class until the catalog lists them as read-only.
func (s *ServerSpec) isSideEffect(tool string, readOnlyHint, destructiveHint bool) bool {
	if matchAny(s.SideEffectsTools, tool) {
		return true
	}
	if destructiveHint {
		return true
	}
	if matchAny(s.ReadOnlyTools, tool) || readOnlyHint {
		return false
	}
	return s.Trust == TrustPrivateData
}

// isKnownReadOnly reports whether the tool may be cached.
func (s *ServerSpec) isKnownReadOnly(tool string, readOnlyHint bool) bool {
	if matchAny(s.SideEffectsTools, tool) {
		return false
	}
	return matchAny(s.ReadOnlyTools, tool) || readOnlyHint
}

// ttlFor returns the cache TTL for a tool.
func (s *ServerSpec) ttlFor(tool string) time.Duration {
	if d, ok := s.Cache.Tools[tool]; ok {
		return d.D()
	}
	return s.Cache.TTL.D()
}

// inlineBytes returns the shaping budget for a tool (gateway default when the spec sets none).
func (s *ServerSpec) inlineBytes(tool string, def int) int {
	if n, ok := s.Shape.ToolBytes[tool]; ok && n > 0 {
		return n
	}
	if s.Shape.MaxInlineBytes > 0 {
		return s.Shape.MaxInlineBytes
	}
	return def
}

// egressRule returns the effective egress rule: explicit, else the public-docs defaults, else none.
func (s *ServerSpec) egressRule() *EgressSpec {
	if s.Egress != nil {
		return s.Egress
	}
	if s.Trust == TrustPrivateData {
		return nil
	}
	return &EgressSpec{MaxArgBytes: 1024, ScanSecrets: true, DenyCodeFences: true, MaxArgLines: 8}
}
