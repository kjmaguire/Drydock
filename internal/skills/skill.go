package skills

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kjmaguire/drydock/internal/packs"
	"github.com/kjmaguire/drydock/internal/security"
)

// Limits (docs/expert-packs.md section 9).
const (
	MaxNameLen        = 64
	MaxDescriptionLen = 1024
	MaxCompatibility  = 500
	// MaxSkillBytes caps a SKILL.md. Published skills reach 105,146 bytes (anthropics/skills claude-api, measured on
	// 2026-10-14) and 90,323 (MicrosoftDocs/Agent-Skills). Size does not reach every turn: relevant delivery lists a skill and
	// the agent opens it on demand, and the context manifest flags more than 32 KB in every turn.
	MaxSkillBytes      = 128 << 10
	GuidanceBodyTokens = 5000
	GuidanceBodyLines  = 500
)

// Metadata keys Drydock reads (everything else in metadata is kept, ignored).
const (
	MetaGlobs  = "drydock.globs"
	MetaRoles  = "drydock.roles"
	MetaSource = "drydock.source"
)

var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ParsedSkill is a validated SKILL.md.
type ParsedSkill struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	Metadata      map[string]string
	AllowedTools  string // recorded, never used: a skill cannot widen permissions
	Globs         []string
	Roles         []string
	Source        string
	Body          string
	// Warnings are spec guidance exceeded (long body) that do not block.
	Warnings []string
	// IgnoredKeys are top-level front matter keys outside the Agent Skills fields (for example Claude Code's `context` or
	// `argument-hint`). They are recorded and ignored: Drydock never acts on them and never writes them into a worktree.
	IgnoredKeys []string
	// DeclaredName is the front matter name when it differs from the directory name a registry skill is stored under
	// (ParseRegistrySkill only); empty otherwise.
	DeclaredName string
}

// Notes are the parse findings worth showing a human next to the skill: ignored keys and a renamed skill.
func (p *ParsedSkill) Notes() []string {
	out := []string{}
	if len(p.IgnoredKeys) > 0 {
		out = append(out, "front matter keys outside the Agent Skills format were ignored: "+strings.Join(p.IgnoredKeys, ", "))
	}
	if p.DeclaredName != "" {
		out = append(out, fmt.Sprintf("the front matter calls it %q; it is stored under its directory name %q", p.DeclaredName, p.Name))
	}
	return out
}

// knownKeys are the Agent Skills front matter fields Drydock reads.
var knownKeys = map[string]bool{"name": true, "description": true, "license": true, "compatibility": true, "metadata": true, "allowed-tools": true}

// maxIgnoredKeys bounds how many unknown keys are tolerated (a front matter of hundreds of keys is not a skill).
const maxIgnoredKeys = 32

type frontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
	// AllowedTools is a space-separated string in the spec; Claude Code also accepts a list. Either is recorded, never used.
	AllowedTools yaml.Node `yaml:"allowed-tools"`
}

// ValidateName applies the Agent Skills naming rules.
func ValidateName(n string) error {
	switch {
	case n == "" || len(n) > MaxNameLen:
		return fmt.Errorf("name must be 1 to %d characters", MaxNameLen)
	case !nameRe.MatchString(n):
		return fmt.Errorf("name %q must use lowercase letters, digits and single hyphens, with no leading or trailing hyphen", n)
	}
	return nil
}

// ParseSkill parses and validates SKILL.md text. wantName is the directory (or file) name the skill must be called; an
// empty wantName skips that comparison. Top-level front matter keys outside the Agent Skills fields are ignored and recorded
// in IgnoredKeys (agent products add their own, such as Claude Code's `context: fork`); the known fields are still checked
// strictly (types, lengths, the name rules).
func ParseSkill(data []byte, wantName string) (*ParsedSkill, error) {
	return parseSkill(data, wantName, false)
}

