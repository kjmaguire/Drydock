package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kjmaguire/drydock/internal/packs"
)

//go:embed registry.yaml
var builtinRegistryYAML []byte

// Registry is one allowlisted source of skills.
type Registry struct {
	ID           string `yaml:"id" json:"id"`
	Repo         string `yaml:"repo" json:"repo"` // owner/name on github.com
	Root         string `yaml:"root,omitempty" json:"root,omitempty"`
	License      string `yaml:"license,omitempty" json:"license,omitempty"`
	LicenseScope string `yaml:"license_scope,omitempty" json:"licenseScope,omitempty"` // "" | per_skill
	Trust        string `yaml:"trust,omitempty" json:"trust"`
	Import       string `yaml:"import,omitempty" json:"importMode,omitempty"` // "" | selective_only | per_skill_review
	Notes        string `yaml:"notes,omitempty" json:"notes,omitempty"`
	Also         string `yaml:"also,omitempty" json:"also,omitempty"`
	Enabled      bool   `yaml:"-" json:"enabled"`
	Curated      bool   `yaml:"-" json:"curated"`
	AddedBy      string `yaml:"-" json:"addedBy,omitempty"`
}

var (
	registryIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)
	repoRe       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)
)

// Validate checks a registry entry.
func (r Registry) Validate() error {
	if !registryIDRe.MatchString(r.ID) {
		return fmt.Errorf("registry id %q must be a slug (lowercase letters, digits, hyphens)", r.ID)
	}
	if !repoRe.MatchString(r.Repo) || strings.Contains(r.Repo, "..") {
		return fmt.Errorf("repo %q must be owner/name on github.com", r.Repo)
	}
	if r.Root != "" {
		n, ok := NormalizePath(r.Root)
		if !ok || n == "" {
			return fmt.Errorf("root %q must be a relative directory inside the repository", r.Root)
		}
	}
	switch r.LicenseScope {
	case "", "per_skill":
	default:
		return errors.New("license_scope must be empty or per_skill")
	}
	switch r.Import {
	case "", "selective_only", "per_skill_review":
	default:
		return errors.New("import must be empty, selective_only or per_skill_review")
	}
	switch r.Trust {
	case "", "vendor_official", "community":
	default:
		return errors.New("trust must be vendor_official or community")
	}
	if r.LicenseScope != "per_skill" && strings.TrimSpace(r.License) == "" {
		return errors.New("a registry needs a license (or license_scope: per_skill): unlicensed content is refused")
	}
	if len(r.Notes) > 500 || len(r.Also) > 500 {
		return errors.New("notes are longer than 500 characters")
	}
	return nil
}

// KnownSkill is what a registry skill's SKILL.md says at a pinned commit, recorded when the skill was named in the shipped
// catalogs (the prebuilt agents), so a page can say what the skill does before anything is fetched. It is a copy of the
// skill's own front matter description, not the skill text.
type KnownSkill struct {
	Registry string `yaml:"registry" json:"registry"`
	Name     string `yaml:"name" json:"name"`
	Ref      string `yaml:"ref" json:"ref"`
	// Path is the skill's directory inside the repository.
	Path        string `yaml:"path" json:"path"`
	Description string `yaml:"description" json:"description"`
	// License is the skill's own license (its LICENSE.txt or front matter), or the registry's when the skill names none.
	License string `yaml:"license" json:"license"`
}

// MaxKnownDescription is the Agent Skills limit on a description.
const MaxKnownDescription = 1024

type registryDoc struct {
	Schema     int          `yaml:"schema"`
	Registries []Registry   `yaml:"registries"`
	Skills     []KnownSkill `yaml:"skills"`
}

func parseRegistryDoc() (registryDoc, error) {
	var doc registryDoc
	dec := yaml.NewDecoder(bytes.NewReader(builtinRegistryYAML))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return doc, fmt.Errorf("registry.yaml: %w", err)
	}
	if doc.Schema != 1 {
		return doc, errors.New("registry.yaml: unsupported schema")
	}
	return doc, nil
}

