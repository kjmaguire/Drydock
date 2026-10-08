package security

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed rules/*.yaml
var rulesFS embed.FS

// Class is a command class (docs/security.md s7).
type Class string

// Command classes.
const (
	ClassReadonly          Class = "readonly"
	ClassWorkspaceWrite    Class = "workspace_write"
	ClassDestructive       Class = "destructive"
	ClassNetwork           Class = "network"
	ClassExecCode          Class = "exec_code"
	ClassDeploy            Class = "deploy"
	ClassSecrets           Class = "secrets"
	ClassDBMigration       Class = "db_migration"
	ClassGitHistoryRewrite Class = "git_history_rewrite"
	ClassUnknown           Class = "unknown"
	classHardDeny          Class = "hard_deny" // rule-file class only; reported through HardDeny, not Classes
)

var validClasses = map[Class]bool{ClassReadonly: true, ClassWorkspaceWrite: true, ClassDestructive: true, ClassNetwork: true, ClassExecCode: true,
	ClassDeploy: true, ClassSecrets: true, ClassDBMigration: true, ClassGitHistoryRewrite: true, classHardDeny: true}

// Rule is one entry of a class table in rules/*.yaml. All conditions must hold for a rule to match an invocation;
// within a list, any entry matching is enough.
type Rule struct {
	ID          string     `yaml:"id"`
	Programs    []string   `yaml:"programs"`              // normalized names; a trailing "*" is a prefix
	Shells      []string   `yaml:"shells"`                // restrict to bash, powershell, cmd; empty = any
	Subcommands []string   `yaml:"subcommands"`           // space separated leading positionals, for example "d1 migrations apply"
	Bare        bool       `yaml:"bare"`                  // no arguments at all after the program and subcommand
	FlagsAny    []string   `yaml:"flags_any"`             // at least one present
	FlagsAll    [][]string `yaml:"flags_all"`             // every group has at least one present
	FlagsNone   []string   `yaml:"flags_none"`            // none present
	ArgsRegex   []string   `yaml:"args_regex"`            // some argument matches
	CI          bool       `yaml:"ci"`                    // flags and subcommands compare case-insensitively (cmdlets, cmd builtins)
	Kind        string     `yaml:"kind"`                  // approval kind override
	Also        []Class    `yaml:"also_classes"`          // extra classes reported with this rule's class
	HardDeny    bool       `yaml:"hard_deny"`             // not approvable from an agent process
	Paths       string     `yaml:"paths"`                 // none | positional | heuristic (default heuristic)
	SkipFirst   bool       `yaml:"skip_first_positional"` // the first positional is a pattern or script, not a path
	SkipUnless  []string   `yaml:"skip_unless_flags"`     // ... unless one of these flags supplies the pattern
	Reason      string     `yaml:"reason"`

	class Class
	args  []*regexp.Regexp
}

type ruleFile struct {
	Class           string              `yaml:"class"`
	Rules           []Rule              `yaml:"rules"`
	CredentialPaths []string            `yaml:"credential_paths"`
	SecretPaths     []string            `yaml:"secret_paths"`
	SecretExclude   []string            `yaml:"secret_paths_exclude"`
	Aliases         map[string]string   `yaml:"aliases"`
	StrictParams    map[string][]string `yaml:"strict_params"`
	CommonParams    []string            `yaml:"common_params"`
	OpaqueCmdlets   []string            `yaml:"opaque_cmdlets"`
	PipelineCmdlets []string            `yaml:"pipeline_cmdlets"`
	OpaqueWrappers  []string            `yaml:"opaque_wrappers"`
}

// RuleSet is the loaded, validated class tables.
type RuleSet struct {
	Rules           []*Rule
	CredentialPaths []string
	SecretPaths     []*regexp.Regexp
	SecretExclude   []*regexp.Regexp
	Aliases         map[string]string
	StrictParams    map[string]map[string]bool
	CommonParams    map[string]bool
	OpaqueCmdlets   map[string]bool
	PipelineCmdlets map[string]bool
	OpaqueWrappers  map[string]bool
	byProgram       map[string][]*Rule
	prefixRules     []*Rule
	known           map[string]bool // every program named by any rule
}

// LoadRules parses the embedded rules/*.yaml. Unknown YAML fields, unknown classes, bad regexes and duplicate rule ids
// are errors, so a typo in a reviewed table fails the build's tests rather than silently weakening a rule.
func LoadRules() (*RuleSet, error) {
	return loadRulesFrom(rulesFS, "rules")
}

func loadRulesFrom(fsys fs.FS, dir string) (*RuleSet, error) {
	ents, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	rs := &RuleSet{Aliases: map[string]string{}, StrictParams: map[string]map[string]bool{}, CommonParams: map[string]bool{},
		OpaqueCmdlets: map[string]bool{}, PipelineCmdlets: map[string]bool{}, OpaqueWrappers: map[string]bool{},
		byProgram: map[string][]*Rule{}, known: map[string]bool{}}
	ids := map[string]string{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		var f ruleFile
		if err := dec.Decode(&f); err != nil {
			return nil, fmt.Errorf("security rules %s: %w", e.Name(), err)
		}
		cls := Class(f.Class)
		if f.Class != "powershell" && !validClasses[cls] {
			return nil, fmt.Errorf("security rules %s: unknown class %q", e.Name(), f.Class)
		}
		for i := range f.Rules {
			r := f.Rules[i]
			r.class = cls
			if r.ID == "" || len(r.Programs) == 0 {
				return nil, fmt.Errorf("security rules %s: rule %d needs id and programs", e.Name(), i)
			}
			if prev, dup := ids[r.ID]; dup {
				return nil, fmt.Errorf("security rules %s: duplicate rule id %s (also in %s)", e.Name(), r.ID, prev)
			}
			ids[r.ID] = e.Name()
			switch r.Paths {
			case "", "none", "positional", "heuristic":
			default:
				return nil, fmt.Errorf("security rules %s: rule %s: bad paths %q", e.Name(), r.ID, r.Paths)
			}
			for _, s := range r.Shells {
				if s != "bash" && s != "powershell" && s != "cmd" {
					return nil, fmt.Errorf("security rules %s: rule %s: bad shell %q", e.Name(), r.ID, s)
				}
			}
			for _, a := range r.Also {
				if !validClasses[a] || a == classHardDeny {
					return nil, fmt.Errorf("security rules %s: rule %s: bad also_classes %q", e.Name(), r.ID, a)
				}
			}
			for _, p := range r.ArgsRegex {
				re, err := regexp.Compile(p)
				if err != nil {
					return nil, fmt.Errorf("security rules %s: rule %s: %w", e.Name(), r.ID, err)
				}
				r.args = append(r.args, re)
			}
			if r.CI {
				r.Subcommands = lowerAll(r.Subcommands)
				r.FlagsAny = lowerAll(r.FlagsAny)
				r.FlagsNone = lowerAll(r.FlagsNone)
				r.SkipUnless = lowerAll(r.SkipUnless)
				for gi := range r.FlagsAll {
					r.FlagsAll[gi] = lowerAll(r.FlagsAll[gi])
				}
			}
			r.Programs = lowerAll(r.Programs)
			rp := &r
			rs.Rules = append(rs.Rules, rp)
			for _, p := range r.Programs {
				rs.known[strings.TrimSuffix(p, "*")] = true
				if strings.HasSuffix(p, "*") {
					rs.prefixRules = append(rs.prefixRules, rp)
				} else {
					rs.byProgram[p] = append(rs.byProgram[p], rp)
				}
			}
		}
		rs.CredentialPaths = append(rs.CredentialPaths, lowerAll(f.CredentialPaths)...)
		for _, p := range f.SecretPaths {
			re, err := regexp.Compile("(?i)" + p)
			if err != nil {
				return nil, fmt.Errorf("security rules %s: secret path %q: %w", e.Name(), p, err)
			}
			rs.SecretPaths = append(rs.SecretPaths, re)
		}
		for _, p := range f.SecretExclude {
			re, err := regexp.Compile("(?i)" + p)
			if err != nil {
				return nil, fmt.Errorf("security rules %s: secret exclude %q: %w", e.Name(), p, err)
			}
			rs.SecretExclude = append(rs.SecretExclude, re)
		}
		for k, v := range f.Aliases {
			rs.Aliases[strings.ToLower(k)] = strings.ToLower(v)
		}
		for k, v := range f.StrictParams {
			m := map[string]bool{}
			for _, p := range v {
				m[strings.ToLower(p)] = true
			}
			rs.StrictParams[strings.ToLower(k)] = m
		}
		for _, p := range f.CommonParams {
			rs.CommonParams[strings.ToLower(p)] = true
		}
		for _, p := range f.OpaqueCmdlets {
			rs.OpaqueCmdlets[strings.ToLower(p)] = true
		}
		for _, p := range f.PipelineCmdlets {
			rs.PipelineCmdlets[strings.ToLower(p)] = true
		}
		for _, p := range f.OpaqueWrappers {
			rs.OpaqueWrappers[strings.ToLower(p)] = true
		}
	}
	sort.SliceStable(rs.Rules, func(i, j int) bool { return rs.Rules[i].ID < rs.Rules[j].ID })
	return rs, nil
}

func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}

// IsKnownProgram reports whether any rule names the (normalized) program.
func (rs *RuleSet) IsKnownProgram(p string) bool {
	if rs.known[p] {
		return true
	}
	for _, r := range rs.prefixRules {
		for _, pat := range r.Programs {
			if strings.HasSuffix(pat, "*") && strings.HasPrefix(p, strings.TrimSuffix(pat, "*")) {
				return true
			}
		}
	}
	return false
}

// candidates returns the rules whose program list contains p.
func (rs *RuleSet) candidates(p string) []*Rule {
	out := append([]*Rule{}, rs.byProgram[p]...)
	for _, r := range rs.prefixRules {
		for _, pat := range r.Programs {
			if strings.HasSuffix(pat, "*") && strings.HasPrefix(p, strings.TrimSuffix(pat, "*")) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// RuleIDs lists every rule id (for tests and the audit view).
func (rs *RuleSet) RuleIDs() []string {
	ids := make([]string, 0, len(rs.Rules))
	for _, r := range rs.Rules {
		ids = append(ids, r.ID)
	}
	return ids
}

// IsSecretPath reports whether p (separators normalized to "/") looks like secret material.
func (rs *RuleSet) IsSecretPath(p string) bool {
	for _, re := range rs.SecretExclude {
		if re.MatchString(p) {
			return false
		}
	}
	for _, re := range rs.SecretPaths {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}
