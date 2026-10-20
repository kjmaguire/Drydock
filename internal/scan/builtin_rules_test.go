package scan

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Decision P77: the built-in rules are three sets (Drydock's own and two MIT ones), each rule complete and named once, each
// set with its licence; no other file of the folder reaches Opengrep.
func TestTheBuiltinRuleSetsAreCompleteAndLicensed(t *testing.T) {
	sets, err := BuiltinRuleSets()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, s := range sets {
		counts[s.ID] = s.Rules
		if s.Rules == 0 || len(s.Languages) == 0 || s.Licence == "" || s.Source == "" {
			t.Errorf("%+v", s)
		}
	}
	if counts["drydock"] != 34 || counts["third_party/dgryski-semgrep-go"] != 66 || counts["third_party/0xdea-c"] != 49 {
		t.Fatalf("rule counts: %v (rules/README.md names them; update both together)", counts)
	}
	readme, err := fs.ReadFile(builtinFS, "rules/README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"| 34 |", "| 66 (in 43 files) |", "| 49 |"} {
		if !strings.Contains(string(readme), n) {
			t.Errorf("rules/README.md does not say %s", n)
		}
	}
	// every rule: an id named once across the sets, a message, a known severity, languages, and something to match
	seen := map[string]string{}
	severities := []string{"ERROR", "WARNING", "INFO"}
	for _, s := range sets {
		dir := "rules/" + s.ID
		if s.ID != "drydock" {
			lic, err := fs.ReadFile(builtinFS, dir+"/LICENSE")
			if err != nil || !strings.Contains(string(lic), "MIT License") && !strings.Contains(string(lic), "The MIT License") {
				t.Errorf("%s ships without its MIT licence: %v", s.ID, err)
			}
		}
		entries, _ := fs.ReadDir(builtinFS, dir)
		for _, e := range entries {
			if !isRuleFile(e.Name()) {
				if e.Name() != "LICENSE" {
					t.Errorf("%s/%s: only rule files and the licence belong in a set", dir, e.Name())
				}
				continue
			}
			b, _ := fs.ReadFile(builtinFS, dir+"/"+e.Name())
			var rf struct {
				Rules []map[string]any `yaml:"rules"`
			}
			if err := yaml.Unmarshal(b, &rf); err != nil || len(rf.Rules) == 0 {
				t.Errorf("%s/%s: %v", dir, e.Name(), err)
				continue
			}
			for _, r := range rf.Rules {
				id, _ := r["id"].(string)
				if id == "" {
					t.Errorf("%s/%s: a rule without an id", dir, e.Name())
					continue
				}
				if prev, dup := seen[id]; dup {
					t.Errorf("rule id %s is in %s and %s", id, prev, dir+"/"+e.Name())
				}
				seen[id] = dir + "/" + e.Name()
				sev, _ := r["severity"].(string)
				msg, _ := r["message"].(string)
				langs, _ := r["languages"].([]any)
				if !slices.Contains(severities, sev) || strings.TrimSpace(msg) == "" || len(langs) == 0 {
					t.Errorf("%s: severity %q, message %d chars, %d languages", id, sev, len(msg), len(langs))
				}
				matches := false
				for _, k := range []string{"pattern", "patterns", "pattern-either", "pattern-regex", "match"} {
					if _, ok := r[k]; ok {
						matches = true
					}
				}
				if !matches {
					t.Errorf("%s: no pattern", id)
				}
				// Drydock's own rules say why and what to do instead, and are named for the set
				if s.ID == "drydock" && (!strings.HasPrefix(id, "drydock.") || len(msg) < 60) {
					t.Errorf("%s: a rule of Drydock's own set is named drydock.* and its message says why and what to do (%d chars)", id, len(msg))
				}
			}
		}
	}
}

// Each of Drydock's own rules has a fixture line that it must find; the fixtures are run by Semgrep or Opengrep (`--test`) where
// one is installed (DRYDOCK_RULES_ENGINE names its program; otherwise the run is skipped and only the annotations are checked).
func TestDrydocksOwnRulesHaveFixtures(t *testing.T) {
	sets, err := BuiltinRuleSets()
	if err != nil {
		t.Fatal(err)
	}
	_ = sets
	fixtures := filepath.Join("testdata", "rules", "drydock")
	annotated := map[string]bool{}
	re := regexp.MustCompile(`ruleid:\s*(drydock\.[a-z0-9.-]+)`)
	err = filepath.WalkDir(fixtures, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			annotated[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := fs.ReadDir(builtinFS, "rules/drydock")
	for _, e := range entries {
		b, _ := fs.ReadFile(builtinFS, "rules/drydock/"+e.Name())
		var rf struct {
			Rules []struct {
				ID string `yaml:"id"`
			} `yaml:"rules"`
		}
		_ = yaml.Unmarshal(b, &rf)
		for _, r := range rf.Rules {
			if !annotated[r.ID] {
				t.Errorf("%s has no `ruleid:` line in %s", r.ID, fixtures)
			}
		}
	}
	engine := os.Getenv("DRYDOCK_RULES_ENGINE")
	if engine == "" {
		t.Skip("DRYDOCK_RULES_ENGINE is not set: the fixtures were checked for annotations only, not run")
	}
	out, err := exec.Command(engine, "--test", "--metrics=off", "--disable-version-check", "--config", filepath.Join("rules", "drydock"), fixtures).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "All tests passed") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// The rules are written once per version under the app's data: rule files and licences only (no README, no fixtures), the
// same folder again on the next start, and an older version's folder removed.
func TestBuiltinRulesAreWrittenOncePerVersion(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "builtin-000000000000")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := WriteBuiltinRules(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("the older version's folder was kept")
	}
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return err
	})
	rules := 0
	for _, f := range files {
		switch {
		case f == ".complete", strings.HasSuffix(f, "/LICENSE"):
		case strings.HasSuffix(f, ".yaml"), strings.HasSuffix(f, ".yml"):
			rules++
		default:
			t.Errorf("%s was written out with the rules", f)
		}
	}
	if rules != 5+43+49 || !slices.Contains(files, "third_party/0xdea-c/LICENSE") || !slices.Contains(files, "drydock/go.yaml") {
		t.Fatalf("%d rule files: %v", rules, files)
	}
	// the next start finds them in place: the same folder, nothing written again
	stamp, _ := os.Stat(filepath.Join(dir, "drydock", "go.yaml"))
	again, err := WriteBuiltinRules(root)
	if err != nil || again != dir {
		t.Fatalf("%v %s %s", err, again, dir)
	}
	if st, _ := os.Stat(filepath.Join(dir, "drydock", "go.yaml")); !st.ModTime().Equal(stamp.ModTime()) {
		t.Fatal("the rules were written again")
	}
}

