package packs

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ChunkOptions are the manifest's chunking settings (docs/expert-packs.md sections 2 and 4).
type ChunkOptions struct {
	SplitLevels      map[int]bool // heading levels that start a new chunk (1..6)
	TargetTokens     int
	MaxTokens        int
	CodeAtomicTokens int
}

// DefaultChunkOptions splits on h1 to h3 with the documented sizes.
func DefaultChunkOptions() ChunkOptions {
	return ChunkOptions{SplitLevels: map[int]bool{1: true, 2: true, 3: true}, TargetTokens: 350, MaxTokens: 700, CodeAtomicTokens: 1200}
}

func (o ChunkOptions) withDefaults() ChunkOptions {
	d := DefaultChunkOptions()
	if len(o.SplitLevels) == 0 {
		o.SplitLevels = d.SplitLevels
	}
	if o.TargetTokens <= 0 {
		o.TargetTokens = d.TargetTokens
	}
	if o.MaxTokens <= 0 {
		o.MaxTokens = d.MaxTokens
	}
	if o.MaxTokens < o.TargetTokens {
		o.MaxTokens = o.TargetTokens
	}
	if o.CodeAtomicTokens <= 0 {
		o.CodeAtomicTokens = d.CodeAtomicTokens
	}
	return o
}

// Chunk is one retrievable and citable piece of a document.
type Chunk struct {
	Ordinal     int
	HeadingPath string // "Page title > H1 > H2"; the page title is repeated in every chunk so BM25 can weight it
	Page        int    // the PDF page the chunk is on (the heading path ends in "Page N" then); 0 for sources without pages
	Body        string
	TokenEst    int
	Hash        string // sha256(heading_path + body)
}

// PathSep joins heading path segments.
const PathSep = " > "

var (
	atxHeading = regexp.MustCompile(`^(#{1,6})[ \t]+(.+?)[ \t]*#*[ \t]*$`)
	fenceOpen  = regexp.MustCompile("^[ ]{0,3}(`{3,}|~{3,})")
)

// SplitFrontMatter removes a leading YAML front matter block and returns its title (if any) and the rest.
func SplitFrontMatter(text string) (title, rest string) {
	if !strings.HasPrefix(text, "---\n") {
		return "", text
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return "", text
	}
	fm := text[4 : 4+end]
	rest = strings.TrimLeft(text[4+end+4:], "\n")
	for _, l := range strings.Split(fm, "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "title") {
			title = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return title, rest
}

type block struct {
	kind string // text | code | table
	text string
}

// ChunkMarkdown splits a normalized Markdown document along its headings. pageTitle is used when the document has no
// title of its own. It returns the title it used and the chunks in document order. Headings inside fenced code are not
// headings. Fenced code is kept whole up to CodeAtomicTokens, tables are split only between rows, and sections longer
// than MaxTokens are split on block boundaries toward TargetTokens.
func ChunkMarkdown(text, pageTitle string, opt ChunkOptions) (string, []Chunk) {
	opt = opt.withDefaults()
	fmTitle, body := SplitFrontMatter(text)
	lines := strings.Split(body, "\n")

	title := firstNonEmpty(fmTitle, "")
	// find the first h1 outside fences for the page title
	inFence, fenceCh, fenceLen := false, byte(0), 0
	for _, l := range lines {
		if ch, n, ok := fenceStart(l); ok {
			if !inFence {
				inFence, fenceCh, fenceLen = true, ch, n
			} else if ch == fenceCh && n >= fenceLen && strings.TrimSpace(l[strings.IndexByte(l, ch):]) == strings.Repeat(string(ch), n) {
				inFence = false
			}
			continue
		}
		if inFence {
			continue
		}
		if m := atxHeading.FindStringSubmatch(l); m != nil && len(m[1]) == 1 {
			if title == "" {
				title = cleanHeading(m[2])
			}
			break
		}
	}
	title = firstNonEmpty(title, pageTitle)

	type section struct {
		path  string
		lines []string
	}
	var sections []section
	stack := make([]string, 7)
	cur := section{path: title}
	curPath := func() string {
		parts := []string{}
		if title != "" {
			parts = append(parts, title)
		}
		for lvl := 1; lvl <= 6; lvl++ {
			h := stack[lvl]
			if h == "" || (lvl == 1 && h == title) {
				continue
			}
			parts = append(parts, h)
		}
		return strings.Join(parts, PathSep)
	}
	flush := func() {
		if strings.TrimSpace(strings.Join(cur.lines, "\n")) != "" {
			sections = append(sections, cur)
		}
	}
	inFence, fenceCh, fenceLen = false, 0, 0
	for _, l := range lines {
		if ch, n, ok := fenceStart(l); ok {
			if !inFence {
				inFence, fenceCh, fenceLen = true, ch, n
			} else if ch == fenceCh && n >= fenceLen && strings.TrimSpace(l[strings.IndexByte(l, ch):]) == strings.Repeat(string(ch), n) {
				inFence = false
			}
			cur.lines = append(cur.lines, l)
			continue
		}
		if !inFence {
			if m := atxHeading.FindStringSubmatch(l); m != nil {
				lvl := len(m[1])
				if opt.SplitLevels[lvl] {
					flush()
					stack[lvl] = cleanHeading(m[2])
					for d := lvl + 1; d <= 6; d++ {
						stack[d] = ""
					}
					cur = section{path: curPath()}
					continue
				}
			}
		}
		cur.lines = append(cur.lines, l)
	}
	flush()

	var out []Chunk
	for _, s := range sections {
		for _, body := range packBlocks(parseBlocks(s.lines), opt) {
			body = strings.TrimSpace(body)
			if body == "" {
				continue
			}
			out = append(out, Chunk{Ordinal: len(out), HeadingPath: s.path, Body: body, TokenEst: EstimateTokens(body), Hash: HashHex(s.path, body)})
		}
	}
	return title, out
}

func cleanHeading(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "*_`")
	return strings.Join(strings.Fields(s), " ")
}

