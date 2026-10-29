package packs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kjmaguire/drydock/internal/git"
)

// SchemaVersion is the only pack.yaml schema this build reads.
const SchemaVersion = 1

// Kinds, visibilities and source types (docs/expert-packs.md section 2).
const (
	KindIndexed = "indexed"
	KindLive    = "live"
	KindHybrid  = "hybrid"

	VisibilityPublic  = "public"
	VisibilityPrivate = "private"

	SourceLlmsTxt  = "llms_txt"
	SourceSitemap  = "sitemap"
	SourceGitDocs  = "git_docs"
	SourceLocalDir = "local_dir"
	SourceURLList  = "url_list"

	RouteTrainableOK = "trainable_ok"
	RoutePrivateOnly = "private_only"

	UseLivePrimary    = "primary"
	UseLiveFallback   = "fallback"
	UseLiveSupplement = "supplement"
)

// Dur is a duration that also reads "7d" and "2w".
type Dur time.Duration

// D returns the value as a time.Duration.
func (d Dur) D() time.Duration { return time.Duration(d) }

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Dur) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: duration must be a scalar like 24h or 7d", n.Line)
	}
	v, err := ParseDur(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %v", n.Line, err)
	}
	*d = Dur(v)
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (d Dur) MarshalYAML() (any, error) { return FormatDur(time.Duration(d)), nil }

// ParseDur parses 90s, 15m, 24h, 7d, 2w.
func ParseDur(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty duration")
	}
	if len(s) > 1 && (strings.HasSuffix(s, "d") || strings.HasSuffix(s, "w")) {
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q (use 90s, 15m, 24h, 7d, 2w)", s)
		}
		unit := 24 * time.Hour
		if s[len(s)-1] == 'w' {
			unit = 7 * 24 * time.Hour
		}
		return time.Duration(n) * unit, nil
	}
	v, err := time.ParseDuration(s)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid duration %q (use 90s, 15m, 24h, 7d, 2w)", s)
	}
	return v, nil
}

// FormatDur renders a duration the way manifests spell it.
func FormatDur(d time.Duration) string {
	switch {
	case d > 0 && d%(24*time.Hour) == 0:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	default:
		return d.String()
	}
}

// Manifest is packs/<id>/pack.yaml.
type Manifest struct {
	Schema     int           `yaml:"schema" json:"schema"`
	ID         string        `yaml:"id" json:"id"`
	Title      string        `yaml:"title" json:"title"`
	Kind       string        `yaml:"kind" json:"kind"`
	Visibility string        `yaml:"visibility" json:"visibility"`
	License    License       `yaml:"license" json:"license"`
	Sources    []Source      `yaml:"sources,omitempty" json:"sources,omitempty"`
	Crawl      Crawl         `yaml:"crawl,omitempty" json:"crawl"`
	Chunking   Chunking      `yaml:"chunking,omitempty" json:"chunking"`
	Refresh    Refresh       `yaml:"refresh,omitempty" json:"refresh"`
	Freshness  Freshness     `yaml:"freshness,omitempty" json:"freshness"`
	Live       *Live         `yaml:"live,omitempty" json:"live,omitempty"`
	Attach     Attach        `yaml:"attach,omitempty" json:"attach"`
	Consult    ConsultConfig `yaml:"consult,omitempty" json:"consult"`
	Offline    *bool         `yaml:"offline,omitempty" json:"offline,omitempty"`
}

// License records the content license and the attribution line rendered in citations and exports.
type License struct {
	Content             string `yaml:"content" json:"content"`
	Code                string `yaml:"code,omitempty" json:"code,omitempty"`
	Attribution         string `yaml:"attribution,omitempty" json:"attribution,omitempty"`
	AttributionRequired bool   `yaml:"attribution_required,omitempty" json:"attributionRequired,omitempty"`
}

// Source is one place a pack is ingested from.
type Source struct {
	Type       string   `yaml:"type" json:"type"`
	URL        string   `yaml:"url,omitempty" json:"url,omitempty"`
	URLs       []string `yaml:"urls,omitempty" json:"urls,omitempty"` // url_list
	Path       string   `yaml:"path,omitempty" json:"path,omitempty"` // local_dir; git_docs: the docs folder in the repository
	Ref        string   `yaml:"ref,omitempty" json:"ref,omitempty"`   // git_docs: the branch, tag or commit
	Follow     string   `yaml:"follow,omitempty" json:"follow,omitempty"`
	PageFormat string   `yaml:"page_format,omitempty" json:"pageFormat,omitempty"`
}

