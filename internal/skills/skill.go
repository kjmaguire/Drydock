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
	MaxNameLen         = 64
	MaxDescriptionLen  = 1024
	MaxCompatibility   = 500
	MaxSkillBytes      = 64 << 10
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
}

type frontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
	AllowedTools  string            `yaml:"allowed-tools"`
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
// empty wantName skips that comparison. Unknown front matter keys are errors: a file that needs them is not spec-valid.
func ParseSkill(data []byte, wantName string) (*ParsedSkill, error) {
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
	var fm frontmatter
	dec := yaml.NewDecoder(strings.NewReader(fmText))
	dec.KnownFields(true)
	if err := dec.Decode(&fm); err != nil {
		return nil, fmt.Errorf("front matter: %w", err)
	}
	p := &ParsedSkill{Name: fm.Name, Description: strings.TrimSpace(fm.Description), License: strings.TrimSpace(fm.License), Compatibility: fm.Compatibility,
		Metadata: fm.Metadata, AllowedTools: strings.TrimSpace(fm.AllowedTools), Body: strings.TrimSpace(body)}
	if err := ValidateName(p.Name); err != nil {
		return nil, err
	}
	if wantName != "" && p.Name != wantName {
		return nil, fmt.Errorf("name %q must equal its directory name %q", p.Name, wantName)
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