// KnownSkills returns the skill descriptions recorded in registry.yaml, keyed by KnownSkillKey.
func KnownSkills() (map[string]KnownSkill, error) {
	doc, err := parseRegistryDoc()
	if err != nil {
		return nil, err
	}
	regs := map[string]bool{}
	for _, r := range doc.Registries {
		regs[r.ID] = true
	}
	out := map[string]KnownSkill{}
	for _, k := range doc.Skills {
		k.Description = strings.Join(strings.Fields(k.Description), " ")
		switch {
		case !regs[k.Registry]:
			return nil, fmt.Errorf("registry.yaml: skill %s names an unknown registry %q", k.Name, k.Registry)
		case ValidateName(k.Name) != nil:
			return nil, fmt.Errorf("registry.yaml: skill %q: %v", k.Name, ValidateName(k.Name))
		case len(k.Ref) != 40 || strings.Trim(k.Ref, "0123456789abcdef") != "":
			return nil, fmt.Errorf("registry.yaml: skill %s: ref must be a full commit id", k.Name)
		case k.Description == "" || len(k.Description) > MaxKnownDescription:
			return nil, fmt.Errorf("registry.yaml: skill %s: description is required (at most %d characters)", k.Name, MaxKnownDescription)
		case strings.TrimSpace(k.License) == "":
			return nil, fmt.Errorf("registry.yaml: skill %s: license is required", k.Name)
		}
		if p, ok := NormalizePath(k.Path); !ok || p == "" {
			return nil, fmt.Errorf("registry.yaml: skill %s: path %q must be a directory inside the repository", k.Name, k.Path)
		}
		key := KnownSkillKey(k.Registry, k.Name, k.Ref)
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("registry.yaml: skill %s is listed twice", key)
		}
		out[key] = k
	}
	return out, nil
}

// KnownSkillKey is "<registry>/<name>@<ref>".
func KnownSkillKey(registry, name, ref string) string { return registry + "/" + name + "@" + ref }

// CuratedRegistries parses the shipped registry.yaml.
func CuratedRegistries() ([]Registry, error) {
	doc, err := parseRegistryDoc()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i := range doc.Registries {
		r := &doc.Registries[i]
		if r.Trust == "" {
			r.Trust = "community"
		}
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("registry.yaml: %s: %w", r.ID, err)
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("registry.yaml: duplicate id %s", r.ID)
		}
		seen[r.ID] = true
		r.Enabled, r.Curated = true, true
	}
	return doc.Registries, nil
}

// RemoteSkill is a skill found inside a registry repository at a commit.
type RemoteSkill struct {
	Name       string
	Dir        string // path of the skill directory inside the repository
	SkillMD    []byte
	HasScripts bool
	LicenseTxt string // text of a LICENSE file in the skill directory, if any
}

// RegistryFetcher fetches skills from a registry repository, pinned to a commit. Implementations must fetch only the repo
// they are asked for.
type RegistryFetcher interface {
	// Resolve maps a ref ("" or "HEAD" for the default branch) to a full commit id.
	Resolve(ctx context.Context, repo, ref string) (string, error)
	// Fetch returns the skills under root at the commit.
	Fetch(ctx context.Context, repo, commit, root string) ([]RemoteSkill, error)
}

// Tarball limits.
const (
	maxTarballBytes  = 16 << 20
	maxExtractedSize = 8 << 20
	maxEntries       = 20000
	maxSkillFiles    = 500
	maxSkillsFound   = 400
)

var commitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// GitHubFetcher reads registries over HTTPS from api.github.com (ref -> commit) and codeload.github.com (tarball at a commit)
// through the allowlisted, address-checked packs.Fetcher. Hosts are fixed to GitHub's; APIBase and TarballBase exist so a
// protocol fixture can stand in for GitHub in tests. Not exercised against the real service from this repository.
type GitHubFetcher struct {
	Fetcher     *packs.Fetcher
	APIBase     string // default https://api.github.com
	TarballBase string // default https://codeload.github.com
}

// githubAccept is the media type GitHub's REST API documents; it answers 415 to the page crawler's markdown and HTML types.
const githubAccept = "application/vnd.github+json"

// NewGitHubFetcher builds the production fetcher: GitHub hosts only, no robots check (these are API endpoints a human
// configured, not a crawl), no loopback.
func NewGitHubFetcher() *GitHubFetcher {
	return &GitHubFetcher{Fetcher: packs.NewFetcher(packs.FetchPolicy{AllowedHosts: []string{"api.github.com", "codeload.github.com"}, NoRobots: true, RPS: 2, MaxBytes: 1 << 20}, nil)}
}

func (g *GitHubFetcher) api() string {
	if g.APIBase != "" {
		return strings.TrimRight(g.APIBase, "/")
	}
	return "https://api.github.com"
}
func (g *GitHubFetcher) tar() string {
	if g.TarballBase != "" {
		return strings.TrimRight(g.TarballBase, "/")
	}
	return "https://codeload.github.com"
}

