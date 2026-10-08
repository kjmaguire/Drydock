package main

import (
	"fmt"
	"os"

	ts "github.com/tree-sitter/go-tree-sitter"
	csharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	golang "github.com/tree-sitter/tree-sitter-go/bindings/go"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	js "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	php "github.com/tree-sitter/tree-sitter-php/bindings/go"
	py "github.com/tree-sitter/tree-sitter-python/bindings/go"
	rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tsx "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
	sql "github.com/DerekStride/tree-sitter-sql/bindings/go"
)

func main() {
	type L struct {
		name string
		ptr  func() *ts.Language
		src  string
	}
	langs := []L{
		{"go", func() *ts.Language { return ts.NewLanguage(golang.Language()) }, "package main\nfunc Add(a, b int) int { return a + b }\ntype T struct{}\nfunc (t T) M() {}\n"},
		{"typescript", func() *ts.Language { return ts.NewLanguage(tsx.LanguageTypescript()) }, "export class A { run(x: number): string { return String(x) } }\nexport function f<T>(a: T): T { return a }\n"},
		{"tsx", func() *ts.Language { return ts.NewLanguage(tsx.LanguageTSX()) }, "export const C = (p: {a: number}) => <div>{p.a}</div>;\n"},
		{"javascript", func() *ts.Language { return ts.NewLanguage(js.Language()) }, "function g(a){return a*2}\nclass K{ m(){} }\n"},
		{"python", func() *ts.Language { return ts.NewLanguage(py.Language()) }, "class A:\n    def m(self):\n        return 1\ndef f(x): return x\n"},
		{"php", func() *ts.Language { return ts.NewLanguage(php.LanguagePHP()) }, "<?php\nclass A { public function m() { return 1; } }\nfunction f($x) { return $x; }\n"},
		{"csharp", func() *ts.Language { return ts.NewLanguage(csharp.Language()) }, "class A { public int M(int x) { return x; } }\n"},
		{"java", func() *ts.Language { return ts.NewLanguage(java.Language()) }, "class A { int m(int x) { return x; } }\n"},
		{"rust", func() *ts.Language { return ts.NewLanguage(rust.Language()) }, "struct S; impl S { fn m(&self) -> i32 { 1 } }\nfn f() {}\n"},
		{"sql", func() *ts.Language { return ts.NewLanguage(sql.Language()) }, "SELECT a, b FROM t WHERE a > 1;\nCREATE TABLE x (id int);\n"},
	}
	for _, l := range langs {
		p := ts.NewParser()
		lang := l.ptr()
		if err := p.SetLanguage(lang); err != nil {
			fmt.Println(l.name, "SetLanguage ERR:", err)
			os.Exit(1)
		}
		tree := p.Parse([]byte(l.src), nil)
		root := tree.RootNode()
		s := root.ToSexp()
		if len(s) > 110 {
			s = s[:110] + "..."
		}
		fmt.Printf("%-10s abi=%d hasError=%v sexp=%s\n", l.name, lang.AbiVersion(), root.HasError(), s)
		tree.Close()
		p.Close()
	}
	// query test with go
	p := ts.NewParser()
	defer p.Close()
	lang := ts.NewLanguage(golang.Language())
	p.SetLanguage(lang)
	src := []byte("package main\nfunc Add(a, b int) int { return a + b }\nfunc Sub() {}\n")
	tree := p.Parse(src, nil)
	defer tree.Close()
	q, qerr := ts.NewQuery(lang, "(function_declaration name: (identifier) @name.definition.function)")
	if qerr != nil {
		fmt.Println("query err", qerr)
		return
	}
	defer q.Close()
	qc := ts.NewQueryCursor()
	defer qc.Close()
	caps := qc.Captures(q, tree.RootNode(), src)
	for {
		m, idx := caps.Next()
		if m == nil {
			break
		}
		c := m.Captures[idx]
		fmt.Printf("capture %s = %s\n", q.CaptureNames()[c.Index], c.Node.Utf8Text(src))
	}
}

