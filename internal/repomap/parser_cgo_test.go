//go:build cgo

package repomap

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// These tests run the real tree-sitter parser (CGO) over testdata/polyglot, a small repository with files in every
// supported language, and the whole Map over a real git checkout of it.

func TestEveryLanguageHasAWorkingQuery(t *testing.T) {
	if !Available() {
		t.Fatal("a CGO build has the parser")
	}
	want := []string{"csharp", "go", "java", "javascript", "python", "rust", "tsx", "typescript"}
	if !slices.Equal(Languages(), want) {
		t.Fatalf("Languages() = %v, want %v", Languages(), want)
	}
	for _, l := range want {
		if _, _, err := grammars[l].load(); err != nil {
			t.Errorf("%s: %v", l, err)
		}
	}
	if !strings.HasPrefix(parserVersion(), "ts-") {
		t.Fatalf("parserVersion = %q", parserVersion())
	}
}

func tagsOf(t *testing.T, rel string) (defs, refs []string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", "polyglot", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	tags, err := extractTags(context.Background(), langFor(rel), src)
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	for _, tg := range tags {
		if tg.Def {
			defs = append(defs, tg.Name)
		} else {
			refs = append(refs, tg.Name)
		}
	}
	return defs, refs
}

func TestDefinitionsAndReferencesPerLanguage(t *testing.T) {
	cases := []struct {
		file       string
		defs, refs []string
	}{
		{"go/store/store.go", []string{"Store", "NewStore", "LookupValue", "SaveValue"}, []string{"Store"}},
		{"go/api/api.go", []string{"ServeRequest", "seedDefaults"}, []string{"Store", "LookupValue", "NewStore", "SaveValue"}},
		{"ts/src/format.ts", []string{"PriceOptions", "Currency", "Rounding", "formatPrice", "PriceFormatter", "format"}, []string{"Currency", "Rounding", "PriceOptions", "formatPrice"}},
		{"ts/src/cart.ts", []string{"defaults", "cartTotal", "CartView", "render"}, []string{"PriceOptions", "formatPrice", "PriceFormatter", "format"}},
		{"ts/src/PriceTag.tsx", []string{"PriceTag", "CheckoutPanel"}, []string{"PriceOptions", "formatPrice", "PriceTag"}},
		{"js/lib/util.js", []string{"slugifyTitle", "Registry", "register"}, []string{"slugifyTitle"}},
		{"js/lib/app.jsx", []string{"TitleBadge", "startApp"}, []string{"slugifyTitle", "Registry", "register", "TitleBadge"}},
		{"python/bank/models.py", []string{"Account", "deposit", "open_account"}, []string{"Account"}},
		{"python/bank/service.py", []string{"SavingsAccount", "add_interest", "onboard"}, []string{"Account", "open_account", "deposit"}},
		{"csharp/Ledger.cs", []string{"Ledger", "Post", "LedgerEntry", "EntryKind", "IAuditable", "Audit"}, []string{"LedgerEntry"}},
		{"csharp/Program.cs", []string{"Program", "Main", "Audit"}, []string{"IAuditable", "Ledger", "Post", "LedgerEntry"}},
		{"java/Inventory.java", []string{"Inventory", "countStock", "StockUnit"}, nil},
		{"java/Shop.java", []string{"Shop", "available"}, []string{"Inventory", "StockUnit", "countStock"}},
		{"rust/src/engine.rs", []string{"Engine", "start_engine", "Runner", "build_engine"}, []string{"Engine"}},
		{"rust/src/main.rs", []string{"Car", "run", "main"}, []string{"Runner", "build_engine", "start_engine"}},
	}
	for _, c := range cases {
		defs, refs := tagsOf(t, c.file)
		for _, d := range c.defs {
			if !slices.Contains(defs, d) {
				t.Errorf("%s: no definition of %s in %v", c.file, d, defs)
			}
		}
		for _, r := range c.refs {
			if !slices.Contains(refs, r) {
				t.Errorf("%s: no reference to %s in %v", c.file, r, refs)
			}
		}
	}
	// a definition carries its line and the extent that encloses its methods
	src, _ := os.ReadFile(filepath.Join("testdata", "polyglot", "ts", "src", "format.ts"))
	tags, _ := extractTags(context.Background(), "typescript", src)
	var class, method Tag
	for _, tg := range tags {
		if tg.Def && tg.Name == "PriceFormatter" {
			class = tg
		}
		if tg.Def && tg.Name == "format" {
			method = tg
		}
	}
	if class.Text != "export class PriceFormatter {" || class.Line != 16 || class.End != 22 || method.Line != 19 {
		t.Fatalf("class %+v method %+v", class, method)
	}
}

