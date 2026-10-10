package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// PlanSite is a place in Drydock's own code that asks for plan mode.
type PlanSite struct {
	File     string `json:"file"` // relative to the scanned root, with forward slashes
	Line     int    `json:"line"`
	Code     string `json:"code"`     // what the code does, e.g. `spec.PermissionMode = "plan"`
	ReadOnly bool   `json:"readOnly"` // the same literal or statement, or another statement of the same block, sets ReadOnly to true
}

// FindPlanModeSites parses every non-test Go file under dirs (relative to root) and returns each place that sets a
// PermissionMode to the literal "plan": a composite literal with `PermissionMode: "plan"`, or an assignment
// `x.PermissionMode = "plan"`. A mode chosen at run time (a variable) is not a literal and is not found here; the
// contract's test reports separately whether the Claude adapter still offers plan mode to the human.
func FindPlanModeSites(root string, dirs ...string) ([]PlanSite, error) {
	var sites []PlanSite
	fset := token.NewFileSet()
	for _, d := range dirs {
		base := filepath.Join(root, d)
		err := filepath.WalkDir(base, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := e.Name()
			if e.IsDir() {
				if path != base && (name == "testdata" || name == "node_modules" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			found, err := scanPlanModes(fset, filepath.ToSlash(rel), src)
			if err != nil {
				return err
			}
			sites = append(sites, found...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].File != sites[j].File {
			return sites[i].File < sites[j].File
		}
		return sites[i].Line < sites[j].Line
	})
	return sites, nil
}

// scanPlanModes finds the plan-mode sites of one file.
func scanPlanModes(fset *token.FileSet, name string, src []byte) ([]PlanSite, error) {
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var sites []PlanSite
	seen := map[token.Pos]bool{}
	add := func(pos token.Pos, code string, ro bool) {
		if seen[pos] {
			if ro {
				for i := range sites {
					if sites[i].Line == fset.Position(pos).Line && sites[i].Code == code {
						sites[i].ReadOnly = true
					}
				}
			}
			return
		}
		seen[pos] = true
		sites = append(sites, PlanSite{File: name, Line: fset.Position(pos).Line, Code: code, ReadOnly: ro})
	}
	// statements are checked with their siblings, so `spec.PermissionMode = "plan"` followed by `spec.ReadOnly = true` counts
	checkList := func(list []ast.Stmt) {
		for _, st := range list {
			as, ok := st.(*ast.AssignStmt)
			if !ok {
				continue
			}
			for _, recv := range planAssignments(as) {
				ro := setsReadOnly(as, recv)
				for _, other := range list {
					if o, ok := other.(*ast.AssignStmt); ok && o != as && setsReadOnly(o, recv) {
						ro = true
					}
				}
				add(as.Pos(), recv+`.PermissionMode = "plan"`, ro)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			if hasKeyValue(x, "PermissionMode", isPlan) {
				add(x.Pos(), types.ExprString(x.Type)+`{PermissionMode: "plan"}`, hasKeyValue(x, "ReadOnly", isTrue))
			}
		case *ast.BlockStmt:
			checkList(x.List)
		case *ast.CaseClause:
			checkList(x.Body)
		case *ast.CommClause:
			checkList(x.Body)
		case *ast.AssignStmt:
			// an assignment outside a statement list (the init of an if, for or switch) can only pair with itself
			for _, recv := range planAssignments(x) {
				add(x.Pos(), recv+`.PermissionMode = "plan"`, setsReadOnly(x, recv))
			}
		}
		return true
	})
	return sites, nil
}

// planAssignments returns the receivers r of `r.PermissionMode = "plan"` in an assignment.
func planAssignments(as *ast.AssignStmt) []string {
	var out []string
	for i, l := range as.Lhs {
		sel, ok := l.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "PermissionMode" || i >= len(as.Rhs) || !isPlan(as.Rhs[i]) {
			continue
		}
		out = append(out, types.ExprString(sel.X))
	}
	return out
}

// setsReadOnly reports whether the assignment sets recv.ReadOnly to true.
func setsReadOnly(as *ast.AssignStmt, recv string) bool {
	for i, l := range as.Lhs {
		if sel, ok := l.(*ast.SelectorExpr); ok && sel.Sel.Name == "ReadOnly" && types.ExprString(sel.X) == recv && i < len(as.Rhs) && isTrue(as.Rhs[i]) {
			return true
		}
	}
	return false
}

func hasKeyValue(cl *ast.CompositeLit, key string, val func(ast.Expr) bool) bool {
	for _, e := range cl.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == key && val(kv.Value) {
				return true
			}
		}
	}
	return false
}

func isPlan(e ast.Expr) bool {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return false
	}
	s, err := strconv.Unquote(bl.Value)
	return err == nil && s == "plan"
}

func isTrue(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "true"
}
