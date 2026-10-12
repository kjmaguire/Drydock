// Package agentcatalog holds Drydock's prebuilt agents (decision P27): ready team members with a role, standing instructions,
// a preferred agent CLI and model with alternatives, the built-in MCP catalog servers they use and the registry skills they are
// equipped with. The catalog is embedded (catalog.yaml) and validated strictly against the MCP catalog and the skills registry
// allowlist, so a shipped entry can never name a server or a registry that does not exist. Applying an entry is the core's job
// (internal/core/ext_agentcatalog.go).
package agentcatalog

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/kjmaguire/drydock/internal/mcp"
	"github.com/kjmaguire/drydock/internal/skills"
)

//go:embed catalog.yaml
var builtinYAML []byte

// Schema is the only catalog schema this build reads.
const Schema = 1

// Limits.
const (
	MaxPurposeRunes     = 4000 // the purpose rides in every run's system prompt (core.MaxPurposeRunes)
	MaxSummaryRunes     = 120
	MaxDescriptionRunes = 400
)

// Roles a prebuilt agent may have: the brief's roles plus the Team Lead supervisor ("lead") and documentation ("docs").
var Roles = map[string]bool{
	"lead": true, "pm": true, "architect": true, "ux designer": true, "frontend": true, "backend": true, "database": true,
	"devops": true, "qa": true, "security": true, "reviewer": true, "docs": true,
}

// Categories group entries on the page.
var Categories = []string{"management", "engineering", "design", "docs", "quality", "security"}

// Adapters an entry may prefer or offer: Claude Code headless and the ACP CLIs.
var Adapters = map[string]bool{"claude": true, "gemini": true, "codex": true, "opencode": true, "goose": true}

// Claude Code's accepted values (internal/agents/claude/adapter.go). ACP CLIs report theirs at runtime.
var (
	claudeModels          = map[string]bool{"fable": true, "opus": true, "sonnet": true, "haiku": true}
	claudeEfforts         = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	claudePermissionModes = map[string]bool{"default": true, "acceptEdits": true, "plan": true, "dontAsk": true, "auto": true, "bypassPermissions": true}
)