// fixtureRepo copies testdata/polyglot into a git checkout (untracked files are listed too, so nothing is committed) with
// a .gitignore and files every map must leave out.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	src := filepath.Join("testdata", "polyglot")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, map[string]string{
		".gitignore":                   "ignored/\n*.local.go\n",
		"ignored/shadow.go":            "package shadow\n\nfunc NewStore() {}\nfunc LookupValue() {}\n",
		"go/store/override.local.go":   "package store\n\nfunc SecretHelper() {}\n",
		"node_modules/left-pad/pad.js": "export function leftPad(s) { return s }\n",
		"go/store/zz_generated.go":     "// Code generated by stringer. DO NOT EDIT.\n\npackage store\n\nfunc GeneratedThing() {}\n",
		"docs/notes.md":                "# NewStore\n",
	})
	cmd := exec.Command("git", "init", "-q", "-b", "main", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return root
}

func TestMapOfARealRepository(t *testing.T) {
	root := fixtureRepo(t)
	m := New(Options{CacheDir: t.TempDir()})
	ctx := context.Background()
	res, err := m.Map(ctx, Request{Root: root, TokenBudget: 1024})
	if err != nil {
		t.Fatal(err)
	}
	// the heavily referenced file ranks first and opens the map
	if res.Ranked[0].Path != "go/store/store.go" || !strings.HasPrefix(res.Map, "go/store/store.go:\n") {
		t.Fatalf("ranked %+v\nmap:\n%s", res.Ranked[:3], res.Map)
	}
	if !strings.Contains(res.Map, "│func NewStore() *Store {") || !strings.Contains(res.Map, "│export class PriceFormatter {") {
		t.Fatalf("map lacks definition lines:\n%s", res.Map)
	}
	// ignored, vendored, generated and unsupported files are left out
	for _, gone := range []string{"ignored/", "override.local.go", "SecretHelper", "node_modules", "leftPad", "GeneratedThing", "notes.md"} {
		if strings.Contains(res.Map, gone) {
			t.Errorf("the map shows %q:\n%s", gone, res.Map)
		}
	}
	for _, r := range res.Ranked {
		if strings.HasPrefix(r.Path, "ignored/") || strings.Contains(r.Path, "node_modules") {
			t.Errorf("ranked an ignored file: %s", r.Path)
		}
	}
	if res.Skipped.Vendored != 1 || res.Skipped.Generated != 1 || res.Skipped.Unsupported < 2 || res.Files != 18 {
		t.Fatalf("files %d skipped %+v", res.Files, res.Skipped)
	}
	if len(res.Languages) != 8 || res.Languages["tsx"] != 1 || res.Languages["javascript"] != 2 {
		t.Fatalf("languages %v", res.Languages)
	}
	if res.Tokens > 1024 || res.Tokens != EstimateTokens(res.Map) {
		t.Fatalf("tokens %d over the budget", res.Tokens)
	}

	// a small budget is respected and keeps the most important file
	small, err := m.Map(ctx, Request{Root: root, TokenBudget: MinBudget})
	if err != nil || small.Tokens > MinBudget || !strings.HasPrefix(small.Map, "go/store/store.go:") || small.Shown >= res.Shown {
		t.Fatalf("budget %d: %d tokens, %d files shown (of %d)\n%s", MinBudget, small.Tokens, small.Shown, res.Shown, small.Map)
	}
	// the second map comes from the cache
	if small.Cached != small.Files {
		t.Fatalf("second map: %d of %d files cached", small.Cached, small.Files)
	}

	// focus personalises: the Java inventory ranks higher with the shop in focus
	pos := func(r Result, path string) int {
		for i, f := range r.Ranked {
			if f.Path == path {
				return i
			}
		}
		return len(r.Ranked)
	}
	focused, err := m.Map(ctx, Request{Root: root, Focus: []string{"java/Shop.java"}, TokenBudget: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if pos(focused, "java/Inventory.java") >= pos(res, "java/Inventory.java") || pos(focused, "java/Inventory.java") > 1 {
		t.Fatalf("focus on the shop: inventory at %d (was %d)", pos(focused, "java/Inventory.java"), pos(res, "java/Inventory.java"))
	}
	if !focused.Ranked[pos(focused, "java/Shop.java")].Focus {
		t.Fatal("the focus file is marked")
	}
}

// The disk cache's version names the grammar modules, so a new grammar release (which can parse the same file into
// different nodes) does not keep serving tags made by the old one.
func TestTheCacheVersionNamesTheGrammarReleases(t *testing.T) {
	mods := grammarModules()
	for _, m := range []string{"github.com/tree-sitter/go-tree-sitter@v", "github.com/tree-sitter/tree-sitter-typescript@v", "github.com/tree-sitter/tree-sitter-go@v"} {
		if !strings.Contains(mods, m) {
			t.Fatalf("%q not in the cache version's module list %q", m, mods)
		}
	}
}