// Resolve implements RegistryFetcher.
func (g *GitHubFetcher) Resolve(ctx context.Context, repo, ref string) (string, error) {
	if !repoRe.MatchString(repo) {
		return "", fmt.Errorf("invalid repo %q", repo)
	}
	if ref == "" {
		ref = "HEAD"
	}
	if commitRe.MatchString(ref) {
		return ref, nil
	}
	resp, err := g.Fetcher.GetAPI(ctx, g.api()+"/repos/"+repo+"/commits/"+url.PathEscape(ref), githubAccept)
	if err != nil {
		return "", fmt.Errorf("resolve %s@%s: %w", repo, ref, err)
	}
	var out struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil || !commitRe.MatchString(out.SHA) {
		return "", fmt.Errorf("resolve %s@%s: unexpected response", repo, ref)
	}
	return out.SHA, nil
}

// Fetch implements RegistryFetcher.
func (g *GitHubFetcher) Fetch(ctx context.Context, repo, commit, root string) ([]RemoteSkill, error) {
	if !repoRe.MatchString(repo) || !commitRe.MatchString(commit) {
		return nil, errors.New("invalid repo or commit")
	}
	resp, err := g.Fetcher.GetBinary(ctx, g.tar()+"/"+repo+"/tar.gz/"+commit, maxTarballBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch %s@%s: %w", repo, commit[:12], err)
	}
	return ExtractSkills(resp.Body, root)
}

// ExtractSkills reads a GitHub-style .tar.gz (one top-level directory) and returns the skills under root. Only regular files
// are read; links, devices, absolute or escaping paths are skipped and counted as errors only if they would be a skill file.
// Nothing is written to disk: contents stay in memory and are capped.
func ExtractSkills(tgz []byte, root string) ([]RemoteSkill, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	defer func() { _ = zr.Close() }()
	rootN := ""
	if root != "" {
		n, ok := NormalizePath(root)
		if !ok {
			return nil, fmt.Errorf("invalid root %q", root)
		}
		rootN = n + "/"
	}
	tr := tar.NewReader(io.LimitReader(zr, maxExtractedSize*4))
	files := map[string][]byte{}
	var total int64
	entries := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading archive: %w", err)
		}
		entries++
		if entries > maxEntries {
			return nil, errors.New("archive has too many entries")
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.ReplaceAll(h.Name, `\`, "/")
		if i := strings.Index(name, "/"); i >= 0 {
			name = name[i+1:] // drop the top-level <repo>-<sha>/ directory
		} else {
			continue
		}
		norm, ok := NormalizePath(name)
		if !ok {
			continue
		}
		if rootN != "" && !strings.HasPrefix(norm, rootN) {
			continue
		}
		base := path.Base(norm)
		wanted := base == "SKILL.md" || strings.EqualFold(base, "LICENSE") || strings.EqualFold(base, "LICENSE.txt") || strings.EqualFold(base, "LICENSE.md") || strings.Contains(norm, "/scripts/")
		if !wanted {
			continue
		}
		if len(files) >= maxSkillFiles {
			return nil, errors.New("too many skill files in the registry")
		}
		if h.Size > MaxSkillBytes && base == "SKILL.md" {
			files[norm] = nil // oversized: reported by ParseSkill's size check through a nil body
			continue
		}
		if h.Size > 256<<10 {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, h.Size+1))
		if err != nil {
			return nil, err
		}
		total += int64(len(b))
		if total > maxExtractedSize {
			return nil, errors.New("skill files exceed the extraction limit")
		}
		files[norm] = b
	}
	var out []RemoteSkill
	for p := range files {
		if path.Base(p) != "SKILL.md" {
			continue
		}
		dir := path.Dir(p)
		s := RemoteSkill{Name: path.Base(dir), Dir: dir, SkillMD: files[p]}
		if dir == "." {
			s.Name = ""
		}
		for q, b := range files {
			if strings.HasPrefix(q, dir+"/scripts/") {
				s.HasScripts = true
			}
			if path.Dir(q) == dir {
				switch strings.ToLower(path.Base(q)) {
				case "license", "license.txt", "license.md":
					s.LicenseTxt = string(b)
				}
			}
		}
		out = append(out, s)
		if len(out) > maxSkillsFound {
			return nil, errors.New("too many skills in the registry; import selectively")
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out, nil
}

// licenseRefused lists license strings that mean "no license".
func licenseRefused(l string) bool {
	switch strings.ToLower(strings.TrimSpace(l)) {
	case "", "none", "unlicensed", "proprietary", "all rights reserved", "unknown":
		return true
	}
	return false
}
