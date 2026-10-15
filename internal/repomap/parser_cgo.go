//go:build cgo

package repomap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unsafe"

	ts "github.com/tree-sitter/go-tree-sitter"
	csharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	golang "github.com/tree-sitter/tree-sitter-go/bindings/go"
	java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// The parser: tree-sitter's C library and grammars through CGO (decision D5, P62). Each language's tag query is the
// grammar's own queries/tags.scm (copied unchanged into queries/, with its license) plus, where the grammar's leaves out
// definitions or references the ranking needs, a query authored here (queries/*-extra.scm). TypeScript and TSX also take
// JavaScript's tags.scm, as the TypeScript grammar's own tree-sitter.json does.

const parserBuilt = true

//go:embed queries/*.scm
var queryFS embed.FS

type grammar struct {
	lang    func() unsafe.Pointer
	queries []string

	once     sync.Once
	language *ts.Language
	query    *ts.Query
	err      error
}

var grammars = map[string]*grammar{
	"go":         {lang: golang.Language, queries: []string{"go-tags.scm"}},
	"typescript": {lang: typescript.LanguageTypescript, queries: []string{"typescript-tags.scm", "javascript-tags.scm", "typescript-extra.scm"}},
	"tsx":        {lang: typescript.LanguageTSX, queries: []string{"typescript-tags.scm", "javascript-tags.scm", "typescript-extra.scm", "jsx-extra.scm"}},
	"javascript": {lang: javascript.Language, queries: []string{"javascript-tags.scm", "javascript-extra.scm", "jsx-extra.scm"}},
	"python":     {lang: python.Language, queries: []string{"python-tags.scm", "python-extra.scm"}},
	"csharp":     {lang: csharp.Language, queries: []string{"csharp-tags.scm", "csharp-extra.scm"}},
	"java":       {lang: java.Language, queries: []string{"java-tags.scm", "java-extra.scm"}},
	"rust":       {lang: rust.Language, queries: []string{"rust-tags.scm", "rust-extra.scm"}},
}

// Languages lists the languages this build parses.
func Languages() []string {
	out := make([]string, 0, len(grammars))
	for l := range grammars {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

func querySource(names []string) (string, error) {
	var b strings.Builder
	for _, n := range names {
		q, err := queryFS.ReadFile("queries/" + n)
		if err != nil {
			return "", err
		}
		b.Write(q)
		b.WriteString("\n")
	}
	return b.String(), nil
}

func (g *grammar) load() (*ts.Language, *ts.Query, error) {
	g.once.Do(func() {
		g.language = ts.NewLanguage(g.lang())
		src, err := querySource(g.queries)
		if err != nil {
			g.err = err
			return
		}
		q, qerr := ts.NewQuery(g.language, src)
		if qerr != nil {
			g.err = fmt.Errorf("tag query %v: %s", g.queries, qerr.Error())
			return
		}
		g.query = q
	})
	return g.language, g.query, g.err
}

// parserVersion names the parser and the queries, for the disk cache.
func parserVersion() string {
	h := sha256.New()
	fmt.Fprintf(h, "tree-sitter %d\n", ts.LANGUAGE_VERSION)
	for _, l := range Languages() {
		src, _ := querySource(grammars[l].queries)
		fmt.Fprintf(h, "%s\x00%s\x00", l, src)
	}
	return "ts-" + hex.EncodeToString(h.Sum(nil))[:16]
}

// extractTags parses src and runs the language's tag query: a match with a @name capture and a @definition.* capture is a
// definition, one with a @reference.* capture a reference. Matches with neither (a Go package clause) are ignored.
func extractTags(ctx context.Context, lang string, src []byte) ([]Tag, error) {
	g := grammars[lang]
	if g == nil {
		return nil, fmt.Errorf("no grammar for %q", lang)
	}
	language, q, err := g.load()
	if err != nil {
		return nil, err
	}
	p := ts.NewParser()
	defer p.Close()
	if err := p.SetLanguage(language); err != nil {
		return nil, err
	}
	// ctx is checked before each file, not during one: a file is at most Options.MaxFileBytes and parses in milliseconds.
	// ParseCtx is deprecated and its cancellation crashes in v0.25.0 (a nil cancellation flag); ParseWithOptions with a
	// progress callback keeps every callback in go-pointer's global table for good.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		return nil, errors.New("tree-sitter returned no tree")
	}
	defer tree.Close()
	qc := ts.NewQueryCursor()
	defer qc.Close()
	names := q.CaptureNames()
	var starts []int // byte offset of each line, computed on the first definition
	lineText := func(row uint) string {
		if starts == nil {
			starts = append(starts, 0)
			for i, c := range src {
				if c == '\n' {
					starts = append(starts, i+1)
				}
			}
		}
		if int(row) >= len(starts) {
			return ""
		}
		s := starts[row]
		e := len(src)
		if int(row)+1 < len(starts) {
			e = starts[row+1] - 1
		}
		line := bytes.TrimRight(src[s:e], "\r")
		if len(line) > 400 {
			line = line[:400]
		}
		return strings.ToValidUTF8(string(line), "")
	}
	type key struct {
		name string
		def  bool
		line int
	}
	seen := map[key]bool{}
	var out []Tag
	ms := qc.Matches(q, tree.RootNode(), src)
	for m := ms.Next(); m != nil; m = ms.Next() {
		var name *ts.Node
		var defNode *ts.Node
		kind := ""
		for i := range m.Captures {
			c := m.Captures[i]
			switch cn := names[c.Index]; {
			case cn == "name":
				n := c.Node
				name = &n
			case strings.HasPrefix(cn, "definition."):
				n := c.Node
				defNode, kind = &n, "def"
			case strings.HasPrefix(cn, "reference.") && kind == "":
				kind = "ref"
			}
		}
		if name == nil || kind == "" {
			continue
		}
		text := name.Utf8Text(src)
		if text == "" || len(text) > 200 || strings.ContainsAny(text, "\n\r") {
			continue
		}
		line := int(name.StartPosition().Row)
		k := key{text, kind == "def", line}
		if seen[k] {
			continue
		}
		seen[k] = true
		t := Tag{Name: text, Line: line}
		if kind == "def" {
			t.Def = true
			t.End = int(defNode.EndPosition().Row)
			t.Text = lineText(uint(line))
		}
		out = append(out, t)
	}
	return out, nil
}
