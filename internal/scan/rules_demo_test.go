package scan

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// pageSource is the Security page's rules help source when the test runs from Drydock's source folder. The Windows verification
// bundle runs the compiled test from a folder of its own that holds no sources (on CI it sits inside a checkout, so the folder
// above may be another's), and there the test is skipped; CI's go job runs it from the source.
func pageSource(t *testing.T) []byte {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if first, _, _ := strings.Cut(string(mod), "\n"); err != nil || strings.TrimSpace(first) != "module github.com/kjmaguire/drydock" {
		t.Skip("not run from Drydock's source folder (a verification bundle carries no sources), and this test reads the page's source")
	}
	src, err := os.ReadFile(filepath.Join(root, "ui", "app", "screens", "security", "rules-sources.ts"))
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// The Security page shows one rule to try the Code engine with (DEMO_RULE in ui/app/screens/security/rules-sources.ts). It is
// text inside a TypeScript file, so nothing else would notice a slip in its indentation or a misspelt key: read it from the
// source, require the fields Semgrep and Opengrep need of a rule and nothing else, and require that Drydock itself accepts a
// folder holding it as a rules folder. (It was not run through a real Opengrep here; docs/security-scan.md says so.)
func TestTheDemoRuleOnTheSecurityPageIsAValidRuleFile(t *testing.T) {
	src := pageSource(t)
	m := regexp.MustCompile("(?s)export const DEMO_RULE = `(.*?)`;").FindSubmatch(src)
	if m == nil {
		t.Fatal("DEMO_RULE is no longer a template literal in rules-sources.ts: update this test with it")
	}
	text := m[1]
	// a template literal shows its escapes and substitutions as something else than the source reads
	if bytes.ContainsAny(text, "\\") || bytes.Contains(text, []byte("${")) {
		t.Fatalf("DEMO_RULE holds an escape or a substitution, so the page would not show what the file says:\n%s", text)
	}
	var doc struct {
		Rules []struct {
			ID        string   `yaml:"id"`
			Pattern   string   `yaml:"pattern"`
			Message   string   `yaml:"message"`
			Languages []string `yaml:"languages"`
			Severity  string   `yaml:"severity"`
		} `yaml:"rules"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(text))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("the demo rule is not valid YAML of the shape a rule file has: %v\n%s", err, text)
	}
	if len(doc.Rules) != 1 {
		t.Fatalf("the demo is one rule, got %d", len(doc.Rules))
	}
	r := doc.Rules[0]
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(r.ID) || strings.TrimSpace(r.Pattern) == "" || strings.TrimSpace(r.Message) == "" {
		t.Fatalf("the demo rule needs an id, a pattern and a message: %+v", r)
	}
	if len(r.Languages) != 1 || r.Languages[0] != "go" {
		t.Fatalf("the page says it flags Go code, so the rule must name exactly the language go: %v", r.Languages)
	}
	switch r.Severity {
	case "INFO", "WARNING", "ERROR":
	default:
		t.Fatalf("severity %q is not one of INFO, WARNING, ERROR", r.Severity)
	}
	// the page tells the person to save it as a .yaml file in an empty folder and choose that folder
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.yaml"), text, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, note := checkRules(dir); !ok || note != "" {
		t.Fatalf("a folder holding the demo rule is not accepted as a rules folder: %q", note)
	}
}
