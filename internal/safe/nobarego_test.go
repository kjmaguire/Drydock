package safe

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// Every goroutine of the app starts through Go (or Recover in its first defer): a panic in a bare `go` statement ends the
// whole process, the core and every agent it runs with it, where Go reports it and the app carries on. Test files, the
// labelled stand-ins and the e2e harness may start their own.
func TestTheAppStartsNoBareGoroutine(t *testing.T) {
	root := filepath.Join("..", "..")
	var bare []string
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
			if d.IsDir() {
				base := d.Name()
				if base == "testdata" || strings.HasSuffix(base, "test") || strings.HasPrefix(base, "fake") || rel == "cmd/drydock-e2e" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || rel == "internal/safe/safe.go" {
				return nil
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if g, ok := n.(*ast.GoStmt); ok && !recovers(g) {
					bare = append(bare, fset.Position(g.Pos()).String())
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(bare) > 0 {
		t.Fatalf("goroutines started without safe.Go (a panic in one ends the app):\n%s", strings.Join(bare, "\n"))
	}
}

// recovers is a `go func() { defer safe.Recover(...) ... }()`: the goroutine recovers its own panic.
func recovers(g *ast.GoStmt) bool {
	lit, ok := g.Call.Fun.(*ast.FuncLit)
	if !ok || len(lit.Body.List) == 0 {
		return false
	}
	d, ok := lit.Body.List[0].(*ast.DeferStmt)
	if !ok {
		return false
	}
	sel, ok := d.Call.Fun.(*ast.SelectorExpr)
	return ok && (sel.Sel.Name == "Recover")
}