var (
	idRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)
	tagRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,29}$`)
	commitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Entry is one prebuilt agent.
type Entry struct {
	ID       string `yaml:"id" json:"id"`
	Name     string `yaml:"name" json:"name"`
	Role     string `yaml:"role" json:"role"`
	Category string `yaml:"category" json:"category"`
	// Summary is one line; Description two or three plain sentences; Purpose the standing instructions the agent gets.
	Summary      string        `yaml:"summary" json:"summary"`
	Description  string        `yaml:"description" json:"description"`
	Purpose      string        `yaml:"purpose" json:"purpose"`
	Preferred    Preferred     `yaml:"preferred" json:"preferred"`
	Alternatives []Alternative `yaml:"alternatives" json:"alternatives"`
	// MCP are built-in MCP catalog ids (internal/mcp/catalog.yaml) the agent is granted when the human connects them.
	MCP []string `yaml:"mcp" json:"mcp"`
	// Skills are registry skills pinned to a commit, installed and assigned to the agent when the human asks for them.
	Skills []SkillRef `yaml:"skills" json:"skills"`
	Tags   []string   `yaml:"tags" json:"tags"`
}

// Preferred is the CLI and settings the entry was written for.
type Preferred struct {
	Adapter        string `yaml:"adapter" json:"adapter"`
	Model          string `yaml:"model,omitempty" json:"model"`
	Effort         string `yaml:"effort,omitempty" json:"effort"`
	PermissionMode string `yaml:"permission_mode,omitempty" json:"permissionMode"`
	// Practices turns the Claude working-practices text on (decision P17); it applies to Claude only. Omitted means on.
	Practices *bool `yaml:"practices,omitempty" json:"practices"`
}

// Alternative is another CLI the entry works with. An empty model means the CLI's default (ACP CLIs report their models at
// runtime, so the human picks one in the agent's settings).
type Alternative struct {
	Adapter string `yaml:"adapter" json:"adapter"`
	Model   string `yaml:"model,omitempty" json:"model"`
	Effort  string `yaml:"effort,omitempty" json:"effort"`
}

// SkillRef names a skill of an allowlisted registry at a commit.
type SkillRef struct {
	Registry string `yaml:"registry" json:"registry"`
	Name     string `yaml:"name" json:"name"`
	Ref      string `yaml:"ref" json:"ref"`
}

// PracticesOn reports whether the Claude practices text is on for the preferred setup.
func (p Preferred) PracticesOn() bool { return p.Practices == nil || *p.Practices }

// Setup is the CLI, model and effort an entry gets on an adapter: the preferred settings on the preferred adapter, a listed
// alternative's on that adapter, and nothing (the CLI's defaults) on any other.
func (e Entry) Setup(adapter string) (model, effort string, listed bool) {
	if adapter == "" || adapter == e.Preferred.Adapter {
		return e.Preferred.Model, e.Preferred.Effort, true
	}
	for _, a := range e.Alternatives {
		if a.Adapter == adapter {
			return a.Model, a.Effort, true
		}
	}
	return "", "", false
}

// Refs resolves what an entry may name: the curated skills registries and the MCP catalog servers that work without a secret.
type Refs struct {
	Registries map[string]bool
	// MCP maps a catalog id to "" when an agent can use it as soon as it is added, or to the reason it cannot be named.
	MCP map[string]string
	// Skills are the recorded descriptions of registry skills (skills.KnownSkills); when set, every skill an entry names must
	// be recorded at the entry's pin, so the page can say what it does.
	Skills map[string]skills.KnownSkill
}

// BuiltinRefs reads the shipped skills registry allowlist and MCP catalog.
func BuiltinRefs() (Refs, error) {
	regs, err := skills.CuratedRegistries()
	if err != nil {
		return Refs{}, err
	}
	cat, err := mcp.LoadBuiltin()
	if err != nil {
		return Refs{}, err
	}
	known, err := skills.KnownSkills()
	if err != nil {
		return Refs{}, err
	}
	r := Refs{Registries: map[string]bool{}, MCP: map[string]string{}, Skills: known}
	for _, g := range regs {
		r.Registries[g.ID] = true
	}
	for _, s := range cat.Servers {
		switch {
		case s.Transport != mcp.TransportHTTP || s.Template:
			r.MCP[s.ID] = "is not a hosted http server"
		case s.Auth.Kind == mcp.AuthNone || (s.Auth.Kind == mcp.AuthBearer && s.Auth.Optional):
			r.MCP[s.ID] = ""
		default:
			r.MCP[s.ID] = "needs an account or a token, which a prebuilt agent cannot bring"
		}
	}
	return r, nil
}

func oneWord(what, v string) error {
	if strings.HasPrefix(v, "-") || strings.IndexFunc(v, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("%s %q must be a single word that does not start with '-'", what, v)
	}
	return nil
}

func checkSetup(adapter, model, effort, mode string) error {
	if !Adapters[adapter] {
		return fmt.Errorf("unknown adapter %q", adapter)
	}
	if err := oneWord("model", model); err != nil {
		return err
	}
	if err := oneWord("effort", effort); err != nil {
		return err
	}
	if adapter == "claude" {
		if model != "" && !claudeModels[model] {
			return fmt.Errorf("claude model %q is not one of opus, sonnet, haiku, fable", model)
		}
		if effort != "" && !claudeEfforts[effort] {
			return fmt.Errorf("claude effort %q is not one of low, medium, high, xhigh, max", effort)
		}
		if mode != "" && !claudePermissionModes[mode] {
			return fmt.Errorf("claude permission mode %q is unknown", mode)
		}
	} else if mode != "" {
		return fmt.Errorf("a permission mode is a Claude Code setting; %s has none", adapter)
	}
	return nil
}

// Validate checks one entry against the limits and refs.
func (e *Entry) Validate(refs Refs) error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	if !idRe.MatchString(e.ID) {
		add("id %q must be a lowercase slug", e.ID)
	}
	if strings.TrimSpace(e.Name) == "" || utf8.RuneCountInString(e.Name) > 60 {
		add("name is required (at most 60 characters)")
	}
	if !Roles[e.Role] {
		add("role %q is not one Drydock recognises", e.Role)
	}
	known := false
	for _, c := range Categories {
		known = known || c == e.Category
	}
	if !known {
		add("category %q must be one of %s", e.Category, strings.Join(Categories, ", "))
	}
	e.Summary, e.Description, e.Purpose = strings.TrimSpace(e.Summary), strings.TrimSpace(e.Description), strings.TrimSpace(e.Purpose)
	if e.Summary == "" || strings.Contains(e.Summary, "\n") || utf8.RuneCountInString(e.Summary) > MaxSummaryRunes {
		add("summary must be one line of at most %d characters", MaxSummaryRunes)
	}
	if e.Description == "" || utf8.RuneCountInString(e.Description) > MaxDescriptionRunes {
		add("description is required (at most %d characters)", MaxDescriptionRunes)
	}
	if e.Purpose == "" || utf8.RuneCountInString(e.Purpose) > MaxPurposeRunes || strings.ContainsRune(e.Purpose, 0) {
		add("purpose is required (at most %d characters)", MaxPurposeRunes)
	}
	if err := checkSetup(e.Preferred.Adapter, e.Preferred.Model, e.Preferred.Effort, e.Preferred.PermissionMode); err != nil {
		add("preferred: %v", err)
	}
	if e.Preferred.Practices != nil && *e.Preferred.Practices && e.Preferred.Adapter != "claude" {
		add("preferred: practices are written for Claude only")
	}
	seenAd := map[string]bool{e.Preferred.Adapter: true}
	for _, a := range e.Alternatives {
		if seenAd[a.Adapter] {
			add("alternative %q is listed twice or is the preferred adapter", a.Adapter)
		}
		seenAd[a.Adapter] = true
		if err := checkSetup(a.Adapter, a.Model, a.Effort, ""); err != nil {
			add("alternative: %v", err)
		}
	}
	seen := map[string]bool{}
	for _, id := range e.MCP {
		reason, ok := refs.MCP[id]
		switch {
		case !ok:
			add("mcp %q is not in the built-in MCP catalog", id)
		case reason != "":
			add("mcp %q %s", id, reason)
		case seen[id]:
			add("mcp %q is listed twice", id)
		}
		seen[id] = true
	}
	seenSkill := map[string]bool{}
	for _, s := range e.Skills {
		if !refs.Registries[s.Registry] {
			add("skill %s: registry %q is not in the curated allowlist", s.Name, s.Registry)
		}
		if err := skills.ValidateName(s.Name); err != nil {
			add("skill %q: %v", s.Name, err)
		}
		if !commitRe.MatchString(s.Ref) {
			add("skill %s: ref must be a full commit id (a pin), got %q", s.Name, s.Ref)
		}
		if refs.Skills != nil {
			if _, ok := refs.Skills[skills.KnownSkillKey(s.Registry, s.Name, s.Ref)]; !ok {
				add("skill %s: registry.yaml records no description for %s at %s (read its SKILL.md at the pin and add it)", s.Name, s.Registry, s.Ref)
			}
		}
		if seenSkill[s.Name] {
			add("skill %s is listed twice (skill names are unique in a project)", s.Name)
		}
		seenSkill[s.Name] = true
	}
	for _, t := range e.Tags {
		if !tagRe.MatchString(t) {
			add("tag %q must be a short lowercase slug", t)
		}
	}
	if e.Alternatives == nil {
		e.Alternatives = []Alternative{}
	}
	if e.MCP == nil {
		e.MCP = []string{}
	}
	if e.Skills == nil {
		e.Skills = []SkillRef{}
	}
	if e.Tags == nil {
		e.Tags = []string{}
	}
	return errors.Join(errs...)
}

// Parse reads a catalog document strictly (unknown keys are errors) and validates every entry.
func Parse(b []byte, refs Refs) ([]Entry, error) {
	var doc struct {
		Schema int     `yaml:"schema"`
		Agents []Entry `yaml:"agents"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("agent catalog: %w", err)
	}
	if doc.Schema != Schema {
		return nil, fmt.Errorf("agent catalog: schema %d is not supported (want %d)", doc.Schema, Schema)
	}
	seen := map[string]bool{}
	var errs []error
	for i := range doc.Agents {
		e := &doc.Agents[i]
		if err := e.Validate(refs); err != nil {
			errs = append(errs, fmt.Errorf("agent %q: %w", e.ID, err))
		}
		if seen[e.ID] {
			errs = append(errs, fmt.Errorf("duplicate agent id %q", e.ID))
		}
		seen[e.ID] = true
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return doc.Agents, nil
}

var (
	once    sync.Once
	builtin []Entry
	loadErr error
)

// Builtin returns the shipped catalog (parsed once). The slice must not be modified.
func Builtin() ([]Entry, error) {
	once.Do(func() {
		refs, err := BuiltinRefs()
		if err != nil {
			loadErr = err
			return
		}
		builtin, loadErr = Parse(builtinYAML, refs)
	})
	return builtin, loadErr
}

// Get returns one shipped entry.
func Get(id string) (Entry, bool) {
	all, err := Builtin()
	if err != nil {
		return Entry{}, false
	}
	for _, e := range all {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}