func fenceStart(l string) (ch byte, n int, ok bool) {
	m := fenceOpen.FindStringSubmatch(l)
	if m == nil {
		return 0, 0, false
	}
	return m[1][0], len(m[1]), true
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// parseBlocks groups a section into paragraphs, fenced code blocks and tables.
func parseBlocks(lines []string) []block {
	var blocks []block
	var para []string
	flushPara := func() {
		if len(para) > 0 {
			blocks = append(blocks, block{"text", strings.Join(para, "\n")})
			para = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if ch, n, ok := fenceStart(l); ok {
			flushPara()
			code := []string{l}
			j := i + 1
			for ; j < len(lines); j++ {
				code = append(code, lines[j])
				if c2, n2, ok2 := fenceStart(lines[j]); ok2 && c2 == ch && n2 >= n && strings.TrimSpace(lines[j]) == strings.Repeat(string(ch), n2) {
					break
				}
			}
			blocks = append(blocks, block{"code", strings.Join(code, "\n")})
			i = j
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(l), "|") {
			flushPara()
			tbl := []string{l}
			j := i + 1
			for ; j < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[j]), "|"); j++ {
				tbl = append(tbl, lines[j])
			}
			blocks = append(blocks, block{"table", strings.Join(tbl, "\n")})
			i = j - 1
			continue
		}
		if strings.TrimSpace(l) == "" {
			flushPara()
			continue
		}
		para = append(para, l)
	}
	flushPara()
	return blocks
}

// packBlocks greedily packs blocks into chunk bodies of about TargetTokens, never above MaxTokens (code blocks may reach
// CodeAtomicTokens on their own).
func packBlocks(blocks []block, opt ChunkOptions) []string {
	var out []string
	var cur []string
	curTok := 0
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, "\n\n"))
			cur, curTok = nil, 0
		}
	}
	add := func(text string) {
		t := EstimateTokens(text)
		if curTok > 0 && curTok+t > opt.TargetTokens && !(curTok < opt.TargetTokens/3 && curTok+t <= opt.MaxTokens) {
			flush()
		}
		cur = append(cur, text)
		curTok += t
	}
	for _, b := range blocks {
		t := EstimateTokens(b.text)
		switch {
		case b.kind == "code" && t > opt.MaxTokens && t <= opt.CodeAtomicTokens:
			flush()
			out = append(out, b.text) // atomic: code stays whole
		case t <= opt.MaxTokens:
			add(b.text)
		case b.kind == "code":
			flush()
			out = append(out, splitCode(b.text, opt.CodeAtomicTokens)...)
		case b.kind == "table":
			flush()
			out = append(out, splitTable(b.text, opt.MaxTokens)...)
		default:
			flush()
			out = append(out, splitText(b.text, opt.TargetTokens, opt.MaxTokens)...)
		}
	}
	flush()
	return out
}