// Crawl limits what the fetcher may do. Every limit has a default; none can be raised past its cap.
type Crawl struct {
	RespectRobots        *bool    `yaml:"respect_robots,omitempty" json:"respectRobots,omitempty"`
	RequireContentSignal string   `yaml:"require_content_signal,omitempty" json:"requireContentSignal,omitempty"`
	AllowedHosts         []string `yaml:"allowed_hosts,omitempty" json:"allowedHosts,omitempty"`
	DenyPaths            []string `yaml:"deny_paths,omitempty" json:"denyPaths,omitempty"`
	RPS                  float64  `yaml:"rps,omitempty" json:"rps,omitempty"`
	Concurrency          int      `yaml:"concurrency,omitempty" json:"concurrency,omitempty"`
	MaxPageBytes         int64    `yaml:"max_page_bytes,omitempty" json:"maxPageBytes,omitempty"`
	Redirects            string   `yaml:"redirects,omitempty" json:"redirects,omitempty"`
	// Extensions (documented in the "As built" section): bounds for one refresh.
	MaxPages int `yaml:"max_pages,omitempty" json:"maxPages,omitempty"`
	Timeout  Dur `yaml:"timeout,omitempty" json:"timeout,omitempty"`              // per request
	Budget   Dur `yaml:"refresh_budget,omitempty" json:"refreshBudget,omitempty"` // whole refresh
	// MaxRepoBytes bounds what one git_docs fetch may download (trees and the wanted files; default 256 MB, at most 2 GB).
	MaxRepoBytes int64 `yaml:"max_repo_bytes,omitempty" json:"maxRepoBytes,omitempty"`
}

// Chunking mirrors ChunkOptions in manifest form.
type Chunking struct {
	SplitOn          []string `yaml:"split_on,omitempty" json:"splitOn,omitempty"`
	TargetTokens     int      `yaml:"target_tokens,omitempty" json:"targetTokens,omitempty"`
	MaxTokens        int      `yaml:"max_tokens,omitempty" json:"maxTokens,omitempty"`
	CodeAtomicTokens int      `yaml:"code_block_atomic_tokens,omitempty" json:"codeBlockAtomicTokens,omitempty"`
}

// Refresh schedules re-ingestion.
type Refresh struct {
	Mode     string       `yaml:"mode,omitempty" json:"mode,omitempty"` // scheduled | manual
	Interval Dur          `yaml:"interval,omitempty" json:"interval,omitempty"`
	Digest   DigestConfig `yaml:"digest,omitempty" json:"digest"`
}

// DigestConfig configures the optional cheap-model summary of changed pages.
type DigestConfig struct {
	Enabled   bool   `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Tier      string `yaml:"tier,omitempty" json:"tier,omitempty"`
	DataClass string `yaml:"data_class,omitempty" json:"dataClass,omitempty"` // trainable_ok | private_only
}

// Freshness sets when results show an age warning and a refresh suggestion.
type Freshness struct {
	WarnAfter  Dur `yaml:"warn_after,omitempty" json:"warnAfter,omitempty"`
	StaleAfter Dur `yaml:"stale_after,omitempty" json:"staleAfter,omitempty"`
}

// Live points at an MCP docs server in the gateway catalog.
type Live struct {
	MCP  string `yaml:"mcp" json:"mcp"`
	Tool string `yaml:"tool" json:"tool"`
	Arg  string `yaml:"arg,omitempty" json:"arg,omitempty"`
	Use  string `yaml:"use,omitempty" json:"use,omitempty"`
}

// Attach is what ATTACH adds to an agent's context.
type Attach struct {
	Instructions string   `yaml:"instructions,omitempty" json:"instructions,omitempty"`
	DefaultK     int      `yaml:"default_k,omitempty" json:"defaultK,omitempty"`
	Roles        []string `yaml:"roles,omitempty" json:"roles,omitempty"`
}

// ConsultConfig configures CONSULT.
type ConsultConfig struct {
	MaxChunks int    `yaml:"max_chunks,omitempty" json:"maxChunks,omitempty"`
	Route     string `yaml:"route,omitempty" json:"route,omitempty"`
}

// ValidateOptions tunes validation. Production leaves it zero.
type ValidateOptions struct {
	// AllowLoopbackHTTP also accepts http://127.0.0.1 and http://localhost sources. The fetcher refuses loopback unless it
	// is configured the same way; only tests and a deliberate local mirror use this.
	AllowLoopbackHTTP bool
}

// Roles a pack can be attached for (brief section 1).
var knownRoles = map[string]string{
	"pm": "PM", "architect": "Architect", "ux designer": "UX Designer", "frontend": "Frontend", "backend": "Backend",
	"database": "Database", "devops": "DevOps", "qa": "QA", "security": "Security", "reviewer": "Reviewer",
}

// CanonicalRole returns the display form of a role name, or "" if unknown.
func CanonicalRole(r string) string { return knownRoles[strings.ToLower(strings.TrimSpace(r))] }

var (
	packIDRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)
	mcpIDRe    = regexp.MustCompile(`^[a-z][a-z0-9]{0,7}$`)
	toolRe     = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)
	argRe      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,40}$`)
	hostRe     = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
	splitNames = map[string]int{"h1": 1, "h2": 2, "h3": 3, "h4": 4}
)

