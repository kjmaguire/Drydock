package scan

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The Code engine's built-in rules (decision P77, on the owner's words of 2026-10-20: "I don't want to choose where the
// opengrep rules are; just build them into the app"). The rules are compiled into Drydock, written once per version under
// the app's data, and handed to Opengrep on every scan; a person may add a folder of their own rules on top. Which sets
// ship, and why the others do not, is in rules/README.md.

//go:embed rules
var builtinFS embed.FS

// RuleSet is one built-in set of rules: where it comes from, under which licence, how many rules and for which languages.
type RuleSet struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Source    string   `json:"source"`
	Licence   string   `json:"licence"`
	Rules     int      `json:"rules"`
	Languages []string `json:"languages"`
	// Commit is the upstream commit a copied set was taken from (empty for Drydock's own); the Software Versions page compares
	// it with the upstream repository's newest commit.
	Commit string `json:"commit,omitempty"`
}

// builtinSets names the folders under rules/ and what each one is.
var builtinSets = []RuleSet{
	{ID: "drydock", Name: "Drydock's own", Source: "written for Drydock", Licence: "the repository's licence"},
	{ID: "third_party/dgryski-semgrep-go", Name: "dgryski/semgrep-go", Source: "https://github.com/dgryski/semgrep-go", Licence: "MIT", Commit: "db0227c03f4b3c4e71d900188d51db4c81d66932"},
	{ID: "third_party/0xdea-c", Name: "0xdea/semgrep-rules (C and C++)", Source: "https://github.com/0xdea/semgrep-rules", Licence: "MIT", Commit: "e0121ac19d9fa259a44f3a24d3e42c95719b17c7"},
}

type ruleFile struct {
	Rules []struct {
		ID        string   `yaml:"id"`
		Languages []string `yaml:"languages"`
		Severity  string   `yaml:"severity"`
		Message   string   `yaml:"message"`
	} `yaml:"rules"`
}

// isRuleFile says whether a file of a set is a rule file (the only files besides each set's LICENSE that are written out).
func isRuleFile(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}

// BuiltinRuleSets reads the built-in sets: each one's rule count and languages.
func BuiltinRuleSets() ([]RuleSet, error) {
	out := make([]RuleSet, 0, len(builtinSets))
	for _, set := range builtinSets {
		langs := map[string]bool{}
		dir := "rules/" + set.ID
		entries, err := fs.ReadDir(builtinFS, dir)
		if err != nil {
			return nil, fmt.Errorf("built-in rules %s: %w", set.ID, err)
		}
		for _, e := range entries {
			if e.IsDir() || !isRuleFile(e.Name()) {
				continue
			}
			b, err := fs.ReadFile(builtinFS, dir+"/"+e.Name())
			if err != nil {
				return nil, err
			}
			var rf ruleFile
			if err := yaml.Unmarshal(b, &rf); err != nil {
				return nil, fmt.Errorf("built-in rules %s/%s: %w", set.ID, e.Name(), err)
			}
			for _, r := range rf.Rules {
				set.Rules++
				for _, l := range r.Languages {
					langs[strings.ToLower(l)] = true
				}
			}
		}
		for l := range langs {
			set.Languages = append(set.Languages, l)
		}
		sort.Strings(set.Languages)
		out = append(out, set)
	}
	return out, nil
}

// builtinDigest is a short hash of every file written out, so a new version of the rules goes to a new folder.
func builtinDigest() (string, error) {
	h := sha256.New()
	err := fs.WalkDir(builtinFS, "rules", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !isRuleFile(p) && path.Base(p) != "LICENSE" {
			return nil
		}
		b, err := fs.ReadFile(builtinFS, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		h.Write(b)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// WriteBuiltinRules writes the built-in rules (the rule files and each set's LICENSE; nothing else) to
// root/builtin-<digest>/ and returns that folder; Opengrep reads it with -f. A folder already written for these rules is
// used as it is, and the folders of older versions are removed. root is the app's rules folder (<data>/rules).
func WriteBuiltinRules(root string) (string, error) {
	digest, err := builtinDigest()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "builtin-"+digest)
	done := filepath.Join(dir, ".complete")
	if _, err := os.Stat(done); err == nil {
		return dir, nil
	}
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	err = fs.WalkDir(builtinFS, "rules", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, "rules"), "/")
		target := filepath.Join(tmp, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !isRuleFile(p) && path.Base(p) != "LICENSE" {
			return nil
		}
		b, err := fs.ReadFile(builtinFS, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, ".complete"), []byte(digest+"\n"), 0o644); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	// older versions of the rules are of no use now
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "builtin-") && e.Name() != filepath.Base(dir) {
				_ = os.RemoveAll(filepath.Join(root, e.Name()))
			}
		}
	}
	return dir, nil
}
