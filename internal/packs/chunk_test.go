package packs

import (
	"strings"
	"testing"
)

const sampleDoc = "---\ntitle: Wrangler Guide\n---\n# Wrangler\n\nIntro paragraph about wrangler.\n\n## Commands\n\nSome commands.\n\n### preview\n\nRun `wrangler versions upload` to preview.\n\n```bash\n# not a heading\nwrangler versions upload\n```\n\n#### Deep heading stays in body\n\ndeep text\n\n## Config\n\nUse wrangler.toml.\n"

func TestChunkMarkdownHeadingPathsAndFences(t *testing.T) {
	title, ch := ChunkMarkdown(sampleDoc, "fallback", DefaultChunkOptions())
	if title != "Wrangler Guide" {
		t.Fatalf("title %q", title)
	}
	var paths []string
	for _, c := range ch {
		paths = append(paths, c.HeadingPath)
		if !strings.HasPrefix(c.HeadingPath, "Wrangler Guide") {
			t.Errorf("every chunk repeats the page title: %q", c.HeadingPath)
		}
		if c.Hash != HashHex(c.HeadingPath, c.Body) || c.TokenEst == 0 {
			t.Errorf("chunk hash/tokens wrong: %+v", c)
		}
	}
	want := []string{"Wrangler Guide > Wrangler", "Wrangler Guide > Wrangler > Commands", "Wrangler Guide > Wrangler > Commands > preview", "Wrangler Guide > Wrangler > Config"}
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Fatalf("paths\n got %v\nwant %v", paths, want)
	}
	prev := ch[2].Body
	if !strings.Contains(prev, "# not a heading") || !strings.Contains(prev, "#### Deep heading stays in body") {
		t.Fatalf("fenced '#' and h4 must stay in the body:\n%s", prev)
	}
	// deterministic
	_, again := ChunkMarkdown(sampleDoc, "fallback", DefaultChunkOptions())
	for i := range ch {
		if ch[i].Hash != again[i].Hash {
			t.Fatal("chunking must be deterministic")
		}
	}
}

func TestChunkTitleFallbackAndNoHeadings(t *testing.T) {
	title, ch := ChunkMarkdown("just text\n\nmore text", "notes.md", DefaultChunkOptions())
	if title != "notes.md" || len(ch) != 1 || ch[0].HeadingPath != "notes.md" {
		t.Fatalf("%q %+v", title, ch)
	}
	if _, ch := ChunkMarkdown("", "x", DefaultChunkOptions()); len(ch) != 0 {
		t.Fatal("empty document has no chunks")
	}
}

func TestLongSectionSplitsOnBlocksUnderMax(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Big\n\n")
	for i := 0; i < 60; i++ {
		b.WriteString(strings.Repeat("word ", 60) + "\n\n") // ~75 tokens per paragraph
	}
	_, ch := ChunkMarkdown(b.String(), "", DefaultChunkOptions())
	if len(ch) < 6 {
		t.Fatalf("expected several chunks, got %d", len(ch))
	}
	for _, c := range ch {
		if c.TokenEst > 700 {
			t.Fatalf("chunk above max tokens: %d", c.TokenEst)
		}
	}
}

func TestCodeBlockAtomicUpToLimitAndTablesSplitBetweenRows(t *testing.T) {
	code := "```go\n" + strings.Repeat("fmt.Println(\"line\")\n", 150) + "```" // ~750 tokens: above max 700, below 1200
	_, ch := ChunkMarkdown("# C\n\n"+code, "", DefaultChunkOptions())
	if len(ch) != 1 || !strings.HasPrefix(ch[0].Body, "```go") || !strings.HasSuffix(ch[0].Body, "```") {
		t.Fatalf("code must stay whole below the atomic limit: %d chunks", len(ch))
	}
	huge := "```go\n" + strings.Repeat("fmt.Println(\"line\")\n", 400) + "```" // ~2000 tokens: split by lines, re-fenced
	_, ch = ChunkMarkdown("# C\n\n"+huge, "", DefaultChunkOptions())
	if len(ch) < 2 {
		t.Fatal("oversized code must split")
	}
	for _, c := range ch {
		if !strings.HasPrefix(c.Body, "```go\n") || !strings.HasSuffix(c.Body, "\n```") {
			t.Fatalf("every piece must be a valid fenced block: %q...", c.Body[:20])
		}
	}
	rows := []string{"| a | b |", "|---|---|"}
	for i := 0; i < 400; i++ {
		rows = append(rows, "| cell number "+strings.Repeat("x", 20)+" | value |")
	}
	_, ch = ChunkMarkdown("# T\n\n"+strings.Join(rows, "\n"), "", DefaultChunkOptions())
	if len(ch) < 2 {
		t.Fatal("a huge table must split")
	}
	for _, c := range ch {
		lines := strings.Split(c.Body, "\n")
		if lines[0] != "| a | b |" || lines[1] != "|---|---|" {
			t.Fatalf("each table piece repeats the header")
		}
		for _, l := range lines {
			if !strings.HasPrefix(l, "|") || !strings.HasSuffix(l, "|") {
				t.Fatalf("a row was cut: %q", l)
			}
		}
	}
}

func TestBuildMatch(t *testing.T) {
	all, any, terms := BuildMatch(`How do I deploy a Worker with wrangler.toml? "; DROP`)
	if all != `"deploy" AND "worker" AND "wrangler toml" AND "drop"*` || any != `"deploy" OR "worker" OR "wrangler toml" OR "drop"*` {
		t.Fatalf("%s / %s", all, any)
	}
	if len(terms) != 4 {
		t.Fatalf("terms %v", terms)
	}
	// only stop words: keep them rather than return nothing
	if a, _, _ := BuildMatch("how to do"); a == "" {
		t.Fatal("stop-word-only queries still search")
	}
	if a, o, tm := BuildMatch("  ?? !! "); a != "" || o != "" || tm != nil {
		t.Fatal("punctuation only is empty")
	}
	// hostile characters cannot form FTS5 syntax
	a, _, _ := BuildMatch(`foo OR NEAR(bar) AND "baz" * ^ column:thing`)
	if strings.Contains(a, "NEAR(") || strings.Contains(a, ":") {
		t.Fatalf("syntax leaked: %s", a)
	}
}

func TestNormalizeStripsHiddenAndMarksIt(t *testing.T) {
	in := "a​​b\r\nc\x00d‮  \n\n\n\ne"
	out, runs := Normalize(in)
	if runs != 3 {
		t.Fatalf("runs %d in %q", runs, out)
	}
	clean, n := StripHidden(out)
	if n != 3 || clean != "ab\ncd\n\ne" {
		t.Fatalf("clean %q n=%d", clean, n)
	}
}