// ParseRegistrySkill parses a skill found in the registry directory dirName. A registry skill is identified by its directory
// (that is how it is listed, imported and pinned), so a front matter name that differs from the directory, which some
// published registries ship (for example "vercel-react-best-practices" in react-best-practices/), is recorded in
// DeclaredName and the skill is stored under dirName. Both names must follow the naming rules. Repository skills keep the
// spec's rule that the two are equal (ParseSkill): the project can fix its own files.
func ParseRegistrySkill(data []byte, dirName string) (*ParsedSkill, error) {
	return parseSkill(data, dirName, true)
}

func parseSkill(data []byte, wantName string, renameToDir bool) (*ParsedSkill, error) {
	if len(data) > MaxSkillBytes {
		return nil, fmt.Errorf("SKILL.md is larger than %d KiB", MaxSkillBytes>>10)
	}
	if bytes.ContainsRune(data, 0) {
		return nil, errors.New("SKILL.md is not text")
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimPrefix(text, "\xef\xbb\xbf")
	if !strings.HasPrefix(text, "---\n") {
		return nil, errors.New("SKILL.md must start with YAML front matter (---)")
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return nil, errors.New("SKILL.md front matter is not closed (---)")
	}
	fmText := text[4 : 4+end]
	body := strings.TrimLeft(text[4+end+4:], "\n")
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(fmText), &root); err != nil {
		return nil, fmt.Errorf("front matter: %w", err)
	}
	if len(root.Content) != 1 || root.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("front matter must be a YAML mapping")
	}
	m := root.Content[0]
	var ignored []string
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind != yaml.ScalarNode {
			return nil, errors.New("front matter keys must be plain names")
		}
		if !knownKeys[k.Value] {
			ignored = append(ignored, k.Value)
		}
	}
	if len(ignored) > maxIgnoredKeys {
		return nil, fmt.Errorf("front matter has %d keys outside the Agent Skills format (at most %d are tolerated)", len(ignored), maxIgnoredKeys)
	}
	sort.Strings(ignored)
	var fm frontmatter
	if err := m.Decode(&fm); err != nil {
		return nil, fmt.Errorf("front matter: %w", err)
	}
	allowed, err := allowedTools(fm.AllowedTools)
	if err != nil {
		return nil, err
	}
	p := &ParsedSkill{Name: fm.Name, Description: strings.TrimSpace(fm.Description), License: strings.TrimSpace(fm.License), Compatibility: fm.Compatibility,
		Metadata: fm.Metadata, AllowedTools: allowed, Body: strings.TrimSpace(body), IgnoredKeys: ignored}
	if err := ValidateName(p.Name); err != nil {
		return nil, err
	}
	if wantName != "" && p.Name != wantName {
		if !renameToDir {
			return nil, fmt.Errorf("name %q must equal its directory name %q", p.Name, wantName)
		}
		if err := ValidateName(wantName); err != nil {
			return nil, fmt.Errorf("directory name: %w", err)
		}
		p.DeclaredName, p.Name = p.Name, wantName
	}
	if p.Description == "" || len(p.Description) > MaxDescriptionLen {
		return nil, fmt.Errorf("description is required and at most %d characters", MaxDescriptionLen)
	}
	if len(p.Compatibility) > MaxCompatibility {
		return nil, fmt.Errorf("compatibility is longer than %d characters", MaxCompatibility)
	}
	if p.Body == "" {
		return nil, errors.New("SKILL.md has no body")
	}
	if g := p.Metadata[MetaGlobs]; g != "" {
		p.Globs = SplitGlobs(g)
		if len(p.Globs) > MaxGlobs {
			return nil, fmt.Errorf("more than %d globs", MaxGlobs)
		}
		for _, x := range p.Globs {
			if err := ValidateGlob(x); err != nil {
				return nil, fmt.Errorf("%s: %q: %w", MetaGlobs, x, err)
			}
		}
	}
	if r := p.Metadata[MetaRoles]; r != "" {
		for _, x := range strings.Split(r, ",") {
			c := packs.CanonicalRole(x)
			if c == "" {
				return nil, fmt.Errorf("%s: unknown role %q", MetaRoles, strings.TrimSpace(x))
			}
			p.Roles = append(p.Roles, c)
		}
		sort.Strings(p.Roles)
	}
	p.Source = p.Metadata[MetaSource]
	if packs.EstimateTokens(p.Body) > GuidanceBodyTokens {
		p.Warnings = append(p.Warnings, fmt.Sprintf("body is longer than %d tokens (spec guidance)", GuidanceBodyTokens))
	}
	if strings.Count(p.Body, "\n")+1 > GuidanceBodyLines {
		p.Warnings = append(p.Warnings, fmt.Sprintf("body is longer than %d lines (spec guidance)", GuidanceBodyLines))
	}
	return p, nil
}

