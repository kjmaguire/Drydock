package contract_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kjmaguire/drydock/internal/agents/manifests"
	"github.com/kjmaguire/drydock/internal/contract"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	d, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
		p := filepath.Dir(d)
		if p == d {
			t.Fatal("no go.mod above the test's directory")
		}
		d = p
	}
}

func TestCatalogIsValid(t *testing.T) {
	cs := contract.All()
	if err := contract.Validate(cs); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, c := range cs {
		ids[c.ID] = true
	}
	// the contracts the feature was asked to cover, at least
	for _, id := range []string{"CLAUDE-STREAM-01", "CLAUDE-PROMPTFILE-01", "CLAUDE-RO-01", "CLAUDE-PLAN-01", "CLAUDE-SETTINGS-01", "CLAUDE-RESTRICTED-01",
		"CLAUDE-RESTRICTED-02", "CLAUDE-RESUME-01", "CLAUDE-INTERRUPT-01", "CLAUDE-MCP-01", "CLAUDE-USAGE-01", "CLAUDE-EFFORT-01", "CLAUDE-DENIAL-01",
		"ACP-INIT-01", "WIN-ARGV-01"} {
		if !ids[id] {
			t.Errorf("the catalog lacks %s", id)
		}
	}
	bad := []contract.Contract{
		{ID: "lower-01", CLI: "claude", Adapter: "claude", Assumption: "a", Why: "b", Files: []string{"f"}, Tests: []contract.TestRef{{Package: "./p", Test: "TestX"}}},
		{ID: "CLAUDE-X-01", CLI: "", Assumption: "a", Why: "b", Files: []string{"f"}},
		{ID: "CLAUDE-Y-01", CLI: "claude", Adapter: "claude", Assumption: "a", Why: "b", Files: []string{"f"}, Tests: []contract.TestRef{{Package: "internal/p", Test: "X", Env: []string{"NOVALUE"}}}},
	}
	err := contract.Validate(bad)
	for _, want := range []string{"id must look like", "no CLI", "no test checks it", "bad test reference", "is not KEY=VALUE"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Validate must report %q: %v", want, err)
		}
	}
}

// pkgInfo is what the Go files of one package declare: its test functions and every string literal (subtest names).
type pkgInfo struct {
	tests    map[string]bool
	literals map[string]bool
}

func readPackage(t *testing.T, dir string) pkgInfo {
	t.Helper()
	info := pkgInfo{tests: map[string]bool{}, literals: map[string]bool{}}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files in %s (%v)", dir, err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		isTest := strings.HasSuffix(f, "_test.go")
		ast.Inspect(af, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncDecl:
				if isTest && x.Recv == nil && strings.HasPrefix(x.Name.Name, "Test") && takesTestingT(x) {
					info.tests[x.Name.Name] = true
				}
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					if s, err := strconv.Unquote(x.Value); err == nil {
						info.literals[s] = true
					}
				}
			}
			return true
		})
	}
	return info
}

// takesTestingT reports a func(t *testing.T) signature.
func takesTestingT(fd *ast.FuncDecl) bool {
	ps := fd.Type.Params.List
	if len(ps) != 1 || len(ps[0].Names) > 1 {
		return false
	}
	star, ok := ps[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "testing" && sel.Sel.Name == "T"
}

// Every test a contract names exists: the package's test files declare it with a func(*testing.T) signature, and a subtest name
// appears as a string in the package (a t.Run name or a table entry). Every file a contract names exists, and the docs explain
// every contract.
func TestEveryContractNamesTestsThatExist(t *testing.T) {
	root := repoRoot(t)
	pkgs := map[string]pkgInfo{}
	docs, err := os.ReadFile(filepath.Join(root, "docs", "contract-tests.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range contract.All() {
		for _, r := range c.Tests {
			info, ok := pkgs[r.Package]
			if !ok {
				info = readPackage(t, filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(r.Package, "./"))))
				pkgs[r.Package] = info
			}
			if !info.tests[r.Test] {
				t.Errorf("%s (%s): %s has no func %s(t *testing.T)", c.ID, c.CLI, r.Package, r.Test)
			}
			if r.Subtest != "" && !info.literals[r.Subtest] {
				t.Errorf("%s (%s): no subtest named %q in %s", c.ID, c.CLI, r.Subtest, r.Package)
			}
			for _, kv := range r.Env {
				if k, _, _ := strings.Cut(kv, "="); k != contract.EnvRealClaude {
					t.Errorf("%s: unexpected env %s", c.ID, kv)
				}
			}
		}
		for _, f := range c.Files {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil {
				t.Errorf("%s: names %s, which does not exist", c.ID, f)
			}
		}
		if !strings.Contains(string(docs), c.ID) {
			t.Errorf("docs/contract-tests.md does not explain %s", c.ID)
		}
	}
}

// The ACP targets follow the manifests: a CLI that speaks ACP itself is the manifest's ACP command, a bridge is the package the
// manifest starts with npx.
func TestACPTargetsMatchTheManifests(t *testing.T) {
	set, err := manifests.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range contract.ACPTargets {
		m, ok := set.Get(tg.Adapter)
		if !ok || m.Transports.ACP == nil || len(m.Transports.ACP.Argv) == 0 {
			t.Errorf("%s: manifest %s has no ACP transport", tg.Name, tg.Adapter)
			continue
		}
		argv := m.Transports.ACP.Argv
		if tg.Bridge {
			spec := argv[len(argv)-1]
			pkg := spec[:strings.LastIndex(spec, "@")]
			if argv[0] != "npx" || path.Base(pkg) != tg.Bin {
				t.Errorf("%s: the manifest runs %v", tg.Name, argv)
			}
		} else if argv[0] != tg.Bin {
			t.Errorf("%s: the manifest runs %v, the target looks for %s", tg.Name, argv, tg.Bin)
		}
		if got, ok := contract.ACPTargetByName(tg.Name); !ok || got.Adapter != tg.Adapter {
			t.Errorf("lookup of %s", tg.Name)
		}
	}
}

func TestFilterMatchesProgramsAndAdapters(t *testing.T) {
	cs := contract.All()
	var ids []string
	for _, c := range contract.Filter(cs, []string{"codex"}) {
		ids = append(ids, c.Key())
	}
	if strings.Join(ids, ",") != "ACP-INIT-01 codex-acp,ACP-PROMPT-01 codex-acp" {
		t.Fatalf("--cli codex: %v", ids)
	}
	for _, c := range contract.Filter(cs, []string{"claude"}) {
		if c.Adapter != "claude" {
			t.Fatalf("--cli claude picked %s", c.Key())
		}
	}
	if len(contract.Filter(cs, nil)) != len(cs) {
		t.Fatal("no filter keeps everything")
	}
	if got := contract.CLIs(contract.Filter(cs, []string{"goose", "claude-agent-acp"})); strings.Join(got, ",") != "claude-agent-acp,goose" {
		t.Fatalf("CLIs %v", got)
	}
}