// The Code engine gets the built-in rules, then a folder of the person's own when it can be used; one that cannot is left out
// rather than stopping the engine.
func TestTheCodeEngineRunsTheBuiltinRulesAndThePersonsFolder(t *testing.T) {
	own := t.TempDir()
	if err := os.WriteFile(filepath.Join(own, "mine.yaml"), []byte("rules: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		o    Options
		want []string
	}{
		{Options{BuiltinRules: "/b"}, []string{"/b"}},
		{Options{BuiltinRules: "/b", RulesDir: own}, []string{"/b", own}},
		{Options{BuiltinRules: "/b", RulesDir: filepath.Join(own, "missing")}, []string{"/b"}},
		{Options{RulesDir: own}, []string{own}},
		{Options{}, nil},
	}
	for i, c := range cases {
		if got := c.o.ruleSources(); !slices.Equal(got, c.want) {
			t.Errorf("case %d: %v, want %v", i, got, c.want)
		}
	}
	if got := strings.Join(opengrepArgs(Tool{Path: "opengrep"}, []string{"/b", own}, "/o.sarif", false), " "); got != "scan -f /b -f "+own+" --sarif-output=/o.sarif --no-progress-bar -q ." {
		t.Fatal(got)
	}
}

// A copied set names the upstream commit it was taken from, and rules/README.md (the human record) names the same one, so the
// Software Versions page, which reads the commit from here, measures what was really copied.
func TestACopiedRuleSetNamesItsCommit(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("rules", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	sets, err := BuiltinRuleSets()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sets {
		copied := strings.HasPrefix(s.ID, "third_party/")
		switch {
		case copied && len(s.Commit) != 40:
			t.Errorf("%s: no full commit (%q)", s.ID, s.Commit)
		case copied && !strings.Contains(string(readme), s.Commit):
			t.Errorf("%s: rules/README.md does not name %s", s.ID, s.Commit)
		case !copied && s.Commit != "":
			t.Errorf("%s: Drydock's own set has a commit", s.ID)
		}
	}
}