// allowedTools reads allowed-tools as the spec's space-separated string or as a list of tool names.
func allowedTools(n yaml.Node) (string, error) {
	switch n.Kind {
	case 0:
		return "", nil
	case yaml.ScalarNode:
		return strings.TrimSpace(n.Value), nil
	case yaml.SequenceNode:
		var parts []string
		for _, c := range n.Content {
			if c.Kind != yaml.ScalarNode {
				return "", errors.New("allowed-tools must be a string or a list of tool names")
			}
			if v := strings.TrimSpace(c.Value); v != "" {
				parts = append(parts, v)
			}
		}
		return strings.Join(parts, " "), nil
	}
	return "", errors.New("allowed-tools must be a string or a list of tool names")
}

// Verdict is the outcome of scanning a skill before use.
type Verdict struct {
	Risk    int
	Flags   []string
	Secrets []string // gitleaks rule ids (never values)
	// Blocked is true when the skill must not be used as is; Reason says why.
	Blocked bool
	Reason  string
	// SecretBlocked means a secret was found: the body is not stored at all.
	SecretBlocked bool
}

// Scan checks a skill's whole text (front matter included) for secrets and injection patterns. A secret blocks the skill
// outright, because rewriting a rules file silently would change what the author wrote. Injection patterns block when their
// score reaches packs.QuarantineRisk; a human can release such a skill after reading it.
func Scan(ctx context.Context, sc security.SecretScanner, raw string) (Verdict, error) {
	var v Verdict
	rules, err := packs.ScanOnce(ctx, sc, raw)
	if err != nil {
		return v, fmt.Errorf("secret scan failed: %w", err) // fail closed
	}
	norm, hidden := packs.Normalize(raw)
	v.Risk, v.Flags = packs.ScanInjection(norm, hidden)
	// hidden characters in a skill are never accepted: they are the classic way to smuggle instructions
	if hidden > 0 {
		v.Flags = append(v.Flags, "hidden_characters")
		v.Risk += packs.QuarantineRisk
	}
	v.Secrets = rules
	switch {
	case len(rules) > 0:
		v.Blocked, v.SecretBlocked = true, true
		v.Reason = "secret detected (" + strings.Join(rules, ", ") + ")"
	case v.Risk >= packs.QuarantineRisk:
		v.Blocked = true
		v.Reason = "possible prompt injection (" + strings.Join(v.Flags, ", ") + ")"
	}
	return v, nil
}

// Reaches reports whether skill k goes to the agent agentID, whose role is role: a skill for the whole team to every agent its
// roles match and to each agent it is given to, a skill for chosen agents only to those. It does not look at the status (only
// accepted skills are ever delivered, Eligible).
func Reaches(k Skill, agentID, role string) bool {
	given := agentID != "" && contains(k.Agents, agentID)
	if k.Audience == AudienceAgents {
		return given
	}
	return given || RolesMatch(k.Roles, role)
}

// RolesMatch reports whether a skill applies to a role: no roles listed means every role.
func RolesMatch(roles []string, role string) bool {
	if len(roles) == 0 {
		return true
	}
	c := packs.CanonicalRole(role)
	if c == "" {
		c = strings.TrimSpace(role)
	}
	for _, r := range roles {
		if strings.EqualFold(r, c) {
			return true
		}
	}
	return false
}

// PathsMatch reports whether any glob matches any path. A skill with no globs applies to every path.
func PathsMatch(globs, paths []string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		for _, p := range paths {
			if GlobMatch(g, p) {
				return true
			}
		}
	}
	return false
}