// Limits (caps for values a manifest may request).
const (
	MaxAttachInstructionTokens = 400
	maxPageBytesCap            = 16 << 20
	maxPagesCap                = 5000
)

// ParseManifest parses pack.yaml strictly: unknown keys, a second document or trailing garbage are errors. The result has
// defaults applied and has passed Validate.
func ParseManifest(b []byte, vo ValidateOptions) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("pack.yaml is empty")
		}
		return nil, fmt.Errorf("pack.yaml: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("pack.yaml must hold exactly one document")
	}
	m.applyDefaults()
	if err := m.Validate(vo); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) applyDefaults() {
	c := &m.Chunking
	if c.TargetTokens == 0 {
		c.TargetTokens = 350
	}
	if c.MaxTokens == 0 {
		c.MaxTokens = 700
	}
	if c.CodeAtomicTokens == 0 {
		c.CodeAtomicTokens = 1200
	}
	if len(c.SplitOn) == 0 {
		c.SplitOn = []string{"h1", "h2", "h3"}
	}
	cr := &m.Crawl
	if cr.RPS == 0 {
		cr.RPS = 2
	}
	if cr.Concurrency == 0 {
		cr.Concurrency = 2
	}
	if cr.MaxPageBytes == 0 {
		cr.MaxPageBytes = 2 << 20
	}
	if cr.Redirects == "" {
		cr.Redirects = "same_host"
	}
	if cr.MaxPages == 0 {
		cr.MaxPages = 500
	}
	if cr.Timeout == 0 {
		cr.Timeout = Dur(20 * time.Second)
	}
	if cr.Budget == 0 {
		cr.Budget = Dur(30 * time.Minute)
	}
	if cr.MaxRepoBytes == 0 {
		cr.MaxRepoBytes = 256 << 20
	}
	if m.Refresh.Mode == "" {
		m.Refresh.Mode = "manual"
	}
	if m.Refresh.Mode == "scheduled" && m.Refresh.Interval == 0 {
		m.Refresh.Interval = Dur(7 * 24 * time.Hour)
	}
	if m.Freshness.WarnAfter == 0 {
		m.Freshness.WarnAfter = Dur(14 * 24 * time.Hour)
	}
	if m.Freshness.StaleAfter == 0 {
		m.Freshness.StaleAfter = Dur(30 * 24 * time.Hour)
	}
	if m.Attach.DefaultK == 0 {
		m.Attach.DefaultK = 5
	}
	if m.Consult.MaxChunks == 0 {
		m.Consult.MaxChunks = 8
	}
	if m.Consult.Route == "" {
		if m.Visibility == VisibilityPrivate {
			m.Consult.Route = RoutePrivateOnly
		} else {
			m.Consult.Route = RouteTrainableOK
		}
	}
	if m.Live != nil {
		if m.Live.Arg == "" {
			m.Live.Arg = "query"
		}
		if m.Live.Use == "" {
			if m.Kind == KindLive {
				m.Live.Use = UseLivePrimary
			} else {
				m.Live.Use = UseLiveSupplement
			}
		}
	}
	if m.Refresh.Digest.DataClass == "" {
		if m.Visibility == VisibilityPrivate {
			m.Refresh.Digest.DataClass = RoutePrivateOnly
		} else {
			m.Refresh.Digest.DataClass = RouteTrainableOK
		}
	}
}