// splitCode splits an oversized fenced block by lines; every piece is re-fenced so it stays valid Markdown.
func splitCode(code string, maxTok int) []string {
	lines := strings.Split(code, "\n")
	open := lines[0]
	closeLine := ""
	body := lines[1:]
	if len(body) > 0 {
		if _, _, ok := fenceStart(body[len(body)-1]); ok {
			closeLine = body[len(body)-1]
			body = body[:len(body)-1]
		}
	}
	if closeLine == "" {
		if ch, n, ok := fenceStart(open); ok {
			closeLine = strings.Repeat(string(ch), n)
		} else {
			closeLine = "```"
		}
	}
	var out []string
	var cur []string
	tok := 0
	emit := func() {
		if len(cur) > 0 {
			out = append(out, open+"\n"+strings.Join(cur, "\n")+"\n"+closeLine)
			cur, tok = nil, 0
		}
	}
	for _, l := range body {
		lt := EstimateTokens(l) + 1
		if lt > maxTok { // a single enormous line: cut it
			emit()
			for _, piece := range hardSplit(l, maxTok*4) {
				out = append(out, open+"\n"+piece+"\n"+closeLine)
			}
			continue
		}
		if tok+lt > maxTok {
			emit()
		}
		cur = append(cur, l)
		tok += lt
	}
	emit()
	return out
}

// splitTable splits only between rows and repeats the header (first two lines) in every piece.
func splitTable(tbl string, maxTok int) []string {
	lines := strings.Split(tbl, "\n")
	if len(lines) < 3 {
		return hardSplit(tbl, maxTok*4)
	}
	head := lines[:2]
	headTok := EstimateTokens(strings.Join(head, "\n"))
	var out []string
	var cur []string
	tok := headTok
	emit := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(head, "\n")+"\n"+strings.Join(cur, "\n"))
			cur, tok = nil, headTok
		}
	}
	for _, r := range lines[2:] {
		rt := EstimateTokens(r) + 1
		if tok+rt > maxTok && len(cur) > 0 {
			emit()
		}
		cur = append(cur, r)
		tok += rt
	}
	emit()
	return out
}

// splitText splits a long paragraph run on line boundaries, then sentences, then by size as a last resort.
func splitText(text string, target, maxTok int) []string {
	var units []string
	for _, l := range strings.Split(text, "\n") {
		if EstimateTokens(l) <= maxTok {
			units = append(units, l)
			continue
		}
		for _, s := range splitSentences(l) {
			if EstimateTokens(s) <= maxTok {
				units = append(units, s)
			} else {
				units = append(units, hardSplit(s, maxTok*4)...)
			}
		}
	}
	var out []string
	var cur []string
	tok := 0
	for _, u := range units {
		ut := EstimateTokens(u) + 1
		if tok+ut > target && len(cur) > 0 {
			out = append(out, strings.Join(cur, "\n"))
			cur, tok = nil, 0
		}
		cur = append(cur, u)
		tok += ut
	}
	if len(cur) > 0 {
		out = append(out, strings.Join(cur, "\n"))
	}
	return out
}

var sentenceEnd = regexp.MustCompile(`([.!?])\s+`)

func splitSentences(l string) []string {
	idx := sentenceEnd.FindAllStringIndex(l, -1)
	var out []string
	last := 0
	for _, m := range idx {
		out = append(out, l[last:m[1]])
		last = m[1]
	}
	if last < len(l) {
		out = append(out, l[last:])
	}
	return out
}

// hardSplit cuts s into pieces of at most n runes on rune boundaries.
func hardSplit(s string, n int) []string {
	if n <= 0 {
		n = 1
	}
	var out []string
	for utf8.RuneCountInString(s) > n {
		r := []rune(s)
		out = append(out, string(r[:n]))
		s = string(r[n:])
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}