// Validate checks the whole manifest and reports every problem it finds in one error.
func (m *Manifest) Validate(vo ValidateOptions) error {
	var errs []string
	bad := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if m.Schema != SchemaVersion {
		bad("schema must be %d", SchemaVersion)
	}
	if !packIDRe.MatchString(m.ID) {
		bad("id %q must be a slug of 2 to 40 lowercase letters, digits and hyphens starting with a letter", m.ID)
	}
	if t := strings.TrimSpace(m.Title); t == "" || len(t) > 120 {
		bad("title is required (at most 120 characters)")
	}
	switch m.Kind {
	case KindIndexed, KindLive, KindHybrid:
	default:
		bad("kind must be indexed, live or hybrid")
	}
	switch m.Visibility {
	case VisibilityPublic, VisibilityPrivate:
	default:
		bad("visibility must be public or private")
	}
	if m.Visibility == VisibilityPublic && strings.TrimSpace(m.License.Content) == "" {
		bad("license.content is required for a public pack (an SPDX id, or \"unknown\" if you do not know)")
	}
	if m.License.AttributionRequired && strings.TrimSpace(m.License.Attribution) == "" {
		bad("license.attribution_required is true but license.attribution is empty")
	}
	if len(m.License.Attribution) > 400 {
		bad("license.attribution is longer than 400 characters")
	}

	hasURLSource := false
	switch m.Kind {
	case KindIndexed, KindHybrid:
		if len(m.Sources) == 0 {
			bad("an %s pack needs at least one source", m.Kind)
		}
	case KindLive:
		if len(m.Sources) > 0 {
			bad("a live pack has no sources (it is queried through its MCP server)")
		}
	}
	for i, s := range m.Sources {
		p := fmt.Sprintf("sources[%d]", i)
		switch s.Type {
		case SourceLlmsTxt, SourceSitemap:
			hasURLSource = true
			if s.Path != "" || len(s.URLs) > 0 {
				bad("%s: %s takes url only", p, s.Type)
			}
			checkURL(&errs, p+".url", s.URL, vo)
		case SourceURLList:
			hasURLSource = true
			if s.URL != "" || s.Path != "" {
				bad("%s: url_list takes urls only", p)
			}
			if len(s.URLs) == 0 {
				bad("%s: urls is empty", p)
			}
			if len(s.URLs) > 1000 {
				bad("%s: more than 1000 urls", p)
			}
			for j, u := range s.URLs {
				checkURL(&errs, fmt.Sprintf("%s.urls[%d]", p, j), u, vo)
			}
		case SourceLocalDir:
			if s.URL != "" || len(s.URLs) > 0 {
				bad("%s: local_dir takes path only", p)
			}
			if strings.TrimSpace(s.Path) == "" {
				bad("%s: path is required", p)
			}
			if strings.ContainsRune(s.Path, 0) {
				bad("%s: path contains NUL", p)
			}
		case SourceGitDocs:
			hasURLSource = true
			if len(s.URLs) > 0 {
				bad("%s: git_docs takes url, ref and path", p)
			}
			checkURL(&errs, p+".url", s.URL, vo)
			if !git.ValidDocsRef(s.Ref) {
				bad("%s: ref %q must be a branch, tag or commit name", p, s.Ref)
			}
			if _, err := git.CleanDocsPath(s.Path); err != nil {
				bad("%s: path %q must be a folder inside the repository", p, s.Path)
			}
			if s.PageFormat != "" {
				bad("%s: git_docs takes no page_format (Markdown, MDX and text files are read by their extension)", p)
			}
		default:
			bad("%s: unknown source type %q (llms_txt, sitemap, url_list, local_dir, git_docs)", p, s.Type)
		}
		if s.Ref != "" && s.Type != SourceGitDocs {
			bad("%s: ref applies to git_docs only", p)
		}
		switch s.Follow {
		case "":
		case "per_product_llms_txt":
			if s.Type != SourceLlmsTxt {
				bad("%s: follow per_product_llms_txt applies to llms_txt only", p)
			}
		default:
			if s.Type != SourceSitemap || !strings.HasPrefix(s.Follow, "/") || len(s.Follow) > 200 {
				bad("%s: follow %q is not valid here (llms_txt: per_product_llms_txt; sitemap: a path prefix starting with /)", p, s.Follow)
			}
		}
		switch s.PageFormat {
		case "", "markdown_suffix", "html", "mdx":
		default:
			bad("%s: page_format must be markdown_suffix, html or mdx", p)
		}
	}

	cr := m.Crawl
	if cr.RespectRobots != nil && !*cr.RespectRobots {
		bad("crawl.respect_robots cannot be false: Drydock crawlers always respect robots.txt")
	}
	switch cr.RequireContentSignal {
	case "", "ai-input", "ai-train", "search":
	default:
		bad("crawl.require_content_signal must be ai-input, ai-train or search")
	}
	if cr.Redirects != "same_host" {
		bad("crawl.redirects must be same_host (cross-host redirects are never followed)")
	}
	if cr.RPS < 0.01 || cr.RPS > 10 {
		bad("crawl.rps must be between 0.01 and 10")
	}
	if cr.Concurrency < 1 || cr.Concurrency > 8 {
		bad("crawl.concurrency must be between 1 and 8")
	}
	if cr.MaxPageBytes < 1024 || cr.MaxPageBytes > maxPageBytesCap {
		bad("crawl.max_page_bytes must be between 1024 and %d", maxPageBytesCap)
	}
	if cr.MaxPages < 1 || cr.MaxPages > maxPagesCap {
		bad("crawl.max_pages must be between 1 and %d", maxPagesCap)
	}
	if cr.Timeout.D() < time.Second || cr.Timeout.D() > 2*time.Minute {
		bad("crawl.timeout must be between 1s and 2m")
	}
	if cr.Budget.D() < time.Minute || cr.Budget.D() > 6*time.Hour {
		bad("crawl.refresh_budget must be between 1m and 6h")
	}
	if cr.MaxRepoBytes < 1<<20 || cr.MaxRepoBytes > 2<<30 {
		bad("crawl.max_repo_bytes must be between 1 MB and 2 GB")
	}
	if hasURLSource {
		if len(cr.AllowedHosts) == 0 {
			bad("crawl.allowed_hosts is required for URL sources: the crawler fetches only from hosts you list")
		}
	}
	for _, h := range cr.AllowedHosts {
		if !hostRe.MatchString(strings.ToLower(h)) || net.ParseIP(strings.Trim(h, "[]")) != nil && !vo.AllowLoopbackHTTP {
			bad("crawl.allowed_hosts: %q is not a plain host name", h)
		}
	}
	for _, d := range cr.DenyPaths {
		if !strings.HasPrefix(d, "/") || len(d) > 200 {
			bad("crawl.deny_paths: %q must start with /", d)
		}
	}
	if hasURLSource {
		for i, s := range m.Sources {
			for _, u := range append([]string{s.URL}, s.URLs...) {
				if u == "" {
					continue
				}
				if pu, err := url.Parse(u); err == nil && pu.Host != "" && !HostAllowed(cr.AllowedHosts, pu.Hostname()) {
					bad("sources[%d]: host %s is not in crawl.allowed_hosts", i, pu.Hostname())
				}
			}
		}
	}

	ck := m.Chunking
	for _, n := range ck.SplitOn {
		if _, ok := splitNames[strings.ToLower(n)]; !ok {
			bad("chunking.split_on: %q is not one of h1, h2, h3, h4", n)
		}
	}
	if ck.TargetTokens < 50 || ck.TargetTokens > 2000 {
		bad("chunking.target_tokens must be between 50 and 2000")
	}
	if ck.MaxTokens < ck.TargetTokens || ck.MaxTokens > 4000 {
		bad("chunking.max_tokens must be between target_tokens and 4000")
	}
	if ck.CodeAtomicTokens < ck.MaxTokens || ck.CodeAtomicTokens > 8000 {
		bad("chunking.code_block_atomic_tokens must be between max_tokens and 8000")
	}

	switch m.Refresh.Mode {
	case "manual", "scheduled":
	default:
		bad("refresh.mode must be scheduled or manual")
	}
	if m.Refresh.Mode == "scheduled" && m.Refresh.Interval.D() < time.Hour {
		bad("refresh.interval must be at least 1h")
	}
	switch m.Refresh.Digest.DataClass {
	case RouteTrainableOK, RoutePrivateOnly:
	default:
		bad("refresh.digest.data_class must be trainable_ok or private_only")
	}
	if m.Visibility == VisibilityPrivate && m.Refresh.Digest.DataClass == RouteTrainableOK {
		bad("a private pack's digest can use a private route only (refresh.digest.data_class: private_only)")
	}
	if m.Freshness.WarnAfter.D() <= 0 || m.Freshness.StaleAfter.D() < m.Freshness.WarnAfter.D() {
		bad("freshness.warn_after must be positive and not after stale_after")
	}

	switch m.Kind {
	case KindLive, KindHybrid:
		if m.Live == nil {
			bad("a %s pack needs a live block (mcp, tool)", m.Kind)
		}
	case KindIndexed:
		if m.Live != nil {
			bad("an indexed pack has no live block (use kind: hybrid)")
		}
	}
	if m.Live != nil {
		if !mcpIDRe.MatchString(m.Live.MCP) {
			bad("live.mcp %q must be a catalog server id (at most 8 lowercase letters and digits)", m.Live.MCP)
		}
		if !toolRe.MatchString(m.Live.Tool) {
			bad("live.tool %q is not a valid tool name", m.Live.Tool)
		}
		if !argRe.MatchString(m.Live.Arg) {
			bad("live.arg %q is not a valid argument name", m.Live.Arg)
		}
		switch m.Live.Use {
		case UseLivePrimary, UseLiveFallback, UseLiveSupplement:
		default:
			bad("live.use must be primary, fallback or supplement")
		}
		if m.Kind == KindLive && m.Live.Use != UseLivePrimary {
			bad("a live pack's live.use must be primary")
		}
	}

	at := m.Attach
	if EstimateTokens(at.Instructions) > MaxAttachInstructionTokens {
		bad("attach.instructions is longer than %d tokens (about %d characters)", MaxAttachInstructionTokens, MaxAttachInstructionTokens*4)
	}
	if risk, flags := ScanInjection(at.Instructions, 0); risk >= QuarantineRisk {
		bad("attach.instructions looks like a prompt injection (%s); instructions say what the pack is for and never contain commands for the agent to obey", strings.Join(flags, ", "))
	}
	if strings.ContainsRune(at.Instructions, HiddenMark) {
		bad("attach.instructions contains hidden characters")
	}
	if at.DefaultK < 1 || at.DefaultK > 10 {
		bad("attach.default_k must be between 1 and 10")
	}
	for _, r := range at.Roles {
		if CanonicalRole(r) == "" {
			bad("attach.roles: unknown role %q", r)
		}
	}
	if m.Consult.MaxChunks < 1 || m.Consult.MaxChunks > 20 {
		bad("consult.max_chunks must be between 1 and 20")
	}
	switch m.Consult.Route {
	case RouteTrainableOK, RoutePrivateOnly:
	default:
		bad("consult.route must be trainable_ok or private_only")
	}
	if m.Visibility == VisibilityPrivate && m.Consult.Route == RouteTrainableOK {
		bad("a private pack's consult.route must be private_only: content derived from it never reaches a trainable provider")
	}
	if len(errs) > 0 {
		return fmt.Errorf("pack.yaml is invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}

func checkURL(errs *[]string, field, raw string, vo ValidateOptions) {
	bad := func(f string, a ...any) { *errs = append(*errs, fmt.Sprintf(f, a...)) }
	if raw == "" {
		bad("%s is required", field)
		return
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		bad("%s: %q is not a valid URL", field, raw)
		return
	}
	if u.User != nil {
		bad("%s: a URL must not carry credentials", field)
	}
	if u.Fragment != "" {
		bad("%s: a URL must not have a fragment", field)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !(vo.AllowLoopbackHTTP && IsLoopbackHost(u.Hostname())) {
			bad("%s: plain http is not allowed (https only)", field)
		}
	default:
		bad("%s: scheme must be https", field)
	}
}

// IsLoopbackHost reports whether h is localhost or a loopback address.
func IsLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

// HostAllowed reports whether host matches an allowlist entry: exact (case-insensitive) or "*.suffix" for any subdomain.
func HostAllowed(allowed []string, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range allowed {
		a = strings.ToLower(a)
		if a == host {
			return true
		}
		if strings.HasPrefix(a, "*.") && strings.HasSuffix(host, a[1:]) && len(host) > len(a)-1 {
			return true
		}
	}
	return false
}

// ChunkOptions converts the manifest's chunking block.
func (m *Manifest) ChunkOptions() ChunkOptions {
	lv := map[int]bool{}
	for _, n := range m.Chunking.SplitOn {
		lv[splitNames[strings.ToLower(n)]] = true
	}
	return ChunkOptions{SplitLevels: lv, TargetTokens: m.Chunking.TargetTokens, MaxTokens: m.Chunking.MaxTokens, CodeAtomicTokens: m.Chunking.CodeAtomicTokens}
}

// Marshal renders the manifest back to YAML (defaults included).
func (m *Manifest) Marshal() ([]byte, error) { return yaml.Marshal(m) }
